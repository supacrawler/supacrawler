package markdown

import (
	"bytes"
	"regexp"
	"strings"

	md "github.com/JohannesKaufmann/html-to-markdown"
	"github.com/PuerkitoBio/goquery"
)

// ConvertHTMLToMarkdown converts HTML to markdown and does a light cleanup.
func ConvertHTMLToMarkdown(html string) string {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return ""
	}

	// Try to find main content, if it exists, work with that
	var contentSelection *goquery.Selection
	mainTags := []string{"main", "[role=\"main\"]", "#content", "#main"}
	for _, tag := range mainTags {
		if doc.Find(tag).Length() > 0 {
			contentSelection = doc.Find(tag).First()
			break
		}
	}

	// If a main content area is found, use it. Otherwise, use the whole body.
	if contentSelection == nil {
		contentSelection = doc.Find("body")
	}

	// Remove common boilerplate elements
	contentSelection.Find("script, style, noscript, nav, header, aside, form, iframe, svg, button, input").Each(func(_ int, s *goquery.Selection) { s.Remove() })
	contentSelection.Find("[role=\"navigation\"], [role=\"banner\"], [role=\"contentinfo\"], [aria-label*=\"cookie\" i], [aria-modal]").Each(func(_ int, s *goquery.Selection) { s.Remove() })

	// Remove elements whose class or id contain boilerplate keywords
	keywords := []string{
		"cookie", "consent", "banner", "navbar", "nav-", "menu-", "header",
		"pagination", "share", "search-", "signup", "signin", "login",
		"ad-", "advert", "promo", "modal", "popup", "dialog",
		"breadcrumbs", "breadcrumb", "sidebar",
	}

	contentSelection.Find("[class], [id]").Each(func(_ int, sel *goquery.Selection) {
		classVal, _ := sel.Attr("class")
		idVal, _ := sel.Attr("id")
		lower := strings.ToLower(classVal + " " + idVal)
		for _, kw := range keywords {
			if strings.Contains(lower, kw) {
				sel.Remove()
				break
			}
		}
	})

	body, err := contentSelection.Html()
	if err != nil {
		return ""
	}

	conv := md.NewConverter("", true, nil)
	out, err := conv.ConvertString(body)
	if err != nil {
		return ""
	}

	// Post-process: de-duplicate and remove markdown boilerplate
	out = RemoveDuplicates(out)
	out = CleanMarkdownBoilerplate(out)

	// Collapse excessive whitespace
	out = regexp.MustCompile(`\n{3,}`).ReplaceAllString(out, "\n\n")
	return strings.TrimSpace(out)
}

// RemoveDuplicates processes the Markdown content to remove duplicate links and dates
func RemoveDuplicates(markdown string) string {
	var cleanedContent bytes.Buffer
	lines := strings.Split(markdown, "\n")

	seenLinks := make(map[string]bool)
	seenDates := make(map[string]bool)
	for _, line := range lines {
		trimmedLine := strings.TrimSpace(line)

		// Normalize for duplicate detection
		normalizedLine := normalizedContent(trimmedLine)

		// Remove duplicate links
		if isLinkLine(trimmedLine) {
			if seenLinks[normalizedLine] {
				continue
			}
			seenLinks[normalizedLine] = true
		}

		// Remove duplicate dates
		if isDateLine(trimmedLine) {
			if seenDates[normalizedLine] {
				continue
			}
			seenDates[normalizedLine] = true
		}

		cleanedContent.WriteString(trimmedLine + "\n")
	}

	return cleanedContent.String()
}

func normalizedContent(line string) string {
	line = normalizeLinks(line)
	line = normalizeDates(line)
	return line
}

func normalizeDates(line string) string {
	datePattern := `\b\d{4}/\d{2}/\d{2}\b|\b\d{2}/\d{2}/\d{4}\b|\b[A-Za-z]{3} \d{1,2}, \d{4}\b`
	re := regexp.MustCompile(datePattern)
	return re.ReplaceAllString(line, "DATE")
}

func normalizeLinks(line string) string {
	linkPattern := `https?://[^\s)]+`
	re := regexp.MustCompile(linkPattern)
	return re.ReplaceAllString(line, "LINK")
}

func isLinkLine(line string) bool {
	// Matches lines starting with ![description](URL) optionally followed by ](path)
	linkPattern := `^!\[[^\]]*\]\((https?:\/\/[^\)]+)\)(\]\([^\)]+\))?$`
	re := regexp.MustCompile(linkPattern)
	return re.MatchString(line)
}

func isDateLine(line string) bool {
	// Matches lines like "Sep 12, 2024" with optional trailing backslash
	datePattern := `^[A-Za-z]{3}\s\d{1,2},\s\d{4}\\?$`
	re := regexp.MustCompile(datePattern)
	return re.MatchString(line)
}

// fixInvalidEscapes handles invalid escape sequences in strings
func fixInvalidEscapes(text string) string {
	// Replace invalid escape sequences
	// Look for backslashes followed by characters that aren't valid escape sequences
	invalidEscapePattern := `\\([^\\nrt"'bfvx0-7])`
	re := regexp.MustCompile(invalidEscapePattern)

	// First fix invalid escapes
	text = re.ReplaceAllString(text, "$1")

	// Fix double backslashes that might cause issues
	text = strings.ReplaceAll(text, "\\\\", "\\")

	// Then fix control characters that need to be escaped
	return fixControlCharacters(text)
}

// fixControlCharacters replaces invalid control characters in JSON strings
func fixControlCharacters(text string) string {
	// Replace control characters (0x00-0x1F except allowed ones)
	// This regex matches any control character that isn't a valid escape
	controlCharsPattern := `[\x00-\x08\x0B\x0C\x0E-\x1F]`
	re := regexp.MustCompile(controlCharsPattern)

	// First remove control characters
	text = re.ReplaceAllString(text, "")

	// Then remove invisible Unicode characters that break JSON
	invisibleChars := []string{
		"\u200B", // zero-width space
		"\u200C", // zero-width non-joiner
		"\u200D", // zero-width joiner
		"\u200E", // left-to-right mark
		"\u200F", // right-to-left mark
		"\u2028", // line separator
		"\u2029", // paragraph separator
		"\uFEFF", // byte order mark
		"\uFFFD", // replacement character (indicates malformed UTF-8)
	}
	for _, char := range invisibleChars {
		text = strings.ReplaceAll(text, char, "")
	}

	// Remove null bytes that might slip through
	text = strings.ReplaceAll(text, "\x00", "")

	// Remove other problematic characters
	text = strings.ReplaceAll(text, "\u0000", "") // null character (alternative)
	text = strings.ReplaceAll(text, "\uFFFF", "") // non-character

	return text
}

// CleanMarkdownBoilerplate removes markdown-level noise after conversion
func CleanMarkdownBoilerplate(mdText string) string {
	lines := strings.Split(mdText, "\n")
	out := make([]string, 0, len(lines))

	imgRe := regexp.MustCompile(`!\[[^\]]*\]\([^\)]+\)`) // markdown images

	for _, l := range lines {
		line := strings.TrimSpace(l)
		if line == "" {
			continue
		}

		// Drop pure image lines
		if imgRe.MatchString(line) && len(strings.TrimSpace(imgRe.ReplaceAllString(line, ""))) == 0 {
			continue
		}

		// Fix invalid escape sequences in strings
		line = fixInvalidEscapes(line)

		out = append(out, line)
	}

	cleaned := strings.Join(out, "\n")
	cleaned = regexp.MustCompile(`\n{3,}`).ReplaceAllString(cleaned, "\n\n")
	return strings.TrimSpace(cleaned)
}
