package concurrency

import (
	"encoding/json"
	"net/http"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// MetricsCollector aggregates and exposes system metrics
type MetricsCollector struct {
	// System components
	workerPool     *WorkerPool
	connectionPool *ConnectionPool
	rateLimiter    *RateLimiter
	circuitBreaker *CircuitBreakerManager
	requestQueue   *RequestQueue
	loadBalancer   *LoadBalancer

	// HTTP metrics
	httpMetrics *HTTPMetrics

	// Custom metrics
	customMetrics map[string]*CustomMetric
	customMu      sync.RWMutex

	// Collection settings
	collectionInterval time.Duration
	retentionPeriod    time.Duration
	stopCollection     chan struct{}
	collectionWg       sync.WaitGroup

	// Historical data
	historyLock    sync.RWMutex
	metricsHistory []SystemSnapshot
	maxHistorySize int
}

// HTTPMetrics tracks HTTP-specific metrics
type HTTPMetrics struct {
	TotalRequests     int64            `json:"total_requests"`
	ActiveRequests    int64            `json:"active_requests"`
	CompletedRequests int64            `json:"completed_requests"`
	FailedRequests    int64            `json:"failed_requests"`
	RequestsPerSecond float64          `json:"requests_per_second"`
	AverageLatency    time.Duration    `json:"average_latency"`
	P95Latency        time.Duration    `json:"p95_latency"`
	P99Latency        time.Duration    `json:"p99_latency"`
	StatusCodes       map[int]int64    `json:"status_codes"`
	ResponseSizes     *HistogramMetric `json:"response_sizes"`
	RequestLatencies  *HistogramMetric `json:"request_latencies"`
	ErrorsByType      map[string]int64 `json:"errors_by_type"`

	// Ring buffer for latency calculation
	latencyBuffer []time.Duration
	bufferIndex   int
	bufferSize    int
	mu            sync.RWMutex
}

// CustomMetric represents a user-defined metric
type CustomMetric struct {
	Name        string            `json:"name"`
	Type        string            `json:"type"` // counter, gauge, histogram
	Value       int64             `json:"value"`
	Description string            `json:"description"`
	Labels      map[string]string `json:"labels,omitempty"`
	LastUpdated time.Time         `json:"last_updated"`
}

// HistogramMetric represents a histogram of values
type HistogramMetric struct {
	Buckets map[float64]int64 `json:"buckets"`
	Count   int64             `json:"count"`
	Sum     float64           `json:"sum"`
	mu      sync.RWMutex
}

// SystemSnapshot represents a point-in-time view of system metrics
type SystemSnapshot struct {
	Timestamp      time.Time                `json:"timestamp"`
	WorkerPool     *WorkerPoolStats         `json:"worker_pool,omitempty"`
	ConnectionPool *ConnectionMetrics       `json:"connection_pool,omitempty"`
	RateLimiter    *RateLimiterStats        `json:"rate_limiter,omitempty"`
	CircuitBreaker *CircuitBreakerStats     `json:"circuit_breaker,omitempty"`
	RequestQueue   *QueueStats              `json:"request_queue,omitempty"`
	LoadBalancer   *LoadBalancerStats       `json:"load_balancer,omitempty"`
	HTTP           *HTTPMetrics             `json:"http"`
	CustomMetrics  map[string]*CustomMetric `json:"custom_metrics,omitempty"`
	SystemHealth   *SystemHealthMetrics     `json:"system_health"`
}

// SystemHealthMetrics provides overall system health indicators
type SystemHealthMetrics struct {
	OverallScore    float64               `json:"overall_score"`    // 0-1 health score
	ComponentHealth map[string]float64    `json:"component_health"` // Per-component health
	AlertsActive    []string              `json:"alerts_active"`    // Active alerts
	ResourceUsage   *ResourceUsageMetrics `json:"resource_usage"`
	ThroughputScore float64               `json:"throughput_score"`
	LatencyScore    float64               `json:"latency_score"`
	ErrorRateScore  float64               `json:"error_rate_score"`
}

// ResourceUsageMetrics tracks system resource utilization
type ResourceUsageMetrics struct {
	CPUPercent     float64 `json:"cpu_percent"`
	MemoryUsedMB   int64   `json:"memory_used_mb"`
	MemoryTotalMB  int64   `json:"memory_total_mb"`
	GoroutineCount int     `json:"goroutine_count"`
	GCPauseMs      float64 `json:"gc_pause_ms"`
	OpenFileCount  int     `json:"open_file_count"`
}

// MetricsConfig holds configuration for the metrics collector
type MetricsConfig struct {
	CollectionInterval time.Duration
	RetentionPeriod    time.Duration
	MaxHistorySize     int
	HTTPLatencyBuffer  int
}

// NewMetricsCollector creates a new metrics collector
func NewMetricsCollector(config MetricsConfig) *MetricsCollector {
	if config.CollectionInterval <= 0 {
		config.CollectionInterval = 10 * time.Second
	}
	if config.RetentionPeriod <= 0 {
		config.RetentionPeriod = 1 * time.Hour
	}
	if config.MaxHistorySize <= 0 {
		config.MaxHistorySize = 360 // 1 hour at 10s intervals
	}
	if config.HTTPLatencyBuffer <= 0 {
		config.HTTPLatencyBuffer = 1000
	}

	mc := &MetricsCollector{
		customMetrics:      make(map[string]*CustomMetric),
		collectionInterval: config.CollectionInterval,
		retentionPeriod:    config.RetentionPeriod,
		maxHistorySize:     config.MaxHistorySize,
		stopCollection:     make(chan struct{}),
		metricsHistory:     make([]SystemSnapshot, 0, config.MaxHistorySize),
		httpMetrics: &HTTPMetrics{
			StatusCodes:      make(map[int]int64),
			ErrorsByType:     make(map[string]int64),
			ResponseSizes:    NewHistogramMetric(),
			RequestLatencies: NewHistogramMetric(),
			latencyBuffer:    make([]time.Duration, config.HTTPLatencyBuffer),
			bufferSize:       config.HTTPLatencyBuffer,
		},
	}

	// Start collection routine
	mc.startCollection()

	return mc
}

// NewHistogramMetric creates a new histogram metric
func NewHistogramMetric() *HistogramMetric {
	return &HistogramMetric{
		Buckets: make(map[float64]int64),
	}
}

// RegisterComponents registers system components for monitoring
func (mc *MetricsCollector) RegisterComponents(
	workerPool *WorkerPool,
	connectionPool *ConnectionPool,
	rateLimiter *RateLimiter,
	circuitBreaker *CircuitBreakerManager,
	requestQueue *RequestQueue,
	loadBalancer *LoadBalancer,
) {
	mc.workerPool = workerPool
	mc.connectionPool = connectionPool
	mc.rateLimiter = rateLimiter
	mc.circuitBreaker = circuitBreaker
	mc.requestQueue = requestQueue
	mc.loadBalancer = loadBalancer
}

// RecordHTTPRequest records metrics for an HTTP request
func (mc *MetricsCollector) RecordHTTPRequest(statusCode int, latency time.Duration, responseSize int64, errorType string) {
	atomic.AddInt64(&mc.httpMetrics.TotalRequests, 1)
	atomic.AddInt64(&mc.httpMetrics.CompletedRequests, 1)

	if statusCode >= 400 {
		atomic.AddInt64(&mc.httpMetrics.FailedRequests, 1)
		if errorType != "" {
			mc.httpMetrics.mu.Lock()
			mc.httpMetrics.ErrorsByType[errorType]++
			mc.httpMetrics.mu.Unlock()
		}
	}

	// Record status code
	mc.httpMetrics.mu.Lock()
	mc.httpMetrics.StatusCodes[statusCode]++
	mc.httpMetrics.mu.Unlock()

	// Record latency
	mc.recordLatency(latency)

	// Record response size
	mc.httpMetrics.ResponseSizes.Record(float64(responseSize))
	mc.httpMetrics.RequestLatencies.Record(float64(latency.Nanoseconds()))
}

// StartHTTPRequest marks the start of an HTTP request
func (mc *MetricsCollector) StartHTTPRequest() {
	atomic.AddInt64(&mc.httpMetrics.ActiveRequests, 1)
}

// EndHTTPRequest marks the end of an HTTP request
func (mc *MetricsCollector) EndHTTPRequest() {
	atomic.AddInt64(&mc.httpMetrics.ActiveRequests, -1)
}

// recordLatency records a latency measurement
func (mc *MetricsCollector) recordLatency(latency time.Duration) {
	mc.httpMetrics.mu.Lock()
	defer mc.httpMetrics.mu.Unlock()

	mc.httpMetrics.latencyBuffer[mc.httpMetrics.bufferIndex] = latency
	mc.httpMetrics.bufferIndex = (mc.httpMetrics.bufferIndex + 1) % mc.httpMetrics.bufferSize
}

// calculateLatencyPercentiles calculates latency percentiles from the buffer
func (mc *MetricsCollector) calculateLatencyPercentiles() {
	mc.httpMetrics.mu.Lock()
	defer mc.httpMetrics.mu.Unlock()

	// Filter out zero values and sort
	validLatencies := make([]time.Duration, 0)
	var totalLatency time.Duration

	for _, latency := range mc.httpMetrics.latencyBuffer {
		if latency > 0 {
			validLatencies = append(validLatencies, latency)
			totalLatency += latency
		}
	}

	if len(validLatencies) == 0 {
		return
	}

	sort.Slice(validLatencies, func(i, j int) bool {
		return validLatencies[i] < validLatencies[j]
	})

	// Calculate average
	mc.httpMetrics.AverageLatency = totalLatency / time.Duration(len(validLatencies))

	// Calculate percentiles
	p95Index := int(float64(len(validLatencies)) * 0.95)
	p99Index := int(float64(len(validLatencies)) * 0.99)

	if p95Index < len(validLatencies) {
		mc.httpMetrics.P95Latency = validLatencies[p95Index]
	}
	if p99Index < len(validLatencies) {
		mc.httpMetrics.P99Latency = validLatencies[p99Index]
	}
}

// Record adds a value to the histogram
func (h *HistogramMetric) Record(value float64) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.Count++
	h.Sum += value

	// Define bucket boundaries (can be customized)
	buckets := []float64{1, 5, 10, 25, 50, 100, 250, 500, 1000, 2500, 5000, 10000}

	for _, bucket := range buckets {
		if value <= bucket {
			h.Buckets[bucket]++
			break
		}
	}

	// Add to +Inf bucket
	h.Buckets[float64(999999999)]++
}

// SetCustomMetric sets a custom metric value
func (mc *MetricsCollector) SetCustomMetric(name, metricType, description string, value int64, labels map[string]string) {
	mc.customMu.Lock()
	defer mc.customMu.Unlock()

	mc.customMetrics[name] = &CustomMetric{
		Name:        name,
		Type:        metricType,
		Value:       value,
		Description: description,
		Labels:      labels,
		LastUpdated: time.Now(),
	}
}

// IncrementCustomMetric increments a custom counter metric
func (mc *MetricsCollector) IncrementCustomMetric(name string, delta int64) {
	mc.customMu.Lock()
	defer mc.customMu.Unlock()

	if metric, exists := mc.customMetrics[name]; exists {
		metric.Value += delta
		metric.LastUpdated = time.Now()
	}
}

// GetCurrentSnapshot returns a current snapshot of all metrics
func (mc *MetricsCollector) GetCurrentSnapshot() *SystemSnapshot {
	snapshot := &SystemSnapshot{
		Timestamp:     time.Now(),
		HTTP:          mc.getHTTPMetrics(),
		SystemHealth:  mc.calculateSystemHealth(),
		CustomMetrics: mc.getCustomMetrics(),
	}

	// Collect component metrics if available
	if mc.workerPool != nil {
		stats := mc.workerPool.Stats()
		snapshot.WorkerPool = &stats
	}

	if mc.connectionPool != nil {
		snapshot.ConnectionPool = mc.connectionPool.GetStats()
	}

	if mc.rateLimiter != nil {
		stats := mc.rateLimiter.GetStats()
		snapshot.RateLimiter = &stats
	}

	if mc.circuitBreaker != nil {
		snapshot.CircuitBreaker = mc.circuitBreaker.GetStats()
	}

	if mc.requestQueue != nil {
		snapshot.RequestQueue = mc.requestQueue.GetStats()
	}

	if mc.loadBalancer != nil {
		snapshot.LoadBalancer = mc.loadBalancer.GetStats()
	}

	return snapshot
}

// getHTTPMetrics returns current HTTP metrics
func (mc *MetricsCollector) getHTTPMetrics() *HTTPMetrics {
	mc.calculateLatencyPercentiles()

	mc.httpMetrics.mu.RLock()
	defer mc.httpMetrics.mu.RUnlock()

	// Calculate requests per second
	total := atomic.LoadInt64(&mc.httpMetrics.TotalRequests)
	if total > 0 {
		// Simple calculation based on collection interval
		mc.httpMetrics.RequestsPerSecond = float64(total) / mc.collectionInterval.Seconds()
	}

	// Create a copy to avoid data races
	statusCodes := make(map[int]int64)
	for code, count := range mc.httpMetrics.StatusCodes {
		statusCodes[code] = count
	}

	errorsByType := make(map[string]int64)
	for errorType, count := range mc.httpMetrics.ErrorsByType {
		errorsByType[errorType] = count
	}

	return &HTTPMetrics{
		TotalRequests:     atomic.LoadInt64(&mc.httpMetrics.TotalRequests),
		ActiveRequests:    atomic.LoadInt64(&mc.httpMetrics.ActiveRequests),
		CompletedRequests: atomic.LoadInt64(&mc.httpMetrics.CompletedRequests),
		FailedRequests:    atomic.LoadInt64(&mc.httpMetrics.FailedRequests),
		RequestsPerSecond: mc.httpMetrics.RequestsPerSecond,
		AverageLatency:    mc.httpMetrics.AverageLatency,
		P95Latency:        mc.httpMetrics.P95Latency,
		P99Latency:        mc.httpMetrics.P99Latency,
		StatusCodes:       statusCodes,
		ErrorsByType:      errorsByType,
		ResponseSizes:     mc.httpMetrics.ResponseSizes,
		RequestLatencies:  mc.httpMetrics.RequestLatencies,
	}
}

// getCustomMetrics returns a copy of custom metrics
func (mc *MetricsCollector) getCustomMetrics() map[string]*CustomMetric {
	mc.customMu.RLock()
	defer mc.customMu.RUnlock()

	result := make(map[string]*CustomMetric)
	for name, metric := range mc.customMetrics {
		// Create a copy
		labels := make(map[string]string)
		for k, v := range metric.Labels {
			labels[k] = v
		}

		result[name] = &CustomMetric{
			Name:        metric.Name,
			Type:        metric.Type,
			Value:       metric.Value,
			Description: metric.Description,
			Labels:      labels,
			LastUpdated: metric.LastUpdated,
		}
	}

	return result
}

// calculateSystemHealth calculates overall system health metrics
func (mc *MetricsCollector) calculateSystemHealth() *SystemHealthMetrics {
	health := &SystemHealthMetrics{
		ComponentHealth: make(map[string]float64),
		AlertsActive:    make([]string, 0),
		ResourceUsage:   mc.getResourceUsage(),
	}

	var totalScore float64
	componentCount := 0

	// Worker pool health
	if mc.workerPool != nil {
		stats := mc.workerPool.Stats()
		score := mc.calculateWorkerPoolHealth(&stats)
		health.ComponentHealth["worker_pool"] = score
		totalScore += score
		componentCount++
	}

	// Load balancer health
	if mc.loadBalancer != nil {
		stats := mc.loadBalancer.GetStats()
		score := mc.calculateLoadBalancerHealth(stats)
		health.ComponentHealth["load_balancer"] = score
		totalScore += score
		componentCount++
	}

	// HTTP performance health
	httpScore := mc.calculateHTTPHealth()
	health.ComponentHealth["http"] = httpScore
	totalScore += httpScore
	componentCount++

	// Calculate individual scores
	health.ThroughputScore = mc.calculateThroughputScore()
	health.LatencyScore = mc.calculateLatencyScore()
	health.ErrorRateScore = mc.calculateErrorRateScore()

	// Overall health score
	if componentCount > 0 {
		health.OverallScore = totalScore / float64(componentCount)
	}

	// Generate alerts
	health.AlertsActive = mc.generateAlerts(health)

	return health
}

// calculateWorkerPoolHealth calculates worker pool health score (0-1)
func (mc *MetricsCollector) calculateWorkerPoolHealth(stats *WorkerPoolStats) float64 {
	if !stats.IsRunning {
		return 0.0
	}

	// Queue utilization (lower is better)
	queueUtilization := float64(stats.QueueLength) / float64(stats.MaxQueue)
	queueScore := 1.0 - queueUtilization

	// Success rate
	successRate := 1.0
	if stats.TotalJobs > 0 {
		successRate = float64(stats.TotalJobs-stats.FailedJobs) / float64(stats.TotalJobs)
	}

	// Worker utilization (optimal around 70-80%)
	workerUtilization := float64(stats.ActiveJobs) / float64(stats.Workers)
	workerScore := 1.0
	if workerUtilization > 0.8 {
		workerScore = 1.0 - (workerUtilization-0.8)*2 // Penalty for over-utilization
	}

	return (queueScore + successRate + workerScore) / 3.0
}

// calculateLoadBalancerHealth calculates load balancer health score (0-1)
func (mc *MetricsCollector) calculateLoadBalancerHealth(stats *LoadBalancerStats) float64 {
	if stats.TotalInstances == 0 {
		return 0.0
	}

	// Healthy instance ratio
	healthyRatio := float64(stats.HealthyInstances) / float64(stats.TotalInstances)

	// Average instance score
	avgScore := stats.AverageScore

	return (healthyRatio + avgScore) / 2.0
}

// calculateHTTPHealth calculates HTTP performance health score (0-1)
func (mc *MetricsCollector) calculateHTTPHealth() float64 {
	return (mc.calculateThroughputScore() + mc.calculateLatencyScore() + mc.calculateErrorRateScore()) / 3.0
}

// calculateThroughputScore calculates throughput health score (0-1)
func (mc *MetricsCollector) calculateThroughputScore() float64 {
	// Simple heuristic: normalize RPS to a reasonable scale
	rps := mc.httpMetrics.RequestsPerSecond
	if rps >= 100 {
		return 1.0
	} else if rps >= 10 {
		return rps / 100.0
	} else {
		return rps / 10.0
	}
}

// calculateLatencyScore calculates latency health score (0-1)
func (mc *MetricsCollector) calculateLatencyScore() float64 {
	p95 := mc.httpMetrics.P95Latency
	if p95 == 0 {
		return 1.0 // No data, assume good
	}

	// Score based on P95 latency thresholds
	if p95 <= 100*time.Millisecond {
		return 1.0
	} else if p95 <= 500*time.Millisecond {
		return 0.8
	} else if p95 <= 1*time.Second {
		return 0.6
	} else if p95 <= 2*time.Second {
		return 0.4
	} else if p95 <= 5*time.Second {
		return 0.2
	} else {
		return 0.1
	}
}

// calculateErrorRateScore calculates error rate health score (0-1)
func (mc *MetricsCollector) calculateErrorRateScore() float64 {
	total := atomic.LoadInt64(&mc.httpMetrics.TotalRequests)
	failed := atomic.LoadInt64(&mc.httpMetrics.FailedRequests)

	if total == 0 {
		return 1.0 // No data, assume good
	}

	errorRate := float64(failed) / float64(total)

	// Score based on error rate thresholds
	if errorRate <= 0.01 { // <= 1%
		return 1.0
	} else if errorRate <= 0.05 { // <= 5%
		return 0.8
	} else if errorRate <= 0.10 { // <= 10%
		return 0.6
	} else if errorRate <= 0.20 { // <= 20%
		return 0.4
	} else {
		return 0.2
	}
}

// getResourceUsage gets current system resource usage
func (mc *MetricsCollector) getResourceUsage() *ResourceUsageMetrics {
	// This would typically integrate with system monitoring
	// For now, return basic Go runtime stats
	return &ResourceUsageMetrics{
		GoroutineCount: 0, // Would use runtime.NumGoroutine()
		// Other fields would be populated with actual system metrics
	}
}

// generateAlerts generates active alerts based on health metrics
func (mc *MetricsCollector) generateAlerts(health *SystemHealthMetrics) []string {
	var alerts []string

	if health.OverallScore < 0.5 {
		alerts = append(alerts, "CRITICAL: Overall system health is poor")
	}

	if health.ErrorRateScore < 0.6 {
		alerts = append(alerts, "WARNING: High error rate detected")
	}

	if health.LatencyScore < 0.4 {
		alerts = append(alerts, "WARNING: High latency detected")
	}

	if health.ComponentHealth["worker_pool"] < 0.3 {
		alerts = append(alerts, "CRITICAL: Worker pool health is critical")
	}

	return alerts
}

// startCollection starts the metrics collection routine
func (mc *MetricsCollector) startCollection() {
	mc.collectionWg.Add(1)
	go func() {
		defer mc.collectionWg.Done()

		ticker := time.NewTicker(mc.collectionInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				snapshot := mc.GetCurrentSnapshot()
				mc.addToHistory(snapshot)
			case <-mc.stopCollection:
				return
			}
		}
	}()
}

// addToHistory adds a snapshot to the metrics history
func (mc *MetricsCollector) addToHistory(snapshot *SystemSnapshot) {
	mc.historyLock.Lock()
	defer mc.historyLock.Unlock()

	mc.metricsHistory = append(mc.metricsHistory, *snapshot)

	// Remove old entries if we exceed max size
	if len(mc.metricsHistory) > mc.maxHistorySize {
		mc.metricsHistory = mc.metricsHistory[1:]
	}

	// Remove entries older than retention period
	cutoff := time.Now().Add(-mc.retentionPeriod)
	for i := 0; i < len(mc.metricsHistory); i++ {
		if mc.metricsHistory[i].Timestamp.After(cutoff) {
			mc.metricsHistory = mc.metricsHistory[i:]
			break
		}
	}
}

// GetHistory returns historical metrics data
func (mc *MetricsCollector) GetHistory(since time.Time) []SystemSnapshot {
	mc.historyLock.RLock()
	defer mc.historyLock.RUnlock()

	var result []SystemSnapshot
	for _, snapshot := range mc.metricsHistory {
		if snapshot.Timestamp.After(since) {
			result = append(result, snapshot)
		}
	}

	return result
}

// ServeHTTP provides an HTTP endpoint for metrics
func (mc *MetricsCollector) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	switch r.URL.Path {
	case "/metrics":
		snapshot := mc.GetCurrentSnapshot()
		json.NewEncoder(w).Encode(snapshot)
	case "/metrics/history":
		since := time.Now().Add(-1 * time.Hour) // Default to last hour
		if sinceParam := r.URL.Query().Get("since"); sinceParam != "" {
			if parsedTime, err := time.Parse(time.RFC3339, sinceParam); err == nil {
				since = parsedTime
			}
		}
		history := mc.GetHistory(since)
		json.NewEncoder(w).Encode(history)
	case "/health":
		snapshot := mc.GetCurrentSnapshot()
		json.NewEncoder(w).Encode(snapshot.SystemHealth)
	default:
		http.NotFound(w, r)
	}
}

// Close shuts down the metrics collector
func (mc *MetricsCollector) Close() error {
	close(mc.stopCollection)
	mc.collectionWg.Wait()

	mc.customMu.Lock()
	mc.customMetrics = make(map[string]*CustomMetric)
	mc.customMu.Unlock()

	return nil
}
