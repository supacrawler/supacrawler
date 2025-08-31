package scrape

import (
	"fmt"
	"scraper/internal/core/mapper"
	"scraper/internal/platform/engineapi"
	"scraper/internal/utils/parser"
	"strings"

	"github.com/gofiber/fiber/v2"
)

type Handler struct {
	service *Service
	mapper  *mapper.Service
}

func NewHandler(service *Service, mapper *mapper.Service) *Handler {
	return &Handler{service: service, mapper: mapper}
}

func (h *Handler) HandleGetScrape(c *fiber.Ctx) error {
	var p engineapi.GetV1ScrapeParams
	fmt.Println("Raw query:", string(c.Request().URI().QueryString()))

	if err := parser.ParseQuery(c, &p); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(engineapi.Error{
			Success: &[]bool{false}[0],
			Error:   &[]string{"invalid query"}[0],
		})
	}

	if p.Url == "" {
		return c.Status(fiber.StatusBadRequest).JSON(engineapi.Error{Success: &[]bool{false}[0], Error: &[]string{"url is required"}[0]})
	}

	if p.Format != nil && *p.Format == engineapi.GetV1ScrapeParamsFormat("links") {
		depth := 0
		if p.Depth != nil {
			depth = *p.Depth
		}
		maxLinks := 0
		if p.MaxLinks != nil {
			maxLinks = *p.MaxLinks
		}
		res, err := h.mapper.MapURL(mapper.Request{URL: p.Url, Depth: depth, LinkLimit: maxLinks})
		if err != nil {
			errMsg := err.Error()
			return c.Status(fiber.StatusInternalServerError).JSON(engineapi.Error{Success: &[]bool{false}[0], Error: &errMsg})
		}
		discovered := len(res.Links)
		return c.JSON(engineapi.ScrapeResponse{Success: true, Url: p.Url, Links: &res.Links, Discovered: &discovered, Metadata: engineapi.ScrapeMetadata{}})
	}

	result, err := h.service.ScrapeURL(c.Context(), p)
	if err != nil {
		errMsg := err.Error()

		// Categorize errors and return appropriate HTTP status codes
		if strings.Contains(errMsg, "disallowed by robots.txt") {
			return c.Status(fiber.StatusForbidden).JSON(engineapi.Error{Success: &[]bool{false}[0], Error: &errMsg})
		}
		if strings.Contains(errMsg, "invalid URL") || strings.Contains(errMsg, "malformed") {
			return c.Status(fiber.StatusBadRequest).JSON(engineapi.Error{Success: &[]bool{false}[0], Error: &errMsg})
		}
		if strings.Contains(errMsg, "stopped after") && strings.Contains(errMsg, "redirects") {
			return c.Status(fiber.StatusBadRequest).JSON(engineapi.Error{Success: &[]bool{false}[0], Error: &errMsg})
		}
		if strings.Contains(errMsg, "timeout") || strings.Contains(errMsg, "deadline exceeded") {
			return c.Status(fiber.StatusRequestTimeout).JSON(engineapi.Error{Success: &[]bool{false}[0], Error: &errMsg})
		}
		if strings.Contains(errMsg, "429") || strings.Contains(errMsg, "rate limit") {
			return c.Status(fiber.StatusTooManyRequests).JSON(engineapi.Error{Success: &[]bool{false}[0], Error: &errMsg})
		}
		if strings.Contains(errMsg, "404") || strings.Contains(errMsg, "not found") {
			return c.Status(fiber.StatusNotFound).JSON(engineapi.Error{Success: &[]bool{false}[0], Error: &errMsg})
		}
		if strings.Contains(errMsg, "filtered out low-quality content") {
			return c.Status(fiber.StatusUnprocessableEntity).JSON(engineapi.Error{Success: &[]bool{false}[0], Error: &errMsg})
		}

		// Default to internal server error for unknown errors
		return c.Status(fiber.StatusInternalServerError).JSON(engineapi.Error{Success: &[]bool{false}[0], Error: &errMsg})
	}
	return c.JSON(result)
}
