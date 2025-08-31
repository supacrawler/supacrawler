package scrape

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"scraper/internal/core/scrape/robots"
	"scraper/internal/logger"
	"scraper/internal/platform/engineapi"
	rds "scraper/internal/platform/redis"
	"scraper/internal/utils/markdown"

	html2markdown "github.com/JohannesKaufmann/html-to-markdown"
	"github.com/playwright-community/playwright-go"
)

type Service struct {
	log        *logger.Logger
	redis      *rds.Service
	httpClient *http.Client
	robots     *robots.Service
}

func NewScrapeService(redis *rds.Service) *Service {
	return &Service{log: logger.New("ScrapeService"), redis: redis, httpClient: newHTTPClient(), robots: robots.New()}
}

// ScrapeWithCache parity helper used by crawl: returns (result, cached, error)
func (s *Service) ScrapeWithCache(ctx context.Context, url string, includeHTML, renderJs bool) (*engineapi.ScrapeResponse, bool, error) {
	format := engineapi.GetV1ScrapeParamsFormat("markdown")
	params := engineapi.GetV1ScrapeParams{Url: url, Format: &format, RenderJs: &renderJs, IncludeHtml: &includeHTML}

	if cached := s.getCached(ctx, params); cached != nil {
		s.log.LogDebugf("scrapeWithCache hit url=%s", url)
		return cached, true, nil
	}

	var res *engineapi.ScrapeResponse
	var err error
	scrapeFormat := engineapi.GetV1ScrapeParamsFormat("markdown")
	if renderJs {
		res, err = s.scrapeWithPlaywright(url, includeHTML, scrapeFormat)
	} else {
		res, err = s.scrapeSimpleHTTP(url, includeHTML, scrapeFormat)
	}
	if err != nil {
		return nil, false, err
	}
	if !s.isValidResult(res) {
		return nil, false, fmt.Errorf("filtered out low-quality content")
	}

	// Cache best-effort
	s.cache(ctx, params, res)
	return res, false, nil
}

// ScrapeURL implements synchronous scrape with caching, robots checks, retry logic, and optional JS rendering
func (s *Service) ScrapeURL(ctx context.Context, params engineapi.GetV1ScrapeParams) (*engineapi.ScrapeResponse, error) {
	s.log.LogDebugf("scrape start url=%s render_js=%v full params=%+v", params.Url, boolVal(params.RenderJs), params)
	fresh := false
	if params.Fresh != nil {
		fresh = *params.Fresh
	}

	// Cache read
	if !fresh {
		if cached := s.getCached(ctx, params); cached != nil {
			s.log.LogDebugf("cache hit url=%s", params.Url)
			return cached, nil
		}
		s.log.LogDebugf("cache miss url=%s", params.Url)
	}

	renderJs := false
	if params.RenderJs != nil {
		renderJs = *params.RenderJs
	}
	format := engineapi.GetV1ScrapeParamsFormat("markdown")
	if params.Format != nil {
		format = *params.Format
	}

	// Get include_html parameter
	includeHTML := false
	if params.IncludeHtml != nil {
		includeHTML = *params.IncludeHtml
	}

	// Respect robots.txt
	if !s.robots.IsAllowed(params.Url, "SupacrawlerBot") {
		s.log.LogWarnf("robots disallow url=%s", params.Url)
		return nil, fmt.Errorf("disallowed by robots.txt")
	}

	// Retry logic for transient failures
	var result *engineapi.ScrapeResponse
	var err error
	maxRetries := 3
	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			// simple backoff: 1s, 2s, 4s
			d := time.Duration(1<<(attempt-1)) * time.Second
			s.log.LogDebugf("retry attempt=%d sleep=%s url=%s", attempt, d, params.Url)
			time.Sleep(d)
		}

		if renderJs {
			result, err = s.scrapeWithPlaywright(params.Url, includeHTML, format)
		} else {
			result, err = s.scrapeSimpleHTTP(params.Url, includeHTML, format)
		}

		if err != nil && s.isRetryableScrapingError(err) && attempt < maxRetries-1 {
			s.log.LogWarnf("retryable error url=%s attempt=%d err=%v", params.Url, attempt+1, err)
			continue
		}
		break
	}

	if err != nil {
		s.log.LogErrorf("scrape failed url=%s err=%v", params.Url, err)
		return nil, fmt.Errorf("failed to scrape URL after %d attempts: %w", maxRetries, err)
	}

	// Cache write (best-effort)
	s.cache(ctx, params, result)
	s.log.LogSuccessf("scrape ok url=%s status=%d", params.Url, intVal(result.Metadata.StatusCode))
	return result, nil
}

func (s *Service) scrapeSimpleHTTP(url string, includeHTML bool, format engineapi.GetV1ScrapeParamsFormat) (*engineapi.ScrapeResponse, error) {
	req, err := s.createHTTPRequest(url)
	if err != nil {
		return nil, fmt.Errorf("fetch: %w", err)
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}

	htmlBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	h := string(htmlBytes)
	md := s.convertHTMLToMarkdown(h)

	var content string

	// Handle different formats
	if format == engineapi.GetV1ScrapeParamsFormat("links") {
		// Extract links from HTML
		links := extractLinksFromHTML(h, url)
		content = strings.Join(links, "\n")
	} else {
		// Default to markdown
		content = s.cleanContent(md)
		if !strings.HasSuffix(content, "\n\n") {
			content = strings.TrimRight(content, "\n") + "\n\n"
		}
	}

	title := extractTitle(h)
	// Build rich metadata from the HTML
	meta := buildMetadataFromHTML(h, url, resp.StatusCode)

	result := &engineapi.ScrapeResponse{
		Success:  true,
		Url:      url,
		Content:  &content,
		Title:    &title,
		Metadata: meta,
	}

	// Include HTML if requested
	if includeHTML {
		htmlContent := strings.TrimSpace(h)
		result.Html = &htmlContent
	}

	// Add links to response if available
	if format == engineapi.GetV1ScrapeParamsFormat("links") {
		links := extractLinksFromHTML(h, url)
		result.Links = &links
	}

	return result, nil
}

func (s *Service) scrapeWithPlaywright(url string, includeHTML bool, format engineapi.GetV1ScrapeParamsFormat) (*engineapi.ScrapeResponse, error) {
	s.log.LogDebugf("scrapeWithPlaywright start url=%s", url)

	pw, err := playwright.Run()
	if err != nil {
		return nil, fmt.Errorf("playwright run: %w", err)
	}
	browser, err := pw.Chromium.Launch(playwright.BrowserTypeLaunchOptions{Headless: playwright.Bool(true)})
	if err != nil {
		_ = pw.Stop()
		return nil, fmt.Errorf("launch: %w", err)
	}
	defer pw.Stop()
	defer browser.Close()

	ctx, err := browser.NewContext()
	if err != nil {
		return nil, err
	}
	page, err := ctx.NewPage()
	if err != nil {
		return nil, err
	}

	var resp playwright.Response
	resp, navErr := page.Goto(url, playwright.PageGotoOptions{WaitUntil: playwright.WaitUntilStateDomcontentloaded, Timeout: playwright.Float(10000)})
	if navErr != nil {
		// fallback to full load longer timeout
		resp, navErr = page.Goto(url, playwright.PageGotoOptions{WaitUntil: playwright.WaitUntilStateLoad, Timeout: playwright.Float(20000)})
		if navErr != nil {
			return nil, fmt.Errorf("goto failed: %w", navErr)
		}
	}
	content, err := page.Content()
	if err != nil {
		return nil, err
	}
	titleStr, _ := page.Title()

	md := markdown.ConvertHTMLToMarkdown(content)

	var out string

	// Handle different formats
	if format == engineapi.GetV1ScrapeParamsFormat("links") {
		// Extract links from HTML
		links := extractLinksFromHTML(content, url)
		out = strings.Join(links, "\n")
	} else {
		// Default to markdown
		out = s.cleanContent(md)
		if !strings.HasSuffix(out, "\n\n") {
			// ensure exactly two newlines at end
			out = strings.TrimRight(out, "\n") + "\n\n"
		}
	}

	status := 200
	if resp != nil {
		status = resp.Status()
	}
	meta := buildMetadataFromHTML(content, url, status)

	result := &engineapi.ScrapeResponse{
		Success:  true,
		Url:      url,
		Content:  &out,
		Title:    &titleStr,
		Metadata: meta,
	}

	// Include HTML if requested
	if includeHTML {
		htmlContent := strings.TrimSpace(content)
		result.Html = &htmlContent
	}

	// Add links to response if available
	if format == engineapi.GetV1ScrapeParamsFormat("links") {
		links := extractLinksFromHTML(content, url)
		result.Links = &links
	}

	return result, nil
}

// Helpers

func newHTTPClient() *http.Client {
	return &http.Client{Timeout: 10 * time.Second}
}

func (s *Service) createHTTPRequest(url string) (*http.Request, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/113.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	return req, nil
}

func (s *Service) convertHTMLToMarkdown(h string) string {
	conv := html2markdown.NewConverter("", true, nil)
	md, _ := conv.ConvertString(h)
	if cleaned := markdown.ConvertHTMLToMarkdown(h); strings.TrimSpace(cleaned) != "" {
		md = cleaned
	}
	return s.cleanContent(md)
}

func (s *Service) cleanContent(md string) string {
	if md == "" {
		return ""
	}
	// 1. Initial Normalization
	cleaned := strings.ReplaceAll(md, "\r\n", "\n")

	// 2. Structural Link Repairs
	cleaned = strings.ReplaceAll(cleaned, ")\\\n[", ")\n[")
	cleaned = strings.ReplaceAll(cleaned, "]\\\n(", "]\n(")

	reEndBS := regexp.MustCompile(`\\+\n`)
	cleaned = reEndBS.ReplaceAllString(cleaned, "\n")

	reImgBold := regexp.MustCompile(`\)\n{1,2}(\*\*[^\]]+\*\*)\]\(`)
	cleaned = reImgBold.ReplaceAllString(cleaned, ") $1](")

	reImgNext := regexp.MustCompile(`\)\n{1,2}\[([^\]]+)\]\(`)
	cleaned = reImgNext.ReplaceAllString(cleaned, ") [$1](")

	// 3. Spacing and Formatting
	reAdj := regexp.MustCompile(`\) \[!\[`) // ") [!["
	cleaned = reAdj.ReplaceAllString(cleaned, ")\n\n[![")

	re := regexp.MustCompile(`\n{3,}`)
	cleaned = re.ReplaceAllString(cleaned, "\n\n")

	reHeaders := regexp.MustCompile("([^\n])\n(#+)")
	cleaned = reHeaders.ReplaceAllString(cleaned, "$1\n\n$2")

	// 4. Finalization
	cleaned = strings.TrimSpace(cleaned) + "\n\n"

	return cleaned
}

func extractTitle(htmlContent string) string {
	start := strings.Index(htmlContent, "<title>")
	if start == -1 {
		return ""
	}
	end := strings.Index(htmlContent, "</title>")
	if end == -1 || end < start {
		return ""
	}
	return htmlContent[start+7 : end]
}

// extractLinksFromHTML extracts all href links from HTML content
func extractLinksFromHTML(htmlContent, baseURL string) []string {
	var links []string
	linkRegex := regexp.MustCompile(`<a[^>]+href=["']([^"']+)["'][^>]*>`)
	matches := linkRegex.FindAllStringSubmatch(htmlContent, -1)

	for _, match := range matches {
		if len(match) > 1 {
			link := strings.TrimSpace(match[1])
			if link != "" {
				// Convert relative URLs to absolute
				if !strings.HasPrefix(link, "http://") && !strings.HasPrefix(link, "https://") {
					if strings.HasPrefix(link, "//") {
						// Protocol-relative URL
						if strings.HasPrefix(baseURL, "https://") {
							link = "https:" + link
						} else {
							link = "http:" + link
						}
					} else if strings.HasPrefix(link, "/") {
						// Absolute path
						if i := strings.Index(baseURL, "://"); i != -1 {
							host := baseURL[i+3:]
							if j := strings.Index(host, "/"); j != -1 {
								link = baseURL[:i+3] + host[:j] + link
							} else {
								link = baseURL + link
							}
						}
					} else if !strings.HasPrefix(link, "#") && !strings.HasPrefix(link, "javascript:") && !strings.HasPrefix(link, "mailto:") {
						// Relative path
						if strings.HasSuffix(baseURL, "/") {
							link = baseURL + link
						} else {
							link = baseURL + "/" + link
						}
					}
				}

				// Only include valid HTTP/HTTPS links
				if strings.HasPrefix(link, "http://") || strings.HasPrefix(link, "https://") {
					links = append(links, link)
				}
			}
		}
	}

	// Remove duplicates
	seen := make(map[string]bool)
	var uniqueLinks []string
	for _, link := range links {
		if !seen[link] {
			seen[link] = true
			uniqueLinks = append(uniqueLinks, link)
		}
	}

	return uniqueLinks
}

// extractPageMetadataFromHTML parses common metadata from an HTML string into a flat map
func extractPageMetadataFromHTML(htmlString string, url string) map[string]interface{} {
	out := make(map[string]interface{})
	out["url"] = url
	// lightweight parse without external deps to avoid unused imports
	// Title
	t := extractTitle(htmlString)
	if strings.TrimSpace(t) != "" {
		out["title"] = strings.TrimSpace(t)
	}
	// very basic meta extraction (name/property + content)
	// Keep simple to avoid heavy HTML parsing here; markdown.ConvertHTMLToMarkdown already parsed DOM earlier
	// We still capture a few high-value tags via regex
	findMeta := func(name string) string {
		// pattern matches: <meta name="NAME" content="...">
		pattern := fmt.Sprintf(`<meta[^>]*(name|property|http-equiv)=["']%s["'][^>]*content=["']([^"']+)["'][^>]*>`, regexp.QuoteMeta(name))
		re := regexp.MustCompile(`(?is)` + pattern)
		m := re.FindStringSubmatch(htmlString)
		if len(m) >= 3 {
			return strings.TrimSpace(m[2])
		}
		return ""
	}
	setIf := func(k, v string) {
		if v != "" {
			out[k] = v
		}
	}
	setIf("description", findMeta("description"))
	setIf("og:title", findMeta("og:title"))
	setIf("og:description", findMeta("og:description"))
	setIf("og:image", findMeta("og:image"))
	setIf("twitter:title", findMeta("twitter:title"))
	setIf("twitter:description", findMeta("twitter:description"))
	setIf("twitter:image", findMeta("twitter:image"))

	// canonical
	reCanon := regexp.MustCompile(`(?is)<link[^>]*rel=["']canonical["'][^>]*href=["']([^"']+)["'][^>]*>`)
	if m := reCanon.FindStringSubmatch(htmlString); len(m) >= 2 {
		out["canonical"] = strings.TrimSpace(m[1])
	}
	// favicon
	reFav := regexp.MustCompile(`(?is)<link[^>]*rel=["'](icon|shortcut icon)["'][^>]*href=["']([^"']+)["'][^>]*>`)
	if m := reFav.FindStringSubmatch(htmlString); len(m) >= 3 {
		out["favicon"] = strings.TrimSpace(m[2])
	}
	return out
}

// buildMetadataFromHTML constructs engineapi.ScrapeMetadata from HTML
func buildMetadataFromHTML(htmlString string, pageURL string, status int) engineapi.ScrapeMetadata {
	meta := engineapi.ScrapeMetadata{}
	// set required basic fields
	meta.StatusCode = &status
	meta.SourceUrl = &pageURL

	// helpers
	set := func(dst **string, val string) {
		if strings.TrimSpace(val) == "" {
			return
		}
		v := strings.TrimSpace(val)
		*dst = &v
	}
	// absolute URL helper
	absolutize := func(u string) string {
		u = strings.TrimSpace(u)
		if u == "" {
			return u
		}
		// already absolute
		if strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://") {
			return u
		}
		// protocol-relative
		if strings.HasPrefix(u, "//") {
			if strings.HasPrefix(pageURL, "https://") {
				return "https:" + u
			}
			return "http:" + u
		}
		// relative -> join with origin
		// crude origin extraction
		origin := pageURL
		if i := strings.Index(origin, "://"); i != -1 {
			origin = origin[i+3:]
			if j := strings.Index(origin, "/"); j != -1 {
				origin = pageURL[:i+3] + origin[:j]
			} else {
				origin = pageURL
			}
		}
		if strings.HasPrefix(u, "/") {
			// origin + path
			if k := strings.Index(pageURL, "://"); k != -1 {
				host := pageURL[k+3:]
				if s := strings.Index(host, "/"); s != -1 {
					origin = pageURL[:k+3] + host[:s]
				} else {
					origin = pageURL
				}
			}
			return origin + u
		}
		// fall back to origin + "/" + u
		if !strings.HasSuffix(origin, "/") {
			return origin + "/" + u
		}
		return origin + u
	}

	// use existing lightweight regex extractor
	raw := extractPageMetadataFromHTML(htmlString, pageURL)
	set(&meta.Title, getString(raw, "title"))
	set(&meta.Description, getString(raw, "description"))
	set(&meta.Language, getString(raw, "language"))
	set(&meta.Canonical, absolutize(getString(raw, "canonical")))
	set(&meta.Favicon, absolutize(getString(raw, "favicon")))
	set(&meta.OgTitle, getString(raw, "og:title"))
	set(&meta.OgDescription, getString(raw, "og:description"))
	set(&meta.OgImage, absolutize(getString(raw, "og:image")))
	set(&meta.OgSiteName, getString(raw, "og:site_name"))
	set(&meta.TwitterTitle, getString(raw, "twitter:title"))
	set(&meta.TwitterDescription, getString(raw, "twitter:description"))
	set(&meta.TwitterImage, absolutize(getString(raw, "twitter:image")))

	return meta
}

func getString(m map[string]interface{}, key string) string {
	if v, ok := m[key]; ok {
		switch t := v.(type) {
		case string:
			return t
		case []string:
			if len(t) > 0 {
				return t[0]
			}
		}
	}
	return ""
}

// Cache helpers

func (s *Service) getCached(ctx context.Context, params engineapi.GetV1ScrapeParams) *engineapi.ScrapeResponse {
	key := s.generateCacheKey(params)
	var res engineapi.ScrapeResponse
	if err := s.redis.CacheGet(ctx, key, &res); err != nil {
		return nil
	}
	return &res
}

func (s *Service) cache(ctx context.Context, params engineapi.GetV1ScrapeParams, res *engineapi.ScrapeResponse) {
	key := s.generateCacheKey(params)
	// TTL: longer for render_js
	renderJs := false
	if params.RenderJs != nil {
		renderJs = *params.RenderJs
	}
	ttl := 300 // 5 minutes
	if renderJs {
		ttl = 900 // 15 minutes
	}
	_ = s.redis.CacheSet(ctx, key, res, ttl)
}

func (s *Service) generateCacheKey(params engineapi.GetV1ScrapeParams) string {
	render := "false"
	if params.RenderJs != nil && *params.RenderJs {
		render = "true"
	}
	format := "markdown"
	if params.Format != nil {
		format = string(*params.Format)
	}
	includeHtml := "false"
	if params.IncludeHtml != nil && *params.IncludeHtml {
		includeHtml = "true"
	}
	// Normalize URL minimally
	safeURL := strings.ReplaceAll(params.Url, ":", "_")
	safeURL = strings.ReplaceAll(safeURL, "/", "_")
	safeURL = strings.ReplaceAll(safeURL, "?", "_")
	safeURL = strings.ReplaceAll(safeURL, "&", "_")
	return fmt.Sprintf("scrape:%s:%s:%s:%s", safeURL, render, format, includeHtml)
}

// Retry classification
func (s *Service) isRetryableScrapingError(err error) bool {
	if err == nil {
		return false
	}
	es := strings.ToLower(err.Error())
	if strings.Contains(es, "429") || strings.Contains(es, "too many requests") || strings.Contains(es, "rate limit") {
		return true
	}
	// Do not retry 403 Forbidden errors
	if strings.Contains(es, "503") || strings.Contains(es, "service unavailable") || strings.Contains(es, "502") || strings.Contains(es, "bad gateway") || strings.Contains(es, "504") || strings.Contains(es, "gateway timeout") {
		return true
	}
	if strings.Contains(es, "connection reset") || strings.Contains(es, "connection refused") || strings.Contains(es, "timeout") && !strings.Contains(es, "permanent") {
		return true
	}
	return false
}

func boolVal(p *bool) bool {
	if p == nil {
		return false
	}
	return *p
}
func intVal(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

func (s *Service) isValidResult(res *engineapi.ScrapeResponse) bool {
	if res == nil {
		return false
	}
	content := ""
	if res.Content != nil {
		content = strings.TrimSpace(*res.Content)
	}
	sc := 0
	if res.Metadata.StatusCode != nil {
		sc = *res.Metadata.StatusCode
	}

	// Basic filters
	if sc == 404 || sc == 403 {
		return false
	}
	if len(content) < 10 {
		return false
	}
	return true
}
