package crawl

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"scraper/internal/config"
	"scraper/internal/core/job"
	"scraper/internal/core/mapper"
	"scraper/internal/core/scrape"
	"scraper/internal/logger"
	"scraper/internal/platform/engineapi"
	tasks "scraper/internal/platform/tasks"
	"scraper/internal/utils/markdown"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
)

type CrawlService struct {
	job    *job.JobService
	tasks  *tasks.Client
	mapper *mapper.Service
	scrape *scrape.Service
	log    *logger.Logger
	config config.Config
}

func NewCrawlService(job *job.JobService, tasks *tasks.Client, mapper *mapper.Service, scrape *scrape.Service, cfg config.Config) *CrawlService {
	return &CrawlService{job: job, tasks: tasks, mapper: mapper, scrape: scrape, log: logger.New("CrawlService"), config: cfg}
}

// StreamCrawlToChannel performs streaming crawl and publishes each page to the provided channel
func (s *CrawlService) StreamCrawlToChannel(ctx context.Context, req engineapi.CrawlCreateRequest, pageChan chan<- *PageResult) {
	defer close(pageChan)

	linkLimit := 0
	if req.LinkLimit != nil {
		linkLimit = *req.LinkLimit
	}
	depth := 1
	if req.Depth != nil {
		depth = *req.Depth
	}
	includeHTML := false
	if req.IncludeHtml != nil {
		includeHTML = *req.IncludeHtml
	}
	renderJs := false
	if req.RenderJs != nil {
		renderJs = *req.RenderJs
	}
	includeSubs := false
	if req.IncludeSubdomains != nil {
		includeSubs = *req.IncludeSubdomains
	}

	fresh := false
	if req.Fresh != nil {
		fresh = *req.Fresh
	}

	// Extract patterns from request
	patterns := []string{}
	if req.Patterns != nil {
		patterns = *req.Patterns
	}

	// Extract user agent from request
	var userAgent *string
	if req.UserAgent != nil && *req.UserAgent != "" {
		userAgent = req.UserAgent
		s.log.LogDebugf("Using custom user agent: %s", *userAgent)
	}

	// Extract wait selectors from request
	var waitSelectors *[]string
	if req.WaitForSelectors != nil && len(*req.WaitForSelectors) > 0 {
		waitSelectors = req.WaitForSelectors
		s.log.LogDebugf("Using custom wait selectors: %v", *waitSelectors)
	}

	processed := make(map[string]struct{})
	var mu sync.Mutex

	// Track successful pages separately for proper limit enforcement
	successCount := 0

	// Scrape starting URL first, but only if it matches patterns
	if matchesPattern(req.Url, patterns) {
		if data, err := s.scrapeWithOptions(ctx, req.Url, includeHTML, renderJs, fresh, userAgent, waitSelectors); err != nil {
			s.log.LogWarnf("start url scrape failed %s: %v", req.Url, err)
			pageChan <- &PageResult{
				URL:   req.Url,
				Error: err.Error(),
			}
		} else if data != nil {
			content := ""
			if data.Content != nil {
				content = *data.Content
			}
			title := ""
			if data.Title != nil {
				title = *data.Title
			}

			cleanContent := cleanContentForJSON(content)

			links := data.Links

			pageContent := engineapi.PageContent{
				Markdown: cleanContent,
				Links:    links,
				Metadata: buildPageMetadataFromScrapeMetadata(data.Metadata, title),
			}
			if includeHTML && data.Html != nil {
				pageContent.Html = data.Html
			}

			pageChan <- &PageResult{
				URL:         req.Url,
				PageContent: &pageContent,
			}

			mu.Lock()
			processed[req.Url] = struct{}{}
			successCount = 1 // Start URL was successful
			mu.Unlock()
		}
	}

	// Setup streaming mapper
	linksCh := make(chan string, 256)
	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	go func() {
		s.log.LogDebugf("Starting mapper with linkLimit=%d for URL=%s", linkLimit, req.Url)

		// First try the mapper
		mapperFoundLinks := 0
		tempLinks := make([]string, 0)

		// Collect all mapper results first
		mapperCh := make(chan string, 256)
		go func() {
			defer close(mapperCh)
			_ = s.mapper.MapLinksStream(streamCtx, mapper.Request{URL: req.Url, Depth: depth, LinkLimit: linkLimit, IncludeSubdomains: includeSubs, Patterns: patterns}, mapperCh)
		}()

		// Collect mapper results
		for link := range mapperCh {
			tempLinks = append(tempLinks, link)
			mapperFoundLinks++
		}

		s.log.LogDebugf("Mapper found %d links", mapperFoundLinks)

		// If mapper found very few links, try scrape service fallback
		s.log.LogDebugf("Checking fallback conditions: mapperFound=%d < 3 AND linkLimit=%d > 3 = %v",
			mapperFoundLinks, linkLimit, mapperFoundLinks < 3 && linkLimit > 3)
		if mapperFoundLinks < 3 && linkLimit > 3 {
			s.log.LogInfof("Mapper found only %d links, trying scrape service fallback for dynamic content", mapperFoundLinks)

			// Use scrape service to get links from the main page
			if additionalLinks := s.getLinks(streamCtx, req.Url, renderJs, userAgent, waitSelectors); len(additionalLinks) > 0 {
				s.log.LogInfof("Scrape service found %d additional links", len(additionalLinks))

				// Filter and add additional links
				for _, link := range additionalLinks {
					if matchesPattern(link, patterns) && len(tempLinks) < linkLimit {
						// Avoid duplicates
						found := false
						for _, existing := range tempLinks {
							if existing == link {
								found = true
								break
							}
						}
						if !found {
							tempLinks = append(tempLinks, link)
						}
					}
				}
			}
		}

		// Send all collected links to the channel
		for _, link := range tempLinks {
			select {
			case linksCh <- link:
			case <-streamCtx.Done():
				close(linksCh)
				return
			}
		}
		close(linksCh)
	}()

	// Worker pool for streaming
	maxWorkers := 10
	if renderJs {
		maxWorkers = 2
	}
	var wg sync.WaitGroup

	// Track reserved slots to prevent race conditions
	reservedSlots := 0

	accept := func(u string) bool {
		mu.Lock()
		defer mu.Unlock()
		if _, seen := processed[u]; seen {
			return false
		}
		// Only stop if we have enough successful pages - allow some over-reservation
		// This ensures we keep trying even if some reserved slots fail
		if linkLimit > 0 && successCount >= linkLimit {
			return false
		}
		// But prevent excessive over-processing (more than 2x the limit)
		if linkLimit > 0 && (successCount+reservedSlots) >= (linkLimit*2) {
			return false
		}
		processed[u] = struct{}{}
		reservedSlots++ // Reserve a slot for this URL
		return true
	}

	releaseSlot := func() {
		reservedSlots--
	}

	worker := func(id int) {
		defer wg.Done()
		for u := range linksCh {
			// Check if we should continue processing this URL
			shouldProcess := accept(u)
			if !shouldProcess {
				// Check if we've reached our success limit - if so, we can stop
				mu.Lock()
				reachedLimit := linkLimit > 0 && successCount >= linkLimit
				mu.Unlock()
				if reachedLimit {
					cancel()
					return
				}
				continue
			}

			res, err := s.scrapeWithOptions(ctx, u, includeHTML, renderJs, fresh, userAgent, waitSelectors)
			if err != nil {
				pageChan <- &PageResult{
					URL:   u,
					Error: err.Error(),
				}
				// If we failed but haven't reached our success limit, we should continue trying more URLs
				// So we don't count failures toward our processed limit
				mu.Lock()
				delete(processed, u)
				releaseSlot() // Release the reserved slot since we failed
				mu.Unlock()
			} else if res != nil {
				content := ""
				if res.Content != nil {
					content = *res.Content
				}
				title := ""
				if res.Title != nil {
					title = *res.Title
				}

				cleanContent := cleanContentForJSON(content)

				// Extract links from scraped data
				links := res.Links

				pageContent := engineapi.PageContent{
					Markdown: cleanContent,
					Links:    links,
					Metadata: buildPageMetadataFromScrapeMetadata(res.Metadata, title),
				}
				if includeHTML && res.Html != nil {
					pageContent.Html = res.Html
				}

				pageChan <- &PageResult{
					URL:         u,
					PageContent: &pageContent,
				}

				// Increment success count and check if we've reached our limit
				mu.Lock()
				successCount++
				// Don't release slot since we successfully used it
				if linkLimit > 0 && successCount >= linkLimit {
					s.log.LogDebugf("worker %d: reached success limit (%d/%d, reserved: %d), stopping", id, successCount, linkLimit, reservedSlots)
					mu.Unlock()
					cancel()
					return
				}
				mu.Unlock()
			} else {
				// If res is nil, remove from processed so we can try more
				mu.Lock()
				delete(processed, u)
				releaseSlot() // Release the reserved slot
				mu.Unlock()
			}
		}
	}

	for i := 0; i < maxWorkers; i++ {
		wg.Add(1)
		go worker(i + 1)
	}

	// Safety timeout
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Minute):
		s.log.LogWarnf("stream crawl timeout for %s", req.Url)
		cancel()
		<-done
	}
}

// PageResult represents a single page result for streaming
type PageResult struct {
	URL         string                 `json:"url"`
	PageContent *engineapi.PageContent `json:"page_content,omitempty"`
	Error       string                 `json:"error,omitempty"`
}

type CrawlTaskPayload struct {
	JobID   string                       `json:"job_id"`
	Request engineapi.CrawlCreateRequest `json:"request"`
}

func (s *CrawlService) Enqueue(ctx context.Context, req engineapi.CrawlCreateRequest) (string, error) {
	id := uuid.New().String()

	payload, _ := json.Marshal(CrawlTaskPayload{JobID: id, Request: req})
	if err := s.job.InitPending(ctx, id, job.TypeCrawl, req.Url); err != nil {
		return "", err
	}
	task := asynq.NewTask(tasks.TaskTypeCrawl, payload)
	if err := s.tasks.Enqueue(task, "default", s.config.TaskMaxRetries); err != nil {
		return "", err
	}
	s.log.LogInfof("enqueued crawl job %s for %s with max pages %d", id, req.Url, req.LinkLimit)
	return id, nil
}

func (s *CrawlService) HandleCrawlTask(ctx context.Context, task *asynq.Task) error {
	var p CrawlTaskPayload
	if err := json.Unmarshal(task.Payload(), &p); err != nil {
		return err
	}
	s.log.LogInfof("processing crawl job %s for %s", p.JobID, p.Request.Url)
	if err := s.job.SetProcessing(ctx, p.JobID, job.TypeCrawl); err != nil {
		return err
	}

	// Streamed crawl for faster time-to-first and strict limit control
	result, errs := s.streamCrawl(ctx, p.Request)

	// Truncate results if we exceeded the limit
	if result != nil && p.Request.LinkLimit != nil && len(*result) > *p.Request.LinkLimit {
		truncated := make(map[string]engineapi.PageContent)
		count := 0
		for url, content := range *result {
			if count >= *p.Request.LinkLimit {
				break
			}
			truncated[url] = content
			count++
		}
		result = &truncated
	}

	data := engineapi.CrawlJobData{}
	if p.Request.Url != "" {
		u := p.Request.Url
		data.Url = &u
	}
	if result != nil && len(*result) > 0 {
		data.CrawlData = result
	}
	if errs != nil && len(*errs) > 0 {
		data.ErrorData = errs
	}
	st := stats(result, errs)
	if st != nil {
		data.Statistics = st
	}

	s.log.LogInfof("completing crawl job %s: success=%d failed=%d total=%d (target was %d)", p.JobID, derefInt(st.SuccessfulPages), derefInt(st.FailedPages), derefInt(st.TotalPages), derefInt(p.Request.LinkLimit))

	// Complete in Redis first
	if err := s.job.Complete(ctx, p.JobID, job.TypeCrawl, job.StatusCompleted, data); err != nil {
		return err
	}

	// Send webhook notification if webhook URL is provided
	if p.Request.WebhookUrl != nil && *p.Request.WebhookUrl != "" {
		s.sendWebhookNotification(ctx, p.JobID, "completed", data, *p.Request.WebhookUrl, p.Request.WebhookHeaders)
	}

	return nil
}

// streamCrawl performs streamed mapping + scraping with a worker pool and strict link limit handling
func (s *CrawlService) streamCrawl(ctx context.Context, r engineapi.CrawlCreateRequest) (*map[string]engineapi.PageContent, *map[string]string) {
	out := make(map[string]engineapi.PageContent)
	errs := make(map[string]string)
	processed := make(map[string]struct{})
	var mu sync.Mutex

	linkLimit := 0
	if r.LinkLimit != nil {
		linkLimit = *r.LinkLimit
	}
	depth := 1
	if r.Depth != nil {
		depth = *r.Depth
	}
	includeHTML := false
	if r.IncludeHtml != nil {
		includeHTML = *r.IncludeHtml
	}
	renderJs := false
	if r.RenderJs != nil {
		renderJs = *r.RenderJs
	}
	includeSubs := false
	if r.IncludeSubdomains != nil {
		includeSubs = *r.IncludeSubdomains
	}

	// Check if fresh data is requested
	fresh := false
	if r.Fresh != nil {
		fresh = *r.Fresh
	}

	// Extract patterns from request
	patterns := []string{}
	if r.Patterns != nil {
		patterns = *r.Patterns
	}

	// Extract user agent from request
	var userAgent *string
	if r.UserAgent != nil && *r.UserAgent != "" {
		userAgent = r.UserAgent
		s.log.LogDebugf("Using custom user agent: %s", *userAgent)
	}

	// Extract wait selectors from request
	var waitSelectors *[]string
	if r.WaitForSelectors != nil && len(*r.WaitForSelectors) > 0 {
		waitSelectors = r.WaitForSelectors
		s.log.LogDebugf("Using custom wait selectors: %v", *waitSelectors)
	}

	// Scrape starting URL first, but only if it matches patterns
	if matchesPattern(r.Url, patterns) {
		if data, err := s.scrapeWithOptions(ctx, r.Url, includeHTML, renderJs, fresh, userAgent, waitSelectors); err != nil {
			s.log.LogWarnf("start url scrape failed %s: %v", r.Url, err)
			errs[r.Url] = err.Error()
		} else if data != nil {
			content := ""
			if data.Content != nil {
				content = *data.Content
			}
			title := ""
			if data.Title != nil {
				title = *data.Title
			}
			// Clean markdown content to ensure JSON compatibility
			cleanContent := cleanContentForJSON(content)

			// Extract links from scraped data
			links := data.Links

			pageContent := engineapi.PageContent{
				Markdown: cleanContent,
				Links:    links,
				Metadata: buildPageMetadataFromScrapeMetadata(data.Metadata, title),
			}
			// Include HTML if requested
			if includeHTML && data.Html != nil {
				pageContent.Html = data.Html
			}
			out[r.Url] = pageContent
		}
	}
	mu.Lock()
	processed[r.Url] = struct{}{}
	mu.Unlock()

	// Setup streaming mapper
	linksCh := make(chan string, 256)
	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	go func() {
		_ = s.mapper.MapLinksStream(streamCtx, mapper.Request{URL: r.Url, Depth: depth, LinkLimit: linkLimit, IncludeSubdomains: includeSubs, Patterns: patterns}, linksCh)
		close(linksCh)
	}()

	// Worker pool
	maxWorkers := 20
	if renderJs {
		maxWorkers = 2
	}
	var wg sync.WaitGroup

	// Track reserved slots to prevent race conditions
	reservedSlots := 0

	accept := func(u string) bool {
		mu.Lock()
		defer mu.Unlock()
		if _, seen := processed[u]; seen {
			return false
		}
		// Only stop if we have enough successful pages - allow some over-reservation
		// This ensures we keep trying even if some reserved slots fail
		if linkLimit > 0 && len(out) >= linkLimit {
			return false
		}
		// But prevent excessive over-processing (more than 2x the limit)
		if linkLimit > 0 && (len(out)+reservedSlots) >= (linkLimit*2) {
			return false
		}
		processed[u] = struct{}{}
		reservedSlots++ // Reserve a slot for this URL
		return true
	}

	releaseSlot := func() {
		reservedSlots--
	}

	worker := func(id int) {
		defer wg.Done()
		for u := range linksCh {
			// Check if we should continue processing this URL
			shouldProcess := accept(u)
			if !shouldProcess {
				// Check if we've reached our success limit - if so, we can stop
				mu.Lock()
				reachedLimit := linkLimit > 0 && len(out) >= linkLimit
				mu.Unlock()
				if reachedLimit {
					cancel()
					return
				}
				continue
			}

			res, err := s.scrapeWithOptions(ctx, u, includeHTML, renderJs, fresh, userAgent, waitSelectors)
			if err != nil {
				mu.Lock()
				errs[u] = err.Error()
				// If we failed but haven't reached our success limit, we should continue trying more URLs
				// So we don't count failures toward our processed limit
				delete(processed, u)
				releaseSlot() // Release the reserved slot since we failed
				mu.Unlock()
			} else if res != nil {
				content := ""
				if res.Content != nil {
					content = *res.Content
				}
				title := ""
				if res.Title != nil {
					title = *res.Title
				}
				mu.Lock()
				// Clean markdown content to ensure JSON compatibility
				cleanContent := cleanContentForJSON(content)

				// Extract links from scraped data
				links := res.Links

				pageContent := engineapi.PageContent{
					Markdown: cleanContent,
					Links:    links,
					Metadata: buildPageMetadataFromScrapeMetadata(res.Metadata, title),
				}
				// Include HTML if requested
				if includeHTML && res.Html != nil {
					pageContent.Html = res.Html
				}
				out[u] = pageContent
				// Don't release slot - we successfully used it

				// Check if we've reached our success limit
				if linkLimit > 0 && len(out) >= linkLimit {
					s.log.LogDebugf("worker %d: reached success limit (%d/%d, reserved: %d), stopping", id, len(out), linkLimit, reservedSlots)
					mu.Unlock()
					cancel()
					return
				}
				mu.Unlock()
			} else {
				// If res is nil (shouldn't happen but just in case), remove from processed so we can try more
				mu.Lock()
				delete(processed, u)
				releaseSlot() // Release the reserved slot
				mu.Unlock()
			}
		}
	}

	for i := 0; i < maxWorkers; i++ {
		wg.Add(1)
		go worker(i + 1)
	}

	// Safety timeout to avoid runaway
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Minute):
		s.log.LogWarnf("stream crawl timeout for %s", r.Url)
		cancel()
		<-done
	}
	return &out, &errs
}

// scrapeWithOptions handles scraping with all optional parameters: cache, user agent, and selectors
func (s *CrawlService) scrapeWithOptions(ctx context.Context, url string, includeHTML, renderJs, fresh bool, userAgent *string, waitSelectors *[]string) (*engineapi.ScrapeResponse, error) {
	if fresh {
		// Bypass cache and scrape fresh
		format := engineapi.GetV1ScrapeParamsFormat("markdown")
		if includeHTML {
			format = engineapi.GetV1ScrapeParamsFormat("html")
		}
		params := engineapi.GetV1ScrapeParams{Url: url, Format: &format, RenderJs: &renderJs, Fresh: &fresh, IncludeHtml: &includeHTML}

		// Set user agent if provided
		if userAgent != nil && *userAgent != "" {
			params.UserAgent = userAgent
		}

		// Set wait selectors if provided
		if waitSelectors != nil && len(*waitSelectors) > 0 {
			params.WaitForSelectors = waitSelectors
		}

		// Bot protection can be enabled via request parameters from backend
		return s.scrape.ScrapeURL(ctx, params)
	} else {
		// Use cache if available
		result, _, err := s.scrape.ScrapeWithCache(ctx, url, includeHTML, renderJs)
		return result, err
	}
}

// getLinks uses the scrape service to extract links from dynamic content with optional user agent and selectors
func (s *CrawlService) getLinks(ctx context.Context, url string, renderJs bool, userAgent *string, waitSelectors *[]string) []string {
	// Use scrape service with "links" format to extract all links
	format := engineapi.GetV1ScrapeParamsFormat("links")
	params := engineapi.GetV1ScrapeParams{
		Url:      url,
		Format:   &format,
		RenderJs: &renderJs,
	}

	// Set user agent if provided
	if userAgent != nil && *userAgent != "" {
		params.UserAgent = userAgent
		s.log.LogDebugf("Using custom user agent for link extraction: %s", *userAgent)
	}

	// Set wait selectors if provided
	if waitSelectors != nil && len(*waitSelectors) > 0 {
		params.WaitForSelectors = waitSelectors
		s.log.LogDebugf("Using custom wait selectors for link extraction: %v", *waitSelectors)
	}

	s.log.LogDebugf("Using scrape service to extract links from: %s", url)

	result, err := s.scrape.ScrapeURL(ctx, params)
	if err != nil {
		s.log.LogWarnf("Scrape service link extraction failed for %s: %v", url, err)
		return nil
	}

	if len(result.Links) == 0 {
		s.log.LogDebugf("No links found by scrape service for: %s", url)
		return nil
	}

	s.log.LogDebugf("Scrape service extracted %d links from: %s", len(result.Links), url)
	return result.Links
}

func stats(res *map[string]engineapi.PageContent, errs *map[string]string) *engineapi.CrawlStatistics {
	r := len(*res)
	e := len(*errs)
	t := r + e
	return &engineapi.CrawlStatistics{TotalPages: &t, SuccessfulPages: &r, FailedPages: &e}
}

// derefInt returns 0 if nil
func derefInt(v *int) int {
	if v == nil {
		return 0
	}
	return *v
}

// cleanContentForJSON ensures markdown content is safe for JSON serialization
func cleanContentForJSON(content string) string {
	if content == "" {
		return ""
	}
	// Apply the markdown cleaning which includes JSON escape fixes
	return markdown.CleanMarkdownBoilerplate(content)
}

// buildPageMetadataFromScrapeMetadata converts rich ScrapeMetadata to PageMetadata with all fields
func buildPageMetadataFromScrapeMetadata(scrapeMetadata engineapi.ScrapeMetadata, title string) engineapi.PageMetadata {
	meta := engineapi.PageMetadata{
		StatusCode: scrapeMetadata.StatusCode,
	}

	// Use title from parameter if available, otherwise from metadata
	if title != "" {
		meta.Title = &title
	} else if scrapeMetadata.Title != nil {
		meta.Title = scrapeMetadata.Title
	}

	// Map all the rich metadata fields from ScrapeMetadata to PageMetadata
	if scrapeMetadata.Description != nil {
		meta.Description = scrapeMetadata.Description
	}
	if scrapeMetadata.Language != nil {
		meta.Language = scrapeMetadata.Language
	}
	if scrapeMetadata.Canonical != nil {
		meta.Canonical = scrapeMetadata.Canonical
	}
	if scrapeMetadata.Favicon != nil {
		meta.Favicon = scrapeMetadata.Favicon
	}
	if scrapeMetadata.OgTitle != nil {
		meta.OgTitle = scrapeMetadata.OgTitle
	}
	if scrapeMetadata.OgDescription != nil {
		meta.OgDescription = scrapeMetadata.OgDescription
	}
	if scrapeMetadata.OgImage != nil {
		meta.OgImage = scrapeMetadata.OgImage
	}
	if scrapeMetadata.OgSiteName != nil {
		meta.OgSiteName = scrapeMetadata.OgSiteName
	}
	if scrapeMetadata.TwitterTitle != nil {
		meta.TwitterTitle = scrapeMetadata.TwitterTitle
	}
	if scrapeMetadata.TwitterDescription != nil {
		meta.TwitterDescription = scrapeMetadata.TwitterDescription
	}
	if scrapeMetadata.TwitterImage != nil {
		meta.TwitterImage = scrapeMetadata.TwitterImage
	}
	if scrapeMetadata.SourceUrl != nil {
		meta.SourceUrl = scrapeMetadata.SourceUrl
	}

	return meta
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

// sendWebhookNotification sends a webhook notification to the provided URL
func (s *CrawlService) sendWebhookNotification(ctx context.Context, jobID, status string, data engineapi.CrawlJobData, webhookURL string, headers *map[string]string) {
	s.log.LogInfof("Sending webhook notification for job %s to %s", jobID, webhookURL)

	// Prepare webhook payload
	payload := map[string]interface{}{
		"job_id": jobID,
		"type":   "crawl",
		"status": status,
		"data":   data,
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		s.log.LogErrorf("Failed to marshal webhook payload for job %s: %v", jobID, err)
		return
	}

	// Create HTTP request
	req, err := http.NewRequestWithContext(ctx, "POST", webhookURL, bytes.NewBuffer(payloadBytes))
	if err != nil {
		s.log.LogErrorf("Failed to create webhook request for job %s: %v", jobID, err)
		return
	}

	// Set default headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Supacrawler-Engine/1.0")
	req.Header.Set("X-Supacrawler-Event", "crawl.completed")
	req.Header.Set("X-Supacrawler-Job-ID", jobID)

	// Add HMAC authentication headers for system auth
	if s.config.SystemAuthSecret != "" {
		timestamp := strconv.FormatInt(time.Now().Unix(), 10)
		signature := s.generateHMACSignature(timestamp, payloadBytes)

		req.Header.Set("X-System-Timestamp", timestamp)
		req.Header.Set("X-System-Signature", signature)
		s.log.LogDebugf("Added HMAC auth headers for webhook: timestamp=%s", timestamp)
	} else {
		s.log.LogWarnf("System auth secret not configured, webhook may fail authentication")
	}

	// Add custom headers if provided
	if headers != nil {
		for key, value := range *headers {
			req.Header.Set(key, value)
		}
	}

	// Send request with timeout
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		s.log.LogWarnf("Failed to send webhook for job %s to %s: %v", jobID, webhookURL, err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		s.log.LogInfof("Successfully sent webhook for job %s to %s (status: %d)", jobID, webhookURL, resp.StatusCode)
	} else {
		s.log.LogWarnf("Webhook returned status %d for job %s to %s", resp.StatusCode, jobID, webhookURL)
	}
}

// generateHMACSignature creates HMAC signature for system authentication
func (s *CrawlService) generateHMACSignature(timestamp string, payload []byte) string {
	// Create payload: timestamp + body (same format as backend expects)
	signaturePayload := timestamp + string(payload)

	// Calculate HMAC
	h := hmac.New(sha256.New, []byte(s.config.SystemAuthSecret))
	h.Write([]byte(signaturePayload))
	signature := hex.EncodeToString(h.Sum(nil))

	s.log.LogDebugf("Generated HMAC signature for payload length %d", len(signaturePayload))
	return signature
}
