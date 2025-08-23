package concurrency

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestLoadBalancerCreation(t *testing.T) {
	tests := []struct {
		name   string
		config LoadBalancerConfig
		want   LoadBalancerConfig
	}{
		{
			name: "default configuration",
			config: LoadBalancerConfig{
				Strategy:            RoundRobin,
				HealthCheckInterval: 0,
				UnhealthyThreshold:  0,
				HealthyThreshold:    0,
				ResponseTimeWindow:  0,
			},
			want: LoadBalancerConfig{
				Strategy:            RoundRobin,
				HealthCheckInterval: 30 * time.Second,
				UnhealthyThreshold:  3,
				HealthyThreshold:    2,
				ResponseTimeWindow:  100,
			},
		},
		{
			name: "custom configuration",
			config: LoadBalancerConfig{
				Strategy:            LeastConnections,
				HealthCheckInterval: 10 * time.Second,
				UnhealthyThreshold:  5,
				HealthyThreshold:    3,
				ResponseTimeWindow:  50,
			},
			want: LoadBalancerConfig{
				Strategy:            LeastConnections,
				HealthCheckInterval: 10 * time.Second,
				UnhealthyThreshold:  5,
				HealthyThreshold:    3,
				ResponseTimeWindow:  50,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lb := NewLoadBalancer(tt.config)
			defer lb.Close()

			if lb.strategy != tt.want.Strategy {
				t.Errorf("strategy = %v, want %v", lb.strategy, tt.want.Strategy)
			}
			if lb.healthCheckInterval != tt.want.HealthCheckInterval {
				t.Errorf("healthCheckInterval = %v, want %v", lb.healthCheckInterval, tt.want.HealthCheckInterval)
			}
			if lb.unhealthyThreshold != tt.want.UnhealthyThreshold {
				t.Errorf("unhealthyThreshold = %d, want %d", lb.unhealthyThreshold, tt.want.UnhealthyThreshold)
			}
			if lb.healthyThreshold != tt.want.HealthyThreshold {
				t.Errorf("healthyThreshold = %d, want %d", lb.healthyThreshold, tt.want.HealthyThreshold)
			}
			if lb.responseTimeWindow != tt.want.ResponseTimeWindow {
				t.Errorf("responseTimeWindow = %d, want %d", lb.responseTimeWindow, tt.want.ResponseTimeWindow)
			}
		})
	}
}

func TestLoadBalancerAddRemoveInstances(t *testing.T) {
	lb := NewLoadBalancer(LoadBalancerConfig{
		Strategy: RoundRobin,
	})
	defer lb.Close()

	// Add instances
	instances := []string{
		"http://server1.example.com",
		"http://server2.example.com",
		"http://server3.example.com",
	}

	for i, instance := range instances {
		lb.AddInstance(instance, i+1)
	}

	stats := lb.GetStats()
	if stats.TotalInstances != int64(len(instances)) {
		t.Errorf("Expected %d total instances, got %d", len(instances), stats.TotalInstances)
	}
	if stats.HealthyInstances != int64(len(instances)) {
		t.Errorf("Expected %d healthy instances, got %d", len(instances), stats.HealthyInstances)
	}

	// Check instance health
	health := lb.GetInstanceHealth()
	if len(health) != len(instances) {
		t.Errorf("Expected %d instances in health map, got %d", len(instances), len(health))
	}

	for _, instance := range instances {
		if h, exists := health[instance]; !exists {
			t.Errorf("Instance %s not found in health map", instance)
		} else if !h.Available {
			t.Errorf("Instance %s should be available", instance)
		}
	}

	// Remove an instance
	removed := lb.RemoveInstance(instances[0])
	if !removed {
		t.Error("Should have successfully removed instance")
	}

	stats = lb.GetStats()
	if stats.TotalInstances != int64(len(instances)-1) {
		t.Errorf("Expected %d total instances after removal, got %d", len(instances)-1, stats.TotalInstances)
	}

	// Try to remove non-existent instance
	removed = lb.RemoveInstance("http://non-existent.com")
	if removed {
		t.Error("Should not have removed non-existent instance")
	}
}

func TestLoadBalancerRoundRobin(t *testing.T) {
	lb := NewLoadBalancer(LoadBalancerConfig{
		Strategy: RoundRobin,
	})
	defer lb.Close()

	instances := []string{
		"http://server1.example.com",
		"http://server2.example.com", 
		"http://server3.example.com",
	}

	for _, instance := range instances {
		lb.AddInstance(instance, 1)
	}

	// Test round-robin distribution
	selectedInstances := make(map[string]int)
	for i := 0; i < 30; i++ {
		instance, err := lb.GetNextInstance()
		if err != nil {
			t.Fatalf("GetNextInstance failed: %v", err)
		}
		selectedInstances[instance.URL]++
		
		// Simulate request completion
		lb.RecordSuccess(instance.URL, 10*time.Millisecond)
	}

	// Each instance should be selected roughly equally
	for _, instance := range instances {
		count := selectedInstances[instance]
		if count < 8 || count > 12 { // Allow some variance
			t.Errorf("Instance %s selected %d times, expected around 10", instance, count)
		}
	}
}

func TestLoadBalancerWeightedRoundRobin(t *testing.T) {
	lb := NewLoadBalancer(LoadBalancerConfig{
		Strategy: WeightedRoundRobin,
	})
	defer lb.Close()

	// Add instances with different weights
	lb.AddInstance("http://server1.example.com", 1)
	lb.AddInstance("http://server2.example.com", 2)
	lb.AddInstance("http://server3.example.com", 3)

	selectedInstances := make(map[string]int)
	for i := 0; i < 60; i++ {
		instance, err := lb.GetNextInstance()
		if err != nil {
			t.Fatalf("GetNextInstance failed: %v", err)
		}
		selectedInstances[instance.URL]++
		
		lb.RecordSuccess(instance.URL, 10*time.Millisecond)
	}

	// Weighted distribution should roughly match weights
	server1Count := selectedInstances["http://server1.example.com"]
	server2Count := selectedInstances["http://server2.example.com"]
	server3Count := selectedInstances["http://server3.example.com"]

	// Server3 should get ~3x more requests than Server1
	if server3Count <= server1Count {
		t.Errorf("Server3 (weight 3) got %d requests, Server1 (weight 1) got %d", server3Count, server1Count)
	}
	// Server2 should get ~2x more requests than Server1
	if server2Count <= server1Count {
		t.Errorf("Server2 (weight 2) got %d requests, Server1 (weight 1) got %d", server2Count, server1Count)
	}
}

func TestLoadBalancerLeastConnections(t *testing.T) {
	lb := NewLoadBalancer(LoadBalancerConfig{
		Strategy: LeastConnections,
	})
	defer lb.Close()

	instances := []string{
		"http://server1.example.com",
		"http://server2.example.com",
		"http://server3.example.com",
	}

	for _, instance := range instances {
		lb.AddInstance(instance, 1)
	}

	// Create artificial connection imbalance
	lb.mu.Lock()
	lb.instances["http://server1.example.com"].ActiveConnections = 10
	lb.instances["http://server2.example.com"].ActiveConnections = 5
	lb.instances["http://server3.example.com"].ActiveConnections = 1
	lb.mu.Unlock()

	// Should prefer server3 with least connections
	instance, err := lb.GetNextInstance()
	if err != nil {
		t.Fatalf("GetNextInstance failed: %v", err)
	}

	if instance.URL != "http://server3.example.com" {
		t.Errorf("Expected server3 with least connections, got %s", instance.URL)
	}

	lb.RecordSuccess(instance.URL, 10*time.Millisecond)
}

func TestLoadBalancerLeastResponseTime(t *testing.T) {
	lb := NewLoadBalancer(LoadBalancerConfig{
		Strategy: LeastResponseTime,
	})
	defer lb.Close()

	instances := []string{
		"http://server1.example.com",
		"http://server2.example.com",
		"http://server3.example.com",
	}

	for _, instance := range instances {
		lb.AddInstance(instance, 1)
	}

	// Set artificial response times
	lb.mu.Lock()
	lb.instances["http://server1.example.com"].ResponseTime = 100 * time.Millisecond
	lb.instances["http://server2.example.com"].ResponseTime = 50 * time.Millisecond
	lb.instances["http://server3.example.com"].ResponseTime = 200 * time.Millisecond
	lb.mu.Unlock()

	// Should prefer server2 with lowest response time
	instance, err := lb.GetNextInstance()
	if err != nil {
		t.Fatalf("GetNextInstance failed: %v", err)
	}

	if instance.URL != "http://server2.example.com" {
		t.Errorf("Expected server2 with lowest response time, got %s", instance.URL)
	}

	lb.RecordSuccess(instance.URL, 10*time.Millisecond)
}

func TestLoadBalancerRandomChoice(t *testing.T) {
	lb := NewLoadBalancer(LoadBalancerConfig{
		Strategy: RandomChoice,
	})
	defer lb.Close()

	instances := []string{
		"http://server1.example.com",
		"http://server2.example.com",
		"http://server3.example.com",
	}

	for _, instance := range instances {
		lb.AddInstance(instance, 1)
	}

	selectedInstances := make(map[string]int)
	for i := 0; i < 100; i++ {
		instance, err := lb.GetNextInstance()
		if err != nil {
			t.Fatalf("GetNextInstance failed: %v", err)
		}
		selectedInstances[instance.URL]++
		
		lb.RecordSuccess(instance.URL, 10*time.Millisecond)
	}

	// Each instance should be selected at least a few times
	for _, instance := range instances {
		count := selectedInstances[instance]
		if count == 0 {
			t.Errorf("Instance %s was never selected", instance)
		}
	}
}

func TestLoadBalancerHealthTracking(t *testing.T) {
	lb := NewLoadBalancer(LoadBalancerConfig{
		Strategy:           RoundRobin,
		UnhealthyThreshold: 2,
		HealthyThreshold:   1,
	})
	defer lb.Close()

	instance := "http://server1.example.com"
	lb.AddInstance(instance, 1)

	// Record failures to make instance unhealthy
	for i := 0; i < 2; i++ {
		_, err := lb.GetNextInstance()
		if err != nil {
			t.Fatalf("GetNextInstance failed: %v", err)
		}
		lb.RecordFailure(instance, errors.New("test error"))
	}

	// Instance should be unhealthy now
	health := lb.GetInstanceHealth()
	if health[instance].Available {
		t.Error("Instance should be unhealthy after failures")
	}

	stats := lb.GetStats()
	if stats.HealthyInstances != 0 {
		t.Errorf("Expected 0 healthy instances, got %d", stats.HealthyInstances)
	}
	if stats.UnhealthyInstances != 1 {
		t.Errorf("Expected 1 unhealthy instance, got %d", stats.UnhealthyInstances)
	}

	// Should not get unhealthy instance
	_, err := lb.GetNextInstance()
	if err == nil {
		t.Error("Should not get unhealthy instance")
	}

	// Wait for cooldown period to pass
	lb.mu.Lock()
	lb.instances[instance].CooldownUntil = time.Time{}
	lb.instances[instance].Available = true
	lb.mu.Unlock()

	// Record success to make it healthy again
	_, err = lb.GetNextInstance()
	if err != nil {
		t.Fatalf("GetNextInstance failed: %v", err)
	}
	lb.RecordSuccess(instance, 50*time.Millisecond)

	health = lb.GetInstanceHealth()
	if !health[instance].Available {
		t.Error("Instance should be healthy after success")
	}
}

func TestLoadBalancerResponseTimeTracking(t *testing.T) {
	lb := NewLoadBalancer(LoadBalancerConfig{
		Strategy:           RoundRobin,
		ResponseTimeWindow: 5,
	})
	defer lb.Close()

	instance := "http://server1.example.com"
	lb.AddInstance(instance, 1)

	// Record various response times
	responseTimes := []time.Duration{
		10 * time.Millisecond,
		20 * time.Millisecond,
		30 * time.Millisecond,
		40 * time.Millisecond,
		50 * time.Millisecond,
	}

	for _, rt := range responseTimes {
		inst, err := lb.GetNextInstance()
		if err != nil {
			t.Fatalf("GetNextInstance failed: %v", err)
		}
		lb.RecordSuccess(inst.URL, rt)
	}

	// Check average response time
	health := lb.GetInstanceHealth()
	avgResponseTime := health[instance].ResponseTime

	expectedAvg := 30 * time.Millisecond // (10+20+30+40+50)/5
	tolerance := 5 * time.Millisecond

	if avgResponseTime < expectedAvg-tolerance || avgResponseTime > expectedAvg+tolerance {
		t.Errorf("Expected average response time around %v, got %v", expectedAvg, avgResponseTime)
	}
}

func TestLoadBalancerHealthScore(t *testing.T) {
	lb := NewLoadBalancer(LoadBalancerConfig{
		Strategy: RoundRobin,
	})
	defer lb.Close()

	instance := "http://server1.example.com"
	lb.AddInstance(instance, 1)

	// Get initial score (should be 1.0)
	health := lb.GetInstanceHealth()
	initialScore := health[instance].Score
	if initialScore != 1.0 {
		t.Errorf("Expected initial score 1.0, got %f", initialScore)
	}

	// Record some successes and failures
	inst, _ := lb.GetNextInstance()
	lb.RecordSuccess(inst.URL, 100*time.Millisecond)
	
	inst, _ = lb.GetNextInstance()
	lb.RecordFailure(inst.URL, errors.New("test error"))

	// Score should be affected
	health = lb.GetInstanceHealth()
	newScore := health[instance].Score
	if newScore >= initialScore {
		t.Errorf("Score should have decreased after failure, got %f", newScore)
	}
}

func TestLoadBalancerConcurrentOperations(t *testing.T) {
	lb := NewLoadBalancer(LoadBalancerConfig{
		Strategy: RoundRobin,
	})
	defer lb.Close()

	instances := []string{
		"http://server1.example.com",
		"http://server2.example.com",
		"http://server3.example.com",
	}

	for _, instance := range instances {
		lb.AddInstance(instance, 1)
	}

	const numGoroutines = 20
	const requestsPerGoroutine = 50
	var wg sync.WaitGroup
	var successCount int64
	var errorCount int64

	// Concurrent load balancing
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			for j := 0; j < requestsPerGoroutine; j++ {
				instance, err := lb.GetNextInstance()
				if err != nil {
					atomic.AddInt64(&errorCount, 1)
					continue
				}

				// Simulate some failures
				if j%10 == 0 {
					lb.RecordFailure(instance.URL, errors.New("simulated failure"))
					atomic.AddInt64(&errorCount, 1)
				} else {
					lb.RecordSuccess(instance.URL, time.Duration(j%100)*time.Millisecond)
					atomic.AddInt64(&successCount, 1)
				}

				time.Sleep(time.Microsecond) // Small delay
			}
		}()
	}

	wg.Wait()

	totalRequests := int64(numGoroutines * requestsPerGoroutine)
	actualTotal := atomic.LoadInt64(&successCount) + atomic.LoadInt64(&errorCount)

	if actualTotal < totalRequests/2 { // Allow for some failures due to unhealthy instances
		t.Errorf("Expected at least %d total requests, got %d", totalRequests/2, actualTotal)
	}

	stats := lb.GetStats()
	if stats.TotalRequests < totalRequests/2 {
		t.Errorf("Expected at least %d total requests in stats, got %d", totalRequests/2, stats.TotalRequests)
	}
}

func TestLoadBalancerSetStrategy(t *testing.T) {
	lb := NewLoadBalancer(LoadBalancerConfig{
		Strategy: RoundRobin,
	})
	defer lb.Close()

	// Change strategy
	lb.SetStrategy(LeastConnections)

	// Verify strategy changed
	if lb.strategy != LeastConnections {
		t.Errorf("Expected strategy %v, got %v", LeastConnections, lb.strategy)
	}
}

func TestLoadBalancerCircuitBreakerIntegration(t *testing.T) {
	cbm := NewCircuitBreakerManager(CircuitBreakerConfig{
		MaxFailures:  2,
		ResetTimeout: 100 * time.Millisecond,
	})
	defer cbm.Close()

	lb := NewLoadBalancer(LoadBalancerConfig{
		Strategy:       RoundRobin,
		CircuitBreaker: cbm,
	})
	defer lb.Close()

	instance := "http://server1.example.com"
	lb.AddInstance(instance, 1)

	// Cause circuit breaker to open
	for i := 0; i < 3; i++ {
		inst, err := lb.GetNextInstance()
		if err != nil {
			continue
		}
		lb.RecordFailure(inst.URL, errors.New("test error"))
	}

	// Check that circuit breaker has some statistics
	cbStats := cbm.GetStats()
	if cbStats.TotalRequests == 0 {
		t.Error("Circuit breaker should have recorded requests")
	}
}

func TestLoadBalancerStatsAccuracy(t *testing.T) {
	lb := NewLoadBalancer(LoadBalancerConfig{
		Strategy: RoundRobin,
	})
	defer lb.Close()

	instances := []string{
		"http://server1.example.com",
		"http://server2.example.com",
	}

	for _, instance := range instances {
		lb.AddInstance(instance, 1)
	}

	// Make some requests
	requestCount := 10
	for i := 0; i < requestCount; i++ {
		instance, err := lb.GetNextInstance()
		if err != nil {
			t.Fatalf("GetNextInstance failed: %v", err)
		}
		lb.RecordSuccess(instance.URL, 10*time.Millisecond)
	}

	stats := lb.GetStats()
	if stats.TotalRequests != int64(requestCount) {
		t.Errorf("Expected %d total requests, got %d", requestCount, stats.TotalRequests)
	}

	// Check distribution map
	totalDistributed := int64(0)
	for _, count := range stats.DistributionMap {
		totalDistributed += count
	}

	if totalDistributed != int64(requestCount) {
		t.Errorf("Expected %d total distributed requests, got %d", requestCount, totalDistributed)
	}
}

func TestLoadBalancerNoHealthyInstances(t *testing.T) {
	lb := NewLoadBalancer(LoadBalancerConfig{
		Strategy:           RoundRobin,
		UnhealthyThreshold: 1,
	})
	defer lb.Close()

	instance := "http://server1.example.com"
	lb.AddInstance(instance, 1)

	// Make instance unhealthy
	inst, _ := lb.GetNextInstance()
	lb.RecordFailure(inst.URL, errors.New("test error"))

	// Should not get any instance
	_, err := lb.GetNextInstance()
	if err == nil {
		t.Error("Should not get instance when none are healthy")
	}
}

func BenchmarkLoadBalancerRoundRobin(b *testing.B) {
	lb := NewLoadBalancer(LoadBalancerConfig{
		Strategy: RoundRobin,
	})
	defer lb.Close()

	// Add instances
	for i := 0; i < 10; i++ {
		lb.AddInstance(fmt.Sprintf("http://server%d.example.com", i), 1)
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			instance, err := lb.GetNextInstance()
			if err != nil {
				b.Error(err)
				continue
			}
			lb.RecordSuccess(instance.URL, 10*time.Millisecond)
		}
	})
}

func BenchmarkLoadBalancerLeastConnections(b *testing.B) {
	lb := NewLoadBalancer(LoadBalancerConfig{
		Strategy: LeastConnections,
	})
	defer lb.Close()

	// Add instances
	for i := 0; i < 10; i++ {
		lb.AddInstance(fmt.Sprintf("http://server%d.example.com", i), 1)
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			instance, err := lb.GetNextInstance()
			if err != nil {
				b.Error(err)
				continue
			}
			lb.RecordSuccess(instance.URL, 10*time.Millisecond)
		}
	})
}

func BenchmarkLoadBalancerHealthScoreCalculation(b *testing.B) {
	lb := NewLoadBalancer(LoadBalancerConfig{
		Strategy: RoundRobin,
	})
	defer lb.Close()

	instance := "http://server1.example.com"
	lb.AddInstance(instance, 1)

	// Create a realistic instance with metrics
	lb.mu.Lock()
	inst := lb.instances[instance]
	inst.TotalRequests = 1000
	inst.SuccessCount = 950
	inst.ResponseTime = 50 * time.Millisecond
	inst.ActiveConnections = 10
	lb.mu.Unlock()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		lb.updateHealthScore(inst)
	}
}

func BenchmarkLoadBalancerConcurrentGetNext(b *testing.B) {
	lb := NewLoadBalancer(LoadBalancerConfig{
		Strategy: RoundRobin,
	})
	defer lb.Close()

	// Add many instances
	for i := 0; i < 100; i++ {
		lb.AddInstance(fmt.Sprintf("http://server%d.example.com", i), 1)
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, err := lb.GetNextInstance()
			if err != nil {
				b.Error(err)
			}
		}
	})
}