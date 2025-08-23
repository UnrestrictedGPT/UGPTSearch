package concurrency

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestMetricsCollectorCreation(t *testing.T) {
	tests := []struct {
		name   string
		config MetricsConfig
		want   MetricsConfig
	}{
		{
			name: "default configuration",
			config: MetricsConfig{
				CollectionInterval: 0,
				RetentionPeriod:    0,
				MaxHistorySize:     0,
				HTTPLatencyBuffer:  0,
			},
			want: MetricsConfig{
				CollectionInterval: 10 * time.Second,
				RetentionPeriod:    1 * time.Hour,
				MaxHistorySize:     360,
				HTTPLatencyBuffer:  1000,
			},
		},
		{
			name: "custom configuration",
			config: MetricsConfig{
				CollectionInterval: 5 * time.Second,
				RetentionPeriod:    30 * time.Minute,
				MaxHistorySize:     100,
				HTTPLatencyBuffer:  500,
			},
			want: MetricsConfig{
				CollectionInterval: 5 * time.Second,
				RetentionPeriod:    30 * time.Minute,
				MaxHistorySize:     100,
				HTTPLatencyBuffer:  500,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mc := NewMetricsCollector(tt.config)
			defer mc.Close()

			if mc.collectionInterval != tt.want.CollectionInterval {
				t.Errorf("collectionInterval = %v, want %v", mc.collectionInterval, tt.want.CollectionInterval)
			}
			if mc.retentionPeriod != tt.want.RetentionPeriod {
				t.Errorf("retentionPeriod = %v, want %v", mc.retentionPeriod, tt.want.RetentionPeriod)
			}
			if mc.maxHistorySize != tt.want.MaxHistorySize {
				t.Errorf("maxHistorySize = %d, want %d", mc.maxHistorySize, tt.want.MaxHistorySize)
			}
			if mc.httpMetrics.bufferSize != tt.want.HTTPLatencyBuffer {
				t.Errorf("HTTPLatencyBuffer = %d, want %d", mc.httpMetrics.bufferSize, tt.want.HTTPLatencyBuffer)
			}
		})
	}
}

func TestHistogramMetric(t *testing.T) {
	h := NewHistogramMetric()

	// Test recording values
	values := []float64{1.5, 15, 75, 150, 750, 2500}
	for _, value := range values {
		h.Record(value)
	}

	if h.Count != int64(len(values)) {
		t.Errorf("Expected count %d, got %d", len(values), h.Count)
	}

	expectedSum := 1.5 + 15 + 75 + 150 + 750 + 2500
	if h.Sum != expectedSum {
		t.Errorf("Expected sum %f, got %f", expectedSum, h.Sum)
	}

	// Check bucket distribution
	if h.Buckets[5] != 2 {     // 1.5 and 15 should be in bucket 5
		t.Errorf("Expected 2 values in bucket 5, got %d", h.Buckets[5])
	}
	if h.Buckets[100] != 3 {   // 75 should also be in bucket 100 (cumulative)
		t.Errorf("Expected 3 values in bucket 100, got %d", h.Buckets[100])
	}
	if h.Buckets[1000] != 4 {  // 150 should be in bucket 1000
		t.Errorf("Expected 4 values in bucket 1000, got %d", h.Buckets[1000])
	}
}

func TestMetricsCollectorHTTPRequests(t *testing.T) {
	mc := NewMetricsCollector(MetricsConfig{
		CollectionInterval: 1 * time.Second,
		HTTPLatencyBuffer:  10,
	})
	defer mc.Close()

	// Record some HTTP requests
	testCases := []struct {
		statusCode   int
		latency      time.Duration
		responseSize int64
		errorType    string
	}{
		{200, 50 * time.Millisecond, 1024, ""},
		{200, 75 * time.Millisecond, 2048, ""},
		{404, 25 * time.Millisecond, 512, "not_found"},
		{500, 100 * time.Millisecond, 256, "server_error"},
		{200, 60 * time.Millisecond, 1536, ""},
	}

	for _, tc := range testCases {
		mc.RecordHTTPRequest(tc.statusCode, tc.latency, tc.responseSize, tc.errorType)
	}

	// Wait a moment for calculations
	time.Sleep(10 * time.Millisecond)

	// Get HTTP metrics
	httpMetrics := mc.getHTTPMetrics()

	// Check basic counters
	if httpMetrics.TotalRequests != int64(len(testCases)) {
		t.Errorf("Expected %d total requests, got %d", len(testCases), httpMetrics.TotalRequests)
	}
	if httpMetrics.FailedRequests != 2 {
		t.Errorf("Expected 2 failed requests, got %d", httpMetrics.FailedRequests)
	}
	if httpMetrics.CompletedRequests != int64(len(testCases)) {
		t.Errorf("Expected %d completed requests, got %d", len(testCases), httpMetrics.CompletedRequests)
	}

	// Check status codes
	if httpMetrics.StatusCodes[200] != 3 {
		t.Errorf("Expected 3 requests with status 200, got %d", httpMetrics.StatusCodes[200])
	}
	if httpMetrics.StatusCodes[404] != 1 {
		t.Errorf("Expected 1 request with status 404, got %d", httpMetrics.StatusCodes[404])
	}
	if httpMetrics.StatusCodes[500] != 1 {
		t.Errorf("Expected 1 request with status 500, got %d", httpMetrics.StatusCodes[500])
	}

	// Check error types
	if httpMetrics.ErrorsByType["not_found"] != 1 {
		t.Errorf("Expected 1 not_found error, got %d", httpMetrics.ErrorsByType["not_found"])
	}
	if httpMetrics.ErrorsByType["server_error"] != 1 {
		t.Errorf("Expected 1 server_error error, got %d", httpMetrics.ErrorsByType["server_error"])
	}

	// Check latency calculations
	if httpMetrics.AverageLatency == 0 {
		t.Error("Average latency should not be 0")
	}
	if httpMetrics.P95Latency == 0 {
		t.Error("P95 latency should not be 0")
	}
	if httpMetrics.P99Latency == 0 {
		t.Error("P99 latency should not be 0")
	}

	// Check histograms
	if httpMetrics.ResponseSizes.Count != int64(len(testCases)) {
		t.Errorf("Expected %d response size records, got %d", len(testCases), httpMetrics.ResponseSizes.Count)
	}
	if httpMetrics.RequestLatencies.Count != int64(len(testCases)) {
		t.Errorf("Expected %d latency records, got %d", len(testCases), httpMetrics.RequestLatencies.Count)
	}
}

func TestMetricsCollectorActiveRequests(t *testing.T) {
	mc := NewMetricsCollector(MetricsConfig{})
	defer mc.Close()

	// Start some requests
	mc.StartHTTPRequest()
	mc.StartHTTPRequest()
	mc.StartHTTPRequest()

	httpMetrics := mc.getHTTPMetrics()
	if httpMetrics.ActiveRequests != 3 {
		t.Errorf("Expected 3 active requests, got %d", httpMetrics.ActiveRequests)
	}

	// End some requests
	mc.EndHTTPRequest()
	mc.EndHTTPRequest()

	httpMetrics = mc.getHTTPMetrics()
	if httpMetrics.ActiveRequests != 1 {
		t.Errorf("Expected 1 active request after ending 2, got %d", httpMetrics.ActiveRequests)
	}

	// End remaining request
	mc.EndHTTPRequest()

	httpMetrics = mc.getHTTPMetrics()
	if httpMetrics.ActiveRequests != 0 {
		t.Errorf("Expected 0 active requests, got %d", httpMetrics.ActiveRequests)
	}
}

func TestMetricsCollectorCustomMetrics(t *testing.T) {
	mc := NewMetricsCollector(MetricsConfig{})
	defer mc.Close()

	// Set custom metrics
	labels := map[string]string{"service": "test", "region": "us-west"}
	mc.SetCustomMetric("test_counter", "counter", "Test counter metric", 42, labels)
	mc.SetCustomMetric("test_gauge", "gauge", "Test gauge metric", 100, nil)

	// Increment counter
	mc.IncrementCustomMetric("test_counter", 10)
	mc.IncrementCustomMetric("test_counter", 5)

	// Get custom metrics
	customMetrics := mc.getCustomMetrics()

	// Check counter
	counterMetric, exists := customMetrics["test_counter"]
	if !exists {
		t.Fatal("test_counter metric not found")
	}
	if counterMetric.Value != 57 { // 42 + 10 + 5
		t.Errorf("Expected counter value 57, got %d", counterMetric.Value)
	}
	if counterMetric.Type != "counter" {
		t.Errorf("Expected counter type, got %s", counterMetric.Type)
	}
	if len(counterMetric.Labels) != 2 {
		t.Errorf("Expected 2 labels, got %d", len(counterMetric.Labels))
	}

	// Check gauge
	gaugeMetric, exists := customMetrics["test_gauge"]
	if !exists {
		t.Fatal("test_gauge metric not found")
	}
	if gaugeMetric.Value != 100 {
		t.Errorf("Expected gauge value 100, got %d", gaugeMetric.Value)
	}
	if gaugeMetric.Type != "gauge" {
		t.Errorf("Expected gauge type, got %s", gaugeMetric.Type)
	}

	// Increment non-existent metric (should not crash)
	mc.IncrementCustomMetric("non_existent", 1)
	customMetrics = mc.getCustomMetrics()
	if _, exists := customMetrics["non_existent"]; exists {
		t.Error("Non-existent metric should not be created by increment")
	}
}

func TestMetricsCollectorComponentRegistration(t *testing.T) {
	mc := NewMetricsCollector(MetricsConfig{})
	defer mc.Close()

	// Create components
	wp := NewWorkerPool(WorkerPoolConfig{Workers: 2, QueueSize: 10})
	wp.Start()
	defer wp.Stop(1 * time.Second)

	cp := NewConnectionPool(ConnectionPoolConfig{})
	defer cp.Close()

	rl := NewRateLimiter(RateLimiterConfig{})
	defer rl.Close()

	cbm := NewCircuitBreakerManager(CircuitBreakerConfig{})
	defer cbm.Close()

	lb := NewLoadBalancer(LoadBalancerConfig{Strategy: RoundRobin})
	defer lb.Close()

	processor := func(ctx context.Context, item *QueueItem) (interface{}, error) {
		return nil, nil
	}
	rq, _ := NewRequestQueue(QueueConfig{
		MaxSize: 10,
		WorkerPool: wp,
		Processor: processor,
	})
	rq.Start()
	defer rq.Stop(1 * time.Second)

	// Register components
	mc.RegisterComponents(wp, cp, rl, cbm, rq, lb)

	// Get snapshot
	snapshot := mc.GetCurrentSnapshot()

	// Check that component stats are included
	if snapshot.WorkerPool == nil {
		t.Error("WorkerPool stats should be included")
	}
	if snapshot.ConnectionPool == nil {
		t.Error("ConnectionPool stats should be included")
	}
	if snapshot.RateLimiter == nil {
		t.Error("RateLimiter stats should be included")
	}
	if snapshot.CircuitBreaker == nil {
		t.Error("CircuitBreaker stats should be included")
	}
	if snapshot.RequestQueue == nil {
		t.Error("RequestQueue stats should be included")
	}
	if snapshot.LoadBalancer == nil {
		t.Error("LoadBalancer stats should be included")
	}
	if snapshot.HTTP == nil {
		t.Error("HTTP stats should be included")
	}
	if snapshot.SystemHealth == nil {
		t.Error("SystemHealth should be included")
	}
}

func TestMetricsCollectorSystemHealth(t *testing.T) {
	mc := NewMetricsCollector(MetricsConfig{})
	defer mc.Close()

	// Create and register a worker pool
	wp := NewWorkerPool(WorkerPoolConfig{Workers: 4, QueueSize: 100})
	wp.Start()
	defer wp.Stop(1 * time.Second)

	mc.RegisterComponents(wp, nil, nil, nil, nil, nil)

	// Record some HTTP requests to generate health data
	mc.RecordHTTPRequest(200, 50*time.Millisecond, 1024, "")
	mc.RecordHTTPRequest(200, 75*time.Millisecond, 2048, "")
	mc.RecordHTTPRequest(500, 200*time.Millisecond, 512, "server_error")

	// Get system health
	snapshot := mc.GetCurrentSnapshot()
	health := snapshot.SystemHealth

	if health == nil {
		t.Fatal("SystemHealth should not be nil")
	}

	// Check overall score
	if health.OverallScore < 0 || health.OverallScore > 1 {
		t.Errorf("OverallScore should be between 0 and 1, got %f", health.OverallScore)
	}

	// Check component health
	if len(health.ComponentHealth) == 0 {
		t.Error("ComponentHealth should have at least one component")
	}

	// Check individual scores
	if health.ThroughputScore < 0 || health.ThroughputScore > 1 {
		t.Errorf("ThroughputScore should be between 0 and 1, got %f", health.ThroughputScore)
	}
	if health.LatencyScore < 0 || health.LatencyScore > 1 {
		t.Errorf("LatencyScore should be between 0 and 1, got %f", health.LatencyScore)
	}
	if health.ErrorRateScore < 0 || health.ErrorRateScore > 1 {
		t.Errorf("ErrorRateScore should be between 0 and 1, got %f", health.ErrorRateScore)
	}

	// Check alerts
	if health.AlertsActive == nil {
		t.Error("AlertsActive should not be nil")
	}

	// Check resource usage
	if health.ResourceUsage == nil {
		t.Error("ResourceUsage should not be nil")
	}
}

func TestMetricsCollectorHealthScoring(t *testing.T) {
	mc := NewMetricsCollector(MetricsConfig{})
	defer mc.Close()

	tests := []struct {
		name              string
		requests          []struct{ status int; latency time.Duration }
		expectedErrorRate float64
	}{
		{
			name: "all successful requests",
			requests: []struct{ status int; latency time.Duration }{
				{200, 50 * time.Millisecond},
				{200, 60 * time.Millisecond},
				{200, 40 * time.Millisecond},
			},
			expectedErrorRate: 1.0, // Perfect score
		},
		{
			name: "high error rate",
			requests: []struct{ status int; latency time.Duration }{
				{200, 50 * time.Millisecond},
				{500, 100 * time.Millisecond},
				{500, 120 * time.Millisecond},
				{500, 90 * time.Millisecond},
			},
			expectedErrorRate: 0.2, // Poor score due to 75% error rate
		},
		{
			name: "mixed results",
			requests: []struct{ status int; latency time.Duration }{
				{200, 50 * time.Millisecond},
				{200, 60 * time.Millisecond},
				{200, 70 * time.Millisecond},
				{200, 80 * time.Millisecond},
				{500, 200 * time.Millisecond}, // 20% error rate
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Reset metrics
			atomic.StoreInt64(&mc.httpMetrics.TotalRequests, 0)
			atomic.StoreInt64(&mc.httpMetrics.FailedRequests, 0)

			// Record requests
			for _, req := range tt.requests {
				errorType := ""
				if req.status >= 500 {
					errorType = "server_error"
				}
				mc.RecordHTTPRequest(req.status, req.latency, 1024, errorType)
			}

			// Calculate scores
			errorScore := mc.calculateErrorRateScore()
			latencyScore := mc.calculateLatencyScore()

			// Error rate score should match expectations
			if tt.expectedErrorRate > 0 && errorScore != tt.expectedErrorRate {
				t.Errorf("Expected error rate score %f, got %f", tt.expectedErrorRate, errorScore)
			}

			// Latency score should be reasonable
			if latencyScore < 0 || latencyScore > 1 {
				t.Errorf("Latency score should be between 0 and 1, got %f", latencyScore)
			}
		})
	}
}

func TestMetricsCollectorHistory(t *testing.T) {
	mc := NewMetricsCollector(MetricsConfig{
		CollectionInterval: 10 * time.Millisecond,
		MaxHistorySize:     5,
	})
	defer mc.Close()

	// Wait for a few collection cycles
	time.Sleep(60 * time.Millisecond)

	// Get recent history
	since := time.Now().Add(-100 * time.Millisecond)
	history := mc.GetHistory(since)

	if len(history) == 0 {
		t.Error("Should have some historical data")
	}

	// Check that entries are in chronological order
	for i := 1; i < len(history); i++ {
		if history[i].Timestamp.Before(history[i-1].Timestamp) {
			t.Error("History entries should be in chronological order")
		}
	}

	// Add more data to test size limiting
	for i := 0; i < 10; i++ {
		mc.RecordHTTPRequest(200, 50*time.Millisecond, 1024, "")
		time.Sleep(15 * time.Millisecond)
	}

	// Check that history size is limited
	mc.historyLock.RLock()
	historySize := len(mc.metricsHistory)
	mc.historyLock.RUnlock()

	if historySize > mc.maxHistorySize {
		t.Errorf("History size %d exceeds maximum %d", historySize, mc.maxHistorySize)
	}
}

func TestMetricsCollectorHTTPHandler(t *testing.T) {
	mc := NewMetricsCollector(MetricsConfig{})
	defer mc.Close()

	// Record some test data
	mc.RecordHTTPRequest(200, 50*time.Millisecond, 1024, "")
	mc.SetCustomMetric("test_metric", "counter", "Test metric", 42, nil)

	tests := []struct {
		path           string
		expectedStatus int
		checkContent   func(t *testing.T, body string)
	}{
		{
			path:           "/metrics",
			expectedStatus: http.StatusOK,
			checkContent: func(t *testing.T, body string) {
				var snapshot SystemSnapshot
				err := json.Unmarshal([]byte(body), &snapshot)
				if err != nil {
					t.Errorf("Failed to unmarshal metrics response: %v", err)
				}
				if snapshot.HTTP == nil {
					t.Error("HTTP metrics should be present")
				}
				if snapshot.SystemHealth == nil {
					t.Error("SystemHealth should be present")
				}
			},
		},
		{
			path:           "/health",
			expectedStatus: http.StatusOK,
			checkContent: func(t *testing.T, body string) {
				var health SystemHealthMetrics
				err := json.Unmarshal([]byte(body), &health)
				if err != nil {
					t.Errorf("Failed to unmarshal health response: %v", err)
				}
				if health.OverallScore < 0 || health.OverallScore > 1 {
					t.Errorf("OverallScore should be between 0 and 1, got %f", health.OverallScore)
				}
			},
		},
		{
			path:           "/metrics/history",
			expectedStatus: http.StatusOK,
			checkContent: func(t *testing.T, body string) {
				var history []SystemSnapshot
				err := json.Unmarshal([]byte(body), &history)
				if err != nil {
					t.Errorf("Failed to unmarshal history response: %v", err)
				}
				// History might be empty in fast tests, so just check it doesn't error
			},
		},
		{
			path:           "/nonexistent",
			expectedStatus: http.StatusNotFound,
			checkContent:   nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			req := httptest.NewRequest("GET", tt.path, nil)
			w := httptest.NewRecorder()

			mc.ServeHTTP(w, req)

			if w.Code != tt.expectedStatus {
				t.Errorf("Expected status %d, got %d", tt.expectedStatus, w.Code)
			}

			if tt.checkContent != nil {
				tt.checkContent(t, w.Body.String())
			}
		})
	}
}

func TestMetricsCollectorConcurrentAccess(t *testing.T) {
	mc := NewMetricsCollector(MetricsConfig{
		HTTPLatencyBuffer: 100,
	})
	defer mc.Close()

	const numGoroutines = 10
	const requestsPerGoroutine = 50
	var wg sync.WaitGroup

	// Concurrent HTTP request recording
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			for j := 0; j < requestsPerGoroutine; j++ {
				status := 200
				if j%10 == 0 {
					status = 500 // 10% error rate
				}

				mc.StartHTTPRequest()
				mc.RecordHTTPRequest(status, time.Duration(j)*time.Millisecond, int64(100+j), "")
				mc.EndHTTPRequest()

				// Set/increment custom metrics
				mc.SetCustomMetric(fmt.Sprintf("metric_%d", id), "counter", "Test metric", int64(j), nil)
				mc.IncrementCustomMetric(fmt.Sprintf("metric_%d", id), 1)

				time.Sleep(time.Microsecond) // Small delay to increase contention
			}
		}(i)
	}

	wg.Wait()

	// Verify results
	snapshot := mc.GetCurrentSnapshot()
	
	expectedTotalRequests := int64(numGoroutines * requestsPerGoroutine)
	if snapshot.HTTP.TotalRequests != expectedTotalRequests {
		t.Errorf("Expected %d total requests, got %d", expectedTotalRequests, snapshot.HTTP.TotalRequests)
	}

	expectedFailedRequests := int64(numGoroutines * (requestsPerGoroutine / 10))
	if snapshot.HTTP.FailedRequests != expectedFailedRequests {
		t.Errorf("Expected %d failed requests, got %d", expectedFailedRequests, snapshot.HTTP.FailedRequests)
	}

	// Check that custom metrics were created
	if len(snapshot.CustomMetrics) != numGoroutines {
		t.Errorf("Expected %d custom metrics, got %d", numGoroutines, len(snapshot.CustomMetrics))
	}

	// Active requests should be 0
	if snapshot.HTTP.ActiveRequests != 0 {
		t.Errorf("Expected 0 active requests, got %d", snapshot.HTTP.ActiveRequests)
	}
}

func TestMetricsCollectorLatencyCalculations(t *testing.T) {
	mc := NewMetricsCollector(MetricsConfig{
		HTTPLatencyBuffer: 10, // Small buffer for predictable testing
	})
	defer mc.Close()

	// Record latencies in ascending order for predictable percentiles
	latencies := []time.Duration{
		10 * time.Millisecond,
		20 * time.Millisecond,
		30 * time.Millisecond,
		40 * time.Millisecond,
		50 * time.Millisecond,
		60 * time.Millisecond,
		70 * time.Millisecond,
		80 * time.Millisecond,
		90 * time.Millisecond,
		100 * time.Millisecond,
	}

	for i, latency := range latencies {
		mc.RecordHTTPRequest(200, latency, 1024, "")
		// Check intermediate calculations
		if i == 4 { // After 5 requests
			httpMetrics := mc.getHTTPMetrics()
			if httpMetrics.AverageLatency == 0 {
				t.Error("Average latency should be calculated")
			}
		}
	}

	httpMetrics := mc.getHTTPMetrics()

	// Check average (should be 55ms for 10-100ms range)
	expectedAvg := 55 * time.Millisecond
	if httpMetrics.AverageLatency != expectedAvg {
		t.Errorf("Expected average latency %v, got %v", expectedAvg, httpMetrics.AverageLatency)
	}

	// P95 should be 90th percentile (95% of 10 = 9.5, so index 9 = 100ms)
	expectedP95 := 100 * time.Millisecond
	if httpMetrics.P95Latency != expectedP95 {
		t.Errorf("Expected P95 latency %v, got %v", expectedP95, httpMetrics.P95Latency)
	}

	// P99 should also be 100ms for this small dataset
	expectedP99 := 100 * time.Millisecond
	if httpMetrics.P99Latency != expectedP99 {
		t.Errorf("Expected P99 latency %v, got %v", expectedP99, httpMetrics.P99Latency)
	}
}

func TestMetricsCollectorClose(t *testing.T) {
	mc := NewMetricsCollector(MetricsConfig{
		CollectionInterval: 50 * time.Millisecond,
	})

	// Add some custom metrics
	mc.SetCustomMetric("test1", "counter", "Test 1", 100, nil)
	mc.SetCustomMetric("test2", "gauge", "Test 2", 200, nil)

	// Verify metrics exist
	snapshot := mc.GetCurrentSnapshot()
	if len(snapshot.CustomMetrics) != 2 {
		t.Errorf("Expected 2 custom metrics before close, got %d", len(snapshot.CustomMetrics))
	}

	// Close collector
	err := mc.Close()
	if err != nil {
		t.Errorf("Close failed: %v", err)
	}

	// Verify cleanup
	mc.customMu.RLock()
	customMetricsCount := len(mc.customMetrics)
	mc.customMu.RUnlock()

	if customMetricsCount != 0 {
		t.Errorf("Expected 0 custom metrics after close, got %d", customMetricsCount)
	}
}

func BenchmarkMetricsCollectorRecordHTTPRequest(b *testing.B) {
	mc := NewMetricsCollector(MetricsConfig{})
	defer mc.Close()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			mc.RecordHTTPRequest(200, 50*time.Millisecond, 1024, "")
		}
	})
}

func BenchmarkMetricsCollectorSetCustomMetric(b *testing.B) {
	mc := NewMetricsCollector(MetricsConfig{})
	defer mc.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		mc.SetCustomMetric(fmt.Sprintf("metric_%d", i%100), "counter", "Test metric", int64(i), nil)
	}
}

func BenchmarkMetricsCollectorIncrementCustomMetric(b *testing.B) {
	mc := NewMetricsCollector(MetricsConfig{})
	defer mc.Close()

	// Pre-create metrics
	for i := 0; i < 100; i++ {
		mc.SetCustomMetric(fmt.Sprintf("metric_%d", i), "counter", "Test metric", 0, nil)
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			mc.IncrementCustomMetric(fmt.Sprintf("metric_%d", i%100), 1)
			i++
		}
	})
}

func BenchmarkMetricsCollectorGetCurrentSnapshot(b *testing.B) {
	mc := NewMetricsCollector(MetricsConfig{})
	defer mc.Close()

	// Add some test data
	for i := 0; i < 100; i++ {
		mc.RecordHTTPRequest(200, 50*time.Millisecond, 1024, "")
		mc.SetCustomMetric(fmt.Sprintf("metric_%d", i), "counter", "Test metric", int64(i), nil)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		snapshot := mc.GetCurrentSnapshot()
		_ = snapshot // Use snapshot to prevent optimization
	}
}

func BenchmarkHistogramMetricRecord(b *testing.B) {
	h := NewHistogramMetric()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		value := 0.0
		for pb.Next() {
			h.Record(value)
			value += 1.0
		}
	})
}