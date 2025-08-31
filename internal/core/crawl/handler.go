package crawl

import (
	"scraper/internal/core/job"
	"scraper/internal/platform/engineapi"

	"github.com/gofiber/fiber/v2"
)

type Handler struct {
	job   *job.JobService
	crawl *CrawlService
}

func NewCrawlHandler(job *job.JobService, crawl *CrawlService) *Handler {
	return &Handler{job: job, crawl: crawl}
}

func (h *Handler) HandleCreateCrawl(c *fiber.Ctx) error {
	var req engineapi.CrawlCreateRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(engineapi.Error{Success: &[]bool{false}[0], Error: &[]string{"invalid body"}[0]})
	}
	id, err := h.crawl.Enqueue(c.Context(), req)
	if err != nil {
		errMsg := err.Error()
		return c.Status(fiber.StatusInternalServerError).JSON(engineapi.Error{Success: &[]bool{false}[0], Error: &errMsg})
	}
	return c.JSON(engineapi.CrawlCreateResponse{Success: true, JobId: id})
}

func (h *Handler) HandleGetCrawl(c *fiber.Ctx) error {
	id := c.Params("jobId")
	j, err := h.job.GetJobStatus(c.Context(), id)
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(engineapi.Error{Success: &[]bool{false}[0], Error: &[]string{"not_found"}[0]})
	}
	status := engineapi.CrawlStatusResponseStatus(string(j.Status))
	resp := engineapi.CrawlStatusResponse{Success: true, JobId: &id, Status: status}
	if j.Status == job.StatusCompleted && j.Results.CrawlResult != nil {
		resp.Data = j.Results.CrawlResult
	}
	return c.JSON(resp)
}
