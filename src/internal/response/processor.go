package response

import (
	"encoding/json"
	"fmt"
	htmlutil "html"
	"regexp"
	"sort"
	"strings"
	"time"

	"golang.org/x/net/html"
)

// Configuration for response processing
type Config struct {
	MaxResults        int     // Maximum number of results to return
	MaxDescriptionLen int     // Maximum description length per result
	MaxTitleLen       int     // Maximum title length per result
	MinQualityScore   float64 // Minimum quality score for results
	TruncateIndicator string  // String to append when truncating text
}

// DefaultConfig provides sensible defaults
var DefaultConfig = Config{
	MaxResults:        10,
	MaxDescriptionLen: 200,
	MaxTitleLen:       80,
	MinQualityScore:   0.0,
	TruncateIndicator: "...",
}

// SearchResult represents a single search result
type SearchResult struct {
	Title       string  `json:"title"`
	URL         string  `json:"url"`
	Description string  `json:"description"`
	Engine      string  `json:"engine,omitempty"`
	Score       float64 `json:"score,omitempty"`
	Category    string  `json:"category,omitempty"`
	Timestamp   string  `json:"timestamp,omitempty"`
}

// ProcessedResponse contains the processed search results with metadata
type ProcessedResponse struct {
	Query          string         `json:"query"`
	Results        []SearchResult `json:"results"`
	ResultCount    int            `json:"result_count"`
	ProcessingTime time.Duration  `json:"processing_time"`
	Instance       string         `json:"instance,omitempty"`
	TotalFound     int            `json:"total_found,omitempty"`
	ProcessedAt    time.Time      `json:"processed_at"`
}

// Processor handles response processing and formatting
type Processor struct {
	config Config
}

// NewProcessor creates a new response processor with the given config
func NewProcessor(config Config) *Processor {
	return &Processor{config: config}
}

// NewDefaultProcessor creates a processor with default configuration
func NewDefaultProcessor() *Processor {
	return NewProcessor(DefaultConfig)
}

// ProcessHTMLResponse parses HTML response from SearXNG and extracts search results
func (p *Processor) ProcessHTMLResponse(htmlContent []byte, query, instance string) (*ProcessedResponse, error) {
	startTime := time.Now()

	doc, err := html.Parse(strings.NewReader(string(htmlContent)))
	if err != nil {
		return nil, fmt.Errorf("failed to parse HTML: %w", err)
	}

	results := p.extractResultsFromHTML(doc)
	processedResults := p.processResults(results)

	response := &ProcessedResponse{
		Query:          query,
		Results:        processedResults,
		ResultCount:    len(processedResults),
		ProcessingTime: time.Since(startTime),
		Instance:       instance,
		TotalFound:     len(results),
		ProcessedAt:    time.Now(),
	}

	return response, nil
}

// ProcessJSONResponse parses JSON response from SearXNG and enhances it
func (p *Processor) ProcessJSONResponse(jsonContent []byte, query, instance string) (*ProcessedResponse, error) {
	startTime := time.Now()

	var rawResponse struct {
		Results   []map[string]interface{} `json:"results"`
		Query     string                   `json:"query"`
		QueryTime float64                  `json:"query_time"`
	}

	if err := json.Unmarshal(jsonContent, &rawResponse); err != nil {
		return nil, fmt.Errorf("failed to parse JSON response: %w", err)
	}

	results := p.convertFromRawJSON(rawResponse.Results)
	processedResults := p.processResults(results)

	response := &ProcessedResponse{
		Query:          query,
		Results:        processedResults,
		ResultCount:    len(processedResults),
		ProcessingTime: time.Since(startTime),
		Instance:       instance,
		TotalFound:     len(results),
		ProcessedAt:    time.Now(),
	}

	return response, nil
}

// FormatAsPlaintext converts processed results to clean plaintext
func (p *Processor) FormatAsPlaintext(response *ProcessedResponse) string {
	var builder strings.Builder

	// Header with metadata
	builder.WriteString(fmt.Sprintf("Search Results for: %s\n", response.Query))
	builder.WriteString(fmt.Sprintf("Found: %d results", response.ResultCount))
	if response.Instance != "" {
		builder.WriteString(fmt.Sprintf(" (via %s)", response.Instance))
	}
	builder.WriteString("\n")
	builder.WriteString(strings.Repeat("=", 50) + "\n\n")

	// Results
	for i, result := range response.Results {
		builder.WriteString(fmt.Sprintf("%d. %s\n", i+1, result.Title))
		builder.WriteString(fmt.Sprintf("   %s\n", result.URL))

		if result.Description != "" {
			// Format description with proper line breaks
			description := p.cleanText(result.Description)
			if len(description) > 0 {
				builder.WriteString(fmt.Sprintf("   %s\n", description))
			}
		}

		builder.WriteString("\n")
	}

	return builder.String()
}

// FormatAsJSON converts processed results to enhanced JSON
func (p *Processor) FormatAsJSON(response *ProcessedResponse) ([]byte, error) {
	return json.MarshalIndent(response, "", "  ")
}

// extractResultsFromHTML parses HTML document and extracts search results
func (p *Processor) extractResultsFromHTML(n *html.Node) []SearchResult {
	var results []SearchResult

	// SearXNG uses different HTML structures, we need to handle common patterns
	p.traverseHTML(n, &results)

	// Fallback: if no results found, try a more aggressive approach
	if len(results) == 0 {
		p.fallbackExtraction(n, &results)
	}

	return results
}

// traverseHTML recursively traverses HTML nodes to find search results
func (p *Processor) traverseHTML(n *html.Node, results *[]SearchResult) {
	if n.Type == html.ElementNode {
		// Look for result containers - SearXNG typically uses these classes/IDs
		if p.isResultContainer(n) {
			if result := p.extractSingleResult(n); result != nil {
				*results = append(*results, *result)
			}
		}
	}

	// Recursively traverse child nodes
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		p.traverseHTML(c, results)
	}
}

// isResultContainer checks if a node represents a search result container
func (p *Processor) isResultContainer(n *html.Node) bool {
	if n.Type != html.ElementNode {
		return false
	}

	// Check for common SearXNG result container patterns
	classAttr := p.getAttr(n, "class")
	idAttr := p.getAttr(n, "id")

	// Common SearXNG patterns (expanded for better compatibility)
	resultPatterns := []string{
		"result",
		"search-result", 
		"result-item",
		"web-result",
		"search_result",
		"results",
		"result_",
		"article",
		"entry",
		"item",
	}

	for _, pattern := range resultPatterns {
		if strings.Contains(classAttr, pattern) || strings.Contains(idAttr, pattern) {
			return true
		}
	}

	return false
}

// extractSingleResult extracts data from a single result container
func (p *Processor) extractSingleResult(n *html.Node) *SearchResult {
	result := &SearchResult{}

	// Extract title (usually in h3, h2, or a tag with specific class)
	if title := p.findResultTitle(n); title != "" {
		result.Title = p.cleanText(title)
	}

	// Extract URL (usually in a tag href)
	if url := p.findResultURL(n); url != "" {
		result.URL = url
	}

	// Extract description (usually in p, span, or div with description class)
	if desc := p.findResultDescription(n); desc != "" {
		result.Description = p.cleanText(desc)
	}

	// Extract engine info if available
	if engine := p.findResultEngine(n); engine != "" {
		result.Engine = engine
	}

	// Only return result if we have at least title and URL
	if result.Title != "" && result.URL != "" {
		result.Score = p.calculateResultScore(result)
		return result
	}

	return nil
}

// findResultTitle looks for title text in result container
func (p *Processor) findResultTitle(n *html.Node) string {
	// Look for common title patterns (expanded for better compatibility)
	titleSelectors := []string{
		"h3", "h2", "h4", "h1", "h5",
		".title", ".result-title", ".search-title",
		".heading", ".link-title", ".entry-title",
		"a[class*='title']", "a[class*='result']",
	}

	for _, selector := range titleSelectors {
		if title := p.findTextBySelector(n, selector); title != "" {
			return title
		}
	}

	// Fallback: look for any link text
	if link := p.findFirstLink(n); link != "" {
		return link
	}

	return ""
}

// findResultURL looks for URL in result container
func (p *Processor) findResultURL(n *html.Node) string {
	// Look for href attributes in a tags
	if href := p.findFirstHref(n); href != "" {
		return href
	}

	return ""
}

// findResultDescription looks for description text in result container
func (p *Processor) findResultDescription(n *html.Node) string {
	// Look for common description patterns (expanded)
	descSelectors := []string{
		".description", ".snippet", ".content", ".summary", 
		".excerpt", ".abstract", ".desc", ".result-content",
		"p", ".text", ".result-description", ".entry-summary",
	}

	for _, selector := range descSelectors {
		if desc := p.findTextBySelector(n, selector); desc != "" {
			return desc
		}
	}

	return ""
}

// findResultEngine looks for engine information
func (p *Processor) findResultEngine(n *html.Node) string {
	// Look for engine info (SearXNG sometimes includes this)
	engineSelectors := []string{".engine", ".source", "[data-engine]"}

	for _, selector := range engineSelectors {
		if engine := p.findTextBySelector(n, selector); engine != "" {
			return engine
		}
	}

	return ""
}

// Helper functions for HTML traversal and text extraction

// getAttr gets attribute value from HTML node
func (p *Processor) getAttr(n *html.Node, attrName string) string {
	for _, attr := range n.Attr {
		if attr.Key == attrName {
			return attr.Val
		}
	}
	return ""
}

// findTextBySelector finds text content matching a simple selector
func (p *Processor) findTextBySelector(n *html.Node, selector string) string {
	var result string
	var traverse func(*html.Node)

	traverse = func(node *html.Node) {
		if result != "" {
			return
		}

		if node.Type == html.ElementNode {
			if p.matchesSelector(node, selector) {
				result = p.extractText(node)
				return
			}
		}

		for c := node.FirstChild; c != nil; c = c.NextSibling {
			traverse(c)
		}
	}

	traverse(n)
	return result
}

// matchesSelector checks if node matches a simple selector
func (p *Processor) matchesSelector(n *html.Node, selector string) bool {
	if n.Type != html.ElementNode {
		return false
	}

	// Handle tag selectors
	if !strings.HasPrefix(selector, ".") && !strings.HasPrefix(selector, "#") {
		return n.Data == selector
	}

	// Handle class selectors
	if strings.HasPrefix(selector, ".") {
		className := selector[1:]
		classAttr := p.getAttr(n, "class")
		return strings.Contains(classAttr, className)
	}

	// Handle ID selectors
	if strings.HasPrefix(selector, "#") {
		idName := selector[1:]
		idAttr := p.getAttr(n, "id")
		return idAttr == idName
	}

	return false
}

// extractText extracts all text content from a node
func (p *Processor) extractText(n *html.Node) string {
	var text strings.Builder

	var traverse func(*html.Node)
	traverse = func(node *html.Node) {
		if node.Type == html.TextNode {
			text.WriteString(node.Data)
		}
		for c := node.FirstChild; c != nil; c = c.NextSibling {
			traverse(c)
		}
	}

	traverse(n)
	return strings.TrimSpace(text.String())
}

// findFirstLink finds the first link text in a node
func (p *Processor) findFirstLink(n *html.Node) string {
	var result string
	var traverse func(*html.Node)

	traverse = func(node *html.Node) {
		if result != "" {
			return
		}

		if node.Type == html.ElementNode && node.Data == "a" {
			result = p.extractText(node)
			return
		}

		for c := node.FirstChild; c != nil; c = c.NextSibling {
			traverse(c)
		}
	}

	traverse(n)
	return result
}

// findFirstHref finds the first href attribute in a node
func (p *Processor) findFirstHref(n *html.Node) string {
	var result string
	var traverse func(*html.Node)

	traverse = func(node *html.Node) {
		if result != "" {
			return
		}

		if node.Type == html.ElementNode && node.Data == "a" {
			if href := p.getAttr(node, "href"); href != "" {
				result = href
				return
			}
		}

		for c := node.FirstChild; c != nil; c = c.NextSibling {
			traverse(c)
		}
	}

	traverse(n)
	return result
}

// convertFromRawJSON converts raw JSON results to SearchResult struct
func (p *Processor) convertFromRawJSON(rawResults []map[string]interface{}) []SearchResult {
	var results []SearchResult

	for _, raw := range rawResults {
		result := SearchResult{}

		if title, ok := raw["title"].(string); ok {
			result.Title = p.cleanText(title)
		}

		if url, ok := raw["url"].(string); ok {
			result.URL = url
		}

		if content, ok := raw["content"].(string); ok {
			result.Description = p.cleanText(content)
		}

		if engine, ok := raw["engine"].(string); ok {
			result.Engine = engine
		}

		if category, ok := raw["category"].(string); ok {
			result.Category = category
		}

		// Only include results with required fields
		if result.Title != "" && result.URL != "" {
			result.Score = p.calculateResultScore(&result)
			results = append(results, result)
		}
	}

	return results
}

// processResults filters, scores, and limits search results
func (p *Processor) processResults(results []SearchResult) []SearchResult {
	// Filter by quality score
	var filtered []SearchResult
	for _, result := range results {
		if result.Score >= p.config.MinQualityScore {
			// Truncate fields to configured limits
			result.Title = p.truncateText(result.Title, p.config.MaxTitleLen)
			result.Description = p.truncateText(result.Description, p.config.MaxDescriptionLen)
			filtered = append(filtered, result)
		}
	}

	// Sort by score (highest first)
	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].Score > filtered[j].Score
	})

	// Limit to max results
	if len(filtered) > p.config.MaxResults {
		filtered = filtered[:p.config.MaxResults]
	}

	return filtered
}

// calculateResultScore assigns a quality score to a result
func (p *Processor) calculateResultScore(result *SearchResult) float64 {
	score := 1.0

	// Title quality
	if len(result.Title) > 10 && len(result.Title) < 100 {
		score += 0.3
	}

	// Description quality
	if len(result.Description) > 20 && len(result.Description) < 300 {
		score += 0.2
	}

	// URL quality (prefer https, shorter URLs)
	if strings.HasPrefix(result.URL, "https://") {
		score += 0.1
	}
	if len(result.URL) < 100 {
		score += 0.1
	}

	// Penalize missing description
	if result.Description == "" {
		score -= 0.3
	}

	// Penalize very short titles
	if len(result.Title) < 5 {
		score -= 0.5
	}

	return score
}

// cleanText removes HTML tags and normalizes whitespace
func (p *Processor) cleanText(text string) string {
	// Decode HTML entities
	text = htmlutil.UnescapeString(text)

	// Remove HTML tags
	htmlTagRegex := regexp.MustCompile(`<[^>]*>`)
	text = htmlTagRegex.ReplaceAllString(text, "")

	// Normalize whitespace
	whitespaceRegex := regexp.MustCompile(`\s+`)
	text = whitespaceRegex.ReplaceAllString(text, " ")

	// Remove common unwanted patterns
	text = strings.ReplaceAll(text, "...", " ")
	text = strings.ReplaceAll(text, "  ", " ")

	return strings.TrimSpace(text)
}

// truncateText truncates text to maxLen with indicator
func (p *Processor) truncateText(text string, maxLen int) string {
	if len(text) <= maxLen {
		return text
	}

	if maxLen <= len(p.config.TruncateIndicator) {
		return text[:maxLen]
	}

	truncated := text[:maxLen-len(p.config.TruncateIndicator)]

	// Try to break at word boundary
	if spaceIdx := strings.LastIndex(truncated, " "); spaceIdx > maxLen/2 {
		truncated = truncated[:spaceIdx]
	}

	return truncated + p.config.TruncateIndicator
}

// fallbackExtraction tries to extract results when normal parsing fails
func (p *Processor) fallbackExtraction(n *html.Node, results *[]SearchResult) {
	// Look for any links that might be search results
	var links []struct {
		href string
		text string
	}
	
	p.collectLinks(n, &links)
	
	// Filter and convert links to results
	for _, link := range links {
		if link.href == "" || link.text == "" {
			continue
		}
		
		// Skip obviously non-result links
		if p.isInternalLink(link.href) {
			continue
		}
		
		result := SearchResult{
			Title: p.cleanText(link.text),
			URL:   link.href,
			Score: 0.5, // Lower score for fallback results
		}
		
		if len(result.Title) > 5 && len(result.URL) > 10 {
			*results = append(*results, result)
		}
	}
}

// collectLinks recursively collects all links from HTML
func (p *Processor) collectLinks(n *html.Node, links *[]struct{ href, text string }) {
	if n.Type == html.ElementNode && n.Data == "a" {
		href := p.getAttr(n, "href")
		text := p.extractText(n)
		if href != "" && text != "" {
			*links = append(*links, struct{ href, text string }{href, text})
		}
	}
	
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		p.collectLinks(c, links)
	}
}

// isInternalLink checks if a link is internal to the search engine
func (p *Processor) isInternalLink(href string) bool {
	// Skip relative URLs and common internal patterns
	if strings.HasPrefix(href, "/") || 
	   strings.Contains(href, "search?") ||
	   strings.Contains(href, "preferences") ||
	   strings.Contains(href, "about") ||
	   strings.Contains(href, "stats") ||
	   strings.Contains(href, "#") {
		return true
	}
	return false
}
