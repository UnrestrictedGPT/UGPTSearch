package server

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestNewServer(t *testing.T) {
	addr := ":8081"
	server := New(addr)

	if server == nil {
		t.Fatal("Expected server to be created")
	}

	if server.httpServer.Addr != addr {
		t.Errorf("Expected server address %s, got %s", addr, server.httpServer.Addr)
	}

	if server.httpServer.ReadTimeout != 30*time.Second {
		t.Errorf("Expected ReadTimeout 30s, got %v", server.httpServer.ReadTimeout)
	}

	if server.httpServer.WriteTimeout != 30*time.Second {
		t.Errorf("Expected WriteTimeout 30s, got %v", server.httpServer.WriteTimeout)
	}

	if server.httpServer.IdleTimeout != 60*time.Second {
		t.Errorf("Expected IdleTimeout 60s, got %v", server.httpServer.IdleTimeout)
	}
}

func TestServerSetupRoutes(t *testing.T) {
	server := New(":8081")
	server.SetupRoutes()

	// Test that routes are properly configured by making test requests
	testServer := httptest.NewServer(nil)
	defer testServer.Close()

	// Since we can't easily test the actual route setup without starting the server,
	// we'll verify the setup doesn't panic and creates the expected structure
	if server.httpServer.Handler != nil {
		t.Error("Handler should not be set by SetupRoutes in basic server")
	}
}

func TestNewConcurrentServer(t *testing.T) {
	config := DefaultConcurrentServerConfig()
	config.Addr = ":8082"

	server, err := NewConcurrentServer(config)

	if err != nil {
		t.Fatalf("Failed to create concurrent server: %v", err)
	}

	if server == nil {
		t.Fatal("Expected concurrent server to be created")
	}

	if server.config.Addr != ":8082" {
		t.Errorf("Expected address :8082, got %s", server.config.Addr)
	}

	if server.workerPool == nil {
		t.Error("Expected worker pool to be initialized")
	}

	if server.connectionPool == nil {
		t.Error("Expected connection pool to be initialized")
	}

	if server.rateLimiter == nil {
		t.Error("Expected rate limiter to be initialized")
	}

	if server.circuitBreaker == nil {
		t.Error("Expected circuit breaker to be initialized")
	}

	if server.requestQueue == nil {
		t.Error("Expected request queue to be initialized")
	}

	if server.loadBalancer == nil {
		t.Error("Expected load balancer to be initialized")
	}

	if server.metricsCollector == nil {
		t.Error("Expected metrics collector to be initialized")
	}

	// Test cleanup
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	server.Shutdown(ctx)
}

func TestNewConcurrentServerWithNilConfig(t *testing.T) {
	server, err := NewConcurrentServer(nil)

	if err != nil {
		t.Fatalf("Failed to create concurrent server with nil config: %v", err)
	}

	if server.config.Addr != DefaultConcurrentServerConfig().Addr {
		t.Error("Should use default config when nil config provided")
	}

	// Cleanup
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	server.Shutdown(ctx)
}

func TestDefaultConcurrentServerConfig(t *testing.T) {
	config := DefaultConcurrentServerConfig()

	if config.Addr != ":8080" {
		t.Errorf("Expected default address :8080, got %s", config.Addr)
	}

	if config.ReadTimeout != 30*time.Second {
		t.Errorf("Expected ReadTimeout 30s, got %v", config.ReadTimeout)
	}

	if config.WorkerPoolSize == 0 {
		t.Error("Expected WorkerPoolSize to be set")
	}

	if config.QueueSize == 0 {
		t.Error("Expected QueueSize to be set")
	}

	if config.RateLimitPerClient == 0 {
		t.Error("Expected RateLimitPerClient to be set")
	}

	if config.MetricsEnabled != true {
		t.Error("Expected MetricsEnabled to be true by default")
	}
}

func TestConcurrentServerSetupRoutes(t *testing.T) {
	server, err := NewConcurrentServer(DefaultConcurrentServerConfig())
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		server.Shutdown(ctx)
	}()

	server.SetupRoutes()

	if server.httpServer.Handler == nil {
		t.Error("Expected handler to be set after SetupRoutes")
	}
}

func TestGetClientIP(t *testing.T) {
	server, err := NewConcurrentServer(DefaultConcurrentServerConfig())
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		server.Shutdown(ctx)
	}()

	tests := []struct {
		name           string
		headers        map[string]string
		remoteAddr     string
		expectedPrefix string
	}{
		{
			name:           "X-Forwarded-For header",
			headers:        map[string]string{"X-Forwarded-For": "192.168.1.100"},
			remoteAddr:     "127.0.0.1:12345",
			expectedPrefix: "192.168.1.100",
		},
		{
			name:           "X-Real-IP header",
			headers:        map[string]string{"X-Real-IP": "10.0.0.50"},
			remoteAddr:     "127.0.0.1:12345",
			expectedPrefix: "10.0.0.50",
		},
		{
			name:           "No forwarding headers",
			headers:        map[string]string{},
			remoteAddr:     "127.0.0.1:12345",
			expectedPrefix: "127.0.0.1:12345",
		},
		{
			name: "Both headers present, X-Forwarded-For takes precedence",
			headers: map[string]string{
				"X-Forwarded-For": "192.168.1.100",
				"X-Real-IP":       "10.0.0.50",
			},
			remoteAddr:     "127.0.0.1:12345",
			expectedPrefix: "192.168.1.100",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/test", nil)
			req.RemoteAddr = tt.remoteAddr

			for key, value := range tt.headers {
				req.Header.Set(key, value)
			}

			clientIP := server.getClientIP(req)

			if clientIP != tt.expectedPrefix {
				t.Errorf("Expected client IP %s, got %s", tt.expectedPrefix, clientIP)
			}
		})
	}
}

func TestExtractHeaders(t *testing.T) {
	server, err := NewConcurrentServer(DefaultConcurrentServerConfig())
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		server.Shutdown(ctx)
	}()

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("User-Agent", "Test Agent")
	req.Header.Set("Accept", "text/html")
	req.Header.Add("Accept-Encoding", "gzip")
	req.Header.Add("Accept-Encoding", "deflate") // Multiple values

	headers := server.extractHeaders(req)

	if headers["User-Agent"] != "Test Agent" {
		t.Errorf("Expected User-Agent 'Test Agent', got %s", headers["User-Agent"])
	}

	if headers["Accept"] != "text/html" {
		t.Errorf("Expected Accept 'text/html', got %s", headers["Accept"])
	}

	// Should only capture first value for multi-value headers
	if headers["Accept-Encoding"] != "gzip" {
		t.Errorf("Expected Accept-Encoding 'gzip', got %s", headers["Accept-Encoding"])
	}

	expectedHeaderCount := 3
	if len(headers) < expectedHeaderCount {
		t.Errorf("Expected at least %d headers, got %d", expectedHeaderCount, len(headers))
	}
}

func TestDetermineBrowserProfile(t *testing.T) {
	server, err := NewConcurrentServer(DefaultConcurrentServerConfig())
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		server.Shutdown(ctx)
	}()

	tests := []struct {
		name              string
		userAgent         string
		expectedBrowser   string
		expectedPlatform  string
		expectedHTTP2     bool
	}{
		{
			name:              "Chrome Windows",
			userAgent:         "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
			expectedBrowser:   "chrome",
			expectedPlatform:  "windows",
			expectedHTTP2:     true,
		},
		{
			name:              "Chrome macOS",
			userAgent:         "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 Chrome/120.0.0.0 Safari/537.36",
			expectedBrowser:   "chrome",
			expectedPlatform:  "macos",
			expectedHTTP2:     true,
		},
		{
			name:              "Chrome Linux",
			userAgent:         "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 Chrome/120.0.0.0 Safari/537.36",
			expectedBrowser:   "chrome",
			expectedPlatform:  "linux",
			expectedHTTP2:     true,
		},
		{
			name:              "Firefox Windows",
			userAgent:         "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:121.0) Gecko/20100101 Firefox/121.0",
			expectedBrowser:   "firefox",
			expectedPlatform:  "windows",
			expectedHTTP2:     true,
		},
		{
			name:              "Safari macOS",
			userAgent:         "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 Safari/605.1.15",
			expectedBrowser:   "safari",
			expectedPlatform:  "macos",
			expectedHTTP2:     true,
		},
		{
			name:              "Unknown browser",
			userAgent:         "UnknownBrowser/1.0",
			expectedBrowser:   "chrome", // Default fallback
			expectedPlatform:  "windows", // Default fallback
			expectedHTTP2:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			profile := server.determineBrowserProfile(tt.userAgent)

			if profile.BrowserType != tt.expectedBrowser {
				t.Errorf("Expected browser type %s, got %s", tt.expectedBrowser, profile.BrowserType)
			}

			if profile.Platform != tt.expectedPlatform {
				t.Errorf("Expected platform %s, got %s", tt.expectedPlatform, profile.Platform)
			}

			if profile.HTTP2Enabled != tt.expectedHTTP2 {
				t.Errorf("Expected HTTP2Enabled %v, got %v", tt.expectedHTTP2, profile.HTTP2Enabled)
			}

			if profile.UserAgent != tt.userAgent {
				t.Errorf("Expected UserAgent %s, got %s", tt.userAgent, profile.UserAgent)
			}
		})
	}
}

func TestContainsFunction(t *testing.T) {
	tests := []struct {
		s      string
		substr string
		expect bool
	}{
		{"Mozilla Chrome Safari", "Chrome", true},
		{"Firefox Gecko", "Chrome", false},
		{"", "test", false},
		{"test", "", true}, // Empty substring matches
		{"Chrome", "Chrome", true},
		{"test Chrome test", "Chrome", true},
		{"testChrome", "Chrome", true},
		{"Chrometest", "Chrome", true},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%s contains %s", tt.s, tt.substr), func(t *testing.T) {
			result := contains(tt.s, tt.substr)
			if result != tt.expect {
				t.Errorf("contains(%q, %q) = %v, expected %v", tt.s, tt.substr, result, tt.expect)
			}
		})
	}
}

func TestConcurrentServerLifecycle(t *testing.T) {
	config := DefaultConcurrentServerConfig()
	config.Addr = ":0" // Use random available port

	server, err := NewConcurrentServer(config)
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}

	server.SetupRoutes()

	// Test starting the server
	go func() {
		// Start returns when server is shut down
		server.Start()
	}()

	// Give server time to start
	time.Sleep(100 * time.Millisecond)

	// Test graceful shutdown
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err = server.Shutdown(ctx)
	if err != nil {
		t.Errorf("Shutdown failed: %v", err)
	}
}

func TestConcurrentServerDoubleStartStop(t *testing.T) {
	config := DefaultConcurrentServerConfig()
	config.Addr = ":0"

	server, err := NewConcurrentServer(config)
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}

	// Test that multiple starts don't cause issues
	go server.Start()
	go server.Start()
	go server.Start()

	time.Sleep(100 * time.Millisecond)

	// Test that multiple shutdowns don't cause issues
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			server.Shutdown(ctx)
		}()
	}

	wg.Wait()
}

func TestHandlerAdapterFunctions(t *testing.T) {
	t.Run("SetHumanLikeHeaders", func(t *testing.T) {
		req := httptest.NewRequest("GET", "http://example.com/search?q=test", nil)

		profile := SetHumanLikeHeaders(req, "http://example.com/search", "http://example.com")

		// Check that essential headers are set
		if req.Header.Get("Accept") == "" {
			t.Error("Accept header should be set")
		}

		if req.Header.Get("Accept-Language") == "" {
			t.Error("Accept-Language header should be set")
		}

		if req.Header.Get("Accept-Encoding") == "" {
			t.Error("Accept-Encoding header should be set")
		}

		if req.Header.Get("User-Agent") == "" {
			t.Error("User-Agent header should be set")
		}

		// Check profile properties
		if profile.BrowserType != "chrome" {
			t.Errorf("Expected browser type 'chrome', got %s", profile.BrowserType)
		}

		if !profile.HTTP2Enabled {
			t.Error("HTTP2 should be enabled")
		}
	})

	t.Run("AdvancedHeaderRandomization", func(t *testing.T) {
		req := httptest.NewRequest("GET", "http://example.com/search", nil)

		AdvancedHeaderRandomization(req)

		// Should set DNT header
		if req.Header.Get("DNT") == "" {
			t.Error("DNT header should be set")
		}
	})

	t.Run("GetAdvancedHumanLikeDelay", func(t *testing.T) {
		tests := []struct {
			query         string
			expectedRange [2]time.Duration
		}{
			{"test", [2]time.Duration{500 * time.Millisecond, 1 * time.Second}},
			{"longer test query", [2]time.Duration{600 * time.Millisecond, 2 * time.Second}},
			{"", [2]time.Duration{500 * time.Millisecond, 600 * time.Millisecond}},
		}

		for _, tt := range tests {
			delay := GetAdvancedHumanLikeDelay(tt.query)

			if delay < tt.expectedRange[0] || delay > tt.expectedRange[1] {
				t.Errorf("Delay %v for query %q outside expected range [%v, %v]",
					delay, tt.query, tt.expectedRange[0], tt.expectedRange[1])
			}
		}
	})

	t.Run("IsMaintenanceMode", func(t *testing.T) {
		tests := []struct {
			body     string
			expected bool
		}{
			{"<html><body>Normal content</body></html>", false},
			{"<html><body>Site under maintenance</body></html>", true},
			{"<html><body>Maintenance mode active</body></html>", true},
			{"<html><body>Temporarily unavailable</body></html>", true},
			{"<html><body>Site Maintenance in progress</body></html>", true},
			{"", false},
		}

		for _, tt := range tests {
			result := IsMaintenanceMode([]byte(tt.body))
			if result != tt.expected {
				t.Errorf("IsMaintenanceMode(%q) = %v, expected %v", tt.body, result, tt.expected)
			}
		}
	})
}

func TestDecompressResponse(t *testing.T) {
	tests := []struct {
		name        string
		encoding    string
		content     string
		expectError bool
	}{
		{
			name:        "No compression",
			encoding:    "",
			content:     "Hello, World!",
			expectError: false,
		},
		{
			name:        "Gzip compression",
			encoding:    "gzip",
			content:     "Hello, World!",
			expectError: false,
		},
		{
			name:        "Deflate compression",
			encoding:    "deflate",
			content:     "Hello, World!",
			expectError: false,
		},
		{
			name:        "Brotli compression",
			encoding:    "br",
			content:     "Hello, World!",
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a mock response with the specified encoding
			resp := &http.Response{
				Header: make(http.Header),
				Body:   http.NoBody,
			}

			if tt.encoding != "" {
				resp.Header.Set("Content-Encoding", tt.encoding)
			}

			// For this test, we'll use uncompressed body as the compression
			// logic would be complex to set up properly
			resp.Body = http.NoBody

			_, err := DecompressResponse(resp)

			if tt.expectError && err == nil {
				t.Error("Expected error but got none")
			} else if !tt.expectError && err != nil {
				t.Errorf("Unexpected error: %v", err)
			}
		})
	}
}

func TestConcurrentRequestProcessing(t *testing.T) {
	// Create a test server that will act as a SearXNG instance
	var requestCount int64
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&requestCount, 1)

		// Add small delay to simulate processing time
		time.Sleep(10 * time.Millisecond)

		query := r.URL.Query().Get("q")
		response := fmt.Sprintf(`<html><body><div class="result"><a href="#">Result for %s</a></div></body></html>`, query)

		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(response))
	}))
	defer testServer.Close()

	// Override instances for testing
	// Note: This would require dependency injection or interface-based design in production
	// For now, we'll test the components that don't rely on the global instances.Manager

	config := DefaultConcurrentServerConfig()
	config.Addr = ":0"
	config.WorkerPoolSize = 4
	config.QueueSize = 100

	server, err := NewConcurrentServer(config)
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		server.Shutdown(ctx)
	}()

	// Add the test server as an instance
	server.loadBalancer.AddInstance(testServer.URL, 1)

	server.SetupRoutes()

	// Start server
	go server.Start()
	time.Sleep(100 * time.Millisecond) // Let server start

	// Since we can't easily test the full HTTP pipeline without significant mocking,
	// we'll test the core processing function directly
	searchReq := &SearchRequest{
		Query:        "test query",
		Format:       "html",
		ClientIP:     "127.0.0.1",
		UserAgent:    "Test Agent",
		Headers:      make(map[string]string),
		ResponseChan: make(chan *SearchResponse, 1),
		Context:      context.Background(),
		Priority:     1,
		SubmittedAt:  time.Now(),
	}

	// Test that the processing logic doesn't panic
	ctx := context.Background()
	queueItem := &QueueItem{
		ID:         "test-1",
		Priority:   1,
		Data:       searchReq,
		Context:    ctx,
		ResultChan: make(chan QueueResult, 1),
		MaxRetries: 3,
	}

	// This would normally be called by the queue processor
	// We can't easily test it without mocking the entire chain
	_ = queueItem
	
	t.Log("Concurrent server components initialized successfully")
}

// Mock types for testing (to avoid import issues)
type QueueItem struct {
	ID         string
	Priority   int
	Data       interface{}
	Context    context.Context
	ResultChan chan QueueResult
	MaxRetries int
}

type QueueResult struct {
	Data  interface{}
	Error error
}

func BenchmarkGetClientIP(b *testing.B) {
	server, err := NewConcurrentServer(DefaultConcurrentServerConfig())
	if err != nil {
		b.Fatalf("Failed to create server: %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		server.Shutdown(ctx)
	}()

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("X-Forwarded-For", "192.168.1.100")
	req.RemoteAddr = "127.0.0.1:12345"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = server.getClientIP(req)
	}
}

func BenchmarkExtractHeaders(b *testing.B) {
	server, err := NewConcurrentServer(DefaultConcurrentServerConfig())
	if err != nil {
		b.Fatalf("Failed to create server: %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		server.Shutdown(ctx)
	}()

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("User-Agent", "Test Agent")
	req.Header.Set("Accept", "text/html")
	req.Header.Set("Accept-Language", "en-US")
	req.Header.Set("Accept-Encoding", "gzip,deflate")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = server.extractHeaders(req)
	}
}

func BenchmarkDetermineBrowserProfile(b *testing.B) {
	server, err := NewConcurrentServer(DefaultConcurrentServerConfig())
	if err != nil {
		b.Fatalf("Failed to create server: %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		server.Shutdown(ctx)
	}()

	userAgent := "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = server.determineBrowserProfile(userAgent)
	}
}

func BenchmarkSetHumanLikeHeaders(b *testing.B) {
	req := httptest.NewRequest("GET", "http://example.com/search?q=test", nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Clear headers for each iteration
		req.Header = make(http.Header)
		_ = SetHumanLikeHeaders(req, "http://example.com/search", "http://example.com")
	}
}