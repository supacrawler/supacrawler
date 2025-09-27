package parse

import (
	"context"
	"fmt"
	"strings"
	"time"

	"scraper/internal/core/crawl"
	"scraper/internal/platform/engineapi"
)

// StreamTestMode is defined in service.go - this simulator provides the implementation

// TestModeSimulator handles all test mode simulation logic
type TestModeSimulator struct {
	service *Service
}

// NewTestModeSimulator creates a new test mode simulator
func NewTestModeSimulator(service *Service) *TestModeSimulator {
	return &TestModeSimulator{
		service: service,
	}
}

// SimulateRealisticCrawling simulates the crawl flow based on real log data for testing
func (t *TestModeSimulator) SimulateRealisticCrawling(ctx context.Context, pageChan chan<- *crawl.PageResult) {
	defer close(pageChan)

	// URLs from actual scraper.log in realistic order with realistic timing
	crawlSequence := []struct {
		url     string
		delay   time.Duration
		content string
	}{
		{
			url:     "https://openai.com/news",
			delay:   500 * time.Millisecond,
			content: "# OpenAI News\n\n## Latest Updates\n\nDiscover the latest developments in AI technology and research.",
		},
		{
			url:     "https://openai.com/news/company-announcements/",
			delay:   800 * time.Millisecond,
			content: "# OpenAI Company Announcements\n\n## Recent Announcements\n\nStay updated with official company news and strategic partnerships.",
		},
		{
			url:     "https://openai.com/news/research/",
			delay:   1200 * time.Millisecond,
			content: "# OpenAI Research\n\n## Research Publications\n\nExplore cutting-edge research in artificial intelligence and machine learning.",
		},
		{
			url:     "https://openai.com/news/product-releases/",
			delay:   600 * time.Millisecond,
			content: "# OpenAI Product Releases\n\n## New Products\n\nLearn about the latest AI products and feature releases.",
		},
		{
			url:     "https://openai.com/news/",
			delay:   400 * time.Millisecond,
			content: "# OpenAI News Hub\n\n## All News\n\nComprehensive coverage of OpenAI developments across all categories.",
		},
		{
			url:     "https://openai.com/news/safety-alignment/",
			delay:   1500 * time.Millisecond,
			content: "# OpenAI Safety & Alignment\n\n## AI Safety Research\n\nAdvancing responsible AI development through safety research and alignment.",
		},
		{
			url:     "https://openai.com/news/security/",
			delay:   900 * time.Millisecond,
			content: "# OpenAI Security\n\n## Security Updates\n\nEnsuring robust security measures and responsible disclosure practices.",
		},
		{
			url:     "https://openai.com/news/global-affairs/",
			delay:   1100 * time.Millisecond,
			content: "# OpenAI Global Affairs\n\n## International Partnerships\n\nBuilding global partnerships and collaborations for responsible AI development.",
		},
	}

	t.service.log.LogInfof("🧪 [StreamTestMode] Starting realistic crawl simulation with %d URLs", len(crawlSequence))

	for i, item := range crawlSequence {
		select {
		case <-ctx.Done():
			t.service.log.LogInfof("🧪 [StreamTestMode] Crawl simulation cancelled")
			return
		default:
			// Simulate realistic processing delay
			time.Sleep(item.delay)

			// Create realistic page content
			title := t.extractTitleFromURL(item.url)
			pageContent := &engineapi.PageContent{
				Markdown: item.content,
				Links:    []string{}, // Simplified for demo
				Metadata: engineapi.PageMetadata{
					Title: &title,
				},
			}

			// Send successful page result
			pageResult := &crawl.PageResult{
				URL:         item.url,
				PageContent: pageContent,
				Error:       "",
			}

			t.service.log.LogInfof("🧪 [StreamTestMode] Sending page %d/%d: %s", i+1, len(crawlSequence), item.url)
			pageChan <- pageResult
		}
	}

	t.service.log.LogInfof("🧪 [StreamTestMode] Crawl simulation completed - sent %d pages", len(crawlSequence))
}

// SimulateStreamingLLMAggregation simulates realistic LLM processing with streaming events
func (t *TestModeSimulator) SimulateStreamingLLMAggregation(ctx context.Context, rawContentList []RawContentItem) (interface{}, *TokenUsage, error) {
	t.service.log.LogInfof("🧪 [StreamTestMode] Starting LLM aggregation simulation for %d sources", len(rawContentList))

	// Stream that we're starting LLM aggregation
	t.publishLLMAggregationEvent(ctx, "llm.aggregation_start", "Starting LLM aggregation of collected content...")

	// Simulate LLM processing time with multiple progress updates
	progressMessages := []struct {
		delay   time.Duration
		content string
	}{
		{500 * time.Millisecond, "Processing page content and extracting structured data..."},
		{800 * time.Millisecond, "Analyzing patterns and removing duplicates..."},
		{1200 * time.Millisecond, "Generating summaries and categorizing content..."},
		{600 * time.Millisecond, "Finalizing output format and validation..."},
	}

	for _, progress := range progressMessages {
		time.Sleep(progress.delay)
		t.publishLLMAggregationEvent(ctx, "llm.aggregation_progress", progress.content)
	}

	// Generate realistic output based on URLs that were actually crawled
	demoData := t.generateRealisticOpenAINewsData(rawContentList)

	// Stream the final LLM response content
	responseContent := t.formatResponseAsJSON(demoData)
	t.publishLLMAggregationEvent(ctx, "llm.aggregation_content", responseContent)

	// Calculate realistic token usage
	totalInputChars := 0
	for _, item := range rawContentList {
		totalInputChars += len(item.Content)
	}

	inputTokens := int32(totalInputChars/4 + 800)   // Realistic estimate
	outputTokens := int32(len(responseContent) / 4) // Based on actual output

	tokenUsage := &TokenUsage{
		InputTokens:    inputTokens,
		OutputTokens:   outputTokens,
		TotalTokens:    inputTokens + outputTokens,
		PagesProcessed: len(rawContentList),
	}

	t.publishLLMAggregationEvent(ctx, "llm.aggregation_success", "LLM aggregation completed successfully")
	t.service.log.LogInfof("🧪 [StreamTestMode] LLM simulation completed - processed %d pages", len(rawContentList))

	return demoData, tokenUsage, nil
}

// GetTestAnalysis returns a test analysis configuration for crawl mode
func (t *TestModeSimulator) GetTestAnalysis() *PromptAnalysis {
	return &PromptAnalysis{
		Action:         "crawl",
		URLs:           []string{"https://openai.com/news"},
		OutputFormat:   "json",
		MaxPages:       10,
		ExtractionGoal: "Extract article titles, authors, published dates, summaries, and tags from OpenAI news",
	}
}

// extractTitleFromURL creates a readable title from URL for simulation
func (t *TestModeSimulator) extractTitleFromURL(url string) string {
	// Extract meaningful title from URL path
	parts := strings.Split(url, "/")
	if len(parts) > 3 {
		section := parts[len(parts)-2] // Get second-to-last part (before trailing slash)
		if section == "news" && len(parts) > 4 {
			section = parts[len(parts)-3] // Get category for news URLs
		}

		// Convert URL sections to readable titles
		switch section {
		case "company-announcements":
			return "Company Announcements"
		case "research":
			return "Research Publications"
		case "product-releases":
			return "Product Releases"
		case "safety-alignment":
			return "Safety & Alignment"
		case "security":
			return "Security Updates"
		case "global-affairs":
			return "Global Affairs"
		case "news":
			return "OpenAI News"
		default:
			// Convert kebab-case to Title Case
			words := strings.Split(section, "-")
			for i, word := range words {
				if len(word) > 0 {
					words[i] = strings.ToUpper(word[:1]) + word[1:]
				}
			}
			return strings.Join(words, " ")
		}
	}
	return "OpenAI News"
}

// generateRealisticOpenAINewsData creates realistic data based on crawled URLs matching real API format
func (t *TestModeSimulator) generateRealisticOpenAINewsData(rawContentList []RawContentItem) []map[string]interface{} {
	newsItems := []map[string]interface{}{}

	for i, item := range rawContentList {
		var newsItem map[string]interface{}

		// Generate content based on the URL - using exact format from real data
		if strings.Contains(item.URL, "company-announcements") {
			newsItem = map[string]interface{}{
				"author":         "OpenAI",
				"published_date": "Sep 25, 2025",
				"reading_time":   nil,
				"summary":        "OpenAI announces strategic partnerships with leading technology companies to accelerate AI development and deployment worldwide. These partnerships focus on expanding AI capabilities and infrastructure to serve global markets more effectively.",
				"tags":           []string{"Company"},
				"title":          "OpenAI expands global partnerships for AI development",
			}
		} else if strings.Contains(item.URL, "research") {
			newsItem = map[string]interface{}{
				"author":         "OpenAI",
				"published_date": "Sep 24, 2025",
				"reading_time":   nil,
				"summary":        "New research publication details breakthrough methods for ensuring AI systems remain aligned with human values and intentions. This work contributes to the broader field of AI safety and responsible development practices.",
				"tags":           []string{"Research"},
				"title":          "Advancing AI safety through novel research methodologies",
			}
		} else if strings.Contains(item.URL, "product-releases") {
			newsItem = map[string]interface{}{
				"author":         "OpenAI",
				"published_date": "Sep 23, 2025",
				"reading_time":   nil,
				"summary":        "Latest GPT model releases offer improved performance, better reasoning capabilities, and enhanced safety features. These updates aim to provide more powerful and reliable AI tools for developers and users across various applications.",
				"tags":           []string{"Product"},
				"title":          "Introducing enhanced GPT models with improved capabilities",
			}
		} else if strings.Contains(item.URL, "safety-alignment") {
			newsItem = map[string]interface{}{
				"author":         "OpenAI",
				"published_date": "Sep 22, 2025",
				"reading_time":   nil,
				"summary":        "Comprehensive overview of OpenAI's approach to developing safe and beneficial artificial general intelligence systems. This article outlines the company's commitment to responsible AI development and alignment research.",
				"tags":           []string{"Safety"},
				"title":          "Building toward safe artificial general intelligence",
			}
		} else if strings.Contains(item.URL, "security") {
			newsItem = map[string]interface{}{
				"author":         "OpenAI",
				"published_date": "Sep 21, 2025",
				"reading_time":   nil,
				"summary":        "Details on new security measures and protocols implemented to protect AI systems and user data. This article discusses OpenAI's approach to scaling security measures through responsible disclosure practices.",
				"tags":           []string{"Security"},
				"title":          "Scaling security with responsible disclosure practices",
			}
		} else if strings.Contains(item.URL, "global-affairs") {
			newsItem = map[string]interface{}{
				"author":         "OpenAI",
				"published_date": "Sep 20, 2025",
				"reading_time":   nil,
				"summary":        "OpenAI partners with international organizations to establish responsible AI governance frameworks. This collaboration aims to enhance online safety and develop AI solutions that benefit communities worldwide.",
				"tags":           []string{"Global Affairs"},
				"title":          "International AI governance and community partnerships",
			}
		} else {
			// Default news item for /news and /news/ pages
			newsItem = map[string]interface{}{
				"author":         "OpenAI",
				"published_date": "Sep 27, 2025",
				"reading_time":   nil,
				"summary":        "Latest developments and updates from OpenAI covering various aspects of AI research and development. This comprehensive overview showcases recent advancements in artificial intelligence technology and applications.",
				"tags":           []string{"Product"},
				"title":          fmt.Sprintf("OpenAI news and updates - Page %d", i+1),
			}
		}

		newsItems = append(newsItems, newsItem)
	}

	return newsItems
}

// formatResponseAsJSON formats the demo data as JSON for streaming
func (t *TestModeSimulator) formatResponseAsJSON(data interface{}) string {
	// Simple JSON formatting to match the expected output
	jsonString := "```json\n["

	if items, ok := data.([]map[string]interface{}); ok {
		for i, item := range items {
			if i > 0 {
				jsonString += ",\n"
			}
			jsonString += "\n {\n"
			jsonString += fmt.Sprintf("  \"title\": \"%s\",\n", item["title"])
			jsonString += fmt.Sprintf("  \"author\": \"%s\",\n", item["author"])
			jsonString += fmt.Sprintf("  \"published_date\": \"%s\",\n", item["published_date"])
			jsonString += fmt.Sprintf("  \"summary\": \"%s\",\n", item["summary"])
			jsonString += fmt.Sprintf("  \"tags\": [")
			if tags, ok := item["tags"].([]string); ok {
				for j, tag := range tags {
					if j > 0 {
						jsonString += ", "
					}
					jsonString += fmt.Sprintf("\"%s\"", tag)
				}
			}
			jsonString += "],\n"
			jsonString += "  \"reading_time\": null\n"
			jsonString += " }"
		}
	}

	jsonString += "\n]\n```"
	return jsonString
}

// publishLLMAggregationEvent publishes LLM aggregation streaming events via the service
func (t *TestModeSimulator) publishLLMAggregationEvent(ctx context.Context, event, content string) {
	// Delegate to the service's method
	t.service.publishLLMAggregationEvent(ctx, event, content)
}

// publishLLMAggregationEvent publishes LLM aggregation streaming events
func (s *Service) publishLLMAggregationEvent(ctx context.Context, event, content string) {
	if tracer, ok := ctx.Value("eino_tracer").(*EinoTracer); ok && tracer != nil {
		traceEvent := map[string]interface{}{
			"jobId":     tracer.jobID,
			"event":     event,
			"component": "llm",
			"timing":    "progress",
			"timestamp": time.Now().UnixMilli(),
			"metadata": map[string]interface{}{
				"content": content,
			},
		}
		tracer.jobService.PublishJobTrace(ctx, tracer.jobID, traceEvent)
	}

	if StreamTestMode {
		time.Sleep(200 * time.Millisecond)
	}
}
