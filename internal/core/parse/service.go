package parse

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"scraper/internal/core/crawl"
	"scraper/internal/core/scrape"
	"scraper/internal/logger"
	"scraper/internal/platform/eino"
	"scraper/internal/platform/engineapi"
	"scraper/prompts"

	gemini "github.com/cloudwego/eino-ext/components/model/gemini"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	"github.com/eino-contrib/jsonschema"
	orderedmap "github.com/wk8/go-ordered-map/v2"
)

// Set to true to test the streaming events without hitting real URLs or LLM APIs
var StreamTestMode = false

// Service implements intelligent parsing using Eino workflows with streaming
type Service struct {
	log           *logger.Logger
	eino          *eino.Service
	scrapeService *scrape.Service
	crawlService  *crawl.CrawlService
	systemPrompts *prompts.SystemPrompts
	workflow      compose.Runnable[*ParseInput, *ParseResult]
}

// WorkflowContext tracks token usage across workflow steps
type WorkflowContext struct {
	TotalInputTokens  int32
	TotalOutputTokens int32
	PagesProcessed    int
}

// ParseInput represents the workflow input
type ParseInput struct {
	Request engineapi.ParseCreateRequest `json:"request"`
}

// ParseResult represents the final workflow output
type ParseResult struct {
	Success    bool        `json:"success"`
	Data       interface{} `json:"data"`
	Errors     []string    `json:"errors,omitempty"`
	TokenUsage *TokenUsage `json:"token_usage,omitempty"`
}

// TokenUsage represents token consumption for billing
type TokenUsage struct {
	InputTokens    int32 `json:"input_tokens"`
	OutputTokens   int32 `json:"output_tokens"`
	TotalTokens    int32 `json:"total_tokens"`
	PagesProcessed int   `json:"pages_processed"`
}

// PromptAnalysis contains the LLM's analysis of the user prompt
type PromptAnalysis struct {
	Action         string                 `json:"action"`          // "scrape" or "crawl"
	URLs           []string               `json:"urls"`            // Extracted URLs
	OutputFormat   string                 `json:"output_format"`   // "json", "csv", "markdown", "xml", "yaml"
	MaxPages       int                    `json:"max_pages"`       // For crawling
	ExtractionGoal string                 `json:"extraction_goal"` // What to extract
	Schema         map[string]interface{} `json:"schema,omitempty"`
	// Crawl-specific configuration
	CrawlConfig *CrawlConfig `json:"crawl_config,omitempty"`
}

// createPromptAnalysisSchema creates a JSON schema that forces the LLM to return valid PromptAnalysis JSON
func createPromptAnalysisSchema() *jsonschema.Schema {
	return &jsonschema.Schema{
		Type:     string(schema.Object),
		Required: []string{"action", "urls", "output_format", "max_pages", "extraction_goal"},
		Properties: orderedmap.New[string, *jsonschema.Schema](
			orderedmap.WithInitialData[string, *jsonschema.Schema](
				orderedmap.Pair[string, *jsonschema.Schema]{
					Key: "action",
					Value: &jsonschema.Schema{
						Type:        string(schema.String),
						Description: "The action to take: 'scrape' for single page, 'crawl' for multiple pages",
						Enum:        []any{"scrape", "crawl"},
					},
				},
				orderedmap.Pair[string, *jsonschema.Schema]{
					Key: "urls",
					Value: &jsonschema.Schema{
						Type:        string(schema.Array),
						Description: "Array of URLs to process",
						Items: &jsonschema.Schema{
							Type: string(schema.String),
						},
					},
				},
				orderedmap.Pair[string, *jsonschema.Schema]{
					Key: "output_format",
					Value: &jsonschema.Schema{
						Type:        string(schema.String),
						Description: "Format for the extracted data",
						Enum:        []any{"json", "csv", "markdown", "xml", "yaml"},
						Default:     "json",
					},
				},
				orderedmap.Pair[string, *jsonschema.Schema]{
					Key: "max_pages",
					Value: &jsonschema.Schema{
						Type:        string(schema.Integer),
						Description: "Maximum number of pages to process",
						Minimum:     json.Number("1"),
						Maximum:     json.Number("100"),
					},
				},
				orderedmap.Pair[string, *jsonschema.Schema]{
					Key: "extraction_goal",
					Value: &jsonschema.Schema{
						Type:        string(schema.String),
						Description: "Clear description of what data to extract",
					},
				},
				orderedmap.Pair[string, *jsonschema.Schema]{
					Key: "crawl_config",
					Value: &jsonschema.Schema{
						Type:        string(schema.Object),
						Description: "Configuration for crawl action (optional)",
						Properties: orderedmap.New[string, *jsonschema.Schema](
							orderedmap.WithInitialData[string, *jsonschema.Schema](
								orderedmap.Pair[string, *jsonschema.Schema]{
									Key: "patterns",
									Value: &jsonschema.Schema{
										Type:        string(schema.Array),
										Description: "URL patterns to match (e.g., ['/jobs/*', '/careers/*'])",
										Items: &jsonschema.Schema{
											Type: string(schema.String),
										},
									},
								},
								orderedmap.Pair[string, *jsonschema.Schema]{
									Key: "depth",
									Value: &jsonschema.Schema{
										Type:        string(schema.Integer),
										Description: "Maximum crawl depth",
										Minimum:     json.Number("1"),
										Maximum:     json.Number("3"),
										Default:     2,
									},
								},
								orderedmap.Pair[string, *jsonschema.Schema]{
									Key: "include_subdomains",
									Value: &jsonschema.Schema{
										Type:        string(schema.Boolean),
										Description: "Whether to include subdomains",
										Default:     false,
									},
								},
								orderedmap.Pair[string, *jsonschema.Schema]{
									Key: "fresh",
									Value: &jsonschema.Schema{
										Type:        string(schema.Boolean),
										Description: "Whether to bypass cache and get fresh data",
										Default:     false,
									},
								},
							),
						),
					},
				},
			),
		),
	}
}

// CrawlConfig contains crawl-specific parameters
type CrawlConfig struct {
	Patterns          []string `json:"patterns,omitempty"`           // URL patterns to match (e.g., ["/jobs/*", "/careers/*"])
	Depth             *int     `json:"depth,omitempty"`              // Max crawl depth
	IncludeSubdomains *bool    `json:"include_subdomains,omitempty"` // Whether to include subdomains
	Fresh             *bool    `json:"fresh,omitempty"`              // Whether to use fresh data (bypass cache)
}

// PageData represents a single page's content for processing
type PageData struct {
	URL         string                 `json:"url"`
	PageContent *engineapi.PageContent `json:"page_content,omitempty"`
	Error       string                 `json:"error,omitempty"`
}

// ExtractionResult represents extracted data from a page
type ExtractionResult struct {
	URL             string           `json:"url"`
	ExtractedData   interface{}      `json:"data"`
	RawContent      string           `json:"raw_content,omitempty"` // NEW: Store raw scraped content for final LLM processing
	Error           string           `json:"error,omitempty"`
	WorkflowContext *WorkflowContext `json:"workflow_context,omitempty"`
	Analysis        *PromptAnalysis  `json:"analysis,omitempty"`
}

// RawContentItem represents raw scraped content from a URL for final LLM processing
type RawContentItem struct {
	URL     string `json:"url"`
	Content string `json:"content"`
}

// NewService creates a new parse service with Eino workflow
func NewService(einoService *eino.Service, scrapeService *scrape.Service, crawlService *crawl.CrawlService) (*Service, error) {
	s := &Service{
		log:           logger.New("ParseService"),
		eino:          einoService,
		scrapeService: scrapeService,
		crawlService:  crawlService,
		systemPrompts: prompts.NewSystemPrompts(),
	}

	// Register custom concat function for PageData to handle stream concatenation
	compose.RegisterStreamChunkConcatFunc(func(pages []*PageData) (*PageData, error) {
		// For PageData, we don't actually want to concat - we want to use the last non-error one
		for i := len(pages) - 1; i >= 0; i-- {
			if pages[i].Error == "" {
				return pages[i], nil
			}
		}
		// If all have errors, return the last one
		if len(pages) > 0 {
			return pages[len(pages)-1], nil
		}
		return &PageData{Error: "no page data available"}, nil
	})

	// Register custom concat function for ExtractionResult to handle stream concatenation
	compose.RegisterStreamChunkConcatFunc(func(results []*ExtractionResult) (*ExtractionResult, error) {
		// For ExtractionResult, use the last non-error one
		for i := len(results) - 1; i >= 0; i-- {
			if results[i].Error == "" {
				return results[i], nil
			}
		}
		// If all have errors, return the last one
		if len(results) > 0 {
			return results[len(results)-1], nil
		}
		return &ExtractionResult{Error: "no extraction results available"}, nil
	})

	// Build the Eino workflow with streaming
	workflow, err := s.buildWorkflow()
	if err != nil {
		return nil, fmt.Errorf("failed to build workflow: %w", err)
	}

	s.workflow = workflow
	s.log.LogInfof("Parse service initialized with streaming Eino workflow")
	return s, nil
}

// buildWorkflow creates the Eino workflow - SIMPLIFIED: No streaming between nodes
func (s *Service) buildWorkflow() (compose.Runnable[*ParseInput, *ParseResult], error) {
	// Create workflow - SIMPLIFIED APPROACH: All processing in one node
	wf := compose.NewWorkflow[*ParseInput, *ParseResult]()

	// Node 1: Analyze user prompt to determine scrape vs crawl strategy
	wf.AddLambdaNode(
		"analyze_prompt",
		compose.InvokableLambda(s.analyzePromptNode),
	).AddInput(compose.START)

	// Node 2: Process ALL content and return final aggregated result (no streaming)
	wf.AddLambdaNode(
		"process_and_aggregate",
		compose.InvokableLambda(s.processAndAggregateNode),
	).AddInput("analyze_prompt")

	// Mark end node
	wf.End().AddInput("process_and_aggregate")

	// Compile the workflow
	ctx := context.Background()
	compiled, err := wf.Compile(ctx)
	if err != nil {
		return nil, fmt.Errorf("workflow compilation failed: %w", err)
	}

	return compiled, nil
}

// ProcessWithWorkflow executes the main workflow
func (s *Service) ProcessWithWorkflow(ctx context.Context, req engineapi.ParseCreateRequest) (*engineapi.ParseResponse, error) {
	// Validate input
	if strings.TrimSpace(req.Prompt) == "" {
		errorMsg := "prompt cannot be empty"
		return &engineapi.ParseResponse{
			Success: false,
			Error:   &errorMsg,
		}, nil
	}

	s.log.LogInfof("[parse] Starting Eino workflow for prompt: %s", req.Prompt)
	startTime := time.Now()

	// Execute workflow with timeout
	workflowCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	input := &ParseInput{Request: req}
	// Attach per-request callbacks if a tracer is present in context
	var opts []compose.Option
	if tracer, ok := ctx.Value("eino_tracer").(*EinoTracer); ok && tracer != nil {
		opts = append(opts, compose.WithCallbacks(tracer.CreateGlobalHandler()))
	}
	s.log.LogDebugf("[parse] Invoking workflow with streaming callbacks: %t", len(opts) > 0)
	result, err := s.workflow.Invoke(workflowCtx, input, opts...)
	if err != nil {
		s.log.LogErrorf("[parse] Workflow execution failed: %v", err)
		errorMsg := fmt.Sprintf("workflow execution failed: %v", err)
		return &engineapi.ParseResponse{
			Success: false,
			Error:   &errorMsg,
		}, nil
	}

	// Convert to API response
	executionTime := int(time.Since(startTime).Milliseconds())
	workflowStatus := "completed"
	if !result.Success {
		workflowStatus = "failed"
		s.log.LogWarnf("[parse] Workflow completed with failures: %v", result.Errors)
	}

	response := &engineapi.ParseResponse{
		Success:        result.Success,
		Data:           result.Data,
		ExecutionTime:  &executionTime,
		WorkflowStatus: (*engineapi.ParseResponseWorkflowStatus)(&workflowStatus),
	}

	// Add token usage data from workflow result
	if result.TokenUsage != nil {
		inputTokens := int(result.TokenUsage.InputTokens)
		outputTokens := int(result.TokenUsage.OutputTokens)
		totalTokens := int(result.TokenUsage.TotalTokens)
		pagesProcessed := result.TokenUsage.PagesProcessed

		response.InputTokens = &inputTokens
		response.OutputTokens = &outputTokens
		response.TotalTokens = &totalTokens
		response.PagesProcessed = &pagesProcessed

		s.log.LogInfof("[parse] Token usage: input=%d, output=%d, total=%d, pages=%d",
			inputTokens, outputTokens, totalTokens, pagesProcessed)
	}

	if len(result.Errors) > 0 {
		errorMsg := strings.Join(result.Errors, "; ")
		response.Error = &errorMsg
	}

	s.log.LogInfof("[parse] Workflow finished in %dms status=%s", executionTime, workflowStatus)
	return response, nil
}

// analyzePromptNode - Workflow node that analyzes user prompt using LLM
func (s *Service) analyzePromptNode(ctx context.Context, input *ParseInput) (map[string]interface{}, error) {
	s.log.LogInfof("Analyzing prompt: %s", input.Request.Prompt)

	// Essential progress: analyzing prompt
	s.publishProgressEvent(ctx, "parse.analyzing", "Analyzing prompt...")

	req := input.Request

	if StreamTestMode {
		time.Sleep(500 * time.Millisecond)
		analysis := &PromptAnalysis{
			Action:         "crawl",
			URLs:           []string{"https://openai.com/news"},
			OutputFormat:   "json",
			MaxPages:       10,
			ExtractionGoal: "Extract article titles, authors, published dates, summaries, and tags from OpenAI news",
		}

		workflowCtx := &WorkflowContext{
			TotalInputTokens:  45000,
			TotalOutputTokens: 12000,
			PagesProcessed:    0,
		}

		return map[string]interface{}{
			"analysis":         analysis,
			"workflow_context": workflowCtx,
		}, nil
	}

	// Prepare template variables for structured prompt
	userSchema := "No specific schema provided"
	if req.Schema != nil {
		schemaJSON, _ := json.MarshalIndent(*req.Schema, "", "  ")
		userSchema = string(schemaJSON)
	}

	outputFormat := "json"
	if req.OutputFormat != nil {
		outputFormat = string(*req.OutputFormat)
	}

	// Set system defaults - these are what WE decide as reasonable limits
	maxPages := 5 // Default max pages to crawl
	if req.MaxPages != nil {
		maxPages = *req.MaxPages
	}

	maxDepth := 2 // Default max crawl depth
	if req.MaxDepth != nil {
		maxDepth = *req.MaxDepth
	}

	templateVars := map[string]any{
		"user_prompt":   req.Prompt,
		"user_schema":   userSchema,
		"output_format": outputFormat,
		"max_pages":     maxPages,
		"max_depth":     maxDepth,
	}

	// Use structured prompt for analysis
	s.log.LogDebugf("Formatting PromptAnalysis template with vars: %+v", templateVars)
	messages, err := s.systemPrompts.PromptAnalysis.Format(ctx, templateVars)
	if err != nil {
		s.log.LogErrorf("PromptAnalysis template formatting failed: %v", err)
		s.log.LogErrorf("Template variables provided: %+v", templateVars)
		// Fallback to regex analysis
		analysis := s.fallbackPromptAnalysis(req)
		return map[string]interface{}{"analysis": analysis}, nil
	}

	// Call LLM for intelligent analysis with token tracking and JSON schema enforcement
	analysisSchema := createPromptAnalysisSchema()
	response, tokenUsage, err := s.eino.GenerateWithTokenUsage(ctx, messages,
		model.WithTemperature(0.1),
		model.WithMaxTokens(800),
		gemini.WithResponseJSONSchema(analysisSchema),
	)
	if err != nil {
		// Fallback to regex analysis
		analysis := s.fallbackPromptAnalysis(req)
		workflowCtx := &WorkflowContext{
			TotalInputTokens:  0,
			TotalOutputTokens: 0,
			PagesProcessed:    0,
		}
		return map[string]interface{}{
			"analysis":         analysis,
			"workflow_context": workflowCtx,
		}, nil
	}
	s.log.LogInfof("PromptAnalysis response: %s", response.Content)

	// Parse LLM response - handle markdown code blocks
	content := response.Content
	// Remove markdown code blocks if present
	content = strings.TrimPrefix(content, "```json\n")
	content = strings.TrimPrefix(content, "```\n")
	content = strings.TrimSuffix(content, "\n```")
	content = strings.TrimSpace(content)

	var analysis PromptAnalysis
	if err := json.Unmarshal([]byte(content), &analysis); err != nil {
		s.log.LogWarnf("Failed to parse LLM response as JSON: %v. Cleaned content: %s", err, content)
		s.log.LogInfof("Falling back to regex analysis")
		analysis = *s.fallbackPromptAnalysis(req)
		s.log.LogDebugf("Fallback analysis result: action=%s, urls=%v", analysis.Action, analysis.URLs)
	} else {
		s.log.LogInfof("Successfully parsed LLM analysis: action=%s, urls=%v", analysis.Action, analysis.URLs)
	}

	// Override with user-provided values
	if req.OutputFormat != nil {
		analysis.OutputFormat = string(*req.OutputFormat)
	}
	if req.MaxPages != nil {
		analysis.MaxPages = *req.MaxPages
	}
	if req.Schema != nil {
		analysis.Schema = *req.Schema
	}

	s.log.LogInfof("Analysis complete: action=%s, urls=%v, format=%s",
		analysis.Action, analysis.URLs, analysis.OutputFormat)

	// Store token usage in context for aggregation
	workflowCtx := &WorkflowContext{
		TotalInputTokens:  tokenUsage.InputTokens,
		TotalOutputTokens: tokenUsage.OutputTokens,
		PagesProcessed:    0,
	}

	return map[string]interface{}{
		"analysis":         analysis,
		"workflow_context": workflowCtx,
	}, nil
}

// processAndAggregateNode - SIMPLIFIED: Process ALL content and return final result (no streaming)
func (s *Service) processAndAggregateNode(ctx context.Context, input map[string]interface{}) (*ParseResult, error) {
	s.log.LogInfof("🔧 Starting unified content processing and aggregation")

	// Essential progress: processing content
	s.publishProgressEvent(ctx, "parse.processing", "Processing and formatting content...")

	// Extract analysis from input
	analysisInterface, ok := input["analysis"]
	if !ok {
		return &ParseResult{Success: false, Errors: []string{"no analysis data found"}}, nil
	}

	// Extract workflow context from previous step
	var workflowCtx *WorkflowContext
	if ctxInterface, ok := input["workflow_context"]; ok {
		ctxBytes, _ := json.Marshal(ctxInterface)
		workflowCtx = &WorkflowContext{}
		json.Unmarshal(ctxBytes, workflowCtx)
	} else {
		workflowCtx = &WorkflowContext{}
	}

	// Convert to PromptAnalysis
	analysisBytes, _ := json.Marshal(analysisInterface)
	var analysis PromptAnalysis
	if err := json.Unmarshal(analysisBytes, &analysis); err != nil {
		return &ParseResult{Success: false, Errors: []string{fmt.Sprintf("analysis parse failed: %v", err)}}, nil
	}

	if len(analysis.URLs) == 0 {
		s.log.LogWarnf("No URLs found to process in analysis")
		return &ParseResult{Success: false, Errors: []string{"no URLs found to process"}}, nil
	}

	s.log.LogInfof("Processing %d URLs with action: %s", len(analysis.URLs), analysis.Action)

	// Step 1: Collect ALL raw content
	var rawContentList []RawContentItem
	var errors []string

	// Use internal content collection (same as before)
	contentReader, contentWriter := schema.Pipe[*PageData](100)

	// Start content collection
	go func() {
		defer contentWriter.Close()
		s.log.LogInfof("Starting content collection with action: %s", analysis.Action)
		switch analysis.Action {
		case "crawl":
			s.streamCrawlContent(ctx, analysis, contentWriter)
		case "scrape":
			s.streamScrapeContent(ctx, analysis, contentWriter)
		default:
			s.log.LogWarnf("Unknown action: %s, defaulting to scrape", analysis.Action)
			s.streamScrapeContent(ctx, analysis, contentWriter)
		}
	}()

	// Collect ALL content from stream (blocking)
	pageCount := 0
	for {
		pageData, err := contentReader.Recv()
		if err != nil {
			s.log.LogInfof("Content collection completed after processing %d pages: %v", pageCount, err)
			break // End of content stream
		}

		pageCount++
		s.log.LogInfof("Processing page %d: %s", pageCount, pageData.URL)

		if pageData.Error != "" {
			s.log.LogWarnf("Page %d has error: %s", pageCount, pageData.Error)
			errors = append(errors, fmt.Sprintf("%s: %s", pageData.URL, pageData.Error))
			continue
		}

		// Extract raw content
		var rawContent string
		if pageData.PageContent != nil && pageData.PageContent.Markdown != "" {
			rawContent = pageData.PageContent.Markdown
			// Limit content size to prevent excessive token usage
			if len(rawContent) > 15000 { // ~4000 tokens max per page
				rawContent = rawContent[:15000] + "...[TRUNCATED]"
			}
		} else {
			s.log.LogWarnf("Page %d has no content", pageCount)
			errors = append(errors, fmt.Sprintf("%s: No content available", pageData.URL))
			continue
		}

		workflowCtx.PagesProcessed++
		s.log.LogInfof("Collected raw content from page %d (%d chars)", pageCount, len(rawContent))

		// Add to raw content list
		rawContentList = append(rawContentList, RawContentItem{
			URL:     pageData.URL,
			Content: rawContent,
		})

		// Preview of content being collected for debugging
		preview := rawContent
		if len(preview) > 200 {
			preview = preview[:200] + "..."
		}
		s.log.LogInfof("Page %d content preview: %s", pageCount, preview)
	}

	s.log.LogInfof("Content collection summary: %d pages collected, %d errors", len(rawContentList), len(errors))

	// Essential progress: aggregating results
	s.publishProgressEvent(ctx, "parse.aggregating", "Processing collected content with LLM...")

	// Step 2: Process ALL raw content with unified LLM call
	var finalData interface{}
	var finalTokenUsage *TokenUsage

	if len(rawContentList) == 0 {
		// No content to process
		finalData = []interface{}{}
		finalTokenUsage = &TokenUsage{
			InputTokens:    0,
			OutputTokens:   0,
			TotalTokens:    0,
			PagesProcessed: workflowCtx.PagesProcessed,
		}
		s.log.LogWarnf("No content to process - returning empty result")
	} else {
		// Call unified LLM processing for all content
		extractedData, tokenUsage, err := s.processAllContentWithUnifiedLLM(ctx, rawContentList, &analysis)
		if err != nil {
			s.log.LogErrorf("Unified LLM processing failed: %v", err)
			finalData = []interface{}{}
			// Estimate tokens for error case
			totalContentLength := 0
			for _, item := range rawContentList {
				totalContentLength += len(item.Content)
			}
			finalTokenUsage = &TokenUsage{
				InputTokens:    int32(totalContentLength / 4), // Rough estimate
				OutputTokens:   100,                           // Small output for error
				TotalTokens:    int32(totalContentLength/4) + 100,
				PagesProcessed: workflowCtx.PagesProcessed,
			}
		} else {
			finalData = extractedData
			// Aggregate tokens from analysis step + content processing step
			finalTokenUsage = &TokenUsage{
				InputTokens:    workflowCtx.TotalInputTokens + tokenUsage.InputTokens,
				OutputTokens:   workflowCtx.TotalOutputTokens + tokenUsage.OutputTokens,
				TotalTokens:    (workflowCtx.TotalInputTokens + tokenUsage.InputTokens) + (workflowCtx.TotalOutputTokens + tokenUsage.OutputTokens),
				PagesProcessed: tokenUsage.PagesProcessed,
			}
			s.log.LogInfof("Total token usage: analysis(%d+%d) + content(%d+%d) = %d total tokens",
				workflowCtx.TotalInputTokens, workflowCtx.TotalOutputTokens,
				tokenUsage.InputTokens, tokenUsage.OutputTokens,
				finalTokenUsage.TotalTokens)
		}
	}

	success := len(rawContentList) > 0 && finalData != nil
	return &ParseResult{
		Success:    success,
		Data:       finalData,
		Errors:     errors,
		TokenUsage: finalTokenUsage,
	}, nil
}

// processAllContentWithUnifiedLLM - NEW: Single LLM call to process all collected content
func (s *Service) processAllContentWithUnifiedLLM(ctx context.Context, rawContentList []RawContentItem, analysis *PromptAnalysis) (interface{}, *TokenUsage, error) {
	if len(rawContentList) == 0 {
		return []interface{}{}, &TokenUsage{}, nil
	}

	if StreamTestMode {
		return s.simulateStreamingLLMAggregation(ctx, rawContentList)
	}

	// Prepare content for LLM processing
	var contentBuilder strings.Builder
	operationType := "scrape"
	if analysis != nil && analysis.Action == "crawl" {
		operationType = "crawl"
	}

	extractionGoal := "Extract the requested data"
	if analysis != nil && analysis.ExtractionGoal != "" {
		extractionGoal = analysis.ExtractionGoal
	}

	// Build combined content for LLM
	s.log.LogInfof("Building unified content from %d sources for LLM processing", len(rawContentList))
	for i, item := range rawContentList {
		s.log.LogInfof("Source %d: %s (%d chars)", i+1, item.URL, len(item.Content))
		contentBuilder.WriteString(fmt.Sprintf("=== SOURCE %d: %s ===\n", i+1, item.URL))
		contentBuilder.WriteString(item.Content)
		contentBuilder.WriteString("\n\n")

		// Log first few lines of each source for verification
		lines := strings.Split(item.Content, "\n")
		if len(lines) > 3 {
			line0 := strings.TrimSpace(lines[0])
			line1 := strings.TrimSpace(lines[1])
			line2 := strings.TrimSpace(lines[2])
			if len(line0) > 50 {
				line0 = line0[:50]
			}
			if len(line1) > 50 {
				line1 = line1[:50]
			}
			if len(line2) > 50 {
				line2 = line2[:50]
			}
			s.log.LogInfof("Source %d preview: %s | %s | %s", i+1, line0, line1, line2)
		}
	}

	combinedContent := contentBuilder.String()

	// Create template with unified aggregation prompt
	template := s.systemPrompts.GetUnifiedAggregationTemplate()

	// Prepare template variables
	templateVars := map[string]interface{}{
		"operation_type":  operationType,
		"extraction_goal": extractionGoal,
		"num_sources":     len(rawContentList),
		"content_data":    combinedContent,
	}

	// Format the prompt
	messages, err := template.Format(ctx, templateVars)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to format unified aggregation template: %w", err)
	}

	s.log.LogInfof("Processing %d content sources with unified LLM (%s operation)", len(rawContentList), operationType)

	// Create JSON schema to enforce proper output format
	aggregationSchema := &jsonschema.Schema{
		Description: "Extracted data in universal format - either array of objects or single object",
		OneOf: []*jsonschema.Schema{
			{
				Type: "array",
				Items: &jsonschema.Schema{
					Type: "object",
				},
				Description: "Array of extracted objects",
			},
			{
				Type:        "object",
				Description: "Single extracted object",
			},
		},
	}

	s.log.LogInfof("Enforcing JSON schema for aggregation output")

	// Stream that we're starting LLM aggregation
	s.publishLLMAggregationEvent(ctx, "llm.aggregation_start", "Starting LLM aggregation of collected content...")

	// Call LLM with token tracking and JSON schema enforcement
	response, tokenUsage, err := s.eino.GenerateWithTokenUsage(ctx, messages,
		model.WithTemperature(0.1),
		model.WithMaxTokens(2000),
		gemini.WithResponseJSONSchema(aggregationSchema),
	)

	if err != nil {
		s.publishLLMAggregationEvent(ctx, "llm.aggregation_error", fmt.Sprintf("LLM aggregation failed: %v", err))
		return nil, s.convertEinoTokenUsage(tokenUsage, len(rawContentList)), fmt.Errorf("LLM processing failed: %w", err)
	}

	// Stream the LLM response content
	s.publishLLMAggregationEvent(ctx, "llm.aggregation_content", response.Content)
	s.log.LogInfof("LLM unified processing response: %s", response.Content)

	// Parse and clean the response
	cleanedData, parseErr := s.parseAndCleanLLMResponse(response.Content)
	if parseErr != nil {
		s.log.LogWarnf("Failed to parse LLM response as JSON, returning raw: %v", parseErr)
		s.publishLLMAggregationEvent(ctx, "llm.aggregation_warning", fmt.Sprintf("JSON parsing warning: %v", parseErr))
		cleanedData = response.Content
	} else {
		// 🔍 LOG: Detailed analysis of extracted data
		s.analyzeExtractedData(cleanedData, len(rawContentList))
		s.publishLLMAggregationEvent(ctx, "llm.aggregation_success", "LLM aggregation completed successfully")
	}

	// Convert Eino token usage to our format
	finalTokenUsage := s.convertEinoTokenUsage(tokenUsage, len(rawContentList))

	return cleanedData, finalTokenUsage, nil
}

// analyzeExtractedData provides detailed logging of extracted data for debugging
func (s *Service) analyzeExtractedData(data interface{}, numSources int) {
	s.log.LogInfof("Extracted data analysis from %d sources:", numSources)

	switch v := data.(type) {
	case []interface{}:
		s.log.LogInfof("Result type: Array with %d items", len(v))

		// Track titles for duplicate detection
		titlesSeen := make(map[string]int)

		for i, item := range v {
			if itemMap, ok := item.(map[string]interface{}); ok {
				title := "N/A"
				author := "N/A"

				// Extract title (various possible keys)
				if t, exists := itemMap["title"]; exists {
					title = fmt.Sprintf("%v", t)
				} else if t, exists := itemMap["Title"]; exists {
					title = fmt.Sprintf("%v", t)
				} else if t, exists := itemMap["name"]; exists {
					title = fmt.Sprintf("%v", t)
				}

				// Extract author (various possible keys)
				if a, exists := itemMap["author"]; exists {
					author = fmt.Sprintf("%v", a)
				} else if a, exists := itemMap["Author"]; exists {
					author = fmt.Sprintf("%v", a)
				}

				// Track duplicates
				titlesSeen[title]++

				s.log.LogInfof("Item %d: Title='%s', Author='%s'", i+1, title, author)
			} else {
				s.log.LogInfof("Item %d: %v", i+1, item)
			}
		}

		// Report duplicates
		duplicates := 0
		for title, count := range titlesSeen {
			if count > 1 {
				s.log.LogWarnf("Duplicate detected: '%s' appears %d times", title, count)
				duplicates++
			}
		}

		if duplicates == 0 {
			s.log.LogInfof("No duplicates detected - all %d items are unique", len(v))
		} else {
			s.log.LogWarnf("Found %d duplicate titles", duplicates)
		}

	case map[string]interface{}:
		s.log.LogInfof("Result type: Object with %d fields", len(v))
		for key, value := range v {
			s.log.LogInfof("Field '%s': %v", key, value)
		}

	default:
		s.log.LogInfof("Result type: %T - %v", data, data)
	}
}

func (s *Service) parseAndCleanLLMResponse(response string) (interface{}, error) {
	cleaned := s.cleanJSONString(response)

	var cleanedStr string
	if str, ok := cleaned.(string); ok {
		cleanedStr = str
	} else {
		cleanedStr = response
	}

	var result interface{}
	if err := json.Unmarshal([]byte(cleanedStr), &result); err != nil {
		return nil, fmt.Errorf("JSON parsing failed: %w", err)
	}

	return result, nil
}

// convertEinoTokenUsage converts Eino's token usage format to our internal format
func (s *Service) convertEinoTokenUsage(einoTokenUsage *eino.TokenUsage, pagesProcessed int) *TokenUsage {
	if einoTokenUsage == nil {
		return &TokenUsage{
			InputTokens:    0,
			OutputTokens:   0,
			TotalTokens:    0,
			PagesProcessed: pagesProcessed,
		}
	}

	return &TokenUsage{
		InputTokens:    einoTokenUsage.InputTokens,
		OutputTokens:   einoTokenUsage.OutputTokens,
		TotalTokens:    einoTokenUsage.InputTokens + einoTokenUsage.OutputTokens,
		PagesProcessed: pagesProcessed,
	}
}

// hasValidData checks if a result contains meaningful data
func (s *Service) hasValidData(data interface{}) bool {
	if data == nil {
		return false
	}

	if str, ok := data.(string); ok {
		// Skip obvious error messages
		if strings.Contains(strings.ToLower(str), "i am sorry") ||
			strings.Contains(strings.ToLower(str), "cannot find") ||
			strings.TrimSpace(str) == "" {
			return false
		}
		return true
	}

	if arr, ok := data.([]interface{}); ok {
		return len(arr) > 0
	}

	if obj, ok := data.(map[string]interface{}); ok {
		// Check if object has meaningful content (not just empty arrays)
		for _, value := range obj {
			if s.hasValidData(value) {
				return true
			}
		}
		return false
	}

	return true
}

// cleanJSONString removes markdown code fences and cleans JSON strings
func (s *Service) cleanJSONString(str string) interface{} {
	trimmed := strings.TrimSpace(str)

	// Remove markdown code fences
	if strings.HasPrefix(trimmed, "```") {
		trimmed = strings.TrimPrefix(trimmed, "```")
		// Remove language identifier (json, etc.)
		if idx := strings.Index(trimmed, "\n"); idx != -1 {
			trimmed = trimmed[idx+1:]
		}
	}
	trimmed = strings.TrimSuffix(trimmed, "```")

	return strings.TrimSpace(trimmed)
}

// streamScrapeContent handles single page scraping
func (s *Service) streamScrapeContent(ctx context.Context, analysis PromptAnalysis, writer *schema.StreamWriter[*PageData]) {
	for _, url := range analysis.URLs {
		// Stream that we're starting to scrape this URL
		s.publishLinkProgressEvent(ctx, "scrape.link_start", url, "Starting to scrape")

		if StreamTestMode {
			time.Sleep(1 * time.Second)
			mockContent := "# Blog Posts\n\nThis is mock content from supacrawler.com/blog"
			mockTitle := "Supacrawler Blog"
			pageContent := &engineapi.PageContent{Markdown: mockContent}
			pageContent.Metadata.Title = &mockTitle

			// Stream success
			s.publishLinkProgressEvent(ctx, "scrape.link_success", url, "Successfully scraped")
			writer.Send(&PageData{URL: url, PageContent: pageContent}, nil)
			continue
		}

		format := engineapi.GetV1ScrapeParamsFormat("markdown")
		params := engineapi.GetV1ScrapeParams{
			Url:    url,
			Format: &format,
		}

		result, err := s.scrapeService.ScrapeURL(ctx, params)
		if err != nil {
			// Stream error
			// s.publishLinkProgressEvent(ctx, "scrape.link_error", url, fmt.Sprintf("Scraping failed: %v", err))
			writer.Send(&PageData{
				URL:   url,
				Error: err.Error(),
			}, nil)
			continue
		}

		content := ""
		if result.Content != nil {
			content = *result.Content
		}

		title := ""
		if result.Title != nil {
			title = *result.Title
		}

		pageContent := &engineapi.PageContent{
			Markdown: content,
		}
		if title != "" {
			pageContent.Metadata.Title = &title
		}

		// Stream success
		s.publishLinkProgressEvent(ctx, "scrape.link_success", url, "Successfully scraped")
		writer.Send(&PageData{
			URL:         url,
			PageContent: pageContent,
		}, nil)
	}
}

// streamCrawlContent handles multi-page crawling with streaming using the new public wrapper
func (s *Service) streamCrawlContent(ctx context.Context, analysis PromptAnalysis, writer *schema.StreamWriter[*PageData]) {
	if len(analysis.URLs) == 0 {
		return
	}

	// Build crawl request from analysis and crawl config
	crawlReq := engineapi.CrawlCreateRequest{
		Url:       analysis.URLs[0], // Use first URL as base
		LinkLimit: &analysis.MaxPages,
	}

	// Always use renderJs=true for parse operations
	renderJs := true
	crawlReq.RenderJs = &renderJs

	// Apply crawl configuration if provided
	if analysis.CrawlConfig != nil {
		if len(analysis.CrawlConfig.Patterns) > 0 {
			crawlReq.Patterns = &analysis.CrawlConfig.Patterns
			s.log.LogInfof("Using LLM-generated patterns: %v", analysis.CrawlConfig.Patterns)
		}
		if analysis.CrawlConfig.Depth != nil {
			crawlReq.Depth = analysis.CrawlConfig.Depth
		}
		if analysis.CrawlConfig.IncludeSubdomains != nil {
			crawlReq.IncludeSubdomains = analysis.CrawlConfig.IncludeSubdomains
		}
		if analysis.CrawlConfig.Fresh != nil {
			crawlReq.Fresh = analysis.CrawlConfig.Fresh
		}
	} else {
		// Fallback defaults if no crawl config provided
		depth := 2
		fresh := false
		crawlReq.Depth = &depth
		crawlReq.Fresh = &fresh
		s.log.LogInfof("Using fallback crawl defaults: depth=2, fresh=false")
	}

	s.log.LogInfof("Crawl request configured: renderJs=true, patterns=%v, depth=%v, fresh=%v, LinkLimit=%v",
		crawlReq.Patterns, crawlReq.Depth, crawlReq.Fresh, crawlReq.LinkLimit)

	// Create a channel to receive streaming results
	pageChan := make(chan *crawl.PageResult, 100)

	// Start streaming crawl in goroutine or simulate if in test mode
	if StreamTestMode {
		go s.simulateRealisticCrawling(ctx, pageChan)
	} else {
		go s.crawlService.StreamCrawlToChannel(ctx, crawlReq, pageChan)
	}

	// Process crawl results in real-time with immediate LLM extraction
	totalPages := 0
	successfulPages := 0
	var wg sync.WaitGroup

	for pageResult := range pageChan {
		totalPages++

		if pageResult.Error != "" {
			// Stream crawl error
			s.publishLinkProgressEvent(ctx, "crawl.link_error", pageResult.URL, fmt.Sprintf("Crawling failed: %s", pageResult.Error))
			writer.Send(&PageData{
				URL:   pageResult.URL,
				Error: pageResult.Error,
			}, nil)
		} else {
			successfulPages++

			// Stream crawl success
			s.publishLinkProgressEvent(ctx, "crawl.link_success", pageResult.URL, "Successfully crawled")

			// NEW: Send raw content directly - NO real-time LLM processing
			pageData := &PageData{
				URL:         pageResult.URL,
				PageContent: pageResult.PageContent,
			}

			s.log.LogDebugf("Forwarding raw content from: %s", pageResult.URL)
			writer.Send(pageData, nil)
		}
	}

	wg.Wait()
	s.log.LogInfof("Crawl completed: %d total pages, %d successful (raw content forwarded)", totalPages, successfulPages)
}

// simulateRealisticCrawling simulates the crawl flow based on real log data for testing
func (s *Service) simulateRealisticCrawling(ctx context.Context, pageChan chan<- *crawl.PageResult) {
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

	s.log.LogInfof("🧪 [StreamTestMode] Starting realistic crawl simulation with %d URLs", len(crawlSequence))

	for i, item := range crawlSequence {
		select {
		case <-ctx.Done():
			s.log.LogInfof("🧪 [StreamTestMode] Crawl simulation cancelled")
			return
		default:
			// Simulate realistic processing delay
			time.Sleep(item.delay)

			// Create realistic page content
			title := extractTitleFromURL(item.url)
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

			s.log.LogInfof("🧪 [StreamTestMode] Sending page %d/%d: %s", i+1, len(crawlSequence), item.url)
			pageChan <- pageResult
		}
	}

	s.log.LogInfof("🧪 [StreamTestMode] Crawl simulation completed - sent %d pages", len(crawlSequence))
}

// extractTitleFromURL creates a readable title from URL for simulation
func extractTitleFromURL(url string) string {
	// Simple title extraction for demo
	if strings.Contains(url, "company-announcements") {
		return "OpenAI Company Announcements"
	} else if strings.Contains(url, "research") {
		return "OpenAI Research"
	} else if strings.Contains(url, "product-releases") {
		return "OpenAI Product Releases"
	} else if strings.Contains(url, "safety-alignment") {
		return "OpenAI Safety & Alignment"
	} else if strings.Contains(url, "security") {
		return "OpenAI Security"
	} else if strings.Contains(url, "global-affairs") {
		return "OpenAI Global Affairs"
	} else if strings.Contains(url, "/news") {
		return "OpenAI News"
	}
	return "OpenAI Page"
}

// simulateStreamingLLMAggregation simulates realistic LLM processing with streaming events
func (s *Service) simulateStreamingLLMAggregation(ctx context.Context, rawContentList []RawContentItem) (interface{}, *TokenUsage, error) {
	s.log.LogInfof("🧪 [StreamTestMode] Starting LLM aggregation simulation for %d sources", len(rawContentList))

	// Stream that we're starting LLM aggregation
	s.publishLLMAggregationEvent(ctx, "llm.aggregation_start", "Starting LLM aggregation of collected content...")

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
		s.publishLLMAggregationEvent(ctx, "llm.aggregation_progress", progress.content)
	}

	// Generate realistic output based on URLs that were actually crawled
	demoData := s.generateRealisticOpenAINewsData(rawContentList)

	// Stream the final LLM response content
	responseContent := s.formatResponseAsJSON(demoData)
	s.publishLLMAggregationEvent(ctx, "llm.aggregation_content", responseContent)

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

	s.publishLLMAggregationEvent(ctx, "llm.aggregation_success", "LLM aggregation completed successfully")
	s.log.LogInfof("🧪 [StreamTestMode] LLM simulation completed - processed %d pages", len(rawContentList))

	return demoData, tokenUsage, nil
}

// generateRealisticOpenAINewsData creates realistic data based on crawled URLs matching real API format
func (s *Service) generateRealisticOpenAINewsData(rawContentList []RawContentItem) []map[string]interface{} {
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

// formatResponseAsJSON formats the response data as JSON string for streaming
func (s *Service) formatResponseAsJSON(data []map[string]interface{}) string {
	jsonBytes, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return `[{"error": "Failed to format response"}]`
	}
	return string(jsonBytes)
}

// fallbackPromptAnalysis provides regex-based analysis when LLM fails
func (s *Service) fallbackPromptAnalysis(req engineapi.ParseCreateRequest) *PromptAnalysis {
	// Extract URLs using regex
	urlRegex := regexp.MustCompile(`(?:https?://)?[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}(?:/[^\s]*)?`)
	urls := urlRegex.FindAllString(req.Prompt, -1)

	// Ensure URLs have protocol
	for i, url := range urls {
		if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
			urls[i] = "https://" + url
		}
	}

	// Determine action based on keywords
	action := "scrape"
	if strings.Contains(strings.ToLower(req.Prompt), "crawl") ||
		strings.Contains(strings.ToLower(req.Prompt), "multiple") ||
		strings.Contains(strings.ToLower(req.Prompt), "all pages") {
		action = "crawl"
	}

	// Determine output format
	outputFormat := "json"
	if req.OutputFormat != nil {
		outputFormat = string(*req.OutputFormat)
	} else if strings.Contains(strings.ToLower(req.Prompt), "csv") {
		outputFormat = "csv"
	}

	maxPages := 10
	if req.MaxPages != nil {
		maxPages = *req.MaxPages
	}

	schema := make(map[string]interface{})
	if req.Schema != nil {
		schema = *req.Schema
	}

	return &PromptAnalysis{
		Action:         action,
		URLs:           urls,
		OutputFormat:   outputFormat,
		MaxPages:       maxPages,
		ExtractionGoal: req.Prompt,
		Schema:         schema,
	}
}

func (s *Service) GetAvailableTemplates() map[string]string {
	return map[string]string{
		"intelligent_workflow": "AI-powered parsing with automatic scrape/crawl detection",
		"streaming_processing": "Real-time streaming workflow for large crawling operations",
		"schema_extraction":    "Structured data extraction with custom JSON schemas",
	}
}

func (s *Service) GetSupportedContentTypes() []string {
	return []string{"any"}
}

func (s *Service) GetSupportedOutputFormats() []string {
	return []string{"json", "csv", "markdown", "xml", "yaml"}
}

func (s *Service) GetExampleOutputSpecs() map[string]map[string]interface{} {
	return map[string]map[string]interface{}{
		"blog_crawl_streaming": {
			"prompt": "Crawl https://example.com/blog and stream the latest posts",
			"schema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"title": map[string]interface{}{"type": "string"},
					"date":  map[string]interface{}{"type": "string"},
					"url":   map[string]interface{}{"type": "string"},
				},
			},
		},
		"product_scrape_single": {
			"prompt": "Extract product details from https://shop.example.com/product/123",
			"schema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"name":  map[string]interface{}{"type": "string"},
					"price": map[string]interface{}{"type": "number"},
				},
			},
		},
	}
}

// publishProgressEvent publishes essential user-facing progress events only
func (s *Service) publishProgressEvent(ctx context.Context, event, message string) {
	// Only publish essential user progress events, not internal Eino workflow noise
	if tracer, ok := ctx.Value("eino_tracer").(*EinoTracer); ok && tracer != nil {
		var step string
		switch event {
		case "parse.analyzing":
			step = "analyze_prompt"
		case "parse.processing":
			step = "process_content"
		case "parse.aggregating":
			step = "aggregate_results"
		default:
			return
		}

		traceEvent := map[string]interface{}{
			"jobId":     tracer.jobID,
			"event":     event,
			"component": "user",
			"timing":    "progress",
			"timestamp": time.Now().UnixMilli(),
			"metadata":  map[string]interface{}{"message": message, "step": step},
		}
		tracer.jobService.PublishJobTrace(ctx, tracer.jobID, traceEvent)
	}

	if StreamTestMode {
		time.Sleep(300 * time.Millisecond)
	}
}

// publishLinkProgressEvent publishes link-level crawling/scraping events
func (s *Service) publishLinkProgressEvent(ctx context.Context, event, url, message string) {
	if tracer, ok := ctx.Value("eino_tracer").(*EinoTracer); ok && tracer != nil {
		traceEvent := map[string]interface{}{
			"jobId":     tracer.jobID,
			"event":     event,
			"component": "link",
			"timing":    "progress",
			"timestamp": time.Now().UnixMilli(),
			"metadata": map[string]interface{}{
				"url":     url,
				"message": message,
			},
		}
		tracer.jobService.PublishJobTrace(ctx, tracer.jobID, traceEvent)
	}

	if StreamTestMode {
		time.Sleep(100 * time.Millisecond)
	}
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
