package job

import (
	"scraper/internal/platform/engineapi"
)

// Internal business logic types that are NOT in engineapi
// DO NOT duplicate types that exist in engineapi

// Job represents internal job storage (not exposed in API)
type Job struct {
	JobID   string    `json:"job_id"`
	Type    Type      `json:"type"`
	Status  Status    `json:"status"`
	Results JobResult `json:"results,omitempty"`
}

// Type for internal job classification
type Type string

const (
	TypeCrawl      Type = "crawl"
	TypeScreenshot Type = "screenshot"
)

// Status for internal job tracking
type Status string

const (
	StatusPending    Status = "pending"
	StatusProcessing Status = "processing"
	StatusCompleted  Status = "completed"
	StatusFailed     Status = "failed"
)

// Internal job result storage
type JobResult struct {
	CrawlResult      *engineapi.CrawlJobData `json:"crawl_result,omitempty"`
	ScreenshotResult *ScreenshotResult       `json:"screenshot_result,omitempty"`
}

type ScreenshotResult struct {
	URL       string                       `json:"url"`
	Path      string                       `json:"path"`
	PublicURL string                       `json:"public_url"`
	Metadata  engineapi.ScreenshotMetadata `json:"metadata"`
}
