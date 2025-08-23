package response

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
	
	"golang.org/x/net/html"
)

func TestNewProcessor(t *testing.T) {
	config := Config{
		MaxResults:        5,
		MaxDescriptionLen: 150,
		MaxTitleLen:       60,
		MinQualityScore:   0.5,
		TruncateIndicator: " [...]",
	}

	processor := NewProcessor(config)

	if processor == nil {
		t.Fatal("Expected processor to be created")
	}

	if processor.config.MaxResults != 5 {
		t.Errorf("Expected MaxResults 5, got %d", processor.config.MaxResults)
	}

	if processor.config.MaxDescriptionLen != 150 {
		t.Errorf("Expected MaxDescriptionLen 150, got %d", processor.config.MaxDescriptionLen)
	}

	if processor.config.MinQualityScore != 0.5 {
		t.Errorf("Expected MinQualityScore 0.5, got %.1f", processor.config.MinQualityScore)
	}
}

func TestNewDefaultProcessor(t *testing.T) {
	processor := NewDefaultProcessor()

	if processor == nil {
		t.Fatal("Expected default processor to be created")
	}

	if processor.config.MaxResults != DefaultConfig.MaxResults {
		t.Errorf("Expected default MaxResults %d, got %d", DefaultConfig.MaxResults, processor.config.MaxResults)
	}
}

func TestProcessHTMLResponse(t *testing.T) {
	processor := NewDefaultProcessor()

	htmlContent := `
	<!DOCTYPE html>
	<html>
	<head><title>Search Results</title></head>
	<body>
		<div id="results">
			<article class="result">
				<h3><a href="https://example1.com">First Test Result</a></h3>
				<p class="description">This is the description for the first result with some detailed information.</p>
				<div class="engine">Google</div>
			</article>
			<article class="result">
				<h3><a href="https://example2.com">Second Test Result</a></h3>
				<p class="description">Description for the second result.</p>
				<div class="engine">Bing</div>
			</article>
			<div class="result-item">
				<h2><a href="https://example3.com">Third Result</a></h2>
				<span class="content">Third result description</span>
			</div>
		</div>
	</body>
	</html>
	`

	query := "test query"
	instance := "https://searxng.example.com"

	response, err := processor.ProcessHTMLResponse([]byte(htmlContent), query, instance)

	if err != nil {
		t.Fatalf("ProcessHTMLResponse failed: %v", err)
	}

	if response.Query != query {
		t.Errorf("Expected query %q, got %q", query, response.Query)
	}

	if response.Instance != instance {
		t.Errorf("Expected instance %q, got %q", instance, response.Instance)
	}

	if response.ResultCount < 2 {
		t.Errorf("Expected at least 2 results, got %d", response.ResultCount)
	}

	if len(response.Results) < 2 {
		t.Errorf("Expected at least 2 results in array, got %d", len(response.Results))
	}

	// Check first result
	result1 := response.Results[0]
	if result1.Title == "" {
		t.Error("Expected first result to have a title")
	}

	if result1.URL == "" {
		t.Error("Expected first result to have a URL")
	}

	if !strings.Contains(result1.URL, "example") {
		t.Errorf("Expected URL to contain 'example', got %s", result1.URL)
	}

	// Check that processing time was recorded
	if response.ProcessingTime <= 0 {
		t.Error("Expected positive processing time")
	}

	// Check that processed at time was set
	if response.ProcessedAt.IsZero() {
		t.Error("Expected ProcessedAt time to be set")
	}
}

func TestProcessHTMLResponseWithSearXNGFormat(t *testing.T) {
	processor := NewDefaultProcessor()

	// Realistic SearXNG HTML structure
	htmlContent := `
	<!DOCTYPE html>
	<html lang="en">
	<head><title>Search - SearXNG</title></head>
	<body>
		<div class="results">
			<article class="result">
				<h3>
					<a href="https://www.example.com/page1" title="Example Page 1">
						Example Website - Page 1
					</a>
				</h3>
				<p class="content">
					This is a sample description from the first search result.
					It contains relevant information about the topic.
				</p>
				<a href="https://www.example.com/page1" class="url_wrapper">
					<span class="url">https://www.example.com/page1</span>
				</a>
			</article>
			
			<article class="result">
				<h3>
					<a href="https://test.org/article">Test Article</a>
				</h3>
				<p class="content">
					Another search result with different content and structure.
				</p>
				<span class="engine">DuckDuckGo</span>
			</article>
			
			<div class="result search-result">
				<h4><a href="https://demo.site/info">Demo Information</a></h4>
				<div class="snippet">Short snippet of information</div>
			</div>
		</div>
	</body>
	</html>
	`

	response, err := processor.ProcessHTMLResponse([]byte(htmlContent), "test", "instance")

	if err != nil {
		t.Fatalf("Failed to process SearXNG HTML: %v", err)
	}

	if response.ResultCount < 3 {
		t.Errorf("Expected at least 3 results, got %d", response.ResultCount)
	}

	// Verify results have proper structure
	for i, result := range response.Results {
		if result.Title == "" {
			t.Errorf("Result %d missing title", i+1)
		}
		if result.URL == "" {
			t.Errorf("Result %d missing URL", i+1)
		}
		if result.Score <= 0 {
			t.Errorf("Result %d has invalid score: %.2f", i+1, result.Score)
		}
	}
}

func TestProcessJSONResponse(t *testing.T) {
	processor := NewDefaultProcessor()

	jsonContent := `{
		"query": "test search",
		"query_time": 0.123,
		"results": [
			{
				"title": "First JSON Result",
				"url": "https://json1.example.com",
				"content": "This is content from the first JSON result.",
				"engine": "Google",
				"category": "general"
			},
			{
				"title": "Second JSON Result",
				"url": "https://json2.example.com",
				"content": "Content from the second JSON result.",
				"engine": "Bing"
			},
			{
				"title": "",
				"url": "https://invalid.example.com",
				"content": "This result should be filtered out due to missing title"
			}
		]
	}`

	query := "test search"
	instance := "https://searxng.example.com"

	response, err := processor.ProcessJSONResponse([]byte(jsonContent), query, instance)

	if err != nil {
		t.Fatalf("ProcessJSONResponse failed: %v", err)
	}

	if response.Query != query {
		t.Errorf("Expected query %q, got %q", query, response.Query)
	}

	if response.ResultCount != 2 {
		t.Errorf("Expected 2 valid results, got %d", response.ResultCount)
	}

	// Check first result
	result1 := response.Results[0]
	if result1.Title != "First JSON Result" {
		t.Errorf("Expected title 'First JSON Result', got %q", result1.Title)
	}

	if result1.Engine != "Google" {
		t.Errorf("Expected engine 'Google', got %q", result1.Engine)
	}

	if result1.Category != "general" {
		t.Errorf("Expected category 'general', got %q", result1.Category)
	}
}

func TestProcessJSONResponseInvalidJSON(t *testing.T) {
	processor := NewDefaultProcessor()

	invalidJSON := `{"incomplete": true, "results": [`

	_, err := processor.ProcessJSONResponse([]byte(invalidJSON), "query", "instance")

	if err == nil {
		t.Error("Expected error for invalid JSON")
	}

	if !strings.Contains(err.Error(), "failed to parse JSON response") {
		t.Errorf("Expected JSON parsing error, got: %v", err)
	}
}

func TestFormatAsPlaintext(t *testing.T) {
	processor := NewDefaultProcessor()

	response := &ProcessedResponse{
		Query:       "test query",
		ResultCount: 2,
		Instance:    "https://searxng.example.com",
		Results: []SearchResult{
			{
				Title:       "First Result",
				URL:         "https://example1.com",
				Description: "Description for first result",
			},
			{
				Title:       "Second Result",
				URL:         "https://example2.com",
				Description: "Description for second result with more details",
			},
		},
	}

	plaintext := processor.FormatAsPlaintext(response)

	// Check header information
	if !strings.Contains(plaintext, "Search Results for: test query") {
		t.Error("Plaintext should contain query information")
	}

	if !strings.Contains(plaintext, "Found: 2 results") {
		t.Error("Plaintext should contain result count")
	}

	if !strings.Contains(plaintext, "searxng.example.com") {
		t.Error("Plaintext should contain instance information")
	}

	// Check results formatting
	if !strings.Contains(plaintext, "1. First Result") {
		t.Error("Plaintext should contain numbered first result")
	}

	if !strings.Contains(plaintext, "2. Second Result") {
		t.Error("Plaintext should contain numbered second result")
	}

	if !strings.Contains(plaintext, "https://example1.com") {
		t.Error("Plaintext should contain URLs")
	}

	if !strings.Contains(plaintext, "Description for first result") {
		t.Error("Plaintext should contain descriptions")
	}

	// Check formatting structure
	if !strings.Contains(plaintext, "=") {
		t.Error("Plaintext should contain separator line")
	}
}

func TestFormatAsJSON(t *testing.T) {
	processor := NewDefaultProcessor()

	response := &ProcessedResponse{
		Query:          "test query",
		ResultCount:    1,
		ProcessingTime: 50 * time.Millisecond,
		Instance:       "https://searxng.example.com",
		TotalFound:     1,
		ProcessedAt:    time.Now(),
		Results: []SearchResult{
			{
				Title:       "Test Result",
				URL:         "https://example.com",
				Description: "Test description",
				Engine:      "Google",
				Score:       1.5,
				Category:    "general",
			},
		},
	}

	jsonData, err := processor.FormatAsJSON(response)

	if err != nil {
		t.Fatalf("FormatAsJSON failed: %v", err)
	}

	// Parse JSON to verify structure
	var parsed map[string]interface{}
	if err := json.Unmarshal(jsonData, &parsed); err != nil {
		t.Fatalf("Generated JSON is invalid: %v", err)
	}

	// Check required fields
	if query, ok := parsed["query"].(string); !ok || query != "test query" {
		t.Error("JSON should contain correct query field")
	}

	if count, ok := parsed["result_count"].(float64); !ok || int(count) != 1 {
		t.Error("JSON should contain correct result_count field")
	}

	if results, ok := parsed["results"].([]interface{}); !ok || len(results) != 1 {
		t.Error("JSON should contain results array with correct length")
	} else {
		result := results[0].(map[string]interface{})
		if title, ok := result["title"].(string); !ok || title != "Test Result" {
			t.Error("JSON result should contain correct title")
		}
	}
}

func TestCalculateResultScore(t *testing.T) {
	processor := NewDefaultProcessor()

	tests := []struct {
		name        string
		result      SearchResult
		minScore    float64
		maxScore    float64
		description string
	}{
		{
			name: "High quality result",
			result: SearchResult{
				Title:       "Well-structured title with good length",
				URL:         "https://example.com/page",
				Description: "Good description with adequate length and meaningful content",
			},
			minScore:    1.4,
			maxScore:    2.0,
			description: "Should score high for good title, HTTPS, and good description",
		},
		{
			name: "Basic result",
			result: SearchResult{
				Title:       "Basic Title",
				URL:         "http://example.com",
				Description: "Short desc",
			},
			minScore:    1.0,
			maxScore:    1.5,
			description: "Should score moderately",
		},
		{
			name: "Low quality result",
			result: SearchResult{
				Title: "A",
				URL:   "http://very-long-url.example.com/with/many/path/segments/that/make/it/very/long/and/unwieldy",
			},
			minScore:    0.0,
			maxScore:    1.0,
			description: "Should score low for short title, no description, HTTP, and long URL",
		},
		{
			name: "Perfect result",
			result: SearchResult{
				Title:       "Perfect Length Title Here",
				URL:         "https://short.com",
				Description: "Perfect description with good length and quality content that provides value",
			},
			minScore:    1.6,
			maxScore:    2.0,
			description: "Should score very high for all good qualities",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			score := processor.calculateResultScore(&tt.result)

			if score < tt.minScore || score > tt.maxScore {
				t.Errorf("Score %.2f outside expected range [%.2f, %.2f] for %s", 
					score, tt.minScore, tt.maxScore, tt.description)
			}

			t.Logf("Result '%s' scored %.2f", tt.result.Title, score)
		})
	}
}

func TestProcessResults(t *testing.T) {
	config := Config{
		MaxResults:        3,
		MaxDescriptionLen: 20,
		MaxTitleLen:       15,
		MinQualityScore:   1.0,
		TruncateIndicator: "...",
	}
	processor := NewProcessor(config)

	results := []SearchResult{
		{Title: "High Quality Result", URL: "https://example1.com", Description: "This is a long description that should be truncated", Score: 1.8},
		{Title: "Very Long Title That Should Be Truncated", URL: "https://example2.com", Description: "Good description", Score: 1.5},
		{Title: "Low Quality", URL: "http://example3.com", Description: "Short", Score: 0.5}, // Should be filtered
		{Title: "Medium Quality", URL: "https://example4.com", Description: "Medium description", Score: 1.2},
		{Title: "Another Good", URL: "https://example5.com", Description: "Another description", Score: 1.6},
	}

	processed := processor.processResults(results)

	// Should filter out low quality result
	if len(processed) > 3 {
		t.Errorf("Expected max 3 results, got %d", len(processed))
	}

	// Should be sorted by score (highest first)
	if len(processed) >= 2 && processed[0].Score < processed[1].Score {
		t.Error("Results should be sorted by score (highest first)")
	}

	// Check truncation
	for i, result := range processed {
		if len(result.Title) > config.MaxTitleLen {
			t.Errorf("Result %d title not truncated: %d chars > %d", i, len(result.Title), config.MaxTitleLen)
		}
		if len(result.Description) > config.MaxDescriptionLen {
			t.Errorf("Result %d description not truncated: %d chars > %d", i, len(result.Description), config.MaxDescriptionLen)
		}
	}
}

func TestCleanText(t *testing.T) {
	processor := NewDefaultProcessor()

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "HTML tags removal",
			input:    "<p>Hello <strong>World</strong></p>",
			expected: "Hello World",
		},
		{
			name:     "HTML entities decoding",
			input:    "Tom &amp; Jerry &lt;test&gt;",
			expected: "Tom & Jerry <test>",
		},
		{
			name:     "Whitespace normalization",
			input:    "Multiple   \t\n  spaces",
			expected: "Multiple spaces",
		},
		{
			name:     "Common patterns removal",
			input:    "Text with ... and  double spaces",
			expected: "Text with and double spaces",
		},
		{
			name:     "Complex HTML",
			input:    "<div class='test'>Text<br/><span>More text</span><!-- comment --></div>",
			expected: "TextMore text",
		},
		{
			name:     "Empty and whitespace-only",
			input:    "   \t\n   ",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := processor.cleanText(tt.input)
			if result != tt.expected {
				t.Errorf("Expected %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestTruncateText(t *testing.T) {
	config := Config{
		MaxResults:        10,
		MaxDescriptionLen: 200,
		MaxTitleLen:       80,
		MinQualityScore:   0.0,
		TruncateIndicator: "...",
	}
	processor := NewProcessor(config)

	tests := []struct {
		name     string
		input    string
		maxLen   int
		expected string
	}{
		{
			name:     "No truncation needed",
			input:    "Short text",
			maxLen:   20,
			expected: "Short text",
		},
		{
			name:     "Simple truncation",
			input:    "This is a very long text that needs truncation",
			maxLen:   20,
			expected: "This is a very...",
		},
		{
			name:     "Word boundary truncation",
			input:    "This is a long sentence with many words",
			maxLen:   25,
			expected: "This is a long sentence...",
		},
		{
			name:     "Very short max length",
			input:    "Hello World",
			maxLen:   5,
			expected: "He...",
		},
		{
			name:     "Max length equal to indicator",
			input:    "Hello",
			maxLen:   3,
			expected: "Hel",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := processor.truncateText(tt.input, tt.maxLen)
			if result != tt.expected {
				t.Errorf("Expected %q, got %q", tt.expected, result)
			}
			if len(result) > tt.maxLen {
				t.Errorf("Result length %d exceeds max %d", len(result), tt.maxLen)
			}
		})
	}
}

func TestFallbackExtraction(t *testing.T) {
	processor := NewDefaultProcessor()

	// HTML with links but no standard result structure
	htmlContent := `
	<!DOCTYPE html>
	<html>
	<body>
		<div>
			<a href="https://example1.com">Example 1 Link</a>
			<a href="/internal-link">Internal Link</a>
			<a href="https://example2.com">Another External Link</a>
			<a href="search?q=test">Search Link</a>
			<a href="https://valid-result.com">Valid External Result</a>
		</div>
	</body>
	</html>
	`

	response, err := processor.ProcessHTMLResponse([]byte(htmlContent), "test", "instance")

	if err != nil {
		t.Fatalf("ProcessHTMLResponse failed: %v", err)
	}

	// Should have extracted some results from fallback
	if response.ResultCount == 0 {
		t.Error("Expected fallback extraction to find some results")
	}

	// Results should have lower scores (fallback results get 0.5)
	for i, result := range response.Results {
		if result.Score > 1.0 {
			t.Errorf("Fallback result %d has unexpectedly high score: %.2f", i, result.Score)
		}
		if result.Title == "" || result.URL == "" {
			t.Errorf("Fallback result %d missing required fields", i)
		}
	}
}

func TestIsResultContainer(t *testing.T) {
	processor := NewDefaultProcessor()

	testHTML := `
	<div class="result">Should match</div>
	<article class="search-result">Should match</article>
	<div class="result-item">Should match</div>
	<div class="web-result">Should match</div>
	<div class="random-class">Should not match</div>
	<div id="result_1">Should match</div>
	<article>Should match</article>
	<span class="result">Should match</span>
	`

	// Parse HTML for testing
	doc, err := parseHTML(testHTML)
	if err != nil {
		t.Fatalf("Failed to parse test HTML: %v", err)
	}

	matches := 0
	var traverse func(*html.Node)
	traverse = func(n *html.Node) {
		if processor.isResultContainer(n) {
			matches++
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			traverse(c)
		}
	}

	traverse(doc)

	expectedMatches := 7 // Based on the test HTML above
	if matches != expectedMatches {
		t.Errorf("Expected %d result containers, found %d", expectedMatches, matches)
	}
}

func TestMatchesSelector(t *testing.T) {
	processor := NewDefaultProcessor()

	// Create test nodes
	testHTML := `
	<div class="test-class">Test Div</div>
	<p>Test Paragraph</p>
	<span id="test-id">Test Span</span>
	<h3 class="title result">Test Heading</h3>
	`

	doc, err := parseHTML(testHTML)
	if err != nil {
		t.Fatalf("Failed to parse test HTML: %v", err)
	}

	tests := []struct {
		selector    string
		shouldMatch int
		description string
	}{
		{"div", 1, "Should match div tags"},
		{"p", 1, "Should match p tags"},
		{".test-class", 1, "Should match class selector"},
		{"#test-id", 1, "Should match ID selector"},
		{".title", 1, "Should match class in multi-class element"},
		{".nonexistent", 0, "Should not match non-existent class"},
		{"h1", 0, "Should not match non-existent tag"},
	}

	for _, tt := range tests {
		t.Run(tt.description, func(t *testing.T) {
			matches := 0
			var traverse func(*html.Node)
			traverse = func(n *html.Node) {
				if processor.matchesSelector(n, tt.selector) {
					matches++
				}
				for c := n.FirstChild; c != nil; c = c.NextSibling {
					traverse(c)
				}
			}

			traverse(doc)

			if matches != tt.shouldMatch {
				t.Errorf("Selector %q: expected %d matches, got %d", tt.selector, tt.shouldMatch, matches)
			}
		})
	}
}

func TestConfigFromParams(t *testing.T) {
	tests := []struct {
		name     string
		params   map[string][]string
		expected Config
	}{
		{
			name: "Default config when no params",
			params: map[string][]string{},
			expected: DefaultConfig,
		},
		{
			name: "Custom max results",
			params: map[string][]string{
				"max_results": {"15"},
			},
			expected: Config{
				MaxResults:        15,
				MaxDescriptionLen: DefaultConfig.MaxDescriptionLen,
				MaxTitleLen:       DefaultConfig.MaxTitleLen,
				MinQualityScore:   DefaultConfig.MinQualityScore,
				TruncateIndicator: DefaultConfig.TruncateIndicator,
			},
		},
		{
			name: "Multiple parameters",
			params: map[string][]string{
				"max_results":        {"8"},
				"max_desc_len":       {"250"},
				"max_title_len":      {"90"},
				"min_score":          {"1.2"},
				"truncate_indicator": {" [more]"},
			},
			expected: Config{
				MaxResults:        8,
				MaxDescriptionLen: 250,
				MaxTitleLen:       90,
				MinQualityScore:   1.2,
				TruncateIndicator: " [more]",
			},
		},
		{
			name: "Invalid values should use defaults",
			params: map[string][]string{
				"max_results":        {"invalid"},
				"max_desc_len":       {"-1"},
				"max_title_len":      {"1000"}, // Too large
				"min_score":          {"invalid"},
				"truncate_indicator": {"this_is_way_too_long_indicator"},
			},
			expected: DefaultConfig, // Should fallback to defaults
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := ConfigFromParams(tt.params)

			if config.MaxResults != tt.expected.MaxResults {
				t.Errorf("MaxResults: expected %d, got %d", tt.expected.MaxResults, config.MaxResults)
			}

			if config.MaxDescriptionLen != tt.expected.MaxDescriptionLen {
				t.Errorf("MaxDescriptionLen: expected %d, got %d", tt.expected.MaxDescriptionLen, config.MaxDescriptionLen)
			}

			if config.MaxTitleLen != tt.expected.MaxTitleLen {
				t.Errorf("MaxTitleLen: expected %d, got %d", tt.expected.MaxTitleLen, config.MaxTitleLen)
			}

			if config.MinQualityScore != tt.expected.MinQualityScore {
				t.Errorf("MinQualityScore: expected %.1f, got %.1f", tt.expected.MinQualityScore, config.MinQualityScore)
			}

			if config.TruncateIndicator != tt.expected.TruncateIndicator {
				t.Errorf("TruncateIndicator: expected %q, got %q", tt.expected.TruncateIndicator, config.TruncateIndicator)
			}
		})
	}
}

func TestGetPresetConfig(t *testing.T) {
	tests := []struct {
		name     string
		preset   string
		expected Config
	}{
		{"Compact preset", "compact", CompactConfig},
		{"Detailed preset", "detailed", DetailedConfig},
		{"API preset", "api", APIConfig},
		{"Default preset", "default", DefaultConfig},
		{"Unknown preset", "unknown", DefaultConfig},
		{"Case insensitive", "COMPACT", CompactConfig},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := GetPresetConfig(tt.preset)

			if config.MaxResults != tt.expected.MaxResults {
				t.Errorf("MaxResults: expected %d, got %d", tt.expected.MaxResults, config.MaxResults)
			}

			if config.MaxDescriptionLen != tt.expected.MaxDescriptionLen {
				t.Errorf("MaxDescriptionLen: expected %d, got %d", tt.expected.MaxDescriptionLen, config.MaxDescriptionLen)
			}
		})
	}
}

func TestConcurrentProcessing(t *testing.T) {
	processor := NewDefaultProcessor()

	htmlContent := `
	<html><body>
		<div class="results">
			<article class="result">
				<h3><a href="https://example.com">Test Result</a></h3>
				<p class="description">Test description</p>
			</article>
		</div>
	</body></html>
	`

	// Process same content concurrently
	const numGoroutines = 50
	results := make(chan *ProcessedResponse, numGoroutines)
	errors := make(chan error, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			response, err := processor.ProcessHTMLResponse(
				[]byte(htmlContent), 
				fmt.Sprintf("query-%d", id), 
				fmt.Sprintf("instance-%d", id),
			)
			if err != nil {
				errors <- err
			} else {
				results <- response
			}
		}(i)
	}

	// Collect results
	successCount := 0
	errorCount := 0

	for i := 0; i < numGoroutines; i++ {
		select {
		case response := <-results:
			successCount++
			if response.ResultCount == 0 {
				t.Error("Expected at least one result in concurrent processing")
			}
		case err := <-errors:
			errorCount++
			t.Errorf("Concurrent processing error: %v", err)
		}
	}

	if successCount != numGoroutines {
		t.Errorf("Expected %d successful operations, got %d", numGoroutines, successCount)
	}

	if errorCount > 0 {
		t.Errorf("Got %d errors in concurrent processing", errorCount)
	}
}

// Helper function to parse HTML for testing
func parseHTML(htmlStr string) (*html.Node, error) {
	return html.Parse(strings.NewReader(htmlStr))
}

func BenchmarkProcessHTMLResponse(b *testing.B) {
	processor := NewDefaultProcessor()

	htmlContent := `
	<!DOCTYPE html>
	<html>
	<body>
		<div class="results">
			<article class="result">
				<h3><a href="https://example1.com">First Result</a></h3>
				<p class="description">Description for first result</p>
			</article>
			<article class="result">
				<h3><a href="https://example2.com">Second Result</a></h3>
				<p class="description">Description for second result</p>
			</article>
		</div>
	</body>
	</html>
	`

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := processor.ProcessHTMLResponse([]byte(htmlContent), "benchmark", "instance")
		if err != nil {
			b.Fatalf("Benchmark failed: %v", err)
		}
	}
}

func BenchmarkCleanText(b *testing.B) {
	processor := NewDefaultProcessor()
	text := "<p>This is a <strong>test</strong> with &amp; HTML entities and   extra spaces</p>"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = processor.cleanText(text)
	}
}

func BenchmarkCalculateResultScore(b *testing.B) {
	processor := NewDefaultProcessor()
	result := &SearchResult{
		Title:       "Sample Title for Benchmarking",
		URL:         "https://example.com/path/to/page",
		Description: "This is a sample description for benchmarking the scoring algorithm.",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = processor.calculateResultScore(result)
	}
}

func BenchmarkFormatAsJSON(b *testing.B) {
	processor := NewDefaultProcessor()
	response := &ProcessedResponse{
		Query:          "benchmark query",
		ResultCount:    2,
		ProcessingTime: 10 * time.Millisecond,
		Instance:       "https://instance.example.com",
		Results: []SearchResult{
			{
				Title:       "Benchmark Result 1",
				URL:         "https://example1.com",
				Description: "Description for benchmark result 1",
				Score:       1.5,
			},
			{
				Title:       "Benchmark Result 2", 
				URL:         "https://example2.com",
				Description: "Description for benchmark result 2",
				Score:       1.3,
			},
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := processor.FormatAsJSON(response)
		if err != nil {
			b.Fatalf("Benchmark failed: %v", err)
		}
	}
}