package scrape

import (
	"context"
	"fmt"
	"math/rand"
	"regexp"
	"strings"
	"sync"
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
	log    *logger.Logger
	redis  *rds.Service
	robots *robots.Service
}

func NewScrapeService(redis *rds.Service) *Service {
	return &Service{log: logger.New("ScrapeService"), redis: redis, robots: robots.New()}
}

// ScrapeWithCache parity helper used by crawl: returns (result, cached, error)
func (s *Service) ScrapeWithCache(ctx context.Context, url string, includeHTML bool) (*engineapi.ScrapeResponse, bool, error) {
	format := engineapi.GetV1ScrapeParamsFormat("markdown")
	params := engineapi.GetV1ScrapeParams{Url: url, Format: &format, IncludeHtml: &includeHTML}

	if cached := s.getCached(ctx, params); cached != nil {
		s.log.Info().Str("url", url).Msg("cache hit")
		return cached, true, nil
	}

	res, err := s.scrapeWithRetriesPlaywright(params)
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

// ScrapeURL implements synchronous scrape with caching, robots checks, and scraping
func (s *Service) ScrapeURL(ctx context.Context, params engineapi.GetV1ScrapeParams) (*engineapi.ScrapeResponse, error) {
	s.log.Info().Str("url", params.Url).Msg("scrape start")
	fresh := false
	if params.Fresh != nil {
		fresh = *params.Fresh
	}

	// Cache read
	if !fresh {
		if cached := s.getCached(ctx, params); cached != nil {
			s.log.Info().Str("url", params.Url).Msg("cache hit")
			return cached, nil
		}
	}

	// Respect robots.txt
	if !s.robots.IsAllowed(params.Url, "SupacrawlerBot") {
		s.log.Info().Str("url", params.Url).Msg("robots disallow")
		return nil, fmt.Errorf("disallowed by robots.txt")
	}

	// Always use Playwright with retry logic
	result, err := s.scrapeWithRetriesPlaywright(params)
	if err != nil {
		s.log.Info().Str("url", params.Url).Str("error", err.Error()).Msg("scrape failed")
		return nil, err
	}

	// Success! Cache and return
	s.cache(ctx, params, result)
	s.log.Info().Str("url", params.Url).Int("status", intVal(result.Metadata.StatusCode)).Str("method", "playwright").Msg("scrape complete")
	return result, nil
}

// scrapeWithRetriesPlaywright attempts scrape with 3 different strategies
func (s *Service) scrapeWithRetriesPlaywright(params engineapi.GetV1ScrapeParams) (*engineapi.ScrapeResponse, error) {
	strategies := GetAllStrategies()
	var lastErr error

	for i, strategy := range strategies {
		s.log.Info().Str("url", params.Url).Int("attempt", i+1).Str("strategy", string(strategy)).Msg("attempt playwright scrape")

		result, err := s.scrapeWithPlaywright(params, strategy)
		if err == nil && !s.isCloudflareBlocked(result) {
			s.log.Info().Str("url", params.Url).Str("strategy", string(strategy)).Msg("playwright scrape succeeded")
			return result, nil
		}

		if err != nil {
			lastErr = err
			s.log.Info().Str("url", params.Url).Str("strategy", string(strategy)).Str("error", err.Error()).Msg("playwright scrape attempt failed")
		} else {
			lastErr = fmt.Errorf("cloudflare challenge detected")
			s.log.Info().Str("url", params.Url).Str("strategy", string(strategy)).Int("status", intVal(result.Metadata.StatusCode)).Msg("cloudflare detected")
		}

		// Longer delay between Playwright retries
		if i < len(strategies)-1 {
			time.Sleep(time.Duration(2000+rand.Intn(2000)) * time.Millisecond)
		}
	}

	return nil, fmt.Errorf("all strategies exhausted: %w", lastErr)
}

func (s *Service) scrapeWithPlaywright(params engineapi.GetV1ScrapeParams, strategy HeaderStrategy) (*engineapi.ScrapeResponse, error) {

	pw, err := playwright.Run()
	if err != nil {
		return nil, fmt.Errorf("playwright run: %w", err)
	}
	browser, err := pw.Chromium.Launch(playwright.BrowserTypeLaunchOptions{
		Headless: playwright.Bool(true),
		Args: []string{
			"--no-sandbox",
			"--disable-dev-shm-usage",
			"--disable-blink-features=AutomationControlled",
			"--disable-web-security",
			"--disable-features=VizDisplayCompositor",
			"--no-first-run",
			"--disable-default-apps",
			"--disable-extensions",
		},
	})
	if err != nil {
		_ = pw.Stop()
		return nil, fmt.Errorf("launch: %w", err)
	}
	defer pw.Stop()
	defer browser.Close()

	// Get header profile for strategy
	profile := GetHeaderProfile(strategy)

	// Override with user-provided agent if available
	userAgent := profile.UserAgent
	if params.UserAgent != nil && *params.UserAgent != "" {
		userAgent = *params.UserAgent
	}

	// Build headers from profile
	headers := map[string]string{
		"Accept":                    profile.Accept,
		"Accept-Language":           profile.AcceptLanguage,
		"Accept-Encoding":           profile.AcceptEncoding,
		"Upgrade-Insecure-Requests": "1",
	}

	if profile.SecFetchDest != "" {
		headers["Sec-Fetch-Dest"] = profile.SecFetchDest
		headers["Sec-Fetch-Mode"] = profile.SecFetchMode
		headers["Sec-Fetch-Site"] = profile.SecFetchSite
		if profile.SecFetchUser != "" {
			headers["Sec-Fetch-User"] = profile.SecFetchUser
		}
	}

	if profile.SecChUa != "" {
		headers["Sec-Ch-Ua"] = profile.SecChUa
		headers["Sec-Ch-Ua-Mobile"] = profile.SecChUaMobile
		headers["Sec-Ch-Ua-Platform"] = profile.SecChUaPlatform
	}

	ctx, err := browser.NewContext(playwright.BrowserNewContextOptions{
		UserAgent:        playwright.String(userAgent),
		ExtraHttpHeaders: headers,
	})
	if err != nil {
		return nil, err
	}
	page, err := ctx.NewPage()
	if err != nil {
		return nil, err
	}

	var resp playwright.Response
	resp, navErr := page.Goto(params.Url, playwright.PageGotoOptions{WaitUntil: playwright.WaitUntilStateDomcontentloaded, Timeout: playwright.Float(10000)})
	if navErr != nil {
		// fallback to full load longer timeout
		resp, navErr = page.Goto(params.Url, playwright.PageGotoOptions{WaitUntil: playwright.WaitUntilStateLoad, Timeout: playwright.Float(20000)})
		if navErr != nil {
			return nil, fmt.Errorf("goto failed: %w", navErr)
		}
	}

	// For dynamic content, wait for content to appear using provided selectors or defaults
	waitSelectors := []string{}
	if params.WaitForSelectors != nil {
		waitSelectors = *params.WaitForSelectors
	}

	jsRendered := s.waitForDynamicContent(page, params.Url, waitSelectors)
	if !jsRendered {
		s.log.LogWarnf("JavaScript content may not have fully rendered for %s", params.Url)
		// Continue anyway - we have some content, even if not ideal
	}
	content, err := page.Content()
	if err != nil {
		return nil, err
	}
	titleStr, _ := page.Title()

	md := markdown.ConvertHTMLToMarkdown(content)

	var out string

	format := engineapi.GetV1ScrapeParamsFormat("markdown")
	if params.Format != nil {
		format = *params.Format
	}

	includeHTML := false
	if params.IncludeHtml != nil {
		includeHTML = *params.IncludeHtml
	}

	// Always use markdown format
	out = s.cleanContent(md)
	if !strings.HasSuffix(out, "\n\n") {
		// ensure exactly two newlines at end
		out = strings.TrimRight(out, "\n") + "\n\n"
	}

	status := 200
	if resp != nil {
		status = resp.Status()
	}
	meta := buildMetadataFromHTML(content, params.Url, status)

    // Always extract and include links
    // Prefer extracting from the live DOM (after JS) and fall back to HTML regex
    links := s.extractLinksFromDOM(page)
    if len(links) == 0 {
        links = extractLinksFromHTML(content, params.Url)
    }
	discovered := len(links)

	result := &engineapi.ScrapeResponse{
		Success:    true,
		Url:        params.Url,
		Content:    &out,
		Title:      &titleStr,
		Links:      links,
		Discovered: &discovered,
		Metadata:   meta,
	}

	// Include HTML if requested
	if includeHTML {
		htmlContent := strings.TrimSpace(content)
		result.Html = &htmlContent
	}

	return result, nil
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
	// Use case-insensitive regex to match title tags
	titleRe := regexp.MustCompile(`(?i)<title[^>]*>(.*?)</title>`)
	matches := titleRe.FindStringSubmatch(htmlContent)
	if len(matches) < 2 {
		return ""
	}
	// Decode HTML entities and clean up the title
	title := strings.TrimSpace(matches[1])
	// Basic HTML entity decoding
	title = strings.ReplaceAll(title, "&lt;", "<")
	title = strings.ReplaceAll(title, "&gt;", ">")
	title = strings.ReplaceAll(title, "&amp;", "&")
	title = strings.ReplaceAll(title, "&quot;", `"`)
	title = strings.ReplaceAll(title, "&#39;", "'")
	return title
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

// extractLinksFromDOM extracts links from the rendered DOM using Playwright
func (s *Service) extractLinksFromDOM(page playwright.Page) []string {
    if page == nil {
        return nil
    }
    result, err := page.Evaluate(`() => {
        const anchors = Array.from(document.querySelectorAll('a[href]'));
        const out = new Set();
        const absolutize = (href) => {
            try {
                return new URL(href, document.baseURI).toString();
            } catch (_) { return null; }
        };
        for (const a of anchors) {
            const href = a.getAttribute('href') || '';
            if (!href) continue;
            if (href.startsWith('javascript:') || href.startsWith('mailto:') || href.startsWith('#')) continue;
            const abs = absolutize(href);
            if (abs && (abs.startsWith('http://') || abs.startsWith('https://'))) out.add(abs);
        }
        return Array.from(out);
    }`)
    if err != nil {
        return nil
    }
    arr, ok := result.([]interface{})
    if !ok || len(arr) == 0 {
        return nil
    }
    links := make([]string, 0, len(arr))
    for _, v := range arr {
        if s, ok := v.(string); ok && s != "" {
            links = append(links, s)
        }
    }
    // dedupe
    seen := make(map[string]bool)
    uniq := make([]string, 0, len(links))
    for _, l := range links {
        if !seen[l] {
            seen[l] = true
            uniq = append(uniq, l)
        }
    }
    return uniq
}

// extractPageMetadataFromHTML parses common metadata from an HTML string into a flat map
func extractPageMetadataFromHTML(htmlString string, url string) map[string]interface{} {
	out := make(map[string]interface{})
	out["url"] = url
	t := extractTitle(htmlString)
	if strings.TrimSpace(t) != "" {
		out["title"] = strings.TrimSpace(t)
	}
	// Basic meta extraction (name/property + content)
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
	// TTL: 15 minutes for all Playwright scrapes
	ttl := 900
	_ = s.redis.CacheSet(ctx, key, res, ttl)
}

func (s *Service) generateCacheKey(params engineapi.GetV1ScrapeParams) string {
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
	return fmt.Sprintf("scrape:%s:%s:%s", safeURL, format, includeHtml)
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

	if sc == 404 {
		return false
	}
	if len(content) < 10 {
		return false
	}
	return true
}

// ContentSignature represents the essential state of page content for comparison
type ContentSignature struct {
	TextLength          int    // Raw text content length
	ElementCount        int    // Total DOM elements
	LinkCount           int    // Number of links (common JS target)
	AsyncLoadIndicators int    // Loading spinners, skeletons etc.
	ContentHash         string // Simple hash for change detection
}

// ScoreDelta calculates how much this signature improved from an initial state
func (cs *ContentSignature) ScoreDelta(initial *ContentSignature) float64 {
	score := 0.0

	// Text growth (primary signal) - 30%+ growth = good
	if initial.TextLength > 100 { // Avoid division by tiny numbers
		textGrowth := float64(cs.TextLength-initial.TextLength) / float64(initial.TextLength)
		score += textGrowth * 100 // Convert to percentage points
	} else if cs.TextLength > 200 { // New content on empty page
		score += 50
	}

	// Element growth (secondary signal) - DOM nodes added
	if initial.ElementCount > 10 {
		elementGrowth := float64(cs.ElementCount-initial.ElementCount) / float64(initial.ElementCount)
		score += elementGrowth * 30 // Lower weight than text
	} else if cs.ElementCount > 50 { // Elements on empty page
		score += 20
	}

	// Loading indicators resolved (tertiary signal)
	loadingReduced := initial.AsyncLoadIndicators - cs.AsyncLoadIndicators
	if loadingReduced > 0 {
		score += float64(loadingReduced) * 10 // 10 points per resolved indicator
	}

	return score
}

// waitForDynamicContent waits for JavaScript to render meaningful content with validation
func (s *Service) waitForDynamicContent(page playwright.Page, url string, waitSelectors []string) bool {
	s.log.LogDebugf("Starting dynamic content validation for %s", url)

	// Get initial content signature for comparison
	initialSignature, err := s.getContentSignature(page)
	if err != nil {
		s.log.LogWarnf("Failed to get initial content signature: %v", err)
		return false
	}

	s.log.LogDebugf("Initial content state: %d chars, %d elements, %d links",
		initialSignature.TextLength, initialSignature.ElementCount, initialSignature.LinkCount)

	// Run all strategies in parallel and get the best result
	finalSignature := s.attemptDynamicContentWait(page, waitSelectors, initialSignature)

	contentChanged := s.hasSignificantContentChange(initialSignature, finalSignature, 0.2)

	s.log.LogDebugf("Content change analysis for %s: initial=%d chars, final=%d chars, changed=%v",
		url, initialSignature.TextLength, finalSignature.TextLength, contentChanged)

	if contentChanged {
		s.log.LogSuccessf("JavaScript rendered new content for %s: %d->%d chars, %d->%d elements",
			url, initialSignature.TextLength, finalSignature.TextLength,
			initialSignature.ElementCount, finalSignature.ElementCount)
		return true
	}

	s.log.LogWarnf("No significant content change detected for %s", url)
	return false
}

// StrategyResult represents the outcome of a waiting strategy
type StrategyResult struct {
	Name      string
	Success   bool
	Signature *ContentSignature
	Duration  time.Duration
}

// attemptDynamicContentWait runs all strategies in parallel and returns the best result
func (s *Service) attemptDynamicContentWait(page playwright.Page, waitSelectors []string, initialSignature *ContentSignature) *ContentSignature {
	const (
		maxTotalTimeout   = 8000
		defaultSelectorTO = 3000
		defaultIdleTO     = 7000
		defaultLoaderTO   = 4000
	)

	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond*time.Duration(maxTotalTimeout))
	defer cancel()

	var wg sync.WaitGroup
	results := make(chan StrategyResult, 3) // Buffer for all strategy results

	// Strategy 1: Custom selectors
	if len(waitSelectors) > 0 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			start := time.Now()

			for _, selector := range waitSelectors {
				select {
				case <-ctx.Done():
					return
				default:
					locator := page.Locator(selector)
					if err := locator.WaitFor(playwright.LocatorWaitForOptions{
						State:   playwright.WaitForSelectorStateVisible,
						Timeout: playwright.Float(defaultSelectorTO),
					}); err == nil {
						signature, _ := s.getContentSignature(page)
						results <- StrategyResult{
							Name:      fmt.Sprintf("custom selector '%s'", selector),
							Success:   true,
							Signature: signature,
							Duration:  time.Since(start),
						}
						return
					}
				}
			}

			// Custom selectors failed
			results <- StrategyResult{
				Name:     "custom selectors",
				Success:  false,
				Duration: time.Since(start),
			}
		}()
	}

	// Strategy 2: Network idle
	wg.Add(1)
	go func() {
		defer wg.Done()
		start := time.Now()

		select {
		case <-ctx.Done():
			return
		default:
			if err := page.WaitForLoadState(playwright.PageWaitForLoadStateOptions{
				State:   playwright.LoadStateNetworkidle,
				Timeout: playwright.Float(defaultIdleTO),
			}); err == nil {
				signature, _ := s.getContentSignature(page)
				results <- StrategyResult{
					Name:      "network idle",
					Success:   true,
					Signature: signature,
					Duration:  time.Since(start),
				}
			} else {
				results <- StrategyResult{
					Name:     "network idle",
					Success:  false,
					Duration: time.Since(start),
				}
			}
		}
	}()

	// Strategy 3: Loading indicators disappear
	wg.Add(1)
	go func() {
		defer wg.Done()
		start := time.Now()

		loadingSelectors := []string{".loading", ".spinner", "[data-loading]", ".loader", ".skeleton"}
		allCleared := true

		for _, selector := range loadingSelectors {
			select {
			case <-ctx.Done():
				return
			default:
				locator := page.Locator(selector)
				if err := locator.WaitFor(playwright.LocatorWaitForOptions{
					State:   playwright.WaitForSelectorStateHidden,
					Timeout: playwright.Float(defaultLoaderTO),
				}); err != nil {
					allCleared = false
				}
			}
		}

		signature, _ := s.getContentSignature(page)
		results <- StrategyResult{
			Name:      "loading indicators cleared",
			Success:   allCleared,
			Signature: signature,
			Duration:  time.Since(start),
		}
	}()

	// Wait for all strategies to complete or timeout
	go func() {
		wg.Wait()
		close(results)
	}()

	// Collect all results
	var allResults []StrategyResult
	for result := range results {
		allResults = append(allResults, result)
		s.log.LogDebugf("Strategy '%s' completed: success=%v, duration=%v", result.Name, result.Success, result.Duration)
	}

	// Find the BEST result (most content change from initial)
	bestResult := s.chooseBestResult(allResults, initialSignature)

	if bestResult != nil {
		s.log.LogDebugf("Best strategy: '%s' with most significant content change", bestResult.Name)
		return bestResult.Signature
	}

	s.log.LogDebugf("No strategies produced significant content changes")
	return initialSignature // Return initial if no improvement
}

// chooseBestResult selects the strategy result with the most significant content change
func (s *Service) chooseBestResult(results []StrategyResult, initial *ContentSignature) *StrategyResult {
	var best *StrategyResult
	bestScore := -1.0

	for i := range results {
		r := &results[i]
		if !r.Success || r.Signature == nil {
			continue
		}

		score := r.Signature.ScoreDelta(initial)
		s.log.LogDebugf("Strategy %q scored %.2f (text: %d→%d, elems: %d→%d)",
			r.Name, score,
			initial.TextLength, r.Signature.TextLength,
			initial.ElementCount, r.Signature.ElementCount,
		)

		if score > bestScore {
			best = r
			bestScore = score
		}
	}
	return best
}

// getContentSignature captures essential page state for pragmatic JS rendering detection
func (s *Service) getContentSignature(page playwright.Page) (*ContentSignature, error) {
	result, err := page.Evaluate(`() => {
		// Get visible text content (excludes scripts, styles, hidden elements)
		const walker = document.createTreeWalker(
			document.body,
			NodeFilter.SHOW_TEXT,
			{
				acceptNode: function(node) {
					const element = node.parentElement;
					if (!element) return NodeFilter.FILTER_REJECT;
					
					// Skip hidden elements and non-content elements
					const style = window.getComputedStyle(element);
					if (style.display === 'none' || style.visibility === 'hidden') {
						return NodeFilter.FILTER_REJECT;
					}
					
					// Skip script and style tags
					const tagName = element.tagName.toLowerCase();
					if (tagName === 'script' || tagName === 'style' || tagName === 'noscript') {
						return NodeFilter.FILTER_REJECT;
					}
					
					return NodeFilter.FILTER_ACCEPT;
				}
			}
		);
		
		let visibleText = '';
		let node;
		while (node = walker.nextNode()) {
			visibleText += node.textContent;
		}
		
		// Count only visible elements (not scripts, styles, hidden)
		const visibleElements = Array.from(document.querySelectorAll('*')).filter(el => {
			const style = window.getComputedStyle(el);
			const tagName = el.tagName.toLowerCase();
			return style.display !== 'none' && 
				   style.visibility !== 'hidden' && 
				   !['script', 'style', 'noscript', 'meta', 'link', 'title'].includes(tagName);
		});
		
		const links = document.querySelectorAll('a[href]');
		
		// Loading indicators that should disappear when content is ready
		const loadingIndicators = document.querySelectorAll([
			'.loading', '.spinner', '.skeleton', '.placeholder', '.loader',
			'[data-loading]', '[data-lazy]', '[aria-busy="true"]', '.shimmer'
		].join(', '));
		
		// Simple hash for change detection
		let hash = 0;
		for (let i = 0; i < visibleText.length; i++) {
			const char = visibleText.charCodeAt(i);
			hash = ((hash << 5) - hash) + char;
			hash = hash & hash;
		}
		
		return {
			textLength: visibleText.length,
			elementCount: visibleElements.length,
			linkCount: links.length,
			asyncLoadIndicators: loadingIndicators.length,
			contentHash: hash.toString()
		};
	}`)

	if err != nil {
		return nil, fmt.Errorf("failed to evaluate content signature: %w", err)
	}

	// Convert result to our struct
	data, ok := result.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("unexpected result type from content signature evaluation")
	}

	// Simple helper to safely convert JavaScript numbers to int
	toInt := func(v interface{}) int {
		switch val := v.(type) {
		case float64:
			return int(val)
		case int:
			return val
		default:
			return 0
		}
	}

	return &ContentSignature{
		TextLength:          toInt(data["textLength"]),
		ElementCount:        toInt(data["elementCount"]),
		LinkCount:           toInt(data["linkCount"]),
		AsyncLoadIndicators: toInt(data["asyncLoadIndicators"]),
		ContentHash:         data["contentHash"].(string),
	}, nil
}

// hasSignificantContentChange uses simple, pragmatic heuristics to detect JS rendering
func (s *Service) hasSignificantContentChange(initial, final *ContentSignature, minChangeRatio float64) bool {
	s.log.LogDebugf("Content comparison: initial={text:%d, elements:%d, links:%d, loading:%d}, final={text:%d, elements:%d, links:%d, loading:%d}",
		initial.TextLength, initial.ElementCount, initial.LinkCount, initial.AsyncLoadIndicators,
		final.TextLength, final.ElementCount, final.LinkCount, final.AsyncLoadIndicators)

	// === SIMPLE, RELIABLE HEURISTICS ===

	reasons := []string{}
	hasChange := false

	// 1. Text content growth (30% more text)
	if initial.TextLength > 0 {
		textGrowth := float64(final.TextLength-initial.TextLength) / float64(initial.TextLength)
		if textGrowth > 0.3 {
			hasChange = true
			reasons = append(reasons, fmt.Sprintf("text grew by %.1f%%", textGrowth*100))
		}
	} else if final.TextLength > 200 {
		hasChange = true
		reasons = append(reasons, "content appeared on empty page")
	}

	// 2. Element count growth (50+ new elements)
	elementGrowth := final.ElementCount - initial.ElementCount
	if elementGrowth > 50 {
		hasChange = true
		reasons = append(reasons, fmt.Sprintf("%d new elements", elementGrowth))
	}

	// 3. Loading indicators disappeared (good sign)
	loadingReduction := initial.AsyncLoadIndicators - final.AsyncLoadIndicators
	if loadingReduction > 0 {
		hasChange = true
		reasons = append(reasons, fmt.Sprintf("%d loading indicators resolved", loadingReduction))
	}

	// 4. Links appeared (navigation/content)
	linkGrowth := final.LinkCount - initial.LinkCount
	if linkGrowth > 5 {
		hasChange = true
		reasons = append(reasons, fmt.Sprintf("%d new links", linkGrowth))
	}

	// 5. Content hash changed (fallback check)
	if initial.ContentHash != final.ContentHash && final.TextLength > initial.TextLength+100 {
		hasChange = true
		reasons = append(reasons, "content hash changed with substantial text")
	}

	if hasChange {
		s.log.LogSuccessf("JS rendered new content: %s", strings.Join(reasons, ", "))
	} else {
		s.log.LogWarnf("No significant content change detected")
	}

	return hasChange
}

// isCloudflareBlocked detects if the response is a Cloudflare challenge page
func (s *Service) isCloudflareBlocked(result *engineapi.ScrapeResponse) bool {
	if result == nil {
		return false
	}

	// Check status code
	if result.Metadata.StatusCode != nil && *result.Metadata.StatusCode == 403 {
		// Check title for Cloudflare indicators
		if result.Title != nil {
			title := *result.Title
			if strings.Contains(title, "Just a moment") ||
				strings.Contains(title, "Checking your browser") ||
				strings.Contains(title, "Attention Required") {
				return true
			}
		}

		// Check content for Cloudflare indicators
		if result.Content != nil {
			content := *result.Content
			if strings.Contains(content, "Waiting for") && strings.Contains(content, "to respond") {
				return true
			}
			if strings.Contains(content, "Cloudflare") && strings.Contains(content, "Ray ID") {
				return true
			}
		}
	}

	return false
}
