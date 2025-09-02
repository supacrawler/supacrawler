package crawl

import (
	"context"
	"encoding/json"
	"sync"
	"time"

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

const TaskTypeCrawl = "crawl:task"

type CrawlService struct {
	job    *job.JobService
	tasks  *tasks.Client
	mapper *mapper.Service
	scrape *scrape.Service
	log    *logger.Logger
}

func NewCrawlService(job *job.JobService, tasks *tasks.Client, mapper *mapper.Service, scrape *scrape.Service) *CrawlService {
	return &CrawlService{job: job, tasks: tasks, mapper: mapper, scrape: scrape, log: logger.New("CrawlService")}
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
	task := asynq.NewTask(TaskTypeCrawl, payload)
	if err := s.tasks.Enqueue(task, "default", 10); err != nil {
		return "", err
	}
	s.log.LogInfof("enqueued crawl job %s for %s", id, req.Url)
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

	s.log.LogInfof("completing crawl job %s: success=%d failed=%d total=%d", p.JobID, derefInt(st.SuccessfulPages), derefInt(st.FailedPages), derefInt(st.TotalPages))
	return s.job.Complete(ctx, p.JobID, job.TypeCrawl, job.StatusCompleted, data)
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

	// Scrape starting URL first
	if data, err := s.scrapeWithFreshOption(ctx, r.Url, includeHTML, renderJs, fresh); err != nil {
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
		pageContent := engineapi.PageContent{
			Markdown: cleanContent,
			Metadata: buildPageMetadataFromScrapeMetadata(data.Metadata, title),
		}
		// Include HTML if requested
		if includeHTML && data.Html != nil {
			pageContent.Html = data.Html
		}
		out[r.Url] = pageContent
		mu.Lock()
		processed[r.Url] = struct{}{}
		mu.Unlock()
	}

	// Setup streaming mapper
	linksCh := make(chan string, 256)
	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	go func() {
		_ = s.mapper.MapLinksStream(streamCtx, mapper.Request{URL: r.Url, Depth: depth, LinkLimit: 0, IncludeSubdomains: includeSubs}, linksCh)
		close(linksCh)
	}()

	// Worker pool
	maxWorkers := 10
	if renderJs {
		maxWorkers = 2
	}
	var wg sync.WaitGroup

	accept := func(u string) bool {
		mu.Lock()
		defer mu.Unlock()
		if _, seen := processed[u]; seen {
			return false
		}
		if linkLimit > 0 && (len(out)+len(errs)) >= linkLimit {
			return false
		}
		processed[u] = struct{}{}
		return true
	}

	worker := func(id int) {
		defer wg.Done()
		for u := range linksCh {
			if !accept(u) {
				// Stop early if we've hit the cap
				if linkLimit > 0 && (len(out)+len(errs)) >= linkLimit {
					cancel()
					return
				}
				continue
			}
			res, err := s.scrapeWithFreshOption(ctx, u, includeHTML, renderJs, fresh)
			if err != nil {
				mu.Lock()
				errs[u] = err.Error()
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
				pageContent := engineapi.PageContent{
					Markdown: cleanContent,
					Metadata: buildPageMetadataFromScrapeMetadata(res.Metadata, title),
				}
				// Include HTML if requested
				if includeHTML && res.Html != nil {
					pageContent.Html = res.Html
				}
				out[u] = pageContent
				mu.Unlock()
			}
			// early stop if reached limit
			if linkLimit > 0 && (len(out)+len(errs)) >= linkLimit {
				cancel()
				return
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

func (s *CrawlService) crawl(ctx context.Context, r engineapi.CrawlCreateRequest) (*map[string]engineapi.PageContent, *map[string]string) {
	// Check if fresh data is requested
	fresh := false
	if r.Fresh != nil {
		fresh = *r.Fresh
	}
	out := make(map[string]engineapi.PageContent)
	errs := make(map[string]string)
	// map links
	depth := 0
	if r.Depth != nil {
		depth = *r.Depth
	}
	linkLimit := 0
	if r.LinkLimit != nil {
		linkLimit = *r.LinkLimit
	}
	incSubs := false
	if r.IncludeSubdomains != nil {
		incSubs = *r.IncludeSubdomains
	}

	mr, err := s.mapper.MapURL(mapper.Request{URL: r.Url, Depth: depth, LinkLimit: linkLimit, IncludeSubdomains: incSubs})
	if err != nil {
		errs[r.Url] = err.Error()
		return &out, &errs
	}
	links := append([]string{r.Url}, mr.Links...)
	if linkLimit > 0 && len(links) > linkLimit {
		links = links[:linkLimit]
	}

	includeHTML := false
	if r.IncludeHtml != nil {
		includeHTML = *r.IncludeHtml
	}
	renderJs := false
	if r.RenderJs != nil {
		renderJs = *r.RenderJs
	}

	var wg sync.WaitGroup
	sem := make(chan struct{}, func() int {
		if renderJs {
			return 2
		}
		return 8
	}())
	for _, u := range links {
		u := u
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			result, e := s.scrapeWithFreshOption(ctx, u, includeHTML, renderJs, fresh)
			if e != nil {
				errs[u] = e.Error()
				return
			}

			content := ""
			if result.Content != nil {
				content = *result.Content
			}
			title := ""
			if result.Title != nil {
				title = *result.Title
			}

			// Build engine page content
			// Clean markdown content to ensure JSON compatibility
			cleanContent := cleanContentForJSON(content)
			pc := engineapi.PageContent{
				Markdown: cleanContent,
				Metadata: buildPageMetadataFromScrapeMetadata(result.Metadata, title),
			}
			// Include HTML if requested
			if includeHTML && result.Html != nil {
				pc.Html = result.Html
			}
			out[u] = pc
		}()
	}
	wg.Wait()
	return &out, &errs
}

func (s *CrawlService) scrapeWithCache(ctx context.Context, url string, includeHTML, renderJs bool) (*engineapi.ScrapeResponse, error) {
	format := engineapi.GetV1ScrapeParamsFormat("markdown")
	if includeHTML {
		format = engineapi.GetV1ScrapeParamsFormat("html")
	}
	params := engineapi.GetV1ScrapeParams{Url: url, Format: &format, RenderJs: &renderJs, IncludeHtml: &includeHTML}
	return s.scrape.ScrapeURL(ctx, params)
}

// scrapeWithFreshOption decides whether to use cache or fresh scraping based on the fresh parameter
func (s *CrawlService) scrapeWithFreshOption(ctx context.Context, url string, includeHTML, renderJs, fresh bool) (*engineapi.ScrapeResponse, error) {
	if fresh {
		// Bypass cache and scrape fresh
		format := engineapi.GetV1ScrapeParamsFormat("markdown")
		if includeHTML {
			format = engineapi.GetV1ScrapeParamsFormat("html")
		}
		params := engineapi.GetV1ScrapeParams{Url: url, Format: &format, RenderJs: &renderJs, Fresh: &fresh, IncludeHtml: &includeHTML}
		return s.scrape.ScrapeURL(ctx, params)
	} else {
		// Use cache if available
		result, _, err := s.scrape.ScrapeWithCache(ctx, url, includeHTML, renderJs)
		return result, err
	}
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
