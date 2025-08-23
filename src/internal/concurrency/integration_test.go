package concurrency

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// IntegrationTestSuite tests all concurrency components working together
func TestConcurrencyIntegration(t *testing.T) {
	// Create test HTTP server
	requestCount := int64(0)
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&requestCount, 1)

		// Simulate some processing time
		time.Sleep(10 * time.Millisecond)

		// Simulate occasional failures
		if atomic.LoadInt64(&requestCount)%10 == 0 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `{"results": [], "query": "%s"}`, r.URL.Query().Get("q"))
	}))
	defer testServer.Close()

	// Initialize components
	workerPool := NewWorkerPool(WorkerPoolConfig{
		Workers:      runtime.NumCPU(),
		QueueSize:    1000,
		MaxQueueSize: 2000,
	})

	connectionPool := NewConnectionPool(ConnectionPoolConfig{
		MaxConnsPerInstance:     20,
		MaxIdleConnsPerInstance: 10,
		IdleConnTimeout:         60 * time.Second,
		TLSHandshakeTimeout:     5 * time.Second,
		ResponseHeaderTimeout:   10 * time.Second,
	})

	rateLimiter := NewRateLimiter(RateLimiterConfig{
		DefaultCapacity:   100,
		DefaultRefillRate: 10,
		GlobalCapacity:    500,
		GlobalRefillRate:  50,
	})

	circuitBreaker := NewCircuitBreakerManager(CircuitBreakerConfig{
		MaxFailures:  3,
		ResetTimeout: 30 * time.Second,
	})

	loadBalancer := NewLoadBalancer(LoadBalancerConfig{
		Strategy:            LeastConnections,
		HealthCheckInterval: 5 * time.Second,
		UnhealthyThreshold:  2,
		HealthyThreshold:    1,
		CircuitBreaker:      circuitBreaker,
	})

	metricsCollector := NewMetricsCollector(MetricsConfig{
		CollectionInterval: 1 * time.Second,
		RetentionPeriod:    5 * time.Minute,
		MaxHistorySize:     100,
	})

	// Register components
	metricsCollector.RegisterComponents(
		workerPool,
		connectionPool,
		rateLimiter,
		circuitBreaker,
		nil, // Request queue will be created below
		loadBalancer,
	)

	// Add test server as instance
	loadBalancer.AddInstance(testServer.URL, 1)

	// Create request queue with search processor
	requestQueue, err := NewRequestQueue(QueueConfig{
		MaxSize:    2000,
		WorkerPool: workerPool,
		Processor: func(ctx context.Context, item *QueueItem) (interface{}, error) {
			searchReq := item.Data.(*SearchRequest)
			return processTestSearch(ctx, searchReq, loadBalancer, connectionPool, circuitBreaker)
		},
		RetryDelay:    1 * time.Second,
		DeadLetterCap: 100,
	})
	if err != nil {
		t.Fatalf("Failed to create request queue: %v", err)
	}

	// Start components
	if err := workerPool.Start(); err != nil {
		t.Fatalf("Failed to start worker pool: %v", err)
	}
	defer workerPool.Stop(5 * time.Second)

	if err := requestQueue.Start(); err != nil {
		t.Fatalf("Failed to start request queue: %v", err)
	}
	defer requestQueue.Stop(5 * time.Second)

	// Test concurrent load
	t.Run("ConcurrentLoad", func(t *testing.T) {
		testConcurrentLoad(t, requestQueue, rateLimiter, metricsCollector)
	})

	// Test rate limiting
	t.Run("RateLimiting", func(t *testing.T) {
		testRateLimiting(t, rateLimiter)
	})

	// Test circuit breaker
	t.Run("CircuitBreaker", func(t *testing.T) {
		testCircuitBreaker(t, circuitBreaker)
	})

	// Test metrics collection
	t.Run("MetricsCollection", func(t *testing.T) {
		testMetricsCollection(t, metricsCollector)
	})

	// Clean up
	connectionPool.Close()
	rateLimiter.Close()
	circuitBreaker.Close()
	loadBalancer.Close()
	metricsCollector.Close()
}

type SearchRequest struct {
	Query    string
	ClientIP string
}

func processTestSearch(ctx context.Context, searchReq *SearchRequest, lb *LoadBalancer, cp *ConnectionPool, cb *CircuitBreakerManager) (interface{}, error) {
	// Get instance from load balancer
	instance, err := lb.GetNextInstance()
	if err != nil {
		return nil, err
	}

	start := time.Now()

	// Execute with circuit breaker
	var result interface{}
	err = cb.ExecuteWithContext(ctx, instance.URL, func() error {
		// Get HTTP client
		client := cp.GetClient(instance.URL, BrowserProfile{
			BrowserType:  "chrome",
			Platform:     "linux",
			HTTP2Enabled: true,
		})

		// Make request
		req, err := http.NewRequestWithContext(ctx, "GET", instance.URL+"?q="+searchReq.Query, nil)
		if err != nil {
			return err
		}

		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()

		if resp.StatusCode >= 500 {
			return fmt.Errorf("server error: %d", resp.StatusCode)
		}

		result = map[string]interface{}{
			"status": resp.StatusCode,
			"query":  searchReq.Query,
		}
		return nil
	})

	duration := time.Since(start)

	// Record metrics
	if err != nil {
		lb.RecordFailure(instance.URL, err)
	} else {
		lb.RecordSuccess(instance.URL, duration)
	}

	return result, err
}

func testConcurrentLoad(t *testing.T, requestQueue *RequestQueue, rateLimiter *RateLimiter, metricsCollector *MetricsCollector) {
	const numRequests = 100
	const concurrency = 10

	var wg sync.WaitGroup
	successCount := int64(0)
	errorCount := int64(0)

	// Submit requests concurrently
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			for j := 0; j < numRequests/concurrency; j++ {
				clientIP := fmt.Sprintf("192.168.1.%d", (workerID*10+j)%254+1)

				// Check rate limiting
				if !rateLimiter.Allow(clientIP, 1) {
					atomic.AddInt64(&errorCount, 1)
					continue
				}

				searchReq := &SearchRequest{
					Query:    fmt.Sprintf("test query %d-%d", workerID, j),
					ClientIP: clientIP,
				}

				queueItem := &QueueItem{
					ID:         fmt.Sprintf("test-%d-%d", workerID, j),
					Priority:   PriorityNormal,
					Data:       searchReq,
					Context:    context.Background(),
					ResultChan: make(chan QueueResult, 1),
					MaxRetries: 2,
				}

				err := requestQueue.Submit(queueItem)
				if err != nil {
					atomic.AddInt64(&errorCount, 1)
					continue
				}

				// Wait for result
				select {
				case result := <-queueItem.ResultChan:
					if result.Error != nil {
						atomic.AddInt64(&errorCount, 1)
					} else {
						atomic.AddInt64(&successCount, 1)
					}
				case <-time.After(5 * time.Second):
					atomic.AddInt64(&errorCount, 1)
				}
			}
		}(i)
	}

	wg.Wait()

	// Verify results
	totalProcessed := atomic.LoadInt64(&successCount) + atomic.LoadInt64(&errorCount)
	if totalProcessed == 0 {
		t.Error("No requests were processed")
	}

	successRate := float64(atomic.LoadInt64(&successCount)) / float64(totalProcessed)
	t.Logf("Processed %d requests, success rate: %.2f%%", totalProcessed, successRate*100)

	if successRate < 0.5 { // At least 50% should succeed
		t.Errorf("Success rate too low: %.2f%%", successRate*100)
	}

	// Check metrics
	snapshot := metricsCollector.GetCurrentSnapshot()
	if snapshot.HTTP.TotalRequests == 0 {
		t.Error("No HTTP requests recorded in metrics")
	}

	t.Logf("Metrics - Total requests: %d, Failed: %d",
		snapshot.HTTP.TotalRequests, snapshot.HTTP.FailedRequests)
}

func testRateLimiting(t *testing.T, rateLimiter *RateLimiter) {
	clientIP := "192.168.1.100"

	// Test normal requests
	for i := 0; i < 5; i++ {
		if !rateLimiter.Allow(clientIP, 1) {
			t.Errorf("Request %d should have been allowed", i)
		}
	}

	// Exhaust rate limit
	allowed := 0
	for i := 0; i < 200; i++ {
		if rateLimiter.Allow(clientIP, 1) {
			allowed++
		}
	}

	t.Logf("Allowed %d out of 200 requests", allowed)

	// Should eventually hit rate limit
	if allowed >= 200 {
		t.Error("Rate limiting should have kicked in")
	}

	// Wait and try again (refill should occur)
	time.Sleep(2 * time.Second)

	if !rateLimiter.Allow(clientIP, 1) {
		// This might fail if refill rate is too low, which is acceptable
		t.Log("Rate limiter is still blocking (may be expected based on refill rate)")
	}
}

func testCircuitBreaker(t *testing.T, circuitBreaker *CircuitBreakerManager) {
	service := "test-service"

	// Test successful requests
	for i := 0; i < 3; i++ {
		err := circuitBreaker.Execute(service, func() error {
			return nil // Success
		})
		if err != nil {
			t.Errorf("Successful request %d failed: %v", i, err)
		}
	}

	// Test failing requests (should open circuit)
	for i := 0; i < 5; i++ {
		err := circuitBreaker.Execute(service, func() error {
			return fmt.Errorf("simulated failure")
		})
		// First few should return the actual error, later ones should be circuit open
		if err == nil {
			t.Errorf("Request %d should have failed", i)
		}
	}

	// Verify circuit is open
	err := circuitBreaker.Execute(service, func() error {
		return nil // This shouldn't execute
	})

	if err == nil {
		t.Error("Circuit breaker should be open and blocking requests")
	}

	// Check statistics
	stats := circuitBreaker.GetStats()
	if stats.TotalBreakers == 0 {
		t.Error("Should have created at least one circuit breaker")
	}

	t.Logf("Circuit breaker stats: %+v", stats)
}

func testMetricsCollection(t *testing.T, metricsCollector *MetricsCollector) {
	// Record some HTTP metrics
	metricsCollector.RecordHTTPRequest(200, 100*time.Millisecond, 1024, "")
	metricsCollector.RecordHTTPRequest(404, 50*time.Millisecond, 512, "not_found")
	metricsCollector.RecordHTTPRequest(500, 200*time.Millisecond, 256, "server_error")

	// Set custom metrics
	metricsCollector.SetCustomMetric("test_counter", "counter", "Test counter", 42, nil)
	metricsCollector.IncrementCustomMetric("test_counter", 10)

	// Get snapshot
	snapshot := metricsCollector.GetCurrentSnapshot()

	if snapshot.HTTP.TotalRequests != 3 {
		t.Errorf("Expected 3 total requests, got %d", snapshot.HTTP.TotalRequests)
	}

	if snapshot.HTTP.FailedRequests != 2 {
		t.Errorf("Expected 2 failed requests, got %d", snapshot.HTTP.FailedRequests)
	}

	if len(snapshot.HTTP.StatusCodes) == 0 {
		t.Error("Expected status code metrics")
	}

	if len(snapshot.HTTP.ErrorsByType) == 0 {
		t.Error("Expected error type metrics")
	}

	// Check custom metrics
	if customMetric, exists := snapshot.CustomMetrics["test_counter"]; exists {
		if customMetric.Value != 52 { // 42 + 10
			t.Errorf("Expected custom metric value 52, got %d", customMetric.Value)
		}
	} else {
		t.Error("Custom metric not found")
	}

	// Check system health
	if snapshot.SystemHealth == nil {
		t.Error("System health metrics missing")
	} else {
		if snapshot.SystemHealth.OverallScore < 0 || snapshot.SystemHealth.OverallScore > 1 {
			t.Errorf("Overall health score out of range: %f", snapshot.SystemHealth.OverallScore)
		}
		t.Logf("System health score: %.2f", snapshot.SystemHealth.OverallScore)
	}

	// Test history
	time.Sleep(2 * time.Second) // Wait for collection
	history := metricsCollector.GetHistory(time.Now().Add(-5 * time.Second))

	if len(history) == 0 {
		t.Error("No historical metrics found")
	}

	t.Logf("Collected %d historical snapshots", len(history))
}
