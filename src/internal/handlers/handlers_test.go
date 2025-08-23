package handlers

import (
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	
	"UGPTSearch/internal/instances"
)

func TestSearchHandler(t *testing.T) {
	// Create a test SearXNG instance
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check query parameter
		query := r.URL.Query().Get("q")
		if query == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		// Mock HTML response with search results
		htmlResponse := fmt.Sprintf(`
		<!DOCTYPE html>
		<html>
		<head><title>Search Results for %s</title></head>
		<body>
			<div id="results">
				<article class="result">
					<h3><a href="https://example1.com">Test Result 1 for %s</a></h3>
					<p class="content">This is a test result for the query %s</p>
					<a href="https://example1.com" class="url_wrapper">
						<span class="url">https://example1.com</span>
					</a>
				</article>
				<article class="result">
					<h3><a href="https://example2.com">Test Result 2 for %s</a></h3>
					<p class="content">Another test result for %s</p>
					<a href="https://example2.com" class="url_wrapper">
						<span class="url">https://example2.com</span>
					</a>
				</article>
			</div>
		</body>
		</html>
		`, query, query, query, query, query)

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(htmlResponse))
	}))
	defer testServer.Close()

	// Mock the instances manager
	origManager := instances.Manager
	defer func() { instances.Manager = origManager }()

	mockManager := instances.New()
	mockManager.AddInstance(testServer.URL)
	instances.Manager = mockManager

	tests := []struct {
		name           string
		query          string
		format         string
		expectedStatus int
		checkContent   func(t *testing.T, body string, contentType string)
	}{
		{
			name:           "Valid search query HTML format",
			query:          "test query",
			format:         "html",
			expectedStatus: http.StatusOK,
			checkContent: func(t *testing.T, body string, contentType string) {
				if !strings.Contains(contentType, "text/html") {
					t.Errorf("Expected HTML content type, got %s", contentType)
				}
				if !strings.Contains(body, "Test Result 1") {
					t.Error("Expected search results in HTML response")
				}
			},
		},
		{
			name:           "Valid search query JSON format",
			query:          "test query",
			format:         "json",
			expectedStatus: http.StatusOK,
			checkContent: func(t *testing.T, body string, contentType string) {
				if !strings.Contains(contentType, "application/json") {
					t.Errorf("Expected JSON content type, got %s", contentType)
				}
				var result map[string]interface{}
				if err := json.Unmarshal([]byte(body), &result); err != nil {
					t.Errorf("Failed to parse JSON response: %v", err)
				}
			},
		},
		{
			name:           "Valid search query text format",
			query:          "test query",
			format:         "text",
			expectedStatus: http.StatusOK,
			checkContent: func(t *testing.T, body string, contentType string) {
				if !strings.Contains(contentType, "text/plain") {
					t.Errorf("Expected plain text content type, got %s", contentType)
				}
			},
		},
		{
			name:           "Empty query",
			query:          "",
			format:         "html",
			expectedStatus: http.StatusBadRequest,
			checkContent: func(t *testing.T, body string, contentType string) {
				if !strings.Contains(body, "Missing search query") {
					t.Error("Expected error message for missing query")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := http.NewRequest("GET", "/search", nil)
			if err != nil {
				t.Fatal(err)
			}

			q := req.URL.Query()
			if tt.query != "" {
				q.Add("q", tt.query)
			}
			if tt.format != "" {
				q.Add("format", tt.format)
			}
			req.URL.RawQuery = q.Encode()

			rr := httptest.NewRecorder()
			handler := http.HandlerFunc(Search)

			handler.ServeHTTP(rr, req)

			if status := rr.Code; status != tt.expectedStatus {
				t.Errorf("Handler returned wrong status code: got %v want %v", status, tt.expectedStatus)
			}

			if tt.checkContent != nil {
				tt.checkContent(t, rr.Body.String(), rr.Header().Get("Content-Type"))
			}
		})
	}
}

func TestSearchJSONHandler(t *testing.T) {
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query().Get("q")
		if query == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		// Mock HTML response with search results
		htmlResponse := `
		<!DOCTYPE html>
		<html>
		<body>
			<div id="results">
				<article class="result">
					<h3><a href="https://example.com">Test Result</a></h3>
					<p class="content">Test content</p>
				</article>
			</div>
		</body>
		</html>
		`

		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(htmlResponse))
	}))
	defer testServer.Close()

	// Mock the instances manager
	origManager := instances.Manager
	defer func() { instances.Manager = origManager }()

	mockManager := instances.New()
	mockManager.AddInstance(testServer.URL)
	instances.Manager = mockManager

	req, err := http.NewRequest("GET", "/search.json", nil)
	if err != nil {
		t.Fatal(err)
	}

	q := req.URL.Query()
	q.Add("q", "test query")
	req.URL.RawQuery = q.Encode()

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(SearchJSON)

	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("Handler returned wrong status code: got %v want %v", status, http.StatusOK)
	}

	contentType := rr.Header().Get("Content-Type")
	if !strings.Contains(contentType, "application/json") {
		t.Errorf("Expected JSON content type, got %s", contentType)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &result); err != nil {
		t.Errorf("Failed to parse JSON response: %v", err)
	}
}

func TestInstancesHandler(t *testing.T) {
	// Mock the instances manager
	origManager := instances.Manager
	defer func() { instances.Manager = origManager }()

	mockManager := instances.New()
	mockManager.AddInstance("http://instance1.com")
	mockManager.AddInstance("http://instance2.com")
	instances.Manager = mockManager

	req, err := http.NewRequest("GET", "/instances", nil)
	if err != nil {
		t.Fatal(err)
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(Instances)

	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("Handler returned wrong status code: got %v want %v", status, http.StatusOK)
	}

	contentType := rr.Header().Get("Content-Type")
	if !strings.Contains(contentType, "application/json") {
		t.Errorf("Expected JSON content type, got %s", contentType)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &result); err != nil {
		t.Errorf("Failed to parse JSON response: %v", err)
	}

	if instances, ok := result["instances"]; !ok {
		t.Error("Expected 'instances' field in response")
	} else if instanceSlice, ok := instances.([]interface{}); !ok {
		t.Error("Expected 'instances' to be an array")
	} else if len(instanceSlice) != 2 {
		t.Errorf("Expected 2 instances, got %d", len(instanceSlice))
	}

	if count, ok := result["count"]; !ok {
		t.Error("Expected 'count' field in response")
	} else if countFloat, ok := count.(float64); !ok {
		t.Error("Expected 'count' to be a number")
	} else if int(countFloat) != 2 {
		t.Errorf("Expected count 2, got %d", int(countFloat))
	}
}

func TestAnubisStatsHandler(t *testing.T) {
	req, err := http.NewRequest("GET", "/anubis-stats", nil)
	if err != nil {
		t.Fatal(err)
	}

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(AnubisStats)

	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("Handler returned wrong status code: got %v want %v", status, http.StatusOK)
	}

	contentType := rr.Header().Get("Content-Type")
	if !strings.Contains(contentType, "application/json") {
		t.Errorf("Expected JSON content type, got %s", contentType)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(rr.Body.Bytes(), &result); err != nil {
		t.Errorf("Failed to parse JSON response: %v", err)
	}

	if _, ok := result["anubis_detection"]; !ok {
		t.Error("Expected 'anubis_detection' field in response")
	}

	if _, ok := result["timestamp"]; !ok {
		t.Error("Expected 'timestamp' field in response")
	}

	if _, ok := result["detection_info"]; !ok {
		t.Error("Expected 'detection_info' field in response")
	}
}

func TestBrowserProfileSelection(t *testing.T) {
	// Test browser profile distribution
	profileCount := make(map[string]int)
	iterations := 1000

	for i := 0; i < iterations; i++ {
		profile := getRandomBrowserProfile()
		
		if strings.Contains(profile.UserAgent, "Chrome") {
			profileCount["Chrome"]++
		} else if strings.Contains(profile.UserAgent, "Firefox") {
			profileCount["Firefox"]++
		} else if strings.Contains(profile.UserAgent, "Safari") {
			profileCount["Safari"]++
		}
	}

	// Chrome should be approximately 65% (±10%)
	chromePercent := float64(profileCount["Chrome"]) / float64(iterations) * 100
	if chromePercent < 55 || chromePercent > 75 {
		t.Errorf("Chrome usage outside expected range: %.1f%% (expected ~65%%)", chromePercent)
	}

	// Firefox should be approximately 20% (±10%)
	firefoxPercent := float64(profileCount["Firefox"]) / float64(iterations) * 100
	if firefoxPercent < 10 || firefoxPercent > 30 {
		t.Errorf("Firefox usage outside expected range: %.1f%% (expected ~20%%)", firefoxPercent)
	}

	// Safari should be approximately 15% (±10%)
	safariPercent := float64(profileCount["Safari"]) / float64(iterations) * 100
	if safariPercent < 5 || safariPercent > 25 {
		t.Errorf("Safari usage outside expected range: %.1f%% (expected ~15%%)", safariPercent)
	}

	t.Logf("Browser distribution - Chrome: %.1f%%, Firefox: %.1f%%, Safari: %.1f%%", 
		chromePercent, firefoxPercent, safariPercent)
}

func TestHTTPClientCreation(t *testing.T) {
	client := createAdvancedHTTPClient()
	
	if client == nil {
		t.Fatal("Expected HTTP client to be created")
	}

	if client.Timeout != 45*time.Second {
		t.Errorf("Expected timeout of 45s, got %v", client.Timeout)
	}

	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatal("Expected HTTP transport")
	}

	if transport.MaxIdleConns != 100 {
		t.Errorf("Expected MaxIdleConns of 100, got %d", transport.MaxIdleConns)
	}

	if transport.MaxIdleConnsPerHost != 10 {
		t.Errorf("Expected MaxIdleConnsPerHost of 10, got %d", transport.MaxIdleConnsPerHost)
	}

	if transport.IdleConnTimeout != 90*time.Second {
		t.Errorf("Expected IdleConnTimeout of 90s, got %v", transport.IdleConnTimeout)
	}

	if !transport.ForceAttemptHTTP2 {
		t.Error("Expected ForceAttemptHTTP2 to be true")
	}
}

func TestBrowserSpecificClient(t *testing.T) {
	profiles := []BrowserProfile{
		{UserAgent: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"},
		{UserAgent: "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:121.0) Gecko/20100101 Firefox/121.0"},
		{UserAgent: "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.1 Safari/605.1.15"},
	}

	for _, profile := range profiles {
		client := getBrowserSpecificClient(profile)
		
		if client == nil {
			t.Fatal("Expected client to be created")
		}

		transport, ok := client.Transport.(*http.Transport)
		if !ok {
			t.Fatal("Expected HTTP transport")
		}

		// Check browser-specific optimizations
		if strings.Contains(profile.UserAgent, "Chrome") {
			if transport.MaxIdleConnsPerHost != 15 {
				t.Errorf("Expected Chrome MaxIdleConnsPerHost of 15, got %d", transport.MaxIdleConnsPerHost)
			}
			if !transport.ForceAttemptHTTP2 {
				t.Error("Expected Chrome to force HTTP/2")
			}
		} else if strings.Contains(profile.UserAgent, "Firefox") {
			if transport.MaxIdleConnsPerHost != 8 {
				t.Errorf("Expected Firefox MaxIdleConnsPerHost of 8, got %d", transport.MaxIdleConnsPerHost)
			}
			if transport.ResponseHeaderTimeout != 25*time.Second {
				t.Errorf("Expected Firefox ResponseHeaderTimeout of 25s, got %v", transport.ResponseHeaderTimeout)
			}
		} else if strings.Contains(profile.UserAgent, "Safari") {
			if transport.MaxIdleConnsPerHost != 6 {
				t.Errorf("Expected Safari MaxIdleConnsPerHost of 6, got %d", transport.MaxIdleConnsPerHost)
			}
			if transport.IdleConnTimeout != 60*time.Second {
				t.Errorf("Expected Safari IdleConnTimeout of 60s, got %v", transport.IdleConnTimeout)
			}
		}
	}
}

func TestHeaderRandomization(t *testing.T) {
	req, _ := http.NewRequest("GET", "http://example.com", nil)
	
	// Test multiple iterations to ensure randomization
	languages := make(map[string]bool)
	accepts := make(map[string]bool)
	
	for i := 0; i < 50; i++ {
		profile := setHumanLikeHeaders(req, "http://example.com", "http://example.com")
		
		// Check that essential headers are set
		if req.Header.Get("User-Agent") == "" {
			t.Error("User-Agent header should be set")
		}
		
		if req.Header.Get("Accept") == "" {
			t.Error("Accept header should be set")
		}
		
		if req.Header.Get("Accept-Language") == "" {
			t.Error("Accept-Language header should be set")
		}
		
		// Collect values for randomization verification
		languages[req.Header.Get("Accept-Language")] = true
		accepts[req.Header.Get("Accept")] = true
		
		// Check browser-specific headers
		if strings.Contains(profile.UserAgent, "Chrome") {
			if req.Header.Get("sec-ch-ua") == "" {
				t.Error("Chrome should have sec-ch-ua header")
			}
			if req.Header.Get("sec-ch-ua-mobile") == "" {
				t.Error("Chrome should have sec-ch-ua-mobile header")
			}
		}
		
		// Clear headers for next iteration
		req.Header = make(http.Header)
	}
	
	// Verify randomization occurred
	if len(languages) < 3 {
		t.Errorf("Expected at least 3 different Accept-Language values, got %d", len(languages))
	}
	
	if len(accepts) < 3 {
		t.Errorf("Expected at least 3 different Accept values, got %d", len(accepts))
	}
}

func TestDecompressResponse(t *testing.T) {
	testData := "This is test data for compression"
	
	tests := []struct {
		name        string
		encoding    string
		setupBody   func() io.ReadCloser
		expectError bool
	}{
		{
			name:     "No compression",
			encoding: "",
			setupBody: func() io.ReadCloser {
				return io.NopCloser(strings.NewReader(testData))
			},
			expectError: false,
		},
		{
			name:     "Gzip compression",
			encoding: "gzip",
			setupBody: func() io.ReadCloser {
				var buf strings.Builder
				writer := gzip.NewWriter(&buf)
				writer.Write([]byte(testData))
				writer.Close()
				return io.NopCloser(strings.NewReader(buf.String()))
			},
			expectError: false,
		},
		{
			name:     "Unsupported compression",
			encoding: "unknown",
			setupBody: func() io.ReadCloser {
				return io.NopCloser(strings.NewReader(testData))
			},
			expectError: true,
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := &http.Response{
				Header: make(http.Header),
				Body:   tt.setupBody(),
			}
			
			if tt.encoding != "" {
				resp.Header.Set("Content-Encoding", tt.encoding)
			}
			
			data, err := decompressResponse(resp)
			
			if tt.expectError {
				if err == nil {
					t.Error("Expected error but got none")
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error: %v", err)
				}
				
				if string(data) != testData {
					t.Errorf("Expected %q, got %q", testData, string(data))
				}
			}
		})
	}
}

func TestHumanLikeDelay(t *testing.T) {
	delays := make([]time.Duration, 100)
	
	for i := 0; i < 100; i++ {
		delays[i] = getHumanLikeDelay()
	}
	
	// Check delay ranges
	fastCount := 0
	normalCount := 0
	slowCount := 0
	
	for _, delay := range delays {
		switch {
		case delay >= 300*time.Millisecond && delay < 800*time.Millisecond:
			fastCount++
		case delay >= 1*time.Second && delay < 4*time.Second:
			normalCount++
		case delay >= 4*time.Second && delay <= 12*time.Second:
			slowCount++
		}
	}
	
	// Verify distribution roughly matches expected percentages
	if fastCount < 15 || fastCount > 35 { // ~25% ±10%
		t.Errorf("Fast delays outside expected range: %d/100 (expected ~25)", fastCount)
	}
	
	if normalCount < 40 || normalCount > 60 { // ~50% ±10%
		t.Errorf("Normal delays outside expected range: %d/100 (expected ~50)", normalCount)
	}
	
	if slowCount < 15 || slowCount > 35 { // ~25% ±10%
		t.Errorf("Slow delays outside expected range: %d/100 (expected ~25)", slowCount)
	}
	
	t.Logf("Delay distribution - Fast: %d, Normal: %d, Slow: %d", fastCount, normalCount, slowCount)
}

func TestConcurrentSearchRequests(t *testing.T) {
	// Create test server that tracks concurrent requests
	var currentConnections int64
	var maxConnections int64
	var totalRequests int64
	
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		current := atomic.AddInt64(&currentConnections, 1)
		atomic.AddInt64(&totalRequests, 1)
		
		// Update max connections
		for {
			max := atomic.LoadInt64(&maxConnections)
			if current <= max || atomic.CompareAndSwapInt64(&maxConnections, max, current) {
				break
			}
		}
		
		// Simulate processing time
		time.Sleep(50 * time.Millisecond)
		
		w.Write([]byte(`<html><body><div id="results"><article class="result"><h3><a href="#">Test</a></h3></article></div></body></html>`))
		
		atomic.AddInt64(&currentConnections, -1)
	}))
	defer testServer.Close()

	// Mock the instances manager
	origManager := instances.Manager
	defer func() { instances.Manager = origManager }()

	mockManager := instances.New()
	mockManager.AddInstance(testServer.URL)
	instances.Manager = mockManager

	// Launch concurrent requests
	const numRequests = 20
	var wg sync.WaitGroup
	var successCount int64
	
	for i := 0; i < numRequests; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			
			req, _ := http.NewRequest("GET", "/search", nil)
			q := req.URL.Query()
			q.Add("q", fmt.Sprintf("test query %d", id))
			q.Add("format", "html")
			req.URL.RawQuery = q.Encode()
			
			rr := httptest.NewRecorder()
			handler := http.HandlerFunc(Search)
			
			handler.ServeHTTP(rr, req)
			
			if rr.Code == http.StatusOK {
				atomic.AddInt64(&successCount, 1)
			}
		}(i)
	}
	
	wg.Wait()
	
	// Verify results
	total := atomic.LoadInt64(&totalRequests)
	success := atomic.LoadInt64(&successCount)
	max := atomic.LoadInt64(&maxConnections)
	
	t.Logf("Concurrent test results - Total requests: %d, Successful: %d, Max concurrent: %d", 
		total, success, max)
	
	if success != numRequests {
		t.Errorf("Expected %d successful requests, got %d", numRequests, success)
	}
	
	if max < 1 || max > numRequests {
		t.Errorf("Max concurrent connections %d outside expected range 1-%d", max, numRequests)
	}
}

func TestRandomHexGeneration(t *testing.T) {
	lengths := []int{8, 16, 32, 64}
	
	for _, length := range lengths {
		hex := generateRandomHex(length)
		
		if len(hex) != length {
			t.Errorf("Expected hex length %d, got %d", length, len(hex))
		}
		
		// Check that it's valid hex
		for _, char := range hex {
			if !((char >= '0' && char <= '9') || (char >= 'a' && char <= 'f') || (char >= 'A' && char <= 'F')) {
				t.Errorf("Invalid hex character: %c", char)
				break
			}
		}
	}
	
	// Test uniqueness
	hex1 := generateRandomHex(32)
	hex2 := generateRandomHex(32)
	
	if hex1 == hex2 {
		t.Error("Generated identical hex strings, should be unique")
	}
}

func BenchmarkBrowserProfileSelection(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_ = getRandomBrowserProfile()
	}
}

func BenchmarkHeaderGeneration(b *testing.B) {
	req, _ := http.NewRequest("GET", "http://example.com", nil)
	
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		setHumanLikeHeaders(req, "http://example.com", "http://example.com")
		// Clear headers for next iteration
		req.Header = make(http.Header)
	}
}

func BenchmarkSearchHandlerThroughput(b *testing.B) {
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html><body><div id="results"><article class="result"><h3><a href="#">Test</a></h3></article></div></body></html>`))
	}))
	defer testServer.Close()

	// Mock the instances manager
	origManager := instances.Manager
	defer func() { instances.Manager = origManager }()

	mockManager := instances.New()
	mockManager.AddInstance(testServer.URL)
	instances.Manager = mockManager

	handler := http.HandlerFunc(Search)
	
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			req, _ := http.NewRequest("GET", "/search", nil)
			q := req.URL.Query()
			q.Add("q", "benchmark query")
			q.Add("format", "html")
			req.URL.RawQuery = q.Encode()
			
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)
		}
	})
}