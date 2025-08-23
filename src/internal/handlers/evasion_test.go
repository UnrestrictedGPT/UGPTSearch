package handlers

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRequestPatternTracking(t *testing.T) {
	pattern := &RequestPattern{
		sessionStart:         time.Now(),
		typicalPauseDuration: 2 * time.Second,
	}

	// Test initial state
	if pattern.requestCount != 0 {
		t.Errorf("Expected initial request count of 0, got %d", pattern.requestCount)
	}

	// Track some requests
	searchTerms := []string{"test query 1", "test query 2", "test query 3"}
	
	for i, term := range searchTerms {
		pattern.TrackRequest(term)
		
		if pattern.requestCount != i+1 {
			t.Errorf("Expected request count %d, got %d", i+1, pattern.requestCount)
		}
		
		if len(pattern.searchTerms) != i+1 {
			t.Errorf("Expected %d search terms, got %d", i+1, len(pattern.searchTerms))
		}
		
		if pattern.searchTerms[i] != term {
			t.Errorf("Expected search term %q, got %q", term, pattern.searchTerms[i])
		}
	}
}

func TestRequestPatternMemoryLimit(t *testing.T) {
	pattern := &RequestPattern{
		sessionStart:         time.Now(),
		typicalPauseDuration: 2 * time.Second,
	}

	// Add more than 10 search terms
	for i := 0; i < 15; i++ {
		pattern.TrackRequest(fmt.Sprintf("query %d", i))
	}

	// Should maintain only the last 10 terms
	if len(pattern.searchTerms) != 10 {
		t.Errorf("Expected 10 search terms, got %d", len(pattern.searchTerms))
	}

	// Verify the terms are the last 10
	for i, term := range pattern.searchTerms {
		expected := fmt.Sprintf("query %d", i+5) // Should be queries 5-14
		if term != expected {
			t.Errorf("Expected term %q, got %q", expected, term)
		}
	}
}

func TestShouldDelayRequest(t *testing.T) {
	pattern := &RequestPattern{
		sessionStart:         time.Now(),
		typicalPauseDuration: 2 * time.Second,
		requestCount:         0,
		lastRequestTime:      time.Now().Add(-5 * time.Minute), // Long ago
	}

	// First request should have no delay
	delay := pattern.ShouldDelayRequest()
	if delay != 0 {
		t.Errorf("Expected no delay for first request, got %v", delay)
	}

	// Recent request should trigger delay
	pattern.requestCount = 5
	pattern.lastRequestTime = time.Now().Add(-1 * time.Second)
	
	delay = pattern.ShouldDelayRequest()
	if delay == 0 {
		t.Error("Expected delay for recent request")
	}

	// Verify delay is within reasonable bounds
	if delay < 200*time.Millisecond || delay > 10*time.Second {
		t.Errorf("Delay %v outside reasonable bounds", delay)
	}
}

func TestDelayBasedOnRequestFrequency(t *testing.T) {
	now := time.Now()
	
	tests := []struct {
		name            string
		sessionDuration time.Duration
		requestCount    int
		expectedRange   [2]time.Duration // min, max expected delay
	}{
		{
			name:            "New session",
			sessionDuration: 1 * time.Minute,
			requestCount:    1,
			expectedRange:   [2]time.Duration{1500 * time.Millisecond, 3500 * time.Millisecond},
		},
		{
			name:            "High frequency established session",
			sessionDuration: 5 * time.Minute,
			requestCount:    55, // 11 requests per minute
			expectedRange:   [2]time.Duration{3000 * time.Millisecond, 7000 * time.Millisecond},
		},
		{
			name:            "Moderate frequency established session",
			sessionDuration: 10 * time.Minute,
			requestCount:    70, // 7 requests per minute
			expectedRange:   [2]time.Duration{1000 * time.Millisecond, 3000 * time.Millisecond},
		},
		{
			name:            "Low frequency established session",
			sessionDuration: 20 * time.Minute,
			requestCount:    40, // 2 requests per minute
			expectedRange:   [2]time.Duration{500 * time.Millisecond, 1500 * time.Millisecond},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pattern := &RequestPattern{
				sessionStart:    now.Add(-tt.sessionDuration),
				requestCount:    tt.requestCount,
				lastRequestTime: now.Add(-1 * time.Second),
			}

			delay := pattern.ShouldDelayRequest()
			
			if delay < tt.expectedRange[0] || delay > tt.expectedRange[1] {
				t.Errorf("Delay %v outside expected range [%v, %v]", 
					delay, tt.expectedRange[0], tt.expectedRange[1])
			}
		})
	}
}

func TestAdvancedHumanLikeDelay(t *testing.T) {
	// Reset global pattern for testing
	oldPattern := globalPattern
	defer func() { globalPattern = oldPattern }()
	
	globalPattern = &RequestPattern{
		sessionStart:         time.Now(),
		typicalPauseDuration: 2 * time.Second,
	}

	queries := []string{
		"simple",
		"this is a more complex query with multiple words",
		"advanced \"quoted search\" +required -excluded",
		"very long query with many words and complex boolean operators and special characters",
	}

	for _, query := range queries {
		delay := GetAdvancedHumanLikeDelay(query)
		
		if delay < 0 {
			t.Errorf("Delay should not be negative, got %v", delay)
		}

		// Verify delay increases with query complexity
		if strings.Contains(query, "\"") || strings.Contains(query, "+") || len(query) > 50 {
			if delay < 500*time.Millisecond {
				t.Errorf("Expected longer delay for complex query %q, got %v", query, delay)
			}
		}
	}
}

func TestQueryComplexityFactor(t *testing.T) {
	tests := []struct {
		name     string
		query    string
		minFactor float64
		maxFactor float64
	}{
		{
			name:      "Simple query",
			query:     "test",
			minFactor: 0.8,
			maxFactor: 1.4,
		},
		{
			name:      "Long query",
			query:     "this is a very long query that should trigger complexity multiplier due to length",
			minFactor: 1.0,
			maxFactor: 2.0,
		},
		{
			name:      "Multi-word query",
			query:     "multiple word search query with several terms",
			minFactor: 1.0,
			maxFactor: 2.0,
		},
		{
			name:      "Advanced search with operators",
			query:     "\"exact phrase\" +required -excluded (grouped)",
			minFactor: 1.1,
			maxFactor: 2.5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			factor := getQueryComplexityFactor(tt.query)
			
			if factor < tt.minFactor || factor > tt.maxFactor {
				t.Errorf("Complexity factor %.3f outside expected range [%.3f, %.3f]", 
					factor, tt.minFactor, tt.maxFactor)
			}
		})
	}
}

func TestTimeOfDayFactor(t *testing.T) {
	// Test different hours
	factors := make(map[int]float64)
	
	// Collect factors for all hours
	for i := 0; i < 24; i++ {
		// Can't easily mock time.Now() without external dependencies,
		// so we test the current hour and verify the range is reasonable
		factor := getTimeOfDayFactor()
		factors[i] = factor
		
		if factor < 0.5 || factor > 2.5 {
			t.Errorf("Time of day factor %.3f outside reasonable range [0.5, 2.5]", factor)
		}
	}
}

func TestAdvancedHeaderRandomization(t *testing.T) {
	testCases := []struct {
		name     string
		checkFn  func(req *http.Request) bool
		testName string
	}{
		{
			name: "Accept-Encoding randomization",
			checkFn: func(req *http.Request) bool {
				encoding := req.Header.Get("Accept-Encoding")
				return strings.Contains(encoding, "gzip") && strings.Contains(encoding, "br")
			},
			testName: "Accept-Encoding should contain standard encodings",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req, _ := http.NewRequest("GET", "http://example.com", nil)
			
			// Test multiple times to account for randomization
			successCount := 0
			iterations := 50
			
			for i := 0; i < iterations; i++ {
				// Clear headers
				req.Header = make(http.Header)
				
				AdvancedHeaderRandomization(req)
				
				if tc.checkFn(req) {
					successCount++
				}
			}
			
			// Should succeed in most iterations
			if successCount < iterations/2 {
				t.Errorf("%s succeeded only %d/%d times", tc.testName, successCount, iterations)
			}
		})
	}
}

func TestAdvancedHeaderRandomizationVariations(t *testing.T) {
	variations := make(map[string]bool)
	iterations := 100
	
	for i := 0; i < iterations; i++ {
		req, _ := http.NewRequest("GET", "http://example.com", nil)
		AdvancedHeaderRandomization(req)
		
		// Check Accept-Encoding variations
		encoding := req.Header.Get("Accept-Encoding")
		variations[encoding] = true
		
		// Verify it contains expected encodings
		expectedEncodings := []string{"gzip", "deflate", "br", "zstd"}
		for _, enc := range expectedEncodings {
			if !strings.Contains(encoding, enc) {
				t.Errorf("Accept-Encoding missing %s: %s", enc, encoding)
			}
		}
	}
	
	// Should have multiple variations due to randomization
	if len(variations) < 5 {
		t.Errorf("Expected at least 5 Accept-Encoding variations, got %d", len(variations))
	}
}

func TestClientDataHeaderGeneration(t *testing.T) {
	// Test multiple generations for uniqueness
	headers := make(map[string]bool)
	
	for i := 0; i < 50; i++ {
		header := generateClientDataHeader()
		
		// Check length range
		if len(header) < 40 || len(header) > 60 {
			t.Errorf("Client data header length %d outside expected range [40, 60]", len(header))
		}
		
		// Check characters are valid base64-like
		for _, char := range header {
			if !((char >= 'A' && char <= 'Z') || 
				 (char >= 'a' && char <= 'z') || 
				 (char >= '0' && char <= '9') || 
				 char == '+' || char == '/') {
				t.Errorf("Invalid character %c in client data header", char)
			}
		}
		
		headers[header] = true
	}
	
	// Should generate unique headers
	if len(headers) < 40 {
		t.Errorf("Expected high uniqueness in generated headers, got %d unique out of 50", len(headers))
	}
}

func TestSessionBasedDelay(t *testing.T) {
	// This test verifies the function doesn't panic and completes
	start := time.Now()
	SessionBasedDelay()
	elapsed := time.Since(start)
	
	// Should complete within reasonable time
	if elapsed > 10*time.Second {
		t.Errorf("SessionBasedDelay took too long: %v", elapsed)
	}
}

func TestAnubisDetectionSignatures(t *testing.T) {
	tests := []struct {
		name               string
		responseBody       string
		statusCode         int
		headers            http.Header
		expectProtected    bool
		minConfidence      float64
		expectedReasons    []string
	}{
		{
			name:            "Normal search results",
			responseBody:    `<html><body><div class="result"><a href="#">Test Result</a></div></body></html>`,
			statusCode:      200,
			headers:         http.Header{"Content-Type": []string{"text/html"}},
			expectProtected: false,
			minConfidence:   0,
		},
		{
			name:            "Explicit Anubis protection",
			responseBody:    `<html><body>Access denied - bot detection system activated</body></html>`,
			statusCode:      403,
			headers:         http.Header{"Content-Type": []string{"text/html"}},
			expectProtected: true,
			minConfidence:   40.0,
			expectedReasons: []string{"Found signature", "HTTP 403 Forbidden"},
		},
		{
			name:            "JavaScript challenge",
			responseBody:    `<html><body><script>document.cookie="challenge";setTimeout(function(){window.location="/verify"},1000);</script></body></html>`,
			statusCode:      200,
			headers:         http.Header{"Content-Type": []string{"text/html"}},
			expectProtected: true,
			minConfidence:   30.0,
			expectedReasons: []string{"JavaScript challenge"},
		},
		{
			name:            "Cloudflare protection",
			responseBody:    `<html><body>Checking your browser before accessing the website</body></html>`,
			statusCode:      503,
			headers:         http.Header{"CF-Ray": []string{"12345-ABC"}, "Server": []string{"cloudflare"}},
			expectProtected: true,
			minConfidence:   40.0,
			expectedReasons: []string{"Suspicious header", "HTTP 503"},
		},
		{
			name:            "CAPTCHA challenge",
			responseBody:    `<html><body>Please complete the CAPTCHA to prove you're not a robot</body></html>`,
			statusCode:      200,
			headers:         http.Header{"Content-Type": []string{"text/html"}},
			expectProtected: true,
			minConfidence:   40.0,
			expectedReasons: []string{"CAPTCHA challenge"},
		},
		{
			name:            "Rate limiting",
			responseBody:    `<html><body>Too many requests</body></html>`,
			statusCode:      429,
			headers:         http.Header{"Content-Type": []string{"text/html"}},
			expectProtected: true,
			minConfidence:   30.0,
			expectedReasons: []string{"HTTP 429 Too Many Requests"},
		},
		{
			name:            "Minimal HTML structure",
			responseBody:    `<html><body>Access denied</body></html>`,
			statusCode:      403,
			headers:         http.Header{"Content-Type": []string{"text/html"}},
			expectProtected: true,
			minConfidence:   40.0,
			expectedReasons: []string{"HTTP 403 Forbidden", "Minimal HTML structure"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := DetectAnubisProtection([]byte(tt.responseBody), tt.headers, tt.statusCode)
			
			if result.IsAnubisProtected != tt.expectProtected {
				t.Errorf("Expected IsAnubisProtected=%v, got %v", tt.expectProtected, result.IsAnubisProtected)
			}
			
			if tt.expectProtected && result.Confidence < tt.minConfidence {
				t.Errorf("Expected confidence >= %.1f, got %.1f", tt.minConfidence, result.Confidence)
			}
			
			// Verify expected reasons are present
			for _, expectedReason := range tt.expectedReasons {
				found := false
				for _, actualReason := range result.DetectionReasons {
					if strings.Contains(actualReason, expectedReason) {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("Expected detection reason containing %q, got %v", expectedReason, result.DetectionReasons)
				}
			}
			
			// Verify response analysis is populated
			if result.ResponseAnalysis["status_code"] != tt.statusCode {
				t.Errorf("Expected status_code %d in analysis, got %v", tt.statusCode, result.ResponseAnalysis["status_code"])
			}
			
			if result.ResponseAnalysis["body_length"] != len(tt.responseBody) {
				t.Errorf("Expected body_length %d in analysis, got %v", len(tt.responseBody), result.ResponseAnalysis["body_length"])
			}
		})
	}
}

func TestAnubisDetectionConfidenceCalculation(t *testing.T) {
	// Test that confidence doesn't exceed 100%
	heavyProtectionBody := `
	<html><body>
		Access denied - bot detection system activated.
		Automated access detected. Challenge required.
		Please enable JavaScript and complete the CAPTCHA.
		<script>document.cookie="challenge";window.location="/verify";</script>
		CloudFlare Ray ID: 12345-ABC
	</body></html>
	`
	
	headers := http.Header{
		"CF-Ray":         []string{"12345-ABC"},
		"X-Protected-By": []string{"Anubis"},
		"X-Security":     []string{"Bot Protection"},
	}
	
	result := DetectAnubisProtection([]byte(heavyProtectionBody), headers, 403)
	
	if result.Confidence > 100.0 {
		t.Errorf("Confidence should not exceed 100%%, got %.1f", result.Confidence)
	}
	
	if !result.IsAnubisProtected {
		t.Error("Expected heavy protection to be detected as Anubis-protected")
	}
	
	if result.Confidence < 80.0 {
		t.Errorf("Expected high confidence for heavy protection, got %.1f", result.Confidence)
	}
}

func TestSearchResultValidation(t *testing.T) {
	tests := []struct {
		name         string
		responseBody string
		expectValid  bool
	}{
		{
			name: "Valid search results",
			responseBody: `
			<html>
			<body>
				<div id="results">
					<article class="result">
						<h3><a href="http://example1.com">Result 1</a></h3>
						<p>Search results found</p>
					</article>
					<article class="result">
						<h3><a href="http://example2.com">Result 2</a></h3>
						<p>More results</p>
					</article>
				</div>
			</body>
			</html>
			`,
			expectValid: true,
		},
		{
			name: "No results found",
			responseBody: `
			<html>
			<body>
				<div id="results">
					<p>No results found for your search query.</p>
					<p>0 results returned.</p>
					<p>Try different keywords.</p>
				</div>
			</body>
			</html>
			`,
			expectValid: false,
		},
		{
			name: "Maintenance page",
			responseBody: `
			<html>
			<body>
				<h1>Site under maintenance</h1>
				<p>Please try again later.</p>
			</body>
			</html>
			`,
			expectValid: false,
		},
		{
			name: "SearXNG search results page",
			responseBody: `
			<html>
			<body>
				<div class="results">
					Results for "test query"
					<article class="result">
						<h3><a href="#">Test result</a></h3>
					</article>
				</div>
				<form action="search.php" method="get">
					<input name="q" type="text">
				</form>
			</body>
			</html>
			`,
			expectValid: true,
		},
		{
			name: "Empty results but valid structure",
			responseBody: `
			<html>
			<body>
				<div class="results">
					<p>Search results</p>
					<p>Results found: 0</p>
				</div>
			</body>
			</html>
			`,
			expectValid: true, // Has 2 valid indicators
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isValid := IsSearchResultValid([]byte(tt.responseBody))
			
			if isValid != tt.expectValid {
				t.Errorf("Expected IsSearchResultValid=%v, got %v", tt.expectValid, isValid)
			}
		})
	}
}

func TestConcurrentRequestPatternTracking(t *testing.T) {
	pattern := &RequestPattern{
		sessionStart:         time.Now(),
		typicalPauseDuration: 2 * time.Second,
	}

	const numGoroutines = 50
	const requestsPerGoroutine = 20
	
	var wg sync.WaitGroup
	
	// Launch concurrent goroutines tracking requests
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(goroutineID int) {
			defer wg.Done()
			
			for j := 0; j < requestsPerGoroutine; j++ {
				query := fmt.Sprintf("query-%d-%d", goroutineID, j)
				pattern.TrackRequest(query)
				
				// Also test delay calculation under concurrency
				delay := pattern.ShouldDelayRequest()
				if delay < 0 {
					t.Errorf("Negative delay returned: %v", delay)
				}
				
				// Small delay to create realistic timing
				time.Sleep(time.Millisecond)
			}
		}(i)
	}
	
	wg.Wait()
	
	// Verify final state
	expectedCount := numGoroutines * requestsPerGoroutine
	if pattern.requestCount != expectedCount {
		t.Errorf("Expected request count %d, got %d", expectedCount, pattern.requestCount)
	}
	
	// Search terms should be limited to last 10
	if len(pattern.searchTerms) != 10 {
		t.Errorf("Expected 10 search terms, got %d", len(pattern.searchTerms))
	}
}

func BenchmarkAdvancedHumanLikeDelay(b *testing.B) {
	// Reset global pattern for consistent benchmarking
	oldPattern := globalPattern
	defer func() { globalPattern = oldPattern }()
	
	globalPattern = &RequestPattern{
		sessionStart:         time.Now(),
		typicalPauseDuration: 2 * time.Second,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = GetAdvancedHumanLikeDelay("benchmark test query")
	}
}

func BenchmarkRequestPatternTracking(b *testing.B) {
	pattern := &RequestPattern{
		sessionStart:         time.Now(),
		typicalPauseDuration: 2 * time.Second,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pattern.TrackRequest(fmt.Sprintf("query %d", i))
	}
}

func BenchmarkAnubisDetection(b *testing.B) {
	responseBody := []byte(`
	<html>
	<body>
		<div class="results">
			<article class="result">
				<h3><a href="http://example.com">Test Result</a></h3>
				<p>This is a test search result</p>
			</article>
		</div>
	</body>
	</html>
	`)
	
	headers := http.Header{
		"Content-Type": []string{"text/html"},
		"Server":       []string{"nginx"},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = DetectAnubisProtection(responseBody, headers, 200)
	}
}

func BenchmarkAdvancedHeaderRandomization(b *testing.B) {
	req, _ := http.NewRequest("GET", "http://example.com", nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Clear headers
		req.Header = make(http.Header)
		AdvancedHeaderRandomization(req)
	}
}

func BenchmarkSearchResultValidation(b *testing.B) {
	responseBody := []byte(`
	<html>
	<body>
		<div id="results">
			<article class="result">
				<h3><a href="http://example1.com">Result 1</a></h3>
				<p>Search results found</p>
			</article>
		</div>
	</body>
	</html>
	`)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = IsSearchResultValid(responseBody)
	}
}