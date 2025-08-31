package screenshot

import (
	"fmt"
	"path/filepath"
	"scraper/internal/core/job"
	"scraper/internal/platform/engineapi"
	tasks "scraper/internal/platform/tasks"

	"github.com/gofiber/fiber/v2"
)

type Handler struct {
	service *Service
	tasks   *tasks.Client
	jobs    *job.JobService
}

func NewHandler(service *Service, tasks *tasks.Client, jobs *job.JobService) *Handler {
	return &Handler{service: service, tasks: tasks, jobs: jobs}
}

func (h *Handler) HandleCreate(c *fiber.Ctx) error {
	var req engineapi.ScreenshotCreateRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(engineapi.Error{
			Success: &[]bool{false}[0],
			Error:   &[]string{"invalid body"}[0],
		})
	}
	if req.Url == "" {
		return c.Status(fiber.StatusBadRequest).JSON(engineapi.Error{
			Success: &[]bool{false}[0],
			Error:   &[]string{"url is required"}[0],
		})
	}

	stream := false
	if req.Stream != nil {
		stream = *req.Stream
	}

	internalReq := req
	internalReq.Stream = &stream

	if stream {
		// synchronous streaming: capture and return bytes
		res, err := h.service.take(c.Context(), internalReq)
		if err != nil {
			errMsg := err.Error()
			return c.Status(fiber.StatusInternalServerError).JSON(engineapi.Error{
				Success: &[]bool{false}[0],
				Error:   &errMsg,
			})
		}
		if req.Format != nil && string(*req.Format) == "jpeg" {
			c.Set("Content-Type", "image/jpeg")
		} else {
			c.Set("Content-Type", "image/png")
		}
		return c.SendFile(res.Path)
	}

	id, err := h.service.Enqueue(c.Context(), h.tasks, internalReq)
	if err != nil {
		errMsg := err.Error()
		return c.Status(fiber.StatusInternalServerError).JSON(engineapi.Error{
			Success: &[]bool{false}[0],
			Error:   &errMsg,
		})
	}

	return c.JSON(engineapi.ScreenshotJobResponse{
		Success: true,
		JobId:   id,
	})
}

func (h *Handler) HandleGet(c *fiber.Ctx) error {
	jobID := c.Query("job_id")
	if jobID == "" {
		return c.Status(fiber.StatusBadRequest).JSON(engineapi.Error{
			Success: &[]bool{false}[0],
			Error:   &[]string{"job_id is required"}[0],
		})
	}

	j, err := h.jobs.GetJobStatus(c.Context(), jobID)
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(engineapi.Error{
			Success: &[]bool{false}[0],
			Error:   &[]string{"not_found"}[0],
		})
	}

	if j.Status != job.StatusCompleted || j.Results.ScreenshotResult == nil {
		status := engineapi.ScreenshotGetResponseStatusProcessing
		return c.Status(fiber.StatusAccepted).JSON(engineapi.ScreenshotGetResponse{
			Success: true,
			JobId:   &j.JobID,
			Status:  &status,
		})
	}

	meta := j.Results.ScreenshotResult.Metadata
	fmt.Println(j.Results.ScreenshotResult)
	fileSize := 0
	if meta.FileSize != nil {
		fileSize = *meta.FileSize
	}

	public := j.Results.ScreenshotResult.PublicURL
	if public == "" && j.Results.ScreenshotResult.Path != "" {
		public = "/files/screenshots/" + filepath.Base(j.Results.ScreenshotResult.Path)
	}

	completedStatus := engineapi.ScreenshotGetResponseStatusCompleted
	return c.JSON(engineapi.ScreenshotGetResponse{
		Success:    true,
		JobId:      &j.JobID,
		Url:        &j.Results.ScreenshotResult.URL,
		Screenshot: &public,
		Status:     &completedStatus,
		Metadata: &engineapi.ScreenshotMetadata{
			Width:    meta.Width,
			Height:   meta.Height,
			Format:   meta.Format,
			FileSize: &fileSize,
			LoadTime: meta.LoadTime,
		},
	})
}
