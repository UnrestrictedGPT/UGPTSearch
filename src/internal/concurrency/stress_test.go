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

// StressSearchRequest represents a search request for stress testing
type StressSearchRequest struct {
	Query    string
	ClientIP string
}

// TestStressHighConcurrentLoad simulates 2000+ concurrent requests
func TestStressHighConcurrentLoad(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping stress test in short mode")
	}

	// Create test server with realistic response times and failures
	var requestCount int64
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := atomic.AddInt64(&requestCount, 1)
		
		// Simulate variable response times (10-100ms)
		responseTime := time.Duration(10+(count%90)) * time.Millisecond
		time.Sleep(responseTime)
		
		// Simulate 5% error rate
		if count%20 == 0 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		
		// Simulate 2% rate limit responses
		if count%50 == 0 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `{"results": [], "query": "%s", "count": %d}`, r.URL.Query().Get("q"), count)
	}))
	defer testServer.Close()

	// High-performance configuration for 2000+ req/s
	workerPool := NewWorkerPool(WorkerPoolConfig{
		Workers:      runtime.NumCPU() * 8, // Scale with CPU cores
		QueueSize:    20000,                 // Large queue
		MaxQueueSize: 50000,                 // Allow bursts
	})

	connectionPool := NewConnectionPool(ConnectionPoolConfig{
		MaxConnsPerInstance:     200, // High connection limit
		MaxIdleConnsPerInstance: 100,
		IdleConnTimeout:         120 * time.Second,
		TLSHandshakeTimeout:     10 * time.Second,
		ResponseHeaderTimeout:   30 * time.Second,
	})

	rateLimiter := NewRateLimiter(RateLimiterConfig{
		DefaultCapacity:   2000, // High capacity per client
		DefaultRefillRate: 1000, // Fast refill
		GlobalCapacity:    50000, // Very high global capacity
		GlobalRefillRate:  10000, // Fast global refill
		MaxBuckets:        10000, // Support many clients
	})

	circuitBreaker := NewCircuitBreakerManager(CircuitBreakerConfig{
		MaxFailures:  20, // Higher tolerance
		ResetTimeout: 15 * time.Second,
	})

	loadBalancer := NewLoadBalancer(LoadBalancerConfig{
		Strategy:            LeastConnections,
		HealthCheckInterval: 10 * time.Second,
		UnhealthyThreshold:  10, // Higher threshold for stress test
		HealthyThreshold:    3,
		CircuitBreaker:      circuitBreaker,
	})

	metricsCollector := NewMetricsCollector(MetricsConfig{
		CollectionInterval: 5 * time.Second,
		RetentionPeriod:    10 * time.Minute,
		MaxHistorySize:     120,
		HTTPLatencyBuffer:  5000, // Large buffer
	})

	// Start all components
	if err := workerPool.Start(); err != nil {
		t.Fatalf("Failed to start worker pool: %v", err)
	}
	defer workerPool.Stop(30 * time.Second)

	metricsCollector.RegisterComponents(workerPool, connectionPool, rateLimiter, circuitBreaker, nil, loadBalancer)

	// Add multiple instances to load balancer
	for i := 0; i < 10; i++ {
		loadBalancer.AddInstance(testServer.URL, 1)
	}

	// Create request processor
	requestQueue, err := NewRequestQueue(QueueConfig{
		MaxSize:    50000,
		WorkerPool: workerPool,
		Processor: func(ctx context.Context, item *QueueItem) (interface{}, error) {
			return processStressTestSearch(ctx, item, loadBalancer, connectionPool, circuitBreaker, metricsCollector)
		},
		RetryDelay:    1 * time.Second,
		DeadLetterCap: 5000,
	})
	if err != nil {
		t.Fatalf("Failed to create request queue: %v", err)
	}

	if err := requestQueue.Start(); err != nil {
		t.Fatalf("Failed to start request queue: %v", err)
	}
	defer requestQueue.Stop(30 * time.Second)

	// Stress test parameters
	const (
		numClients    = 500  // Simulate 500 concurrent clients
		duration      = 30   // Run for 30 seconds
		targetRPS     = 2000 // Target 2000 requests per second
	)

	t.Logf("Starting stress test: %d clients, %d seconds, target %d RPS", numClients, duration, targetRPS)

	var wg sync.WaitGroup
	var totalRequests int64
	var successfulRequests int64
	var failedRequests int64
	var rateLimitedRequests int64

	startTime := time.Now()
	endTime := startTime.Add(duration * time.Second)

	// Launch concurrent clients
	for clientID := 0; clientID < numClients; clientID++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			
			clientIP := fmt.Sprintf("192.168.%d.%d", id/254+1, id%254+1)
			requestID := int64(0)

			for time.Now().Before(endTime) {
				atomic.AddInt64(&totalRequests, 1)
				requestID++

				// Rate limiting check
				if !rateLimiter.Allow(clientIP, 1) {
					atomic.AddInt64(&rateLimitedRequests, 1)
					time.Sleep(10 * time.Millisecond) // Back off on rate limit
					continue
				}

				searchReq := &StressSearchRequest{
					Query:    fmt.Sprintf("stress test query %d-%d", id, requestID),
					ClientIP: clientIP,
				}

				queueItem := &QueueItem{
					ID:         fmt.Sprintf("stress-%d-%d", id, requestID),
					Priority:   PriorityNormal,
					Data:       searchReq,
					Context:    context.Background(),
					ResultChan: make(chan QueueResult, 1),
					MaxRetries: 3,
				}

				err := requestQueue.Submit(queueItem)
				if err != nil {
					atomic.AddInt64(&failedRequests, 1)
					time.Sleep(5 * time.Millisecond) // Back off on queue full
					continue
				}

				// Process result asynchronously
				go func() {
					select {
					case result := <-queueItem.ResultChan:
						if result.Error != nil {
							atomic.AddInt64(&failedRequests, 1)
						} else {
							atomic.AddInt64(&successfulRequests, 1)
						}
					case <-time.After(10 * time.Second):
						atomic.AddInt64(&failedRequests, 1)
					}
				}()

				// Control request rate per client
				requestInterval := time.Duration(float64(time.Second) / float64(targetRPS) * float64(numClients))
				time.Sleep(requestInterval)
			}
		}(clientID)
	}

	wg.Wait()
	
	// Wait for remaining requests to complete
	time.Sleep(5 * time.Second)

	totalTime := time.Since(startTime)
	actualRPS := float64(atomic.LoadInt64(&totalRequests)) / totalTime.Seconds()
	
	total := atomic.LoadInt64(&totalRequests)
	successful := atomic.LoadInt64(&successfulRequests)
	failed := atomic.LoadInt64(&failedRequests)
	rateLimited := atomic.LoadInt64(&rateLimitedRequests)
	
	successRate := float64(successful) / float64(total) * 100
	
	t.Logf("Stress test results:")
	t.Logf("  Duration: %v", totalTime)
	t.Logf("  Total requests: %d", total)
	t.Logf("  Successful: %d (%.2f%%)", successful, successRate)
	t.Logf("  Failed: %d", failed)
	t.Logf("  Rate limited: %d", rateLimited)
	t.Logf("  Actual RPS: %.2f", actualRPS)
	t.Logf("  Server processed: %d", atomic.LoadInt64(&requestCount))

	// Verify system performed adequately
	if successRate < 80 {
		t.Errorf("Success rate too low: %.2f%% (expected > 80%%)", successRate)
	}
	
	if actualRPS < float64(targetRPS)*0.5 {
		t.Errorf("RPS too low: %.2f (expected > %.2f)", actualRPS, float64(targetRPS)*0.5)
	}

	// Check component health
	workerStats := workerPool.Stats()
	if !workerStats.IsRunning {
		t.Error("Worker pool should still be running")
	}

	rateLimiterStats := rateLimiter.GetStats()
	t.Logf("Rate limiter stats: Total: %d, Allowed: %d, Limited: %d", 
		rateLimiterStats.TotalRequests, rateLimiterStats.AllowedRequests, rateLimiterStats.RateLimitedReqs)

	loadBalancerStats := loadBalancer.GetStats()
	t.Logf("Load balancer stats: Total requests: %d, Healthy instances: %d", 
		loadBalancerStats.TotalRequests, loadBalancerStats.HealthyInstances)

	// Cleanup
	connectionPool.Close()
	rateLimiter.Close()
	circuitBreaker.Close()
	loadBalancer.Close()
	metricsCollector.Close()
}

func processStressTestSearch(ctx context.Context, item *QueueItem, lb *LoadBalancer, cp *ConnectionPool, cb *CircuitBreakerManager, metrics *MetricsCollector) (interface{}, error) {
	req := item.Data.(*StressSearchRequest)
	
	// Record request start
	metrics.StartHTTPRequest()
	defer metrics.EndHTTPRequest()
	
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
		httpReq, err := http.NewRequestWithContext(ctx, "GET", instance.URL+"?q="+req.Query, nil)
		if err != nil {
			return err
		}

		resp, err := client.Do(httpReq)
		if err != nil {
			return err
		}
		defer resp.Body.Close()

		// Record metrics
		latency := time.Since(start)
		errorType := ""
		if resp.StatusCode >= 400 {
			errorType = "http_error"
		}
		metrics.RecordHTTPRequest(resp.StatusCode, latency, resp.ContentLength, errorType)

		if resp.StatusCode >= 500 {
			return fmt.Errorf("server error: %d", resp.StatusCode)
		}

		result = map[string]interface{}{
			"status": resp.StatusCode,
			"query":  req.Query,
		}
		return nil
	})

	duration := time.Since(start)

	// Record load balancer metrics
	if err != nil {
		lb.RecordFailure(instance.URL, err)
	} else {
		lb.RecordSuccess(instance.URL, duration)
	}

	return result, err
}

// TestStressMemoryUsage tests memory usage under sustained load
func TestStressMemoryUsage(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping memory stress test in short mode")
	}

	// Record initial memory
	var initialMem runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&initialMem)

	// Create components
	workerPool := NewWorkerPool(WorkerPoolConfig{
		Workers:   runtime.NumCPU() * 4,
		QueueSize: 10000,
	})
	workerPool.Start()
	defer workerPool.Stop(10 * time.Second)

	rateLimiter := NewRateLimiter(RateLimiterConfig{
		DefaultCapacity:  1000,
		MaxBuckets:      5000, // Many buckets
	})
	defer rateLimiter.Close()

	connectionPool := NewConnectionPool(ConnectionPoolConfig{
		MaxConnsPerInstance: 100,
	})
	defer connectionPool.Close()

	metricsCollector := NewMetricsCollector(MetricsConfig{
		MaxHistorySize:    1000,
		HTTPLatencyBuffer: 10000,
	})
	defer metricsCollector.Close()

	loadBalancer := NewLoadBalancer(LoadBalancerConfig{Strategy: RoundRobin})
	defer loadBalancer.Close()

	// Add many instances
	for i := 0; i < 100; i++ {
		loadBalancer.AddInstance(fmt.Sprintf("http://server%d.example.com", i), 1)
	}

	// Sustained load for memory testing
	const duration = 30 * time.Second
	const concurrency = 100

	t.Logf("Running memory stress test for %v with %d concurrent goroutines", duration, concurrency)

	startTime := time.Now()
	endTime := startTime.Add(duration)
	
	var wg sync.WaitGroup
	var totalOps int64

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			
			ops := int64(0)
			for time.Now().Before(endTime) {
				ops++
				
				// Rate limiting operations
				clientIP := fmt.Sprintf("client-%d-%d", workerID, ops%1000)
				rateLimiter.Allow(clientIP, 1)

				// Connection pool operations
				instance := fmt.Sprintf("http://server%d.example.com", ops%100)
				client := connectionPool.GetClient(instance, BrowserProfile{
					BrowserType: "chrome",
					Platform:    "linux",
				})
				_ = client

				// Load balancer operations
				if inst, err := loadBalancer.GetNextInstance(); err == nil {
					loadBalancer.RecordSuccess(inst.URL, time.Millisecond)
				}

				// Metrics operations
				metricsCollector.RecordHTTPRequest(200, time.Millisecond, 1024, "")
				metricsCollector.SetCustomMetric(fmt.Sprintf("metric-%d-%d", workerID, ops%100), "counter", "test", ops, nil)

				// Worker pool operations
				task := Task{
					ID: fmt.Sprintf("mem-test-%d-%d", workerID, ops),
					Fn: func(ctx context.Context) error { return nil },
				}
				workerPool.Submit(task)

				atomic.AddInt64(&totalOps, 1)
				
				// Small delay to control rate
				time.Sleep(time.Microsecond * 100)
			}
		}(i)
	}

	wg.Wait()

	// Force garbage collection and measure memory
	runtime.GC()
	time.Sleep(time.Second) // Allow GC to complete
	runtime.GC()

	var finalMem runtime.MemStats
	runtime.ReadMemStats(&finalMem)

	memoryIncrease := finalMem.Alloc - initialMem.Alloc
	totalOperations := atomic.LoadInt64(&totalOps)
	
	t.Logf("Memory stress test results:")
	t.Logf("  Duration: %v", time.Since(startTime))
	t.Logf("  Total operations: %d", totalOperations)
	t.Logf("  Initial memory: %d bytes (%.2f MB)", initialMem.Alloc, float64(initialMem.Alloc)/(1024*1024))
	t.Logf("  Final memory: %d bytes (%.2f MB)", finalMem.Alloc, float64(finalMem.Alloc)/(1024*1024))
	t.Logf("  Memory increase: %d bytes (%.2f MB)", memoryIncrease, float64(memoryIncrease)/(1024*1024))
	t.Logf("  Memory per operation: %.2f bytes", float64(memoryIncrease)/float64(totalOperations))
	t.Logf("  GC runs: %d", finalMem.NumGC-initialMem.NumGC)
	t.Logf("  Total allocations: %.2f MB", float64(finalMem.TotalAlloc-initialMem.TotalAlloc)/(1024*1024))

	// Verify memory usage is reasonable (< 100MB increase for stress test)
	maxExpectedIncrease := uint64(100 * 1024 * 1024) // 100MB
	if memoryIncrease > maxExpectedIncrease {
		t.Errorf("Memory increase too high: %d bytes (expected < %d bytes)", memoryIncrease, maxExpectedIncrease)
	}
}

// TestStressFailureRecovery tests system recovery from various failure scenarios
func TestStressFailureRecovery(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping failure recovery stress test in short mode")
	}

	// Create test server with controllable failure modes
	var failureMode int64 // 0=normal, 1=errors, 2=timeouts, 3=mixed
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mode := atomic.LoadInt64(&failureMode)
		
		switch mode {
		case 1: // Error mode
			w.WriteHeader(http.StatusInternalServerError)
			return
		case 2: // Timeout mode
			time.Sleep(5 * time.Second)
			w.WriteHeader(http.StatusOK)
		case 3: // Mixed mode
			if time.Now().UnixNano()%3 == 0 {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
		}
		
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status": "ok"}`))
	}))
	defer testServer.Close()

	// Create resilient configuration
	workerPool := NewWorkerPool(WorkerPoolConfig{
		Workers:   runtime.NumCPU() * 2,
		QueueSize: 5000,
	})
	workerPool.Start()
	defer workerPool.Stop(10 * time.Second)

	circuitBreaker := NewCircuitBreakerManager(CircuitBreakerConfig{
		MaxFailures:  5,
		ResetTimeout: 5 * time.Second,
	})
	defer circuitBreaker.Close()

	loadBalancer := NewLoadBalancer(LoadBalancerConfig{
		Strategy:           RoundRobin,
		UnhealthyThreshold: 3,
		HealthyThreshold:   2,
		CircuitBreaker:     circuitBreaker,
	})
	defer loadBalancer.Close()

	connectionPool := NewConnectionPool(ConnectionPoolConfig{
		MaxConnsPerInstance:     50,
		ResponseHeaderTimeout:   2 * time.Second, // Short timeout for failure test
		TLSHandshakeTimeout:     2 * time.Second,
	})
	defer connectionPool.Close()

	// Add instances
	for i := 0; i < 5; i++ {
		loadBalancer.AddInstance(testServer.URL, 1)
	}

	t.Logf("Starting failure recovery stress test")

	var successCount int64
	var failureCount int64
	
	// Test phases: normal -> failures -> recovery
	phases := []struct {
		name        string
		mode        int64
		duration    time.Duration
		concurrency int
	}{
		{"Normal", 0, 10 * time.Second, 50},
		{"High Errors", 1, 15 * time.Second, 50},
		{"Recovery", 0, 10 * time.Second, 50},
		{"Mixed Failures", 3, 10 * time.Second, 50},
		{"Final Recovery", 0, 10 * time.Second, 50},
	}

	for _, phase := range phases {
		t.Logf("Phase: %s (mode: %d, duration: %v)", phase.name, phase.mode, phase.duration)
		
		// Set failure mode
		atomic.StoreInt64(&failureMode, phase.mode)
		
		var phaseWg sync.WaitGroup
		phaseStart := time.Now()
		phaseEnd := phaseStart.Add(phase.duration)
		
		// Launch concurrent workers for this phase
		for i := 0; i < phase.concurrency; i++ {
			phaseWg.Add(1)
			go func(workerID int) {
				defer phaseWg.Done()
				
				for time.Now().Before(phaseEnd) {
					// Get instance
					instance, err := loadBalancer.GetNextInstance()
					if err != nil {
						atomic.AddInt64(&failureCount, 1)
						time.Sleep(100 * time.Millisecond)
						continue
					}

					// Make request with circuit breaker
					start := time.Now()
					err = circuitBreaker.ExecuteWithContext(context.Background(), instance.URL, func() error {
						client := connectionPool.GetClient(instance.URL, BrowserProfile{
							BrowserType: "chrome",
						})

						req, err := http.NewRequest("GET", instance.URL, nil)
						if err != nil {
							return err
						}

						ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
						defer cancel()
						req = req.WithContext(ctx)

						resp, err := client.Do(req)
						if err != nil {
							return err
						}
						defer resp.Body.Close()

						if resp.StatusCode >= 500 {
							return fmt.Errorf("server error: %d", resp.StatusCode)
						}
						return nil
					})

					duration := time.Since(start)
					if err != nil {
						atomic.AddInt64(&failureCount, 1)
						loadBalancer.RecordFailure(instance.URL, err)
					} else {
						atomic.AddInt64(&successCount, 1)
						loadBalancer.RecordSuccess(instance.URL, duration)
					}

					time.Sleep(time.Duration(50+workerID*10) * time.Millisecond)
				}
			}(i)
		}

		phaseWg.Wait()
		
		// Log phase results
		successes := atomic.LoadInt64(&successCount)
		failures := atomic.LoadInt64(&failureCount)
		total := successes + failures
		successRate := float64(successes) / float64(total) * 100
		
		t.Logf("Phase %s completed: Success rate: %.2f%% (%d/%d)", 
			phase.name, successRate, successes, total)

		// Check component health
		lbStats := loadBalancer.GetStats()
		cbStats := circuitBreaker.GetStats()
		
		t.Logf("  Load balancer: %d healthy instances", lbStats.HealthyInstances)
		t.Logf("  Circuit breaker: %d total breakers, %d rejected", cbStats.TotalBreakers, cbStats.RejectedRequests)
		
		// Allow system to stabilize between phases
		time.Sleep(2 * time.Second)
	}

	// Final verification
	finalSuccess := atomic.LoadInt64(&successCount)
	finalFailure := atomic.LoadInt64(&failureCount)
	finalTotal := finalSuccess + finalFailure
	finalSuccessRate := float64(finalSuccess) / float64(finalTotal) * 100

	t.Logf("Final results: Success rate: %.2f%% (%d/%d)", 
		finalSuccessRate, finalSuccess, finalTotal)

	// System should have processed significant requests despite failures
	if finalTotal < 1000 {
		t.Errorf("Total requests too low: %d (expected > 1000)", finalTotal)
	}

	// Success rate should be reasonable even with failures
	if finalSuccessRate < 30 {
		t.Errorf("Overall success rate too low: %.2f%% (expected > 30%%)", finalSuccessRate)
	}

	// Check that system components are still functional
	if !workerPool.Stats().IsRunning {
		t.Error("Worker pool should still be running")
	}

	lbStats := loadBalancer.GetStats()
	if lbStats.HealthyInstances == 0 {
		t.Error("Load balancer should have at least some healthy instances")
	}
}

// TestStressRateLimitingAccuracy tests rate limiting accuracy under high load
func TestStressRateLimitingAccuracy(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping rate limiting accuracy stress test in short mode")
	}

	rateLimiter := NewRateLimiter(RateLimiterConfig{
		DefaultCapacity:   100,  // 100 requests per client
		DefaultRefillRate: 10,   // 10 requests per second refill
		GlobalCapacity:    5000, // 5000 global capacity
		GlobalRefillRate:  500,  // 500 global refill per second
		MaxBuckets:        1000,
	})
	defer rateLimiter.Close()

	const (
		numClients = 200
		duration   = 30 * time.Second
		targetRPS  = 5 // 5 requests per second per client
	)

	t.Logf("Testing rate limiting accuracy: %d clients, %v duration, %d RPS per client", 
		numClients, duration, targetRPS)

	var wg sync.WaitGroup
	var totalRequests int64
	var allowedRequests int64
	var deniedRequests int64

	startTime := time.Now()
	endTime := startTime.Add(duration)

	// Launch clients
	for clientID := 0; clientID < numClients; clientID++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			
			clientIP := fmt.Sprintf("client-%d", id)
			ticker := time.NewTicker(time.Second / targetRPS) // Control request rate
			defer ticker.Stop()

			for time.Now().Before(endTime) {
				select {
				case <-ticker.C:
					atomic.AddInt64(&totalRequests, 1)
					if rateLimiter.Allow(clientIP, 1) {
						atomic.AddInt64(&allowedRequests, 1)
					} else {
						atomic.AddInt64(&deniedRequests, 1)
					}
				case <-time.After(time.Second):
					// Timeout to prevent goroutine leak
					return
				}
			}
		}(clientID)
	}

	wg.Wait()

	totalTime := time.Since(startTime)
	total := atomic.LoadInt64(&totalRequests)
	allowed := atomic.LoadInt64(&allowedRequests)
	denied := atomic.LoadInt64(&deniedRequests)

	actualRequestRate := float64(total) / totalTime.Seconds()
	allowedRate := float64(allowed) / totalTime.Seconds()
	denialRate := float64(denied) / float64(total) * 100

	t.Logf("Rate limiting accuracy results:")
	t.Logf("  Duration: %v", totalTime)
	t.Logf("  Total requests: %d", total)
	t.Logf("  Allowed requests: %d", allowed)
	t.Logf("  Denied requests: %d", denied)
	t.Logf("  Actual request rate: %.2f RPS", actualRequestRate)
	t.Logf("  Allowed rate: %.2f RPS", allowedRate)
	t.Logf("  Denial rate: %.2f%%", denialRate)

	stats := rateLimiter.GetStats()
	t.Logf("  Rate limiter stats: Total: %d, Buckets: %d", 
		stats.TotalRequests, stats.ActiveBuckets)

	// Verify rate limiting worked
	expectedTotalRate := float64(numClients * targetRPS)
	if actualRequestRate < expectedTotalRate*0.8 || actualRequestRate > expectedTotalRate*1.2 {
		t.Errorf("Request rate out of expected range: %.2f (expected ~%.2f)", 
			actualRequestRate, expectedTotalRate)
	}

	// Should have some denials due to rate limiting
	if denialRate < 5 {
		t.Logf("Warning: Low denial rate (%.2f%%) might indicate rate limiting not working", denialRate)
	}

	// Verify bucket count is reasonable
	if stats.ActiveBuckets != int64(numClients) {
		t.Errorf("Expected %d active buckets, got %d", numClients, stats.ActiveBuckets)
	}
}