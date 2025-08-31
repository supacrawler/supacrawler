package job

import (
	"context"
	"fmt"

	"scraper/internal/platform/engineapi"
	rds "scraper/internal/platform/redis"
)

type JobService struct{ redis *rds.Service }

func NewJobService(redis *rds.Service) *JobService { return &JobService{redis: redis} }

func (s *JobService) GetJobStatus(ctx context.Context, jobID string) (*Job, error) {
	var job Job
	if err := s.redis.CacheGet(ctx, key(jobID), &job); err != nil {
		return nil, fmt.Errorf("job not found: %s", jobID)
	}
	return &job, nil
}

func (s *JobService) store(ctx context.Context, jobID string, jobType Type, status Status, result interface{}) error {
	var job Job
	_ = s.redis.CacheGet(ctx, key(jobID), &job)
	job.JobID = jobID
	job.Type = jobType
	job.Status = status
	// result mapping
	switch v := result.(type) {
	case engineapi.CrawlJobData:
		job.Results = JobResult{CrawlResult: &v}
	case *engineapi.CrawlJobData:
		job.Results = JobResult{CrawlResult: v}
	case ScreenshotResult:
		job.Results = JobResult{ScreenshotResult: &v}
	case *ScreenshotResult:
		job.Results = JobResult{ScreenshotResult: v}
	case nil:
		// no-op
	default:
		// ignore unknown types
	}
	// Save to Redis cache
	if err := s.redis.CacheSet(ctx, key(jobID), job, ttl(status)); err != nil {
		return err
	}
	// Publish an update event for SSE listeners
	_ = s.redis.Client().Publish(ctx, key(jobID), "updated").Err()
	return nil
}

func (s *JobService) Complete(ctx context.Context, jobID string, jobType Type, status Status, result interface{}) error {
	return s.store(ctx, jobID, jobType, status, result)
}

func (s *JobService) SetProcessing(ctx context.Context, jobID string, jobType Type) error {
	return s.store(ctx, jobID, jobType, StatusProcessing, nil)
}

func (s *JobService) InitPending(ctx context.Context, jobID string, jobType Type, url string) error {
	if jobType == TypeScreenshot {
		return s.store(ctx, jobID, jobType, StatusPending, ScreenshotResult{URL: url})
	}
	u := url
	init := engineapi.CrawlJobData{Url: &u}
	return s.store(ctx, jobID, jobType, StatusPending, init)
}

func key(id string) string { return "job:" + id }
func ttl(s Status) int {
	if s == StatusCompleted || s == StatusFailed {
		return 3600
	}
	return 600
}
