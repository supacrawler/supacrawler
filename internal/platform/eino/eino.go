package eino

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/prompt"
	"github.com/cloudwego/eino/schema"

	// LLM Provider integrations - easily switchable
	gemini "github.com/cloudwego/eino-ext/components/model/gemini"
	// openai "github.com/cloudwego/eino-ext/components/model/openai" // Uncomment to use OpenAI
	// claude "github.com/cloudwego/eino-ext/components/model/claude" // Uncomment to use Claude
	// ollama "github.com/cloudwego/eino-ext/components/model/ollama" // Uncomment to use Ollama
	"google.golang.org/genai"
)

// Config represents the configuration for Eino LLM integration
type Config struct {
	Provider string `json:"provider"` // "gemini", "openai", "claude", etc.
	APIKey   string `json:"api_key"`
	BaseURL  string `json:"base_url,omitempty"`
	Model    string `json:"model"`
}

// Service wraps the Eino LLM functionality with proper Eino integration
type Service struct {
	config       Config
	chatModel    model.BaseChatModel
	chatTemplate prompt.ChatTemplate
	geminiClient *genai.Client
}

// ParseRequest represents a parsing request with dynamic schema
type ParseRequest struct {
	HTMLContent string                 `json:"html_content"`
	UserPrompt  string                 `json:"user_prompt"`
	OutputSpec  map[string]interface{} `json:"output_spec"` // Dynamic DTO specification
}

// ParseResponse represents the parsed result
type ParseResponse struct {
	Success bool                   `json:"success"`
	Data    map[string]interface{} `json:"data"`
	Error   string                 `json:"error,omitempty"`
}

// TokenUsage represents token usage information
type TokenUsage struct {
	InputTokens  int32 `json:"input_tokens"`
	OutputTokens int32 `json:"output_tokens"`
	TotalTokens  int32 `json:"total_tokens"`
}

// NewService creates a new Eino service instance with proper provider initialization
func NewService(config Config) (*Service, error) {
	service := &Service{config: config}

	// Initialize the chat model based on provider
	if err := service.initializeChatModel(); err != nil {
		return nil, fmt.Errorf("failed to initialize chat model: %w", err)
	}

	// Initialize the chat template
	service.initializeChatTemplate()

	return service, nil
}

// NewServiceWithModel creates a new Eino service instance with a pre-configured chat model
func NewServiceWithModel(config Config, chatModel model.BaseChatModel) (*Service, error) {
	service := &Service{
		config:    config,
		chatModel: chatModel,
	}

	// Initialize the chat template
	service.initializeChatTemplate()

	return service, nil
}

// initializeChatModel initializes the chat model based on provider using proper Eino components
func (s *Service) initializeChatModel() error {
	switch strings.ToLower(s.config.Provider) {
	case "gemini":
		return s.initializeGeminiModel()

	// case "openai":
	// 	return s.initializeOpenAIModel()
	//
	// case "claude":
	// 	return s.initializeClaudeModel()
	//
	// case "ollama":
	// 	return s.initializeOllamaModel()

	default:
		return fmt.Errorf("unsupported provider: %s. Supported: gemini", s.config.Provider)
	}
}

// initializeGeminiModel sets up Google Gemini as the LLM provider
func (s *Service) initializeGeminiModel() error {
	// Initialize Gemini client with API key
	client, err := genai.NewClient(context.Background(), &genai.ClientConfig{
		APIKey: s.config.APIKey,
	})
	if err != nil {
		return fmt.Errorf("failed to create Gemini client: %w", err)
	}

	// Store client for token counting
	s.geminiClient = client

	// Create Gemini chat model using Eino's Gemini component
	geminiModel, err := gemini.NewChatModel(context.Background(), &gemini.Config{
		Client: client,
		Model:  s.config.Model, // e.g., "gemini-1.5-flash", "gemini-1.5-pro"
	})
	if err != nil {
		return fmt.Errorf("failed to create Gemini chat model: %w", err)
	}

	s.chatModel = geminiModel
	return nil
}

// initializeOpenAIModel sets up OpenAI as the LLM provider
// Uncomment the openai import above and this function to use OpenAI
// func (s *Service) initializeOpenAIModel() error {
// 	// Create OpenAI chat model using Eino's OpenAI component
// 	openaiModel, err := openai.NewChatModel(context.Background(), &openai.Config{
// 		APIKey:  s.config.APIKey,
// 		BaseURL: s.config.BaseURL, // Optional: for custom endpoints
// 		Model:   s.config.Model,   // e.g., "gpt-4", "gpt-3.5-turbo"
// 	})
// 	if err != nil {
// 		return fmt.Errorf("failed to create OpenAI chat model: %w", err)
// 	}
//
// 	s.chatModel = openaiModel
// 	return nil
// }

// initializeClaudeModel sets up Anthropic Claude as the LLM provider
// Uncomment the claude import above and this function to use Claude
// func (s *Service) initializeClaudeModel() error {
// 	// Create Claude chat model using Eino's Claude component
// 	claudeModel, err := claude.NewChatModel(context.Background(), &claude.Config{
// 		APIKey:  s.config.APIKey,
// 		BaseURL: s.config.BaseURL, // Optional: for custom endpoints
// 		Model:   s.config.Model,   // e.g., "claude-3-sonnet", "claude-3-haiku"
// 	})
// 	if err != nil {
// 		return fmt.Errorf("failed to create Claude chat model: %w", err)
// 	}
//
// 	s.chatModel = claudeModel
// 	return nil
// }

// initializeOllamaModel sets up Ollama as the LLM provider for local models
// Uncomment the ollama import above and this function to use Ollama
// func (s *Service) initializeOllamaModel() error {
// 	// Create Ollama chat model using Eino's Ollama component
// 	ollamaModel, err := ollama.NewChatModel(context.Background(), &ollama.Config{
// 		BaseURL: s.config.BaseURL, // Required: Ollama server URL (e.g., "http://localhost:11434")
// 		Model:   s.config.Model,   // e.g., "llama2", "codellama", "mistral"
// 	})
// 	if err != nil {
// 		return fmt.Errorf("failed to create Ollama chat model: %w", err)
// 	}
//
// 	s.chatModel = ollamaModel
// 	return nil
// }

// initializeChatTemplate initializes the chat template using Eino's prompt components
func (s *Service) initializeChatTemplate() {
	// Create a system message template for HTML parsing
	systemTemplate := schema.SystemMessage(`You are an expert HTML parser that extracts structured data from HTML content.

CRITICAL REQUIREMENTS:
1. You MUST return ONLY a valid JSON response that exactly matches the provided schema
2. Do NOT include any explanations, markdown formatting, or additional text
3. If you cannot extract certain fields, use null values
4. All field names and types must match the schema exactly
5. The response must be parseable as valid JSON

REQUIRED OUTPUT SCHEMA:
{output_spec}

Remember: Return ONLY the JSON object. No other text, formatting, or explanations.`)

	// Create a user message template
	userTemplate := schema.UserMessage(`USER REQUEST: {user_prompt}

HTML CONTENT TO PARSE:
{html_content}

Extract the requested information according to the schema provided in the system message. Return only the JSON response.`)

	// Create chat template using Eino's prompt.FromMessages
	s.chatTemplate = prompt.FromMessages(
		schema.FString, // Use Python f-string formatting
		systemTemplate,
		userTemplate,
	)
}

// ParseHTML parses HTML content using LLM with enforced output structure
func (s *Service) ParseHTML(ctx context.Context, req ParseRequest) (*ParseResponse, error) {
	if s.chatModel == nil {
		return nil, fmt.Errorf("chat model not initialized")
	}

	// Prepare template variables
	outputSpecJSON, err := json.MarshalIndent(req.OutputSpec, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to marshal output spec: %w", err)
	}

	// Clean HTML content for LLM processing
	cleanHTML := s.cleanHTMLForLLM(req.HTMLContent)

	templateVars := map[string]any{
		"output_spec":  string(outputSpecJSON),
		"user_prompt":  req.UserPrompt,
		"html_content": cleanHTML,
	}

	// Format the template using Eino's chat template
	messages, err := s.chatTemplate.Format(ctx, templateVars)
	if err != nil {
		return nil, fmt.Errorf("failed to format chat template: %w", err)
	}

	// Call the LLM using Eino's chat model
	response, err := s.chatModel.Generate(ctx, messages)
	if err != nil {
		return &ParseResponse{
			Success: false,
			Error:   fmt.Sprintf("LLM generation failed: %v", err),
		}, nil
	}

	// Parse the response
	data, err := s.parseResponse(response.Content, req.OutputSpec)
	if err != nil {
		return &ParseResponse{
			Success: false,
			Error:   fmt.Sprintf("Failed to parse LLM response: %v", err),
		}, nil
	}

	return &ParseResponse{
		Success: true,
		Data:    data,
	}, nil
}

// ParseHTMLWithTemplate parses HTML using a custom Eino chat template
func (s *Service) ParseHTMLWithTemplate(ctx context.Context, req ParseRequest, customTemplate prompt.ChatTemplate) (*ParseResponse, error) {
	if s.chatModel == nil {
		return nil, fmt.Errorf("chat model not initialized")
	}

	// Prepare template variables
	outputSpecJSON, err := json.MarshalIndent(req.OutputSpec, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to marshal output spec: %w", err)
	}

	templateVars := map[string]any{
		"output_spec":  string(outputSpecJSON),
		"user_prompt":  req.UserPrompt,
		"html_content": s.cleanHTMLForLLM(req.HTMLContent),
	}

	// Format the custom template
	messages, err := customTemplate.Format(ctx, templateVars)
	if err != nil {
		return nil, fmt.Errorf("failed to format custom template: %w", err)
	}

	// Call the LLM
	response, err := s.chatModel.Generate(ctx, messages)
	if err != nil {
		return &ParseResponse{
			Success: false,
			Error:   fmt.Sprintf("LLM generation failed: %v", err),
		}, nil
	}

	// Parse the response
	data, err := s.parseResponse(response.Content, req.OutputSpec)
	if err != nil {
		return &ParseResponse{
			Success: false,
			Error:   fmt.Sprintf("Failed to parse LLM response: %v", err),
		}, nil
	}

	return &ParseResponse{
		Success: true,
		Data:    data,
	}, nil
}

// cleanHTMLForLLM prepares HTML content for LLM processing
func (s *Service) cleanHTMLForLLM(html string) string {
	// Remove common noise from HTML
	html = strings.ReplaceAll(html, "\r\n", "\n")
	html = strings.ReplaceAll(html, "\r", "\n")

	// Remove excessive whitespace
	lines := strings.Split(html, "\n")
	var cleanLines []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			cleanLines = append(cleanLines, trimmed)
		}
	}

	cleaned := strings.Join(cleanLines, "\n")

	// Limit size for LLM context window
	const maxLength = 10000
	if len(cleaned) > maxLength {
		cleaned = cleaned[:maxLength] + "\n...[content truncated for processing]"
	}

	return cleaned
}

// parseResponse parses the LLM response and validates against the expected schema
func (s *Service) parseResponse(content string, outputSpec map[string]interface{}) (map[string]interface{}, error) {
	// Clean the response (remove potential markdown formatting)
	content = strings.TrimSpace(content)
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	content = strings.TrimSpace(content)

	// Parse JSON
	var result map[string]interface{}
	if err := json.Unmarshal([]byte(content), &result); err != nil {
		return nil, fmt.Errorf("invalid JSON response: %w", err)
	}

	// Validate structure matches output specification
	if err := s.validateStructure(result, outputSpec); err != nil {
		return nil, fmt.Errorf("response structure validation failed: %w", err)
	}

	return result, nil
}

// validateStructure ensures the response matches the expected schema
func (s *Service) validateStructure(response, spec map[string]interface{}) error {
	for key, expectedType := range spec {
		value, exists := response[key]
		if !exists {
			// Allow missing fields - they should be null
			continue
		}

		if value == nil {
			// Null values are acceptable
			continue
		}

		// Basic type checking
		if err := s.validateFieldType(key, value, expectedType); err != nil {
			return err
		}
	}

	return nil
}

// validateFieldType validates individual field types
func (s *Service) validateFieldType(fieldName string, value, expectedType interface{}) error {
	switch et := expectedType.(type) {
	case string:
		switch et {
		case "string":
			if _, ok := value.(string); !ok {
				return fmt.Errorf("field '%s' should be string, got %T", fieldName, value)
			}
		case "number":
			if !isNumeric(value) {
				return fmt.Errorf("field '%s' should be number, got %T", fieldName, value)
			}
		case "boolean":
			if _, ok := value.(bool); !ok {
				return fmt.Errorf("field '%s' should be boolean, got %T", fieldName, value)
			}
		case "array":
			if !isArray(value) {
				return fmt.Errorf("field '%s' should be array, got %T", fieldName, value)
			}
		case "object":
			if !isObject(value) {
				return fmt.Errorf("field '%s' should be object, got %T", fieldName, value)
			}
		}
	case map[string]interface{}:
		// Nested object validation
		if valueMap, ok := value.(map[string]interface{}); ok {
			return s.validateStructure(valueMap, et)
		}
		return fmt.Errorf("field '%s' should be object, got %T", fieldName, value)
	}

	return nil
}

// Helper functions for type checking
func isNumeric(value interface{}) bool {
	switch value.(type) {
	case float64, float32, int, int64, int32:
		return true
	default:
		return false
	}
}

func isArray(value interface{}) bool {
	switch value.(type) {
	case []interface{}:
		return true
	default:
		return false
	}
}

func isObject(value interface{}) bool {
	_, ok := value.(map[string]interface{})
	return ok
}

// GetChatModel returns the underlying chat model for advanced usage
func (s *Service) GetChatModel() model.BaseChatModel {
	return s.chatModel
}

// GetChatTemplate returns the chat template for customization
func (s *Service) GetChatTemplate() prompt.ChatTemplate {
	return s.chatTemplate
}

// SetChatTemplate allows setting a custom chat template
func (s *Service) SetChatTemplate(template prompt.ChatTemplate) {
	s.chatTemplate = template
}

// GetAvailableProviders returns list of supported LLM providers
func GetAvailableProviders() []string {
	return []string{
		"gemini", // Active: Google Gemini (gemini-1.5-flash, gemini-1.5-pro)
		// "openai",  // Commented: OpenAI (gpt-4, gpt-3.5-turbo) - uncomment import & function
		// "claude",  // Commented: Anthropic Claude (claude-3-sonnet, claude-3-haiku) - uncomment import & function
		// "ollama",  // Commented: Local Ollama (llama2, codellama, mistral) - uncomment import & function
	}
}

// GetProviderInstructions returns setup instructions for each provider
func GetProviderInstructions() map[string]string {
	return map[string]string{
		"gemini": "Set GEMINI_API_KEY environment variable. Get key from: https://aistudio.google.com/app/apikey",
		"openai": "Set OPENAI_API_KEY environment variable. Get key from: https://platform.openai.com/api-keys",
		"claude": "Set CLAUDE_API_KEY environment variable. Get key from: https://console.anthropic.com/",
		"ollama": "Set OLLAMA_BASE_URL (e.g., http://localhost:11434). Install: https://ollama.ai/",
	}
}

// CountPromptTokens counts input tokens for a prompt using Gemini's official CountTokens API
func (s *Service) CountPromptTokens(ctx context.Context, messages []*schema.Message) (int32, error) {
	if s.geminiClient == nil {
		return 0, fmt.Errorf("gemini client not initialized")
	}

	// Convert Eino messages to Gemini Content format
	var contents []*genai.Content
	for _, msg := range messages {
		content := genai.NewContentFromText(msg.Content, genai.RoleUser)
		contents = append(contents, content)
	}

	// Use CountTokens API as shown in the documentation
	countResp, err := s.geminiClient.Models.CountTokens(ctx, s.config.Model, contents, nil)
	if err != nil {
		return 0, fmt.Errorf("failed to count tokens with Gemini API: %w", err)
	}

	return countResp.TotalTokens, nil
}

// CountResponseTokens estimates output tokens from a text response
func (s *Service) CountResponseTokens(responseText string) int32 {
	// Simple estimation: ~4 characters per token (as documented by Gemini)
	return int32(len(responseText) / 4)
}

// CountTokensInText counts tokens in any text string using character estimation
func (s *Service) CountTokensInText(text string) int32 {
	// Use the Gemini documented ratio of ~4 characters per token
	return int32(len(text) / 4)
}

// ExtractTokenUsageFromResponse extracts token usage from Gemini GenerateContent response
func (s *Service) ExtractTokenUsageFromResponse(response interface{}) *TokenUsage {
	usage := &TokenUsage{}

	// Try to extract usage metadata from Gemini response
	// The response should contain UsageMetadata with token counts
	if respMap, ok := response.(map[string]interface{}); ok {
		if usageMetadata, ok := respMap["UsageMetadata"].(map[string]interface{}); ok {
			if promptTokens, ok := usageMetadata["prompt_token_count"].(int32); ok {
				usage.InputTokens = promptTokens
			}
			if candidateTokens, ok := usageMetadata["candidates_token_count"].(int32); ok {
				usage.OutputTokens = candidateTokens
			}
			if totalTokens, ok := usageMetadata["total_token_count"].(int32); ok {
				usage.TotalTokens = totalTokens
			}
		}
	}

	// If we didn't get the structured data, calculate total
	if usage.TotalTokens == 0 {
		usage.TotalTokens = usage.InputTokens + usage.OutputTokens
	}

	return usage
}

// ExtractTokenUsage extracts token usage from schema.Message (legacy method)
func (s *Service) ExtractTokenUsage(response *schema.Message) *TokenUsage {
	if response == nil {
		return &TokenUsage{}
	}

	// Estimate output tokens from response content
	usage := &TokenUsage{}
	if response.Content != "" {
		usage.OutputTokens = int32(len(response.Content) / 4) // Rough estimate
	}

	return usage
}

// GenerateWithTokenUsage generates a response using Gemini API directly to get accurate token usage
func (s *Service) GenerateWithTokenUsage(ctx context.Context, messages []*schema.Message, options ...model.Option) (*schema.Message, *TokenUsage, error) {
	if s.geminiClient == nil {
		return nil, nil, fmt.Errorf("gemini client not initialized")
	}

	// Convert Eino messages to Gemini Content format
	var contents []*genai.Content
	for _, msg := range messages {
		content := genai.NewContentFromText(msg.Content, genai.RoleUser)
		contents = append(contents, content)
	}

	// Use Gemini API directly to get accurate token usage from UsageMetadata
	response, err := s.geminiClient.Models.GenerateContent(ctx, s.config.Model, contents, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("gemini api generation failed: %w", err)
	}

	// Extract token usage from UsageMetadata as shown in documentation
	usage := &TokenUsage{}
	if response.UsageMetadata != nil {
		usage.InputTokens = response.UsageMetadata.PromptTokenCount
		usage.OutputTokens = response.UsageMetadata.CandidatesTokenCount
		usage.TotalTokens = response.UsageMetadata.TotalTokenCount
	}

	// Convert Gemini response back to Eino format
	responseContent := ""
	if len(response.Candidates) > 0 && response.Candidates[0].Content != nil && len(response.Candidates[0].Content.Parts) > 0 {
		if textPart := response.Candidates[0].Content.Parts[0].Text; textPart != "" {
			responseContent = textPart
		}
	}

	// Fallback token counting if metadata is not available
	if usage.TotalTokens == 0 {
		// Use fallback estimation
		usage.InputTokens = s.CountTokensInText(s.messagesToText(messages))
		usage.OutputTokens = s.CountResponseTokens(responseContent)
		usage.TotalTokens = usage.InputTokens + usage.OutputTokens
	}

	einoResponse := &schema.Message{
		Content: responseContent,
	}

	return einoResponse, usage, nil
}

// messagesToText converts messages to a single text string for token counting
func (s *Service) messagesToText(messages []*schema.Message) string {
	var text strings.Builder
	for _, msg := range messages {
		text.WriteString(msg.Content)
		text.WriteString("\n")
	}
	return text.String()
}
