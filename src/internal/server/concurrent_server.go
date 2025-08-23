package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"runtime"
	"strings"
	"sync"
	"time"

	"UGPTSearch/internal/concurrency"
	"UGPTSearch/internal/handlers"
	"UGPTSearch/internal/instances"
	"UGPTSearch/internal/middleware"
)

// ConcurrentServer provides high-performance concurrent request handling
type ConcurrentServer struct {
	httpServer       *http.Server
	workerPool       *concurrency.WorkerPool
	connectionPool   *concurrency.ConnectionPool
	rateLimiter      *concurrency.RateLimiter
	circuitBreaker   *concurrency.CircuitBreakerManager
	requestQueue     *concurrency.RequestQueue
	loadBalancer     *concurrency.LoadBalancer
	metricsCollector *concurrency.MetricsCollector

	// Configuration
	config *ConcurrentServerConfig

	// Lifecycle management
	startOnce    sync.Once
	stopOnce     sync.Once
	shutdownChan chan struct{}
	wg           sync.WaitGroup
}

// ConcurrentServerConfig holds configuration for the concurrent server
type ConcurrentServerConfig struct {
	Addr                    string
	ReadTimeout             time.Duration
	WriteTimeout            time.Duration
	IdleTimeout             time.Duration
	MaxConcurrentRequests   int
	WorkerPoolSize          int
	QueueSize               int
	MaxQueueSize            int
	RateLimitPerClient      int64
	RateLimitGlobal         int64
	CircuitBreakerThreshold int64
	ConnectionPoolMaxConns  int
	ConnectionPoolIdleConns int
	MetricsEnabled          bool
	HealthCheckInterval     time.Duration
}

// SearchRequest represents a search request for processing
type SearchRequest struct {
	Query        string
	Format       string
	ClientIP     string
	UserAgent    string
	Headers      map[string]string
	ResponseChan chan *SearchResponse
	Context      context.Context
	Priority     int
	SubmittedAt  time.Time
}

// SearchResponse represents the response to a search request
type SearchResponse struct {
	Data        []byte
	ContentType string
	StatusCode  int
	Error       error
	Instance    string
	Duration    time.Duration
}

// DefaultConcurrentServerConfig returns a default configuration
func DefaultConcurrentServerConfig() *ConcurrentServerConfig {
	return &ConcurrentServerConfig{
		Addr:                    ":8080",
		ReadTimeout:             30 * time.Second,
		WriteTimeout:            30 * time.Second,
		IdleTimeout:             60 * time.Second,
		MaxConcurrentRequests:   1000,
		WorkerPoolSize:          runtime.NumCPU() * 4,
		QueueSize:               10000,
		MaxQueueSize:            20000,
		RateLimitPerClient:      100,  // 100 requests per client
		RateLimitGlobal:         1000, // 1000 requests globally
		CircuitBreakerThreshold: 5,
		ConnectionPoolMaxConns:  100,
		ConnectionPoolIdleConns: 20,
		MetricsEnabled:          true,
		HealthCheckInterval:     30 * time.Second,
	}
}

// NewConcurrentServer creates a new high-performance concurrent server
func NewConcurrentServer(config *ConcurrentServerConfig) (*ConcurrentServer, error) {
	if config == nil {
		config = DefaultConcurrentServerConfig()
	}

	// Initialize worker pool
	workerPool := concurrency.NewWorkerPool(concurrency.WorkerPoolConfig{
		Workers:      config.WorkerPoolSize,
		QueueSize:    config.QueueSize,
		MaxQueueSize: config.MaxQueueSize,
	})

	// Initialize connection pool
	connectionPool := concurrency.NewConnectionPool(concurrency.ConnectionPoolConfig{
		MaxConnsPerInstance:     config.ConnectionPoolMaxConns,
		MaxIdleConnsPerInstance: config.ConnectionPoolIdleConns,
		IdleConnTimeout:         90 * time.Second,
		TLSHandshakeTimeout:     10 * time.Second,
		ResponseHeaderTimeout:   30 * time.Second,
		DialTimeout:             10 * time.Second,
		KeepAlive:               30 * time.Second,
		MaxLifetime:             10 * time.Minute,
	})

	// Initialize rate limiter
	rateLimiter := concurrency.NewRateLimiter(concurrency.RateLimiterConfig{
		DefaultCapacity:   config.RateLimitPerClient,
		DefaultRefillRate: config.RateLimitPerClient / 60, // Per minute to per second
		GlobalCapacity:    config.RateLimitGlobal,
		GlobalRefillRate:  config.RateLimitGlobal / 60,
		CleanupInterval:   5 * time.Minute,
		MaxBuckets:        10000,
	})

	// Initialize circuit breaker
	circuitBreaker := concurrency.NewCircuitBreakerManager(concurrency.CircuitBreakerConfig{
		MaxFailures:  config.CircuitBreakerThreshold,
		ResetTimeout: 60 * time.Second,
		OnStateChange: func(name string, from, to concurrency.CircuitState) {
			log.Printf("Circuit breaker '%s' state changed from %s to %s", name, from, to)
		},
	})

	// Initialize load balancer
	loadBalancer := concurrency.NewLoadBalancer(concurrency.LoadBalancerConfig{
		Strategy:            concurrency.LeastResponseTime,
		HealthCheckInterval: config.HealthCheckInterval,
		UnhealthyThreshold:  3,
		HealthyThreshold:    2,
		ResponseTimeWindow:  100,
		CircuitBreaker:      circuitBreaker,
	})

	// Initialize metrics collector
	var metricsCollector *concurrency.MetricsCollector
	if config.MetricsEnabled {
		metricsCollector = concurrency.NewMetricsCollector(concurrency.MetricsConfig{
			CollectionInterval: 10 * time.Second,
			RetentionPeriod:    1 * time.Hour,
			MaxHistorySize:     360,
			HTTPLatencyBuffer:  1000,
		})

		// Register components with metrics collector
		metricsCollector.RegisterComponents(
			workerPool,
			connectionPool,
			rateLimiter,
			circuitBreaker,
			nil, // Request queue will be initialized below
			loadBalancer,
		)
	}

	// Initialize request queue
	requestQueue, err := concurrency.NewRequestQueue(concurrency.QueueConfig{
		MaxSize:       config.MaxQueueSize,
		WorkerPool:    workerPool,
		Processor:     nil, // Will be set in the server
		RetryDelay:    5 * time.Second,
		DeadLetterCap: 1000,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create request queue: %w", err)
	}

	// Create HTTP server
	server := &http.Server{
		Addr:         config.Addr,
		ReadTimeout:  config.ReadTimeout,
		WriteTimeout: config.WriteTimeout,
		IdleTimeout:  config.IdleTimeout,
	}

	cs := &ConcurrentServer{
		httpServer:       server,
		workerPool:       workerPool,
		connectionPool:   connectionPool,
		rateLimiter:      rateLimiter,
		circuitBreaker:   circuitBreaker,
		requestQueue:     requestQueue,
		loadBalancer:     loadBalancer,
		metricsCollector: metricsCollector,
		config:           config,
		shutdownChan:     make(chan struct{}),
	}

	// Set the request processor
	requestQueue, _ = concurrency.NewRequestQueue(concurrency.QueueConfig{
		MaxSize:       config.MaxQueueSize,
		WorkerPool:    workerPool,
		Processor:     cs.processSearchRequest,
		RetryDelay:    5 * time.Second,
		DeadLetterCap: 1000,
	})
	cs.requestQueue = requestQueue

	// Add instances from the existing instance manager to the load balancer
	cs.initializeLoadBalancer()

	return cs, nil
}

// initializeLoadBalancer sets up the load balancer with instances from the instance manager
func (cs *ConcurrentServer) initializeLoadBalancer() {
	instanceList := instances.Manager.GetInstances()
	for _, instanceURL := range instanceList {
		cs.loadBalancer.AddInstance(instanceURL, 1) // Default weight of 1
	}
}

// SetupRoutes configures the HTTP routes with enhanced middleware
func (cs *ConcurrentServer) SetupRoutes() {
	// Create a custom mux for better control
	mux := http.NewServeMux()

	// Enhanced middleware chain
	searchHandler := cs.enhancedMiddleware(cs.handleConcurrentSearch)
	searchJSONHandler := cs.enhancedMiddleware(cs.handleConcurrentSearchJSON)
	instancesHandler := cs.enhancedMiddleware(handlers.Instances)
	anubisStatsHandler := cs.enhancedMiddleware(handlers.AnubisStats)

	mux.HandleFunc("/search", searchHandler)
	mux.HandleFunc("/api/search", searchJSONHandler)
	mux.HandleFunc("/instances", instancesHandler)
	mux.HandleFunc("/anubis-stats", anubisStatsHandler)

	// Add metrics endpoints if enabled
	if cs.metricsCollector != nil {
		mux.HandleFunc("/metrics", cs.metricsCollector.ServeHTTP)
		mux.HandleFunc("/metrics/history", cs.metricsCollector.ServeHTTP)
		mux.HandleFunc("/health", cs.metricsCollector.ServeHTTP)
		mux.HandleFunc("/admin/stats", cs.handleAdminStats)
	}

	cs.httpServer.Handler = mux
}

// enhancedMiddleware provides comprehensive middleware chain
func (cs *ConcurrentServer) enhancedMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// Record request start
		if cs.metricsCollector != nil {
			cs.metricsCollector.StartHTTPRequest()
			defer cs.metricsCollector.EndHTTPRequest()
		}

		// Get client IP for rate limiting
		clientIP := cs.getClientIP(r)

		// Apply rate limiting
		if !cs.rateLimiter.AllowWithContext(r.Context(), clientIP, 1) {
			cs.recordMetrics(http.StatusTooManyRequests, time.Since(start), 0, "rate_limited")
			http.Error(w, "Rate limit exceeded", http.StatusTooManyRequests)
			return
		}

		// Apply logging middleware
		middleware.Logging(next)(w, r)

		// Record metrics
		duration := time.Since(start)
		cs.recordMetrics(200, duration, 0, "")
	}
}

// handleConcurrentSearch handles search requests with full concurrency support
func (cs *ConcurrentServer) handleConcurrentSearch(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("q")
	if query == "" {
		http.Error(w, "Missing search query", http.StatusBadRequest)
		return
	}

	format := r.URL.Query().Get("format")
	if format == "" {
		format = "html"
	}

	// Create search request
	searchReq := &SearchRequest{
		Query:        query,
		Format:       format,
		ClientIP:     cs.getClientIP(r),
		UserAgent:    r.UserAgent(),
		Headers:      cs.extractHeaders(r),
		ResponseChan: make(chan *SearchResponse, 1),
		Context:      r.Context(),
		Priority:     concurrency.PriorityNormal,
		SubmittedAt:  time.Now(),
	}

	// Submit to queue
	queueItem := &concurrency.QueueItem{
		ID:         fmt.Sprintf("search-%d", time.Now().UnixNano()),
		Priority:   searchReq.Priority,
		Data:       searchReq,
		Context:    searchReq.Context,
		ResultChan: make(chan concurrency.QueueResult, 1),
		MaxRetries: 3,
	}

	err := cs.requestQueue.Submit(queueItem)
	if err != nil {
		cs.recordMetrics(http.StatusServiceUnavailable, 0, 0, "queue_full")
		http.Error(w, "Service temporarily unavailable", http.StatusServiceUnavailable)
		return
	}

	// Wait for response
	select {
	case result := <-queueItem.ResultChan:
		if result.Error != nil {
			cs.recordMetrics(http.StatusInternalServerError, 0, 0, "processing_error")
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		response := result.Data.(*SearchResponse)
		w.Header().Set("Content-Type", response.ContentType)
		w.WriteHeader(response.StatusCode)
		w.Write(response.Data)

		cs.recordMetrics(response.StatusCode, response.Duration, int64(len(response.Data)), "")

	case <-r.Context().Done():
		cs.recordMetrics(http.StatusRequestTimeout, 0, 0, "timeout")
		http.Error(w, "Request timeout", http.StatusRequestTimeout)
	}
}

// handleConcurrentSearchJSON handles JSON search requests
func (cs *ConcurrentServer) handleConcurrentSearchJSON(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("q")
	if query == "" {
		http.Error(w, "Missing search query", http.StatusBadRequest)
		return
	}

	// Create search request with higher priority for API calls
	searchReq := &SearchRequest{
		Query:        query,
		Format:       "json",
		ClientIP:     cs.getClientIP(r),
		UserAgent:    r.UserAgent(),
		Headers:      cs.extractHeaders(r),
		ResponseChan: make(chan *SearchResponse, 1),
		Context:      r.Context(),
		Priority:     concurrency.PriorityHigh,
		SubmittedAt:  time.Now(),
	}

	// Submit to queue with higher priority
	queueItem := &concurrency.QueueItem{
		ID:         fmt.Sprintf("json-search-%d", time.Now().UnixNano()),
		Priority:   searchReq.Priority,
		Data:       searchReq,
		Context:    searchReq.Context,
		ResultChan: make(chan concurrency.QueueResult, 1),
		MaxRetries: 3,
	}

	err := cs.requestQueue.Submit(queueItem)
	if err != nil {
		cs.recordMetrics(http.StatusServiceUnavailable, 0, 0, "queue_full")
		http.Error(w, "Service temporarily unavailable", http.StatusServiceUnavailable)
		return
	}

	// Wait for response
	select {
	case result := <-queueItem.ResultChan:
		if result.Error != nil {
			cs.recordMetrics(http.StatusInternalServerError, 0, 0, "processing_error")
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		response := result.Data.(*SearchResponse)
		w.Header().Set("Content-Type", response.ContentType)
		w.WriteHeader(response.StatusCode)
		w.Write(response.Data)

		cs.recordMetrics(response.StatusCode, response.Duration, int64(len(response.Data)), "")

	case <-r.Context().Done():
		cs.recordMetrics(http.StatusRequestTimeout, 0, 0, "timeout")
		http.Error(w, "Request timeout", http.StatusRequestTimeout)
	}
}

// processSearchRequest processes a search request (used by the queue)
func (cs *ConcurrentServer) processSearchRequest(ctx context.Context, item *concurrency.QueueItem) (interface{}, error) {
	searchReq := item.Data.(*SearchRequest)
	start := time.Now()

	// Get instance from load balancer
	instanceHealth, err := cs.loadBalancer.GetNextInstance()
	if err != nil {
		return nil, fmt.Errorf("no healthy instances available: %w", err)
	}

	instanceURL := instanceHealth.URL

	// Execute with circuit breaker protection
	var response *SearchResponse
	err = cs.circuitBreaker.ExecuteWithContext(ctx, instanceURL, func() error {
		var searchErr error
		response, searchErr = cs.executeSearchRequest(ctx, searchReq, instanceURL, instanceHealth)
		return searchErr
	})

	duration := time.Since(start)

	if err != nil {
		cs.loadBalancer.RecordFailure(instanceURL, err)
		return nil, err
	}

	// Record success
	cs.loadBalancer.RecordSuccess(instanceURL, duration)
	response.Duration = duration
	response.Instance = instanceURL

	return response, nil
}

// executeSearchRequest performs the actual search request with all evasion techniques
func (cs *ConcurrentServer) executeSearchRequest(ctx context.Context, searchReq *SearchRequest, instanceURL string, instanceHealth *concurrency.InstanceHealth) (*SearchResponse, error) {
	// Determine browser profile for connection optimization
	browserProfile := cs.determineBrowserProfile(searchReq.UserAgent)

	// Get optimized HTTP client from connection pool
	client := cs.connectionPool.GetClient(instanceURL, browserProfile)

	// Build search URL
	searchURL := fmt.Sprintf("%s?q=%s&format=%s", instanceURL,
		url.QueryEscape(searchReq.Query), searchReq.Format)

	// Create request with context
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, searchURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Apply all evasion techniques from the original handlers
	SetHumanLikeHeaders(req, searchURL, instanceURL)
	AdvancedHeaderRandomization(req)

	// Add human-like delay
	delay := GetAdvancedHumanLikeDelay(searchReq.Query)
	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	// Execute request
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	// Handle rate limiting
	if resp.StatusCode == http.StatusTooManyRequests {
		instances.Manager.MarkRateLimit(instanceURL)
		return nil, fmt.Errorf("instance returned rate limit: %d", resp.StatusCode)
	}

	// Handle server errors
	if resp.StatusCode >= 500 {
		instances.Manager.MarkError(instanceURL)
		return nil, fmt.Errorf("instance returned server error: %d", resp.StatusCode)
	}

	// Decompress response using existing logic
	body, err := DecompressResponse(resp)
	if err != nil {
		return nil, fmt.Errorf("failed to decompress response: %w", err)
	}

	// Check for maintenance mode
	if IsMaintenanceMode(body) {
		instances.Manager.MarkError(instanceURL)
		return nil, fmt.Errorf("instance is under maintenance")
	}

	// Mark instance as successful
	instances.Manager.MarkSuccess(instanceURL)

	// Store cookies if provided
	if len(resp.Cookies()) > 0 {
		instances.Manager.SetCookies(instanceURL, resp.Cookies())
	}

	// Determine content type
	contentType := "text/html; charset=utf-8"
	if searchReq.Format == "json" {
		contentType = "application/json"
	}

	return &SearchResponse{
		Data:        body,
		ContentType: contentType,
		StatusCode:  resp.StatusCode,
		Error:       nil,
		Instance:    instanceURL,
	}, nil
}

// handleAdminStats provides administrative statistics
func (cs *ConcurrentServer) handleAdminStats(w http.ResponseWriter, r *http.Request) {
	stats := map[string]interface{}{
		"worker_pool":     cs.workerPool.Stats(),
		"connection_pool": cs.connectionPool.GetStats(),
		"rate_limiter":    cs.rateLimiter.GetStats(),
		"circuit_breaker": cs.circuitBreaker.GetStats(),
		"request_queue":   cs.requestQueue.GetStats(),
		"load_balancer":   cs.loadBalancer.GetStats(),
		"instance_health": cs.loadBalancer.GetInstanceHealth(),
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(stats); err != nil {
		http.Error(w, "Failed to encode stats", http.StatusInternalServerError)
	}
}

// Helper functions

func (cs *ConcurrentServer) getClientIP(r *http.Request) string {
	// Check for forwarded headers first
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return xff
	}
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return xri
	}
	return r.RemoteAddr
}

func (cs *ConcurrentServer) extractHeaders(r *http.Request) map[string]string {
	headers := make(map[string]string)
	for key, values := range r.Header {
		if len(values) > 0 {
			headers[key] = values[0]
		}
	}
	return headers
}

func (cs *ConcurrentServer) determineBrowserProfile(userAgent string) concurrency.BrowserProfile {
	// Simple browser detection logic
	profile := concurrency.BrowserProfile{
		UserAgent:       userAgent,
		HTTP2Enabled:    true,
		CompressionType: "gzip,deflate,br",
	}

	if contains(userAgent, "Chrome") {
		profile.BrowserType = "chrome"
		profile.Platform = "windows"
		if contains(userAgent, "Mac") {
			profile.Platform = "macos"
		} else if contains(userAgent, "Linux") {
			profile.Platform = "linux"
		}
	} else if contains(userAgent, "Firefox") {
		profile.BrowserType = "firefox"
		profile.Platform = "windows"
		if contains(userAgent, "Mac") {
			profile.Platform = "macos"
		} else if contains(userAgent, "Linux") {
			profile.Platform = "linux"
		}
	} else if contains(userAgent, "Safari") {
		profile.BrowserType = "safari"
		profile.Platform = "macos"
	} else {
		profile.BrowserType = "chrome" // Default fallback
		profile.Platform = "windows"
	}

	return profile
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr ||
		(len(s) > len(substr) && (s[:len(substr)] == substr ||
			s[len(s)-len(substr):] == substr ||
			strings.Contains(s, substr))))
}

func (cs *ConcurrentServer) recordMetrics(statusCode int, latency time.Duration, responseSize int64, errorType string) {
	if cs.metricsCollector != nil {
		cs.metricsCollector.RecordHTTPRequest(statusCode, latency, responseSize, errorType)
	}
}

// Lifecycle methods

func (cs *ConcurrentServer) Start() error {
	var startErr error
	cs.startOnce.Do(func() {
		// Start all components
		if err := cs.workerPool.Start(); err != nil {
			startErr = fmt.Errorf("failed to start worker pool: %w", err)
			return
		}

		if err := cs.requestQueue.Start(); err != nil {
			startErr = fmt.Errorf("failed to start request queue: %w", err)
			return
		}

		cs.wg.Add(1)
		go func() {
			defer cs.wg.Done()
			log.Printf("Starting concurrent server on %s", cs.httpServer.Addr)
			if err := cs.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				log.Printf("Server error: %v", err)
			}
		}()
	})

	return startErr
}

func (cs *ConcurrentServer) Shutdown(ctx context.Context) error {
	var shutdownErr error
	cs.stopOnce.Do(func() {
		log.Println("Shutting down concurrent server...")

		// Signal shutdown
		close(cs.shutdownChan)

		// Shutdown HTTP server
		if err := cs.httpServer.Shutdown(ctx); err != nil {
			shutdownErr = err
		}

		// Stop components in order
		if err := cs.requestQueue.Stop(5 * time.Second); err != nil {
			log.Printf("Error stopping request queue: %v", err)
		}

		if err := cs.workerPool.Stop(5 * time.Second); err != nil {
			log.Printf("Error stopping worker pool: %v", err)
		}

		if err := cs.connectionPool.Close(); err != nil {
			log.Printf("Error closing connection pool: %v", err)
		}

		if err := cs.rateLimiter.Close(); err != nil {
			log.Printf("Error closing rate limiter: %v", err)
		}

		if err := cs.circuitBreaker.Close(); err != nil {
			log.Printf("Error closing circuit breaker: %v", err)
		}

		if err := cs.loadBalancer.Close(); err != nil {
			log.Printf("Error closing load balancer: %v", err)
		}

		if cs.metricsCollector != nil {
			if err := cs.metricsCollector.Close(); err != nil {
				log.Printf("Error closing metrics collector: %v", err)
			}
		}

		// Wait for HTTP server shutdown
		cs.wg.Wait()
	})

	return shutdownErr
}
