package tasks

import (
	"scraper/internal/platform/redis"

	"github.com/hibiken/asynq"
)

const (
	TaskTypeCrawl = "crawl:task"
)

type Client struct{ c *asynq.Client }

func New(r *redis.Service) *Client { return &Client{c: asynq.NewClient(r.AsynqRedisOpt())} }

func (t *Client) Enqueue(task *asynq.Task, queue string, maxRetries int) error {
	_, err := t.c.Enqueue(task, asynq.Queue(queue), asynq.MaxRetry(maxRetries))
	return err
}
