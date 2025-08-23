package concurrency

import (
	"fmt"
	"math"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// LoadBalancingStrategy defines different load balancing algorithms
type LoadBalancingStrategy int

const (
	RoundRobin LoadBalancingStrategy = iota
	WeightedRoundRobin
	LeastConnections
	LeastResponseTime
	ConsistentHash
	RandomChoice
)

// InstanceHealth represents the health status of a backend instance
type InstanceHealth struct {
	URL               string
	Available         bool
	Weight            int           // Weight for weighted algorithms
	ActiveConnections int64         // Current active connections
	TotalConnections  int64         // Total connections served
	TotalRequests     int64         // Total requests served
	FailureCount      int64         // Consecutive failures
	SuccessCount      int64         // Consecutive successes
	LastRequest       time.Time     // Last request time
	LastFailure       time.Time     // Last failure time
	ResponseTime      time.Duration // Average response time
	CooldownUntil     time.Time     // Cooldown end time
	CircuitOpen       bool          // Circuit breaker state
	Score             float64       // Calculated health score (0-1)

	// Detailed metrics
	Metrics *InstanceMetrics
}

// InstanceMetrics contains detailed performance metrics
type InstanceMetrics struct {
	RequestsPerSecond   float64         `json:"requests_per_second"`
	AverageResponseTime time.Duration   `json:"average_response_time"`
	P95ResponseTime     time.Duration   `json:"p95_response_time"`
	P99ResponseTime     time.Duration   `json:"p99_response_time"`
	ErrorRate           float64         `json:"error_rate"`
	TimeoutRate         float64         `json:"timeout_rate"`
	LastResponseTimes   []time.Duration // Ring buffer for response times
	ResponseTimeIndex   int             // Index in ring buffer
	mu                  sync.RWMutex
}

// LoadBalancer manages backend instances with various load balancing strategies
type LoadBalancer struct {
	instances    map[string]*InstanceHealth
	strategy     LoadBalancingStrategy
	currentIndex int64 // For round-robin
	mu           sync.RWMutex

	// Health monitoring
	healthCheckInterval time.Duration
	unhealthyThreshold  int64
	healthyThreshold    int64
	responseTimeWindow  int
	stopHealthCheck     chan struct{}
	healthCheckWg       sync.WaitGroup

	// Circuit breaker integration
	circuitBreaker *CircuitBreakerManager

	// Performance tracking
	stats *LoadBalancerStats
}

// LoadBalancerStats tracks load balancer performance
type LoadBalancerStats struct {
	TotalInstances     int64            `json:"total_instances"`
	HealthyInstances   int64            `json:"healthy_instances"`
	UnhealthyInstances int64            `json:"unhealthy_instances"`
	TotalRequests      int64            `json:"total_requests"`
	DistributionMap    map[string]int64 `json:"distribution_map"`
	AverageScore       float64          `json:"average_score"`
	mu                 sync.RWMutex
}

// LoadBalancerConfig holds configuration for the load balancer
type LoadBalancerConfig struct {
	Strategy            LoadBalancingStrategy
	HealthCheckInterval time.Duration
	UnhealthyThreshold  int64 // Failures before marking unhealthy
	HealthyThreshold    int64 // Successes before marking healthy
	ResponseTimeWindow  int   // Number of response times to track
	CircuitBreaker      *CircuitBreakerManager
}

// NewLoadBalancer creates a new load balancer
func NewLoadBalancer(config LoadBalancerConfig) *LoadBalancer {
	if config.HealthCheckInterval <= 0 {
		config.HealthCheckInterval = 30 * time.Second
	}
	if config.UnhealthyThreshold <= 0 {
		config.UnhealthyThreshold = 3
	}
	if config.HealthyThreshold <= 0 {
		config.HealthyThreshold = 2
	}
	if config.ResponseTimeWindow <= 0 {
		config.ResponseTimeWindow = 100
	}

	lb := &LoadBalancer{
		instances:           make(map[string]*InstanceHealth),
		strategy:            config.Strategy,
		healthCheckInterval: config.HealthCheckInterval,
		unhealthyThreshold:  config.UnhealthyThreshold,
		healthyThreshold:    config.HealthyThreshold,
		responseTimeWindow:  config.ResponseTimeWindow,
		circuitBreaker:      config.CircuitBreaker,
		stopHealthCheck:     make(chan struct{}),
		stats:               &LoadBalancerStats{DistributionMap: make(map[string]int64)},
	}

	// Start health monitoring
	lb.startHealthMonitoring()

	return lb
}

// AddInstance adds a new backend instance
func (lb *LoadBalancer) AddInstance(url string, weight int) {
	lb.mu.Lock()
	defer lb.mu.Unlock()

	if weight <= 0 {
		weight = 1
	}

	lb.instances[url] = &InstanceHealth{
		URL:       url,
		Available: true,
		Weight:    weight,
		Score:     1.0, // Start with perfect score
		Metrics: &InstanceMetrics{
			LastResponseTimes: make([]time.Duration, lb.responseTimeWindow),
		},
	}

	atomic.AddInt64(&lb.stats.TotalInstances, 1)
	atomic.AddInt64(&lb.stats.HealthyInstances, 1)

	lb.stats.mu.Lock()
	lb.stats.DistributionMap[url] = 0
	lb.stats.mu.Unlock()
}

// RemoveInstance removes a backend instance
func (lb *LoadBalancer) RemoveInstance(url string) bool {
	lb.mu.Lock()
	defer lb.mu.Unlock()

	instance, exists := lb.instances[url]
	if !exists {
		return false
	}

	delete(lb.instances, url)

	atomic.AddInt64(&lb.stats.TotalInstances, -1)
	if instance.Available {
		atomic.AddInt64(&lb.stats.HealthyInstances, -1)
	} else {
		atomic.AddInt64(&lb.stats.UnhealthyInstances, -1)
	}

	lb.stats.mu.Lock()
	delete(lb.stats.DistributionMap, url)
	lb.stats.mu.Unlock()

	return true
}

// GetNextInstance returns the next instance according to the load balancing strategy
func (lb *LoadBalancer) GetNextInstance() (*InstanceHealth, error) {
	lb.mu.RLock()
	defer lb.mu.RUnlock()

	// Filter available instances
	var availableInstances []*InstanceHealth
	now := time.Now()

	for _, instance := range lb.instances {
		if instance.Available && !instance.CircuitOpen && now.After(instance.CooldownUntil) {
			availableInstances = append(availableInstances, instance)
		}
	}

	if len(availableInstances) == 0 {
		return nil, fmt.Errorf("no healthy instances available")
	}

	atomic.AddInt64(&lb.stats.TotalRequests, 1)

	var selected *InstanceHealth

	switch lb.strategy {
	case RoundRobin:
		selected = lb.roundRobin(availableInstances)
	case WeightedRoundRobin:
		selected = lb.weightedRoundRobin(availableInstances)
	case LeastConnections:
		selected = lb.leastConnections(availableInstances)
	case LeastResponseTime:
		selected = lb.leastResponseTime(availableInstances)
	case ConsistentHash:
		selected = lb.consistentHash(availableInstances)
	case RandomChoice:
		selected = lb.randomChoice(availableInstances)
	default:
		selected = lb.roundRobin(availableInstances)
	}

	if selected != nil {
		atomic.AddInt64(&selected.ActiveConnections, 1)
		atomic.AddInt64(&selected.TotalConnections, 1)
		selected.LastRequest = now

		lb.stats.mu.Lock()
		lb.stats.DistributionMap[selected.URL]++
		lb.stats.mu.Unlock()
	}

	return selected, nil
}

// roundRobin implements round-robin load balancing
func (lb *LoadBalancer) roundRobin(instances []*InstanceHealth) *InstanceHealth {
	if len(instances) == 0 {
		return nil
	}

	index := atomic.AddInt64(&lb.currentIndex, 1) - 1
	return instances[index%int64(len(instances))]
}

// weightedRoundRobin implements weighted round-robin load balancing
func (lb *LoadBalancer) weightedRoundRobin(instances []*InstanceHealth) *InstanceHealth {
	if len(instances) == 0 {
		return nil
	}

	// Calculate total weight
	totalWeight := 0
	for _, instance := range instances {
		// Adjust weight based on health score
		effectiveWeight := int(float64(instance.Weight) * instance.Score)
		totalWeight += effectiveWeight
	}

	if totalWeight == 0 {
		return lb.roundRobin(instances) // Fallback
	}

	// Select based on weight
	index := atomic.AddInt64(&lb.currentIndex, 1) % int64(totalWeight)
	currentWeight := int64(0)

	for _, instance := range instances {
		effectiveWeight := int64(float64(instance.Weight) * instance.Score)
		currentWeight += effectiveWeight
		if index < currentWeight {
			return instance
		}
	}

	return instances[0] // Fallback
}

// leastConnections implements least connections load balancing
func (lb *LoadBalancer) leastConnections(instances []*InstanceHealth) *InstanceHealth {
	if len(instances) == 0 {
		return nil
	}

	var selected *InstanceHealth
	minConnections := int64(math.MaxInt64)

	for _, instance := range instances {
		// Adjust connections based on health score (lower score = higher effective connections)
		effectiveConnections := int64(float64(atomic.LoadInt64(&instance.ActiveConnections)) / math.Max(instance.Score, 0.1))

		if effectiveConnections < minConnections {
			minConnections = effectiveConnections
			selected = instance
		}
	}

	return selected
}

// leastResponseTime implements least response time load balancing
func (lb *LoadBalancer) leastResponseTime(instances []*InstanceHealth) *InstanceHealth {
	if len(instances) == 0 {
		return nil
	}

	var selected *InstanceHealth
	minResponseTime := time.Duration(math.MaxInt64)

	for _, instance := range instances {
		responseTime := instance.ResponseTime
		if responseTime == 0 {
			responseTime = 1 * time.Millisecond // Default for new instances
		}

		// Adjust response time based on health score (lower score = higher effective response time)
		effectiveResponseTime := time.Duration(float64(responseTime) / math.Max(instance.Score, 0.1))

		if effectiveResponseTime < minResponseTime {
			minResponseTime = effectiveResponseTime
			selected = instance
		}
	}

	return selected
}

// consistentHash implements consistent hashing (simplified version)
func (lb *LoadBalancer) consistentHash(instances []*InstanceHealth) *InstanceHealth {
	if len(instances) == 0 {
		return nil
	}

	// Simple hash based on timestamp and number of instances
	now := time.Now().UnixNano()
	index := now % int64(len(instances))
	return instances[index]
}

// randomChoice implements random load balancing with health score weighting
func (lb *LoadBalancer) randomChoice(instances []*InstanceHealth) *InstanceHealth {
	if len(instances) == 0 {
		return nil
	}

	// Weight by health score
	totalScore := 0.0
	for _, instance := range instances {
		totalScore += instance.Score
	}

	if totalScore == 0 {
		return instances[0] // Fallback
	}

	// Random selection weighted by score
	target := totalScore * float64(time.Now().UnixNano()%1000) / 1000.0
	currentScore := 0.0

	for _, instance := range instances {
		currentScore += instance.Score
		if target <= currentScore {
			return instance
		}
	}

	return instances[len(instances)-1] // Fallback
}

// RecordSuccess records a successful request for an instance
func (lb *LoadBalancer) RecordSuccess(url string, responseTime time.Duration) {
	lb.mu.Lock()
	defer lb.mu.Unlock()

	instance, exists := lb.instances[url]
	if !exists {
		return
	}

	atomic.AddInt64(&instance.ActiveConnections, -1)
	atomic.AddInt64(&instance.TotalRequests, 1)
	atomic.AddInt64(&instance.SuccessCount, 1)
	atomic.StoreInt64(&instance.FailureCount, 0) // Reset failure count on success

	// Update response time metrics
	instance.Metrics.mu.Lock()
	instance.Metrics.LastResponseTimes[instance.Metrics.ResponseTimeIndex] = responseTime
	instance.Metrics.ResponseTimeIndex = (instance.Metrics.ResponseTimeIndex + 1) % len(instance.Metrics.LastResponseTimes)
	instance.Metrics.mu.Unlock()

	// Recalculate health score
	lb.updateHealthScore(instance)

	// Mark as healthy if it was unhealthy
	if !instance.Available && instance.SuccessCount >= lb.healthyThreshold {
		instance.Available = true
		instance.CircuitOpen = false
		instance.CooldownUntil = time.Time{}

		atomic.AddInt64(&lb.stats.HealthyInstances, 1)
		atomic.AddInt64(&lb.stats.UnhealthyInstances, -1)
	}
}

// RecordFailure records a failed request for an instance
func (lb *LoadBalancer) RecordFailure(url string, err error) {
	lb.mu.Lock()
	defer lb.mu.Unlock()

	instance, exists := lb.instances[url]
	if !exists {
		return
	}

	atomic.AddInt64(&instance.ActiveConnections, -1)
	atomic.AddInt64(&instance.FailureCount, 1)
	atomic.StoreInt64(&instance.SuccessCount, 0) // Reset success count on failure
	instance.LastFailure = time.Now()

	// Update health score
	lb.updateHealthScore(instance)

	// Mark as unhealthy if too many failures
	if instance.Available && instance.FailureCount >= lb.unhealthyThreshold {
		instance.Available = false

		// Set cooldown period with exponential backoff
		cooldownDuration := time.Duration(math.Pow(2, float64(instance.FailureCount))) * time.Second
		if cooldownDuration > 5*time.Minute {
			cooldownDuration = 5 * time.Minute
		}
		instance.CooldownUntil = time.Now().Add(cooldownDuration)

		atomic.AddInt64(&lb.stats.HealthyInstances, -1)
		atomic.AddInt64(&lb.stats.UnhealthyInstances, 1)
	}

	// Circuit breaker integration
	if lb.circuitBreaker != nil {
		lb.circuitBreaker.Execute(url, func() error { return err })
	}
}

// updateHealthScore calculates and updates the health score for an instance
func (lb *LoadBalancer) updateHealthScore(instance *InstanceHealth) {
	// Factors for health score calculation
	successRate := 1.0
	if totalReqs := instance.TotalRequests; totalReqs > 0 {
		successRate = float64(instance.SuccessCount) / float64(totalReqs)
	}

	responseTimeFactor := 1.0
	if instance.ResponseTime > 0 {
		// Normalize response time (1s = 0.5 factor, 100ms = 0.9 factor)
		responseTimeFactor = math.Max(0.1, 1.0-(float64(instance.ResponseTime)/float64(time.Second))*0.5)
	}

	loadFactor := 1.0
	if connections := atomic.LoadInt64(&instance.ActiveConnections); connections > 0 {
		// Penalize high load (100 connections = 0.5 factor)
		loadFactor = math.Max(0.1, 1.0-float64(connections)/200.0)
	}

	// Combine factors
	instance.Score = successRate * responseTimeFactor * loadFactor

	// Apply penalties for recent failures
	if time.Since(instance.LastFailure) < time.Minute {
		instance.Score *= 0.5 // 50% penalty for recent failure
	}

	// Ensure score is between 0 and 1
	if instance.Score < 0 {
		instance.Score = 0
	} else if instance.Score > 1 {
		instance.Score = 1
	}
}

// startHealthMonitoring starts the health monitoring routine
func (lb *LoadBalancer) startHealthMonitoring() {
	lb.healthCheckWg.Add(1)
	go func() {
		defer lb.healthCheckWg.Done()

		ticker := time.NewTicker(lb.healthCheckInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				lb.performHealthCheck()
			case <-lb.stopHealthCheck:
				return
			}
		}
	}()
}

// performHealthCheck performs health checks on all instances
func (lb *LoadBalancer) performHealthCheck() {
	lb.mu.Lock()
	instances := make([]*InstanceHealth, 0, len(lb.instances))
	for _, instance := range lb.instances {
		instances = append(instances, instance)
	}
	lb.mu.Unlock()

	var totalScore float64
	healthyCount := int64(0)

	for _, instance := range instances {
		// Update response time metrics
		lb.updateResponseTimeMetrics(instance)

		// Update health score
		lb.updateHealthScore(instance)
		totalScore += instance.Score

		if instance.Available {
			healthyCount++
		}
	}

	// Update global stats
	lb.stats.mu.Lock()
	if len(instances) > 0 {
		lb.stats.AverageScore = totalScore / float64(len(instances))
	}
	lb.stats.mu.Unlock()

	atomic.StoreInt64(&lb.stats.HealthyInstances, healthyCount)
	atomic.StoreInt64(&lb.stats.UnhealthyInstances, int64(len(instances))-healthyCount)
}

// updateResponseTimeMetrics updates response time metrics for an instance
func (lb *LoadBalancer) updateResponseTimeMetrics(instance *InstanceHealth) {
	instance.Metrics.mu.Lock()
	defer instance.Metrics.mu.Unlock()

	responseTimes := instance.Metrics.LastResponseTimes
	validTimes := make([]time.Duration, 0)

	// Filter out zero values
	for _, rt := range responseTimes {
		if rt > 0 {
			validTimes = append(validTimes, rt)
		}
	}

	if len(validTimes) == 0 {
		return
	}

	// Calculate average
	var total time.Duration
	for _, rt := range validTimes {
		total += rt
	}
	instance.ResponseTime = total / time.Duration(len(validTimes))
	instance.Metrics.AverageResponseTime = instance.ResponseTime

	// Calculate percentiles
	sort.Slice(validTimes, func(i, j int) bool {
		return validTimes[i] < validTimes[j]
	})

	p95Index := int(float64(len(validTimes)) * 0.95)
	p99Index := int(float64(len(validTimes)) * 0.99)

	if p95Index < len(validTimes) {
		instance.Metrics.P95ResponseTime = validTimes[p95Index]
	}
	if p99Index < len(validTimes) {
		instance.Metrics.P99ResponseTime = validTimes[p99Index]
	}
}

// GetStats returns load balancer statistics
func (lb *LoadBalancer) GetStats() *LoadBalancerStats {
	lb.stats.mu.RLock()
	defer lb.stats.mu.RUnlock()

	// Create a copy to avoid race conditions
	distMap := make(map[string]int64)
	for k, v := range lb.stats.DistributionMap {
		distMap[k] = v
	}

	return &LoadBalancerStats{
		TotalInstances:     atomic.LoadInt64(&lb.stats.TotalInstances),
		HealthyInstances:   atomic.LoadInt64(&lb.stats.HealthyInstances),
		UnhealthyInstances: atomic.LoadInt64(&lb.stats.UnhealthyInstances),
		TotalRequests:      atomic.LoadInt64(&lb.stats.TotalRequests),
		DistributionMap:    distMap,
		AverageScore:       lb.stats.AverageScore,
	}
}

// GetInstanceHealth returns health information for all instances
func (lb *LoadBalancer) GetInstanceHealth() map[string]*InstanceHealth {
	lb.mu.RLock()
	defer lb.mu.RUnlock()

	result := make(map[string]*InstanceHealth)
	for url, instance := range lb.instances {
		// Create a copy to avoid data races
		result[url] = &InstanceHealth{
			URL:               instance.URL,
			Available:         instance.Available,
			Weight:            instance.Weight,
			ActiveConnections: atomic.LoadInt64(&instance.ActiveConnections),
			TotalConnections:  atomic.LoadInt64(&instance.TotalConnections),
			TotalRequests:     atomic.LoadInt64(&instance.TotalRequests),
			FailureCount:      atomic.LoadInt64(&instance.FailureCount),
			SuccessCount:      atomic.LoadInt64(&instance.SuccessCount),
			LastRequest:       instance.LastRequest,
			LastFailure:       instance.LastFailure,
			ResponseTime:      instance.ResponseTime,
			CooldownUntil:     instance.CooldownUntil,
			CircuitOpen:       instance.CircuitOpen,
			Score:             instance.Score,
		}
	}

	return result
}

// SetStrategy changes the load balancing strategy
func (lb *LoadBalancer) SetStrategy(strategy LoadBalancingStrategy) {
	lb.mu.Lock()
	defer lb.mu.Unlock()
	lb.strategy = strategy
}

// Close shuts down the load balancer
func (lb *LoadBalancer) Close() error {
	close(lb.stopHealthCheck)
	lb.healthCheckWg.Wait()

	lb.mu.Lock()
	defer lb.mu.Unlock()
	lb.instances = make(map[string]*InstanceHealth)

	return nil
}
