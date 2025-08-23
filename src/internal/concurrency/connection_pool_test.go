package concurrency

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestConnectionPoolCreation(t *testing.T) {
	tests := []struct {
		name   string
		config ConnectionPoolConfig
		want   ConnectionPoolConfig
	}{
		{
			name: "default configuration",
			config: ConnectionPoolConfig{
				MaxConnsPerInstance:     0,
				MaxIdleConnsPerInstance: 0,
				IdleConnTimeout:         0,
				TLSHandshakeTimeout:     0,
				ResponseHeaderTimeout:   0,
				DialTimeout:             0,
				KeepAlive:               0,
				MaxLifetime:             0,
			},
			want: ConnectionPoolConfig{
				MaxConnsPerInstance:     20,
				MaxIdleConnsPerInstance: 10,
				IdleConnTimeout:         90 * time.Second,
				TLSHandshakeTimeout:     10 * time.Second,
				ResponseHeaderTimeout:   30 * time.Second,
				DialTimeout:             10 * time.Second,
				KeepAlive:               30 * time.Second,
				MaxLifetime:             10 * time.Minute,
			},
		},
		{
			name: "custom configuration",
			config: ConnectionPoolConfig{
				MaxConnsPerInstance:     50,
				MaxIdleConnsPerInstance: 25,
				IdleConnTimeout:         120 * time.Second,
				TLSHandshakeTimeout:     15 * time.Second,
				ResponseHeaderTimeout:   45 * time.Second,
				DialTimeout:             15 * time.Second,
				KeepAlive:               45 * time.Second,
				MaxLifetime:             20 * time.Minute,
			},
			want: ConnectionPoolConfig{
				MaxConnsPerInstance:     50,
				MaxIdleConnsPerInstance: 25,
				IdleConnTimeout:         120 * time.Second,
				TLSHandshakeTimeout:     15 * time.Second,
				ResponseHeaderTimeout:   45 * time.Second,
				DialTimeout:             15 * time.Second,
				KeepAlive:               45 * time.Second,
				MaxLifetime:             20 * time.Minute,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cp := NewConnectionPool(tt.config)
			defer cp.Close()

			if cp.config.MaxConnsPerInstance != tt.want.MaxConnsPerInstance {
				t.Errorf("MaxConnsPerInstance = %d, want %d", cp.config.MaxConnsPerInstance, tt.want.MaxConnsPerInstance)
			}
			if cp.config.MaxIdleConnsPerInstance != tt.want.MaxIdleConnsPerInstance {
				t.Errorf("MaxIdleConnsPerInstance = %d, want %d", cp.config.MaxIdleConnsPerInstance, tt.want.MaxIdleConnsPerInstance)
			}
			if cp.config.IdleConnTimeout != tt.want.IdleConnTimeout {
				t.Errorf("IdleConnTimeout = %v, want %v", cp.config.IdleConnTimeout, tt.want.IdleConnTimeout)
			}
			if cp.config.TLSHandshakeTimeout != tt.want.TLSHandshakeTimeout {
				t.Errorf("TLSHandshakeTimeout = %v, want %v", cp.config.TLSHandshakeTimeout, tt.want.TLSHandshakeTimeout)
			}
		})
	}
}

func TestConnectionPoolGetClient(t *testing.T) {
	cp := NewConnectionPool(ConnectionPoolConfig{})
	defer cp.Close()

	instance := "http://example.com"
	profile := BrowserProfile{
		UserAgent:       "test-agent",
		BrowserType:     "chrome",
		Platform:        "linux",
		HTTP2Enabled:    true,
		CompressionType: "gzip,deflate,br",
	}

	// Get client
	client1 := cp.GetClient(instance, profile)
	if client1 == nil {
		t.Fatal("GetClient returned nil")
	}

	// Get client again - should be the same instance (reuse)
	client2 := cp.GetClient(instance, profile)
	if client1 != client2 {
		t.Error("Expected same client instance for same profile")
	}

	// Different profile should get different client
	differentProfile := profile
	differentProfile.BrowserType = "firefox"
	
	client3 := cp.GetClient(instance, differentProfile)
	if client1 == client3 {
		t.Error("Expected different client for different profile")
	}

	// Check connection reuse stats
	stats := cp.GetStats()
	if stats.ConnectionReuse == 0 {
		t.Error("Expected connection reuse to be tracked")
	}
}

func TestConnectionPoolBrowserProfiles(t *testing.T) {
	cp := NewConnectionPool(ConnectionPoolConfig{})
	defer cp.Close()

	instance := "http://example.com"
	
	profiles := []BrowserProfile{
		{
			BrowserType:     "chrome",
			Platform:        "linux",
			HTTP2Enabled:    true,
			CompressionType: "gzip,deflate,br",
		},
		{
			BrowserType:     "firefox",
			Platform:        "linux",
			HTTP2Enabled:    true,
			CompressionType: "gzip,deflate,br",
		},
		{
			BrowserType:     "safari",
			Platform:        "macos",
			HTTP2Enabled:    false,
			CompressionType: "gzip,deflate",
		},
	}

	clients := make([]*http.Client, len(profiles))
	for i, profile := range profiles {
		client := cp.GetClient(instance, profile)
		if client == nil {
			t.Fatalf("GetClient returned nil for profile %d", i)
		}
		clients[i] = client
	}

	// All clients should be different
	for i := 0; i < len(clients); i++ {
		for j := i + 1; j < len(clients); j++ {
			if clients[i] == clients[j] {
				t.Errorf("Clients %d and %d should be different but are the same", i, j)
			}
		}
	}
}

func TestConnectionPoolHTTPRequests(t *testing.T) {
	// Create test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("test response"))
	}))
	defer server.Close()

	cp := NewConnectionPool(ConnectionPoolConfig{
		MaxConnsPerInstance:     5,
		MaxIdleConnsPerInstance: 3,
	})
	defer cp.Close()

	profile := BrowserProfile{
		BrowserType:  "chrome",
		Platform:     "linux",
		HTTP2Enabled: false, // Use HTTP/1.1 for test server
	}

	client := cp.GetClient(server.URL, profile)

	// Make requests
	for i := 0; i < 5; i++ {
		req, err := http.NewRequest("GET", server.URL, nil)
		if err != nil {
			t.Fatalf("Failed to create request: %v", err)
		}

		resp, err := client.Do(req)
		if err != nil {
			t.Errorf("Request %d failed: %v", i, err)
			continue
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			t.Errorf("Failed to read response body: %v", err)
			continue
		}

		if string(body) != "test response" {
			t.Errorf("Unexpected response body: %s", body)
		}
	}

	// Check stats
	stats := cp.GetStats()
	if stats.TotalConnections == 0 {
		t.Error("Expected some total connections")
	}
}

func TestConnectionPoolMetrics(t *testing.T) {
	// Create test server with delay
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(50 * time.Millisecond) // Simulate processing time
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("test response"))
	}))
	defer server.Close()

	cp := NewConnectionPool(ConnectionPoolConfig{
		MaxConnsPerInstance: 10,
	})
	defer cp.Close()

	profile := BrowserProfile{
		BrowserType:  "chrome",
		Platform:     "linux",
		HTTP2Enabled: false,
	}

	client := cp.GetClient(server.URL, profile)

	// Make concurrent requests
	const numRequests = 10
	var wg sync.WaitGroup

	for i := 0; i < numRequests; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			req, err := http.NewRequest("GET", server.URL, nil)
			if err != nil {
				t.Errorf("Failed to create request %d: %v", id, err)
				return
			}

			resp, err := client.Do(req)
			if err != nil {
				t.Errorf("Request %d failed: %v", id, err)
				return
			}
			resp.Body.Close()
		}(i)
	}

	wg.Wait()

	// Check metrics
	stats := cp.GetStats()
	if stats.TotalConnections == 0 {
		t.Error("Expected some total connections to be recorded")
	}
	
	if stats.ConnectionTime == 0 {
		t.Error("Expected connection time to be recorded")
	}
}

func TestConnectionPoolConcurrentAccess(t *testing.T) {
	cp := NewConnectionPool(ConnectionPoolConfig{
		MaxConnsPerInstance: 20,
	})
	defer cp.Close()

	instances := []string{
		"http://server1.example.com",
		"http://server2.example.com", 
		"http://server3.example.com",
	}

	profiles := []BrowserProfile{
		{BrowserType: "chrome", Platform: "linux"},
		{BrowserType: "firefox", Platform: "linux"},
		{BrowserType: "safari", Platform: "macos"},
	}

	const numGoroutines = 20
	const requestsPerGoroutine = 10
	var wg sync.WaitGroup

	// Concurrent client retrieval
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			for j := 0; j < requestsPerGoroutine; j++ {
				instance := instances[j%len(instances)]
				profile := profiles[j%len(profiles)]

				client := cp.GetClient(instance, profile)
				if client == nil {
					t.Errorf("GetClient returned nil for goroutine %d, request %d", id, j)
				}

				// Small delay to increase contention
				time.Sleep(time.Microsecond)
			}
		}(i)
	}

	wg.Wait()

	// Check that we have the expected number of instance pools
	cp.mu.RLock()
	poolCount := len(cp.pools)
	cp.mu.RUnlock()

	if poolCount != len(instances) {
		t.Errorf("Expected %d instance pools, got %d", len(instances), poolCount)
	}

	// Check stats
	stats := cp.GetStats()
	if stats.TotalConnections == 0 {
		t.Error("Expected some connections to be created")
	}
	if stats.ConnectionReuse == 0 {
		t.Error("Expected some connection reuse")
	}
}

func TestConnectionPoolCleanup(t *testing.T) {
	cp := NewConnectionPool(ConnectionPoolConfig{})
	defer cp.Close()

	instance := "http://example.com"
	profile := BrowserProfile{BrowserType: "chrome"}

	// Get a client to create instance pool
	client := cp.GetClient(instance, profile)
	if client == nil {
		t.Fatal("GetClient returned nil")
	}

	// Check that instance pool was created
	cp.mu.RLock()
	initialPoolCount := len(cp.pools)
	cp.mu.RUnlock()

	if initialPoolCount != 1 {
		t.Errorf("Expected 1 instance pool, got %d", initialPoolCount)
	}

	// Simulate old last used time
	cp.mu.Lock()
	if pool, exists := cp.pools[instance]; exists {
		pool.lastUsed = time.Now().Add(-15 * time.Minute) // Make it old
	}
	cp.mu.Unlock()

	// Run cleanup
	cp.cleanup()

	// Check that pool was cleaned up
	cp.mu.RLock()
	finalPoolCount := len(cp.pools)
	cp.mu.RUnlock()

	if finalPoolCount != 0 {
		t.Errorf("Expected 0 instance pools after cleanup, got %d", finalPoolCount)
	}
}

func TestConnectionPoolClose(t *testing.T) {
	cp := NewConnectionPool(ConnectionPoolConfig{})

	instances := []string{
		"http://server1.example.com",
		"http://server2.example.com",
	}

	profile := BrowserProfile{BrowserType: "chrome"}

	// Create clients for multiple instances
	for _, instance := range instances {
		client := cp.GetClient(instance, profile)
		if client == nil {
			t.Fatalf("GetClient returned nil for %s", instance)
		}
	}

	// Check that pools were created
	cp.mu.RLock()
	poolCount := len(cp.pools)
	cp.mu.RUnlock()

	if poolCount != len(instances) {
		t.Errorf("Expected %d pools, got %d", len(instances), poolCount)
	}

	// Close connection pool
	err := cp.Close()
	if err != nil {
		t.Errorf("Close failed: %v", err)
	}

	// Check that pools were cleaned up
	cp.mu.RLock()
	finalPoolCount := len(cp.pools)
	cp.mu.RUnlock()

	if finalPoolCount != 0 {
		t.Errorf("Expected 0 pools after close, got %d", finalPoolCount)
	}
}

func TestConnectionPoolTrackedConn(t *testing.T) {
	// Create a mock connection
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	metrics := &ConnectionMetrics{}
	
	// Create tracked connection
	trackedConn := &trackedConn{
		Conn:    client,
		metrics: metrics,
	}

	// Initially should have 1 active connection (set by dialer)
	atomic.StoreInt64(&metrics.ActiveConnections, 1)

	if atomic.LoadInt64(&metrics.ActiveConnections) != 1 {
		t.Errorf("Expected 1 active connection, got %d", atomic.LoadInt64(&metrics.ActiveConnections))
	}

	// Close tracked connection
	err := trackedConn.Close()
	if err != nil {
		t.Errorf("Close failed: %v", err)
	}

	// Active connections should decrease
	if atomic.LoadInt64(&metrics.ActiveConnections) != 0 {
		t.Errorf("Expected 0 active connections after close, got %d", atomic.LoadInt64(&metrics.ActiveConnections))
	}

	// Double close should not decrease again
	trackedConn.Close()
	if atomic.LoadInt64(&metrics.ActiveConnections) != 0 {
		t.Errorf("Expected 0 active connections after double close, got %d", atomic.LoadInt64(&metrics.ActiveConnections))
	}
}

func TestConnectionPoolBrowserSpecificTimeouts(t *testing.T) {
	cp := NewConnectionPool(ConnectionPoolConfig{})
	defer cp.Close()

	instance := "http://example.com"
	
	browsers := []struct {
		browserType     string
		expectedTimeout time.Duration
	}{
		{"chrome", 30 * time.Second},
		{"firefox", 60 * time.Second},
		{"safari", 45 * time.Second},
		{"unknown", 45 * time.Second},
	}

	for _, browser := range browsers {
		profile := BrowserProfile{
			BrowserType: browser.browserType,
			Platform:    "linux",
		}

		client := cp.GetClient(instance, profile)
		if client == nil {
			t.Fatalf("GetClient returned nil for %s", browser.browserType)
		}

		if client.Timeout != browser.expectedTimeout {
			t.Errorf("Browser %s: expected timeout %v, got %v", 
				browser.browserType, browser.expectedTimeout, client.Timeout)
		}
	}
}

func TestConnectionPoolTLSConfiguration(t *testing.T) {
	cp := NewConnectionPool(ConnectionPoolConfig{})
	defer cp.Close()

	instance := "https://example.com"
	
	browsers := []string{"chrome", "firefox", "safari", "unknown"}

	for _, browserType := range browsers {
		profile := BrowserProfile{
			BrowserType: browserType,
			Platform:    "linux",
		}

		client := cp.GetClient(instance, profile)
		if client == nil {
			t.Fatalf("GetClient returned nil for %s", browserType)
		}

		transport, ok := client.Transport.(*http.Transport)
		if !ok {
			t.Fatalf("Expected *http.Transport, got %T", client.Transport)
		}

		if transport.TLSClientConfig == nil {
			t.Errorf("Browser %s: TLS config should not be nil", browserType)
			continue
		}

		// Check TLS version requirements
		if transport.TLSClientConfig.MinVersion < 0x0303 { // TLS 1.2
			t.Errorf("Browser %s: MinVersion should be at least TLS 1.2", browserType)
		}

		// Chrome and Firefox should have specific cipher suites
		if browserType == "chrome" || browserType == "firefox" {
			if len(transport.TLSClientConfig.CipherSuites) == 0 {
				t.Errorf("Browser %s: should have specific cipher suites", browserType)
			}
		}
	}
}

func TestConnectionPoolRedirectHandling(t *testing.T) {
	cp := NewConnectionPool(ConnectionPoolConfig{})
	defer cp.Close()

	instance := "http://example.com"
	profile := BrowserProfile{BrowserType: "chrome"}

	client := cp.GetClient(instance, profile)
	if client == nil {
		t.Fatal("GetClient returned nil")
	}

	if client.CheckRedirect == nil {
		t.Error("CheckRedirect should not be nil")
	}

	// Test redirect limit
	var via []*http.Request
	for i := 0; i < 4; i++ {
		req, _ := http.NewRequest("GET", "http://example.com", nil)
		via = append(via, req)
	}

	// Should stop after 3 redirects
	err := client.CheckRedirect(nil, via[:3])
	if err != nil {
		t.Errorf("Should allow 3 redirects, got error: %v", err)
	}

	err = client.CheckRedirect(nil, via)
	if err != http.ErrUseLastResponse {
		t.Errorf("Should stop after 3 redirects, got error: %v", err)
	}
}

func TestConnectionPoolStats(t *testing.T) {
	cp := NewConnectionPool(ConnectionPoolConfig{})
	defer cp.Close()

	// Initial stats
	stats := cp.GetStats()
	if stats.TotalConnections != 0 {
		t.Error("Initial total connections should be 0")
	}
	if stats.ActiveConnections != 0 {
		t.Error("Initial active connections should be 0")
	}
	if stats.ConnectionReuse != 0 {
		t.Error("Initial connection reuse should be 0")
	}

	instance := "http://example.com"
	profile := BrowserProfile{BrowserType: "chrome"}

	// Get client multiple times to trigger reuse
	client1 := cp.GetClient(instance, profile)
	client2 := cp.GetClient(instance, profile)

	if client1 != client2 {
		t.Error("Should reuse same client")
	}

	stats = cp.GetStats()
	if stats.ConnectionReuse == 0 {
		t.Error("Should have recorded connection reuse")
	}

	// Idle connections should be calculated correctly
	expectedIdle := stats.TotalConnections - stats.ActiveConnections
	if stats.IdleConnections != expectedIdle {
		t.Errorf("Expected %d idle connections, got %d", expectedIdle, stats.IdleConnections)
	}
}

func BenchmarkConnectionPoolGetClient(b *testing.B) {
	cp := NewConnectionPool(ConnectionPoolConfig{})
	defer cp.Close()

	instance := "http://example.com"
	profile := BrowserProfile{
		BrowserType: "chrome",
		Platform:    "linux",
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			client := cp.GetClient(instance, profile)
			_ = client // Use client to prevent optimization
		}
	})
}

func BenchmarkConnectionPoolMultipleInstances(b *testing.B) {
	cp := NewConnectionPool(ConnectionPoolConfig{})
	defer cp.Close()

	instances := make([]string, 100)
	for i := range instances {
		instances[i] = fmt.Sprintf("http://server%d.example.com", i)
	}

	profile := BrowserProfile{
		BrowserType: "chrome",
		Platform:    "linux",
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			instance := instances[i%len(instances)]
			client := cp.GetClient(instance, profile)
			_ = client
			i++
		}
	})
}

func BenchmarkConnectionPoolMultipleProfiles(b *testing.B) {
	cp := NewConnectionPool(ConnectionPoolConfig{})
	defer cp.Close()

	instance := "http://example.com"
	profiles := []BrowserProfile{
		{BrowserType: "chrome", Platform: "linux"},
		{BrowserType: "firefox", Platform: "linux"},
		{BrowserType: "safari", Platform: "macos"},
		{BrowserType: "chrome", Platform: "windows"},
		{BrowserType: "firefox", Platform: "windows"},
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			profile := profiles[i%len(profiles)]
			client := cp.GetClient(instance, profile)
			_ = client
			i++
		}
	})
}

func BenchmarkConnectionPoolConcurrentDifferentInstances(b *testing.B) {
	cp := NewConnectionPool(ConnectionPoolConfig{
		MaxConnsPerInstance: 50,
	})
	defer cp.Close()

	numInstances := 10
	instances := make([]string, numInstances)
	for i := range instances {
		instances[i] = fmt.Sprintf("http://server%d.example.com", i)
	}

	profile := BrowserProfile{BrowserType: "chrome"}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			instance := instances[i%numInstances]
			client := cp.GetClient(instance, profile)
			_ = client
			i++
		}
	})
}