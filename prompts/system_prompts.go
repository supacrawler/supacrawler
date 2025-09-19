package prompts

import (
	"github.com/cloudwego/eino/components/prompt"
)

// SystemPrompts contains all the prompt templates organized by use case
type SystemPrompts struct {
	// Core parsing templates
	DefaultHTML     prompt.ChatTemplate
	DefaultMarkdown prompt.ChatTemplate

	// E-commerce specific templates
	ProductJSON     prompt.ChatTemplate
	ProductCSV      prompt.ChatTemplate
	ProductBulkJSON prompt.ChatTemplate
	ProductBulkCSV  prompt.ChatTemplate

	// Content extraction templates
	ArticleJSON prompt.ChatTemplate
	ArticleCSV  prompt.ChatTemplate
	ContactJSON prompt.ChatTemplate

	// Job listing templates
	JobJSON    prompt.ChatTemplate
	JobCSV     prompt.ChatTemplate
	JobBulkCSV prompt.ChatTemplate

	// Workflow-specific templates
	PromptAnalysis    prompt.ChatTemplate
	ContentExtraction prompt.ChatTemplate
}

// NewSystemPrompts creates and initializes all prompt templates
func NewSystemPrompts() *SystemPrompts {
	sp := &SystemPrompts{}
	sp.initializePrompts()
	return sp
}

// initializePrompts sets up all the prompt templates
func (sp *SystemPrompts) initializePrompts() {
	// Initialize core parsing templates
	sp.DefaultHTML = sp.createDefaultHTMLTemplate()
	sp.DefaultMarkdown = sp.createDefaultMarkdownTemplate()

	// Initialize e-commerce templates
	sp.ProductJSON = sp.createProductJSONTemplate()
	sp.ProductCSV = sp.createProductCSVTemplate()
	sp.ProductBulkJSON = sp.createProductBulkJSONTemplate()
	sp.ProductBulkCSV = sp.createProductBulkCSVTemplate()

	// Initialize content templates
	sp.ArticleJSON = sp.createArticleJSONTemplate()
	sp.ArticleCSV = sp.createArticleCSVTemplate()
	sp.ContactJSON = sp.createContactJSONTemplate()

	// Initialize job listing templates
	sp.JobJSON = sp.createJobJSONTemplate()
	sp.JobCSV = sp.createJobCSVTemplate()
	sp.JobBulkCSV = sp.createJobBulkCSVTemplate()

	// Initialize workflow templates
	sp.PromptAnalysis = sp.createPromptAnalysisTemplate()
	sp.ContentExtraction = sp.createContentExtractionTemplate()
}

// GetTemplate returns the appropriate template based on content type and output format
func (sp *SystemPrompts) GetTemplate(contentType, outputFormat string, isBulk bool) prompt.ChatTemplate {
	switch contentType {
	case "product", "ecommerce":
		return sp.getProductTemplate(outputFormat, isBulk)
	case "article", "blog":
		return sp.getArticleTemplate(outputFormat)
	case "contact":
		return sp.ContactJSON
	case "job", "jobs":
		return sp.getJobTemplate(outputFormat, isBulk)
	default:
		return sp.getDefaultTemplate(outputFormat)
	}
}

// Helper methods for template selection
func (sp *SystemPrompts) getProductTemplate(format string, isBulk bool) prompt.ChatTemplate {
	if isBulk {
		if format == "csv" {
			return sp.ProductBulkCSV
		}
		return sp.ProductBulkJSON
	}
	if format == "csv" {
		return sp.ProductCSV
	}
	return sp.ProductJSON
}

func (sp *SystemPrompts) getArticleTemplate(format string) prompt.ChatTemplate {
	if format == "csv" {
		return sp.ArticleCSV
	}
	return sp.ArticleJSON
}

func (sp *SystemPrompts) getJobTemplate(format string, isBulk bool) prompt.ChatTemplate {
	if isBulk && format == "csv" {
		return sp.JobBulkCSV
	}
	if format == "csv" {
		return sp.JobCSV
	}
	return sp.JobJSON
}

func (sp *SystemPrompts) getDefaultTemplate(format string) prompt.ChatTemplate {
	// Default to markdown-optimized template
	return sp.DefaultMarkdown
}

// GetAvailableTemplates returns a map of all available templates with descriptions
func (sp *SystemPrompts) GetAvailableTemplates() map[string]string {
	return map[string]string{
		"product_json":      "Extract single product information as JSON",
		"product_csv":       "Extract single product information as CSV row",
		"product_bulk_json": "Extract multiple products as JSON array",
		"product_bulk_csv":  "Extract multiple products as CSV with headers",
		"article_json":      "Extract article metadata as JSON",
		"article_csv":       "Extract article metadata as CSV row",
		"contact_json":      "Extract contact information as JSON",
		"job_json":          "Extract job listing as JSON",
		"job_csv":           "Extract job listing as CSV row",
		"job_bulk_csv":      "Extract multiple job listings as CSV",
		"default_html":      "General HTML parsing template",
		"default_markdown":  "General Markdown parsing template (recommended)",
	}
}
