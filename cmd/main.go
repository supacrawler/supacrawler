package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/hibiken/asynq"

	"scraper/internal/config"
	"scraper/internal/core/crawl"
	"scraper/internal/core/job"
	"scraper/internal/core/mapper"
	"scraper/internal/core/scrape"
	"scraper/internal/core/screenshot"
	"scraper/internal/logger"
	rds "scraper/internal/platform/redis"
	tasks "scraper/internal/platform/tasks"
	"scraper/internal/server"
	"scraper/internal/worker"
)

func main() {
	cfg := config.Load()
	log.Printf("[scraper] starting at %s (env=%s)\n", cfg.HTTPAddr, cfg.AppEnv)

	// Initialize logger
	logr := logger.New("main")

	// Redis client
	redisSvc, err := rds.New(rds.Options{
		Addr:     cfg.RedisAddr,
		Password: cfg.RedisPassword,
	})
	if err != nil {
		log.Fatal(err)
	}
	defer redisSvc.Close()

	// Asynq client and server
	taskClient := tasks.New(redisSvc)
	asynqServer := asynq.NewServer(redisSvc.AsynqRedisOpt(), asynq.Config{
		Concurrency: 10,
		Queues:      map[string]int{"default": 1},
	})

	// Core services
	jobSvc := job.NewJobService(redisSvc)
	mapSvc := mapper.NewMapService()
	scrapeSvc := scrape.NewScrapeService(redisSvc)
	crawlSvc := crawl.NewCrawlService(jobSvc, taskClient, mapSvc, scrapeSvc)
	screenshotSvc, err := screenshot.New(cfg, jobSvc)
	if err != nil {
		log.Fatal(err)
	}

	// Worker mux
	mux := worker.NewMux()
	mux.HandleFunc(crawl.TaskTypeCrawl, crawlSvc.HandleCrawlTask)
	mux.HandleFunc(screenshot.TaskTypeScreenshot, screenshotSvc.HandleTask)

	// Start worker
	_, workerCancel := context.WithCancel(context.Background())
	go func() {
		if err := asynqServer.Start(mux.Mux()); err != nil {
			log.Printf("[worker] stopped: %v\n", err)
		}
	}()

	// HTTP server
	app := fiber.New(fiber.Config{AppName: "Scraper Engine"})
	// Serve saved artifacts (e.g., screenshots) from DATA_DIR under /files
	app.Static("/files", cfg.DataDir)

	// Register routes with health handler
	deps := server.Dependencies{
		Job:        jobSvc,
		Crawl:      crawlSvc,
		Scrape:     scrapeSvc,
		Map:        mapSvc,
		Screenshot: screenshotSvc,
		Tasks:      taskClient,
		Redis:      redisSvc,
	}
	healthHandler := server.RegisterRoutes(app, deps)

	// Mark application as ready after all services are initialized
	go func() {
		time.Sleep(5 * time.Second) // Allow services to fully initialize
		healthHandler.SetReady()
	}()

	// Graceful shutdown
	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-shutdown
		logr.LogInfo("Shutting down...")
		workerCancel()
		asynqServer.Shutdown()
		_ = app.ShutdownWithTimeout(5 * time.Second)
	}()

	if err := app.Listen(cfg.HTTPAddr); err != nil {
		log.Fatalf("server listen: %v", err)
	}
}
