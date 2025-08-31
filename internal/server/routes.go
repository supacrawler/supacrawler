package server

import (
	"scraper/internal/core/crawl"
	"scraper/internal/core/job"
	"scraper/internal/core/mapper"
	"scraper/internal/core/scrape"
	"scraper/internal/core/screenshot"
	"scraper/internal/health"
	"scraper/internal/platform/redis"
	tasks "scraper/internal/platform/tasks"

	"github.com/gofiber/fiber/v2"
)

type Dependencies struct {
	Job        *job.JobService
	Crawl      *crawl.CrawlService
	Scrape     *scrape.Service
	Map        *mapper.Service
	Screenshot *screenshot.Service
	Tasks      *tasks.Client
	Redis      *redis.Service
}

func RegisterRoutes(app *fiber.App, d Dependencies) *health.HealthHandler {
	// Health endpoints
	healthHandler := health.NewHealthHandler(d.Redis)
	app.Get("/v1/health", health.HealthLimiter(), healthHandler.HandleHealth)

	api := app.Group("/v1")

	scrapeHandler := scrape.NewHandler(d.Scrape, d.Map)
	api.Get("/scrape", scrapeHandler.HandleGetScrape)

	crawlHandler := crawl.NewCrawlHandler(d.Job, d.Crawl)
	api.Post("/crawl", crawlHandler.HandleCreateCrawl)
	api.Get("/crawl/:jobId", crawlHandler.HandleGetCrawl)

	screenshotHandler := screenshot.NewHandler(d.Screenshot, d.Tasks, d.Job)
	api.Post("/screenshots", screenshotHandler.HandleCreate)
	api.Get("/screenshots", screenshotHandler.HandleGet)

	return healthHandler
}
