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

// BenchmarkHighTrafficSearch simulates high-traffic search scenarios
func BenchmarkHighTrafficSearch(b *testing.B) {
	// Create test server to simulate SearXNG instance
	requestCount := int64(0)
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&requestCount, 1)
		
		// Simulate processing time
		time.Sleep(time.Duration(10+requestCount%50) * time.Millisecond)
		
		// Simulate occasional errors (5%)
		if atomic.LoadInt64(&requestCount)%20 == 0 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `{"results": [], "query": "%s"}`, r.URL.Query().Get("q"))
	}))
	defer testServer.Close()

	// Initialize high-performance configuration
	workerPool := NewWorkerPool(WorkerPoolConfig{
		Workers:      runtime.NumCPU() * 4,
		QueueSize:    10000,
		MaxQueueSize: 20000,
	})

	connectionPool := NewConnectionPool(ConnectionPoolConfig{
		MaxConnsPerInstance:     100,
		MaxIdleConnsPerInstance: 50,
		IdleConnTimeout:         60 * time.Second,
		TLSHandshakeTimeout:     5 * time.Second,
		ResponseHeaderTimeout:   15 * time.Second,
	})

	rateLimiter := NewRateLimiter(RateLimiterConfig{
		DefaultCapacity:   1000,
		DefaultRefillRate: 500,
		GlobalCapacity:    10000,
		GlobalRefillRate:  5000,
	})

	circuitBreaker := NewCircuitBreakerManager(CircuitBreakerConfig{
		MaxFailures:  10,
		ResetTimeout: 30 * time.Second,
	})

	loadBalancer := NewLoadBalancer(LoadBalancerConfig{
		Strategy:            LeastConnections,
		HealthCheckInterval: 5 * time.Second,
		UnhealthyThreshold:  5,
		HealthyThreshold:    2,
		CircuitBreaker:      circuitBreaker,
	})

	metricsCollector := NewMetricsCollector(MetricsConfig{
		CollectionInterval: 1 * time.Second,
		RetentionPeriod:    5 * time.Minute,
		MaxHistorySize:     300,
	})

	// Register components
	metricsCollector.RegisterComponents(
		workerPool,
		connectionPool,
		rateLimiter,
		circuitBreaker,
		nil,
		loadBalancer,
	)

	// Start components
	workerPool.Start()
	defer workerPool.Stop(10 * time.Second)

	// Add multiple server instances to simulate real load balancing
	for i := 0; i < 5; i++ {
		loadBalancer.AddInstance(testServer.URL, 1)
	}

	// Create request queue with search processor
	requestQueue, err := NewRequestQueue(QueueConfig{
		MaxSize:    20000,
		WorkerPool: workerPool,
		Processor: func(ctx context.Context, item *QueueItem) (interface{}, error) {
			return processHighTrafficSearch(ctx, item, loadBalancer, connectionPool, circuitBreaker, rateLimiter)
		},
		RetryDelay:    500 * time.Millisecond,
		DeadLetterCap: 1000,
	})
	if err != nil {
		b.Fatalf("Failed to create request queue: %v", err)
	}

	requestQueue.Start()
	defer requestQueue.Stop(10 * time.Second)

	b.ResetTimer()
	
	// Benchmark high concurrent load
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			clientIP := fmt.Sprintf("192.168.1.%d", (atomic.LoadInt64(&requestCount))%254+1)
			
			// Check rate limiting
			if !rateLimiter.Allow(clientIP, 1) {
				continue
			}

			searchReq := &BenchmarkSearchRequest{
				Query:    fmt.Sprintf("test query %d", atomic.LoadInt64(&requestCount)),
				ClientIP: clientIP,
			}

			queueItem := &QueueItem{
				ID:         fmt.Sprintf("bench-%d", atomic.LoadInt64(&requestCount)),
				Priority:   PriorityNormal,
				Data:       searchReq,
				Context:    context.Background(),
				ResultChan: make(chan QueueResult, 1),
				MaxRetries: 2,
			}

			err := requestQueue.Submit(queueItem)
			if err != nil {
				continue
			}

			// Don't wait for result to maximize throughput
			go func() {
				select {
				case <-queueItem.ResultChan:
					// Result received
				case <-time.After(5 * time.Second):
					// Timeout
				}
			}()
		}
	})

	// Cleanup
	connectionPool.Close()
	rateLimiter.Close()
	circuitBreaker.Close()
	loadBalancer.Close()
	metricsCollector.Close()

	b.Logf("Processed %d total requests", atomic.LoadInt64(&requestCount))
}

func processHighTrafficSearch(ctx context.Context, searchReq *QueueItem, lb *LoadBalancer, cp *ConnectionPool, cb *CircuitBreakerManager, rl *RateLimiter) (interface{}, error) {
	req := searchReq.Data.(*BenchmarkSearchRequest)
	
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

	// Record metrics
	if err != nil {
		lb.RecordFailure(instance.URL, err)
	} else {
		lb.RecordSuccess(instance.URL, duration)
	}

	return result, err
}

// BenchmarkConcurrentComponents tests all components working together under load
func BenchmarkConcurrentComponents(b *testing.B) {
	workerPool := NewWorkerPool(WorkerPoolConfig{
		Workers:   runtime.NumCPU() * 2,
		QueueSize: 5000,
	})
	defer func() {
		workerPool.Stop(5 * time.Second)
	}()
	workerPool.Start()

	rateLimiter := NewRateLimiter(RateLimiterConfig{
		DefaultCapacity:   500,
		DefaultRefillRate: 100,
		GlobalCapacity:    5000,
		GlobalRefillRate:  1000,
	})
	defer rateLimiter.Close()

	circuitBreaker := NewCircuitBreakerManager(CircuitBreakerConfig{
		MaxFailures:  5,
		ResetTimeout: 1 * time.Second,
	})
	defer circuitBreaker.Close()

	loadBalancer := NewLoadBalancer(LoadBalancerConfig{
		Strategy:           RoundRobin,
		UnhealthyThreshold: 3,
		HealthyThreshold:   1,
		CircuitBreaker:     circuitBreaker,
	})
	defer loadBalancer.Close()

	// Add mock instances
	for i := 0; i < 10; i++ {
		loadBalancer.AddInstance(fmt.Sprintf("http://server%d.example.com", i), 1)
	}

	b.ResetTimer()
	
	b.RunParallel(func(pb *testing.PB) {
		clientID := 0
		for pb.Next() {
			clientIP := fmt.Sprintf("client-%d", clientID%1000)
			clientID++

			// Rate limiting
			if !rateLimiter.Allow(clientIP, 1) {
				continue
			}

			// Load balancing
			instance, err := loadBalancer.GetNextInstance()
			if err != nil {
				continue
			}

			// Circuit breaker + worker pool
			task := Task{
				ID: fmt.Sprintf("bench-task-%d", clientID),
				Fn: func(ctx context.Context) error {
					return circuitBreaker.ExecuteWithContext(ctx, instance.URL, func() error {
						// Simulate work with occasional failures
						if clientID%50 == 0 {
							return fmt.Errorf("simulated error")
						}
						time.Sleep(time.Duration(clientID%10) * time.Microsecond)
						return nil
					})
				},
				Callback: func(err error) {
					if err != nil {
						loadBalancer.RecordFailure(instance.URL, err)
					} else {
						loadBalancer.RecordSuccess(instance.URL, time.Millisecond)
					}
				},
			}

			workerPool.Submit(task)
		}
	})
}

// BenchmarkMemoryEfficiency tests memory usage under high load
func BenchmarkMemoryEfficiency(b *testing.B) {
	// Record initial memory stats
	var initialStats runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&initialStats)

	// Create components with limited memory configuration
	workerPool := NewWorkerPool(WorkerPoolConfig{
		Workers:   runtime.NumCPU(),
		QueueSize: 1000,
	})
	workerPool.Start()
	defer workerPool.Stop(5 * time.Second)

	rateLimiter := NewRateLimiter(RateLimiterConfig{
		DefaultCapacity:  100,
		MaxBuckets:      1000, // Limited buckets to control memory
	})
	defer rateLimiter.Close()

	connectionPool := NewConnectionPool(ConnectionPoolConfig{
		MaxConnsPerInstance:     10,
		MaxIdleConnsPerInstance: 5,
	})
	defer connectionPool.Close()

	metricsCollector := NewMetricsCollector(MetricsConfig{
		MaxHistorySize:     100, // Limited history
		HTTPLatencyBuffer:  100, // Limited buffer
	})
	defer metricsCollector.Close()

	b.ResetTimer()

	// Intensive memory usage test
	for i := 0; i < b.N; i++ {
		// Create clients (tests connection pool memory usage)
		client := connectionPool.GetClient(
			fmt.Sprintf("http://server%d.example.com", i%10),
			BrowserProfile{BrowserType: "chrome", Platform: "linux"},
		)
		_ = client

		// Rate limiting (tests bucket memory usage)
		rateLimiter.Allow(fmt.Sprintf("client-%d", i%500), 1)

		// Metrics (tests metrics memory usage)
		metricsCollector.RecordHTTPRequest(200, time.Millisecond, 1024, "")

		// Worker pool (tests task queue memory usage)
		task := Task{
			ID: fmt.Sprintf("mem-test-%d", i),
			Fn: func(ctx context.Context) error { return nil },
		}
		workerPool.Submit(task)

		// Periodic garbage collection to measure steady-state memory
		if i%1000 == 0 {
			runtime.GC()
		}
	}

	// Measure final memory stats
	var finalStats runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&finalStats)

	memoryIncrease := finalStats.Alloc - initialStats.Alloc
	b.Logf("Memory increase: %d bytes (%.2f MB)", memoryIncrease, float64(memoryIncrease)/(1024*1024))
	b.Logf("Total allocations: %d", finalStats.TotalAlloc)
	b.Logf("GC runs: %d", finalStats.NumGC-initialStats.NumGC)
}

// BenchmarkThroughputScaling tests how components scale with increasing load
func BenchmarkThroughputScaling(b *testing.B) {
	workerCounts := []int{1, 2, 4, 8, 16, 32}
	
	for _, workers := range workerCounts {
		b.Run(fmt.Sprintf("Workers_%d", workers), func(b *testing.B) {
			workerPool := NewWorkerPool(WorkerPoolConfig{
				Workers:   workers,
				QueueSize: workers * 100,
			})
			workerPool.Start()
			defer workerPool.Stop(5 * time.Second)

			loadBalancer := NewLoadBalancer(LoadBalancerConfig{
				Strategy: LeastConnections,
			})
			defer loadBalancer.Close()

			// Add instances
			for i := 0; i < workers; i++ {
				loadBalancer.AddInstance(fmt.Sprintf("http://server%d.example.com", i), 1)
			}

			var processed int64
			
			b.ResetTimer()
			
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					instance, err := loadBalancer.GetNextInstance()
					if err != nil {
						continue
					}

					task := Task{
						ID: "throughput-test",
						Fn: func(ctx context.Context) error {
							atomic.AddInt64(&processed, 1)
							// Minimal work to test pure throughput
							return nil
						},
						Callback: func(err error) {
							if err == nil {
								loadBalancer.RecordSuccess(instance.URL, time.Microsecond)
							}
						},
					}

					workerPool.Submit(task)
				}
			})

			b.Logf("Workers: %d, Processed: %d requests", workers, atomic.LoadInt64(&processed))
		})
	}
}

// BenchmarkLatencyUnderLoad tests latency characteristics under increasing load
func BenchmarkLatencyUnderLoad(b *testing.B) {
	workerPool := NewWorkerPool(WorkerPoolConfig{
		Workers:   runtime.NumCPU(),
		QueueSize: 10000,
	})
	workerPool.Start()
	defer workerPool.Stop(10 * time.Second)

	requestQueue, _ := NewRequestQueue(QueueConfig{
		MaxSize:    10000,
		WorkerPool: workerPool,
		Processor: func(ctx context.Context, item *QueueItem) (interface{}, error) {
			// Simulate variable processing time
			workDuration := time.Duration(item.Priority*10) * time.Microsecond
			time.Sleep(workDuration)
			return "processed", nil
		},
	})
	requestQueue.Start()
	defer requestQueue.Stop(10 * time.Second)

	latencies := make([]time.Duration, 0, b.N)
	var latencyMutex sync.Mutex

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		start := time.Now()
		
		item := &QueueItem{
			ID:         fmt.Sprintf("latency-test-%d", i),
			Priority:   PriorityNormal,
			Data:       "test data",
			Context:    context.Background(),
			ResultChan: make(chan QueueResult, 1),
		}

		requestQueue.Submit(item)

		// Wait for result and measure latency
		go func(startTime time.Time) {
			select {
			case <-item.ResultChan:
				latency := time.Since(startTime)
				latencyMutex.Lock()
				latencies = append(latencies, latency)
				latencyMutex.Unlock()
			case <-time.After(5 * time.Second):
				// Timeout
			}
		}(start)
	}

	// Wait for all requests to complete
	time.Sleep(2 * time.Second)

	// Calculate latency statistics
	if len(latencies) > 0 {
		var totalLatency time.Duration
		minLatency := latencies[0]
		maxLatency := latencies[0]

		for _, latency := range latencies {
			totalLatency += latency
			if latency < minLatency {
				minLatency = latency
			}
			if latency > maxLatency {
				maxLatency = latency
			}
		}

		avgLatency := totalLatency / time.Duration(len(latencies))
		b.Logf("Latency stats - Min: %v, Avg: %v, Max: %v, Samples: %d", 
			minLatency, avgLatency, maxLatency, len(latencies))
	}
}

// BenchmarkResourceContention tests behavior under resource contention
func BenchmarkResourceContention(b *testing.B) {
	// Create components with limited resources
	workerPool := NewWorkerPool(WorkerPoolConfig{
		Workers:      2, // Very limited workers
		QueueSize:    10, // Small queue
		MaxQueueSize: 20,
	})
	workerPool.Start()
	defer workerPool.Stop(10 * time.Second)

	rateLimiter := NewRateLimiter(RateLimiterConfig{
		DefaultCapacity:   10, // Small capacity
		DefaultRefillRate: 1,  // Slow refill
		MaxBuckets:        10, // Limited buckets
	})
	defer rateLimiter.Close()

	var contentionCount int64
	var successCount int64

	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		clientID := 0
		for pb.Next() {
			clientIP := fmt.Sprintf("client-%d", clientID%5) // Few clients to create contention
			clientID++

			// Try rate limiting
			if !rateLimiter.Allow(clientIP, 1) {
				atomic.AddInt64(&contentionCount, 1)
				continue
			}

			// Try submitting to worker pool
			task := Task{
				ID: fmt.Sprintf("contention-test-%d", clientID),
				Fn: func(ctx context.Context) error {
					// Simulate longer work to create queue pressure
					time.Sleep(10 * time.Millisecond)
					atomic.AddInt64(&successCount, 1)
					return nil
				},
			}

			err := workerPool.Submit(task)
			if err != nil {
				atomic.AddInt64(&contentionCount, 1)
			}
		}
	})

	contentions := atomic.LoadInt64(&contentionCount)
	successes := atomic.LoadInt64(&successCount)
	total := contentions + successes
	
	if total > 0 {
		contentionRate := float64(contentions) / float64(total) * 100
		b.Logf("Contention rate: %.2f%% (%d/%d)", contentionRate, contentions, total)
	}
}

// SearchRequest represents a search request for benchmarking
type BenchmarkSearchRequest struct {
	Query    string
	ClientIP string
}