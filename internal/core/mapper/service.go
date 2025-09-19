package mapper

import (
	"context"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"scraper/internal/core/scrape/robots"
	"scraper/internal/logger"

	"github.com/gocolly/colly"
)

type Service struct {
	log    *logger.Logger
	robots *robots.Service
}

func NewMapService() *Service { return &Service{log: logger.New("MapService"), robots: robots.New()} }

type Request struct {
	URL               string
	Depth             int
	LinkLimit         int
	IncludeSubdomains bool
	Patterns          []string
}

type Result struct {
	Links []string `json:"links"`
}

func (s *Service) MapURL(req Request) (*Result, error) {
	s.log.LogDebugf("Map start url=%s depth=%d limit=%d subdomains=%v", req.URL, req.Depth, req.LinkLimit, req.IncludeSubdomains)
	links := make(map[string]struct{})
	var mu sync.Mutex
	c := colly.NewCollector(colly.MaxDepth(max(1, req.Depth)), colly.Async(true))
	cleaned := cleanURL(req.URL)
	dom := extractDomain(cleaned)

	// Limits / robots on requests
	c.OnRequest(func(r *colly.Request) {
		mu.Lock()
		reached := req.LinkLimit > 0 && len(links) >= max(1, req.LinkLimit)
		mu.Unlock()
		if reached {
			s.log.LogDebugf("Map abort (limit reached) %s", r.URL)
			r.Abort()
			return
		}
		if !s.robots.IsAllowed(r.URL.String(), "SupacrawlerBot") {
			s.log.LogDebugf("Map disallow robots %s", r.URL)
			r.Abort()
			return
		}
	})

	c.OnHTML("a[href]", func(e *colly.HTMLElement) {
		link := e.Request.AbsoluteURL(e.Attr("href"))
		link = normalize(link)
		if link == "" {
			return
		}
		ldom := extractDomain(link)
		if domainsMatch(ldom, dom, req.IncludeSubdomains) {
			if !s.robots.IsAllowed(link, "SupacrawlerBot") {
				return
			}
			// Check if link matches patterns
			if !matchesPattern(link, req.Patterns) {
				return
			}
			mu.Lock()
			_, exists := links[link]
			if !exists {
				links[link] = struct{}{}
			}
			reached := req.LinkLimit > 0 && len(links) >= max(1, req.LinkLimit)
			mu.Unlock()
			if reached {
				return
			}
			if !exists && e.Request.Depth < max(1, req.Depth) {
				_ = e.Request.Visit(link)
			}
		}
	})

	c.Limit(&colly.LimitRule{DomainGlob: "*", Parallelism: 10, RandomDelay: 500 * time.Millisecond})

	if err := c.Visit(cleaned); err != nil {
		return nil, fmt.Errorf("visit: %w", err)
	}
	c.Wait()
	out := make([]string, 0, len(links))
	for l := range links {
		out = append(out, l)
	}
	s.log.LogSuccessf("Map ok url=%s discovered=%d", req.URL, len(out))
	return &Result{Links: out}, nil
}

// MapLinksStream streams discovered links as they are found. Caller manages channel lifecycle.
func (s *Service) MapLinksStream(ctx context.Context, req Request, out chan<- string) error {
	s.log.LogDebugf("Map stream start url=%s depth=%d limit=%d subdomains=%v", req.URL, req.Depth, req.LinkLimit, req.IncludeSubdomains)
	links := make(map[string]struct{})
	var mu sync.Mutex
	c := colly.NewCollector(colly.MaxDepth(max(1, req.Depth)), colly.Async(true))
	cleaned := cleanURL(req.URL)
	dom := extractDomain(cleaned)

	limitReached := false

	c.OnError(func(r *colly.Response, err error) {
		s.log.LogWarnf("Map stream error %s %d: %v", r.Request.URL, r.StatusCode, err)
	})

	c.Limit(&colly.LimitRule{DomainGlob: "*", Parallelism: 10, RandomDelay: 500 * time.Millisecond})

	c.OnHTML("a[href]", func(e *colly.HTMLElement) {
		select {
		case <-ctx.Done():
			return
		default:
		}

		link := e.Request.AbsoluteURL(e.Attr("href"))
		link = normalize(link)
		if link == "" {
			return
		}
		ldom := extractDomain(link)
		if domainsMatch(ldom, dom, req.IncludeSubdomains) {
			if !s.robots.IsAllowed(link, "SupacrawlerBot") {
				return
			}
			// Check if link matches patterns
			if !matchesPattern(link, req.Patterns) {
				return
			}

			mu.Lock()
			if limitReached {
				mu.Unlock()
				return
			}

			if _, exists := links[link]; !exists {
				links[link] = struct{}{}
				shouldVisit := e.Request.Depth < max(1, req.Depth)

				// Check limit after adding
				if req.LinkLimit > 0 && len(links) >= max(1, req.LinkLimit) {
					limitReached = true
					shouldVisit = false // Don't visit more if we hit limit
				}
				mu.Unlock()

				// Channel send outside critical section
				select {
				case <-ctx.Done():
					return
				case out <- link:
				}

				if shouldVisit {
					_ = e.Request.Visit(link)
				}
			} else {
				mu.Unlock()
			}
		}
	})

	c.OnRequest(func(r *colly.Request) {
		// Quick check - single lock/unlock
		mu.Lock()
		reached := limitReached
		mu.Unlock()

		if reached {
			r.Abort()
			return
		}

		select {
		case <-ctx.Done():
			r.Abort()
			return
		default:
		}

		if !s.robots.IsAllowed(r.URL.String(), "SupacrawlerBot") {
			r.Abort()
			return
		}
	})

	if err := c.Visit(cleaned); err != nil {
		return fmt.Errorf("failed to visit URL: %w", err)
	}
	c.Wait()
	mu.Lock()
	linksCount := len(links)
	mu.Unlock()
	s.log.LogSuccessf("Map stream done url=%s emitted=%d", req.URL, linksCount)
	return nil
}

func cleanURL(u string) string {
	if !strings.HasPrefix(u, "http") {
		u = "https://" + u
	}
	return u
}

func extractDomain(u string) string {
	p, _ := url.Parse(u)
	if p != nil {
		return p.Hostname()
	}
	return ""
}

func normalize(u string) string {
	p, _ := url.Parse(u)
	if p == nil {
		return u
	}
	p.Fragment = ""
	if p.Path == "/" {
		p.Path = ""
	}
	return p.String()
}

func domainsMatch(a, b string, includeSub bool) bool {
	if a == b {
		return true
	}
	a = strings.TrimPrefix(a, "www.")
	b = strings.TrimPrefix(b, "www.")
	if a == b {
		return true
	}
	if includeSub && (strings.HasSuffix(a, "."+b) || strings.HasSuffix(b, "."+a)) {
		return true
	}
	return false
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// matchesPattern checks if a URL matches any of the given patterns
func matchesPattern(u string, patterns []string) bool {
	if len(patterns) == 0 {
		return true // If no patterns, allow all URLs
	}

	parsed, err := url.Parse(u)
	if err != nil {
		return false
	}

	path := parsed.Path
	if path == "" {
		path = "/"
	}

	for _, pattern := range patterns {
		// Use filepath.Match for glob-style pattern matching
		matched, err := filepath.Match(pattern, path)
		if err == nil && matched {
			return true
		}

		// Also check if pattern is a prefix (for patterns like "/blog/*")
		if strings.HasSuffix(pattern, "*") {
			prefix := strings.TrimSuffix(pattern, "*")
			// Handle exact match (e.g., "/blog" matches "/blog/*")
			if path == strings.TrimSuffix(prefix, "/") {
				return true
			}
			// Handle prefix match (e.g., "/blog/post" matches "/blog/*")
			if strings.HasPrefix(path, prefix) {
				return true
			}
		}
	}

	return false
}
