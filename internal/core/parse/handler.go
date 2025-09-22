package parse

import (
	"context"

	"scraper/internal/core/job"
	"scraper/internal/logger"
	"scraper/internal/platform/engineapi"

	"github.com/gofiber/fiber/v2"
)

// Handler handles HTTP requests for the parse service using REAL Eino workflows
type Handler struct {
	service *Service
	jobs    *job.JobService
	log     *logger.Logger
}

// NewHandler creates a new parse handler with service
func NewHandler(service *Service, jobs *job.JobService) *Handler {
	return &Handler{
		service: service,
		jobs:    jobs,
		log:     logger.New("ParseHandler"),
	}
}

// HandleParseContent handles POST /v1/parse requests using Eino workflows
func (h *Handler) HandleParseContent(c *fiber.Ctx) error {
	var req engineapi.ParseCreateRequest
	if err := c.BodyParser(&req); err != nil {
		h.log.LogWarnf("Parse request parse error: %v", err)
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"success": false,
			"error":   "Invalid request body",
		})
	}

	h.log.LogInfof("Processing request with Eino workflow for prompt: %s", req.Prompt)

	// Attach engine-side tracer from propagated job ID using user context
	ctx := c.UserContext()
	if jobID := c.Get("X-Trace-JobID"); jobID != "" && h.jobs != nil {
		h.log.LogInfof("[HandleParseContent] Creating EinoTracer for job: %s", jobID)
		tracer := NewEinoTracer(h.jobs, jobID)
		ctx = context.WithValue(ctx, "eino_tracer", tracer)
		h.log.LogDebugf("[HandleParseContent] EinoTracer attached to context for job: %s", jobID)
	} else {
		h.log.LogWarnf("[HandleParseContent] No EinoTracer created - jobID: %s, jobs: %v", c.Get("X-Trace-JobID"), h.jobs != nil)
	}

	// Execute the Eino workflow
	result, err := h.service.ProcessWithWorkflow(ctx, req)
	if err != nil {
		h.log.LogErrorf("Workflow execution error: %v", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"success": false,
			"error":   "Internal server error",
		})
	}

	if !result.Success {
		return c.Status(fiber.StatusBadRequest).JSON(result)
	}

	return c.JSON(result)
}

// HandleGetTemplates handles GET /v1/parse/templates requests
func (h *Handler) HandleGetTemplates(c *fiber.Ctx) error {
	templates := h.service.GetAvailableTemplates()
	contentTypes := h.service.GetSupportedContentTypes()
	outputFormats := h.service.GetSupportedOutputFormats()

	return c.JSON(fiber.Map{
		"success":        true,
		"templates":      templates,
		"content_types":  contentTypes,
		"output_formats": outputFormats,
	})
}

// HandleGetExamples handles GET /v1/parse/examples requests
func (h *Handler) HandleGetExamples(c *fiber.Ctx) error {
	examples := h.service.GetExampleOutputSpecs()

	return c.JSON(fiber.Map{
		"success":  true,
		"examples": examples,
	})
}
