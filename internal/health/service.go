package health

import (
	"context"
	"net/http"
	"scraper/internal/logger"
	"scraper/internal/platform/redis"
	"sync"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/limiter"
)

// HealthHandler handles health check requests
type HealthHandler struct {
	log          *logger.Logger
	redisService *redis.Service
	startTime    time.Time
	isReady      bool
}

// NewHealthHandler creates a new instance of HealthHandler
func NewHealthHandler(redisSvc *redis.Service) *HealthHandler {
	return &HealthHandler{
		log:          logger.New("HealthCheck"),
		redisService: redisSvc,
		startTime:    time.Now(),
		isReady:      false,
	}
}

// SetReady marks the application as ready to receive traffic
func (h *HealthHandler) SetReady() {
	h.isReady = true
	h.log.LogSuccessf("Application marked as ready for traffic after %v", time.Since(h.startTime))
}

// ComponentStatus holds the status of a dependent component
type ComponentStatus struct {
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

// OverallHealth represents the overall health status including components
type OverallHealth struct {
	OverallStatus string                     `json:"overall_status"`
	Timestamp     string                     `json:"timestamp"`
	Ready         bool                       `json:"ready"`
	UptimeSeconds int64                      `json:"uptime_seconds"`
	Components    map[string]ComponentStatus `json:"components"`
}

// HandleHealth responds with the system's health status, including dependencies
func (h *HealthHandler) HandleHealth(c *fiber.Ctx) error {
	startTime := time.Now()
	// Only log health checks in debug mode or when they fail
	h.log.LogDebugf("Health check started")

	ctx, cancel := context.WithTimeout(c.Context(), 8*time.Second)
	defer cancel()

	statuses := make(map[string]ComponentStatus)
	var wg sync.WaitGroup
	var mu sync.Mutex

	allOk := true

	checkComponent := func(name string, checkFunc func(context.Context) error) {
		defer wg.Done()
		componentStart := time.Now()
		// Only log start in debug mode
		h.log.LogDebugf("Starting health check for %s", name)

		componentState := "ok"
		var errStr string

		if err := checkFunc(ctx); err != nil {
			componentState = "error"
			errStr = err.Error()
			mu.Lock()
			allOk = false
			mu.Unlock()
			// Always log failures
			h.log.LogErrorf("Health check failed for %s after %v: %v", err, name, time.Since(componentStart))
		} else {
			// Only log success in debug mode
			h.log.LogDebugf("Health check passed for %s in %v", name, time.Since(componentStart))
		}

		mu.Lock()
		statuses[name] = ComponentStatus{Status: componentState, Error: errStr}
		mu.Unlock()
	}

	wg.Add(1)
	go checkComponent("redis", h.redisService.HealthCheck)

	wg.Wait()

	response := OverallHealth{
		Timestamp:     time.Now().UTC().Format(time.RFC3339Nano),
		Ready:         h.isReady,
		UptimeSeconds: int64(time.Since(h.startTime).Seconds()),
		Components:    statuses,
	}

	// Application must be ready AND all components healthy
	if allOk && h.isReady {
		response.OverallStatus = "ok"
		// Only log success in debug mode
		h.log.LogDebugf("Health check completed successfully in %v", time.Since(startTime))
		return c.Status(http.StatusOK).JSON(response)
	}

	// Return appropriate status based on readiness
	if !h.isReady {
		response.OverallStatus = "starting"
		h.log.LogDebugf("Health check: application not ready (uptime: %v)", time.Since(h.startTime))
		return c.Status(http.StatusServiceUnavailable).JSON(response)
	}

	response.OverallStatus = "error"
	// Always log failures
	h.log.LogWarnf("Health check failed after %v. Statuses: %+v", time.Since(startTime), statuses)
	return c.Status(http.StatusServiceUnavailable).JSON(response)
}

func HealthLimiter() fiber.Handler {
	return limiter.New(limiter.Config{
		Max:        300,
		Expiration: 1 * time.Minute,
		KeyGenerator: func(c *fiber.Ctx) string {
			return c.IP()
		},
		LimitReached: func(c *fiber.Ctx) error {
			return c.Status(429).JSON(fiber.Map{"error": "Rate limit exceeded"})
		},
	})
}
