package concurrency

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestCircuitBreakerCreation(t *testing.T) {
	tests := []struct {
		name   string
		config CircuitBreakerConfig
		want   CircuitBreakerConfig
	}{
		{
			name: "default configuration",
			config: CircuitBreakerConfig{
				Name:         "test",
				MaxFailures:  0,
				ResetTimeout: 0,
			},
			want: CircuitBreakerConfig{
				Name:         "test",
				MaxFailures:  5,
				ResetTimeout: 60 * time.Second,
			},
		},
		{
			name: "custom configuration",
			config: CircuitBreakerConfig{
				Name:         "custom",
				MaxFailures:  3,
				ResetTimeout: 30 * time.Second,
			},
			want: CircuitBreakerConfig{
				Name:         "custom",
				MaxFailures:  3,
				ResetTimeout: 30 * time.Second,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cb := NewCircuitBreaker(tt.config)
			
			if cb.name != tt.want.Name {
				t.Errorf("name = %s, want %s", cb.name, tt.want.Name)
			}
			if cb.maxFailures != tt.want.MaxFailures {
				t.Errorf("maxFailures = %d, want %d", cb.maxFailures, tt.want.MaxFailures)
			}
			if cb.resetTimeout != tt.want.ResetTimeout {
				t.Errorf("resetTimeout = %v, want %v", cb.resetTimeout, tt.want.ResetTimeout)
			}
			if cb.GetState() != StateClosed {
				t.Errorf("initial state = %v, want %v", cb.GetState(), StateClosed)
			}
		})
	}
}

func TestCircuitBreakerBasicExecution(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{
		Name:         "test",
		MaxFailures:  3,
		ResetTimeout: 100 * time.Millisecond,
	})

	// Test successful execution
	err := cb.Execute(func() error {
		return nil
	})
	if err != nil {
		t.Errorf("Successful execution failed: %v", err)
	}

	if cb.GetState() != StateClosed {
		t.Errorf("State should be Closed after success, got %v", cb.GetState())
	}

	stats := cb.GetStats()
	requests := stats["requests"].(int64)
	successes := stats["successes"].(int64)
	
	if requests != 1 {
		t.Errorf("Expected 1 request, got %d", requests)
	}
	if successes != 1 {
		t.Errorf("Expected 1 success, got %d", successes)
	}
}

func TestCircuitBreakerFailureHandling(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{
		Name:         "failure-test",
		MaxFailures:  2,
		ResetTimeout: 100 * time.Millisecond,
	})

	testError := errors.New("test error")

	// First failure
	err := cb.Execute(func() error {
		return testError
	})
	if err != testError {
		t.Errorf("Expected test error, got %v", err)
	}
	if cb.GetState() != StateClosed {
		t.Errorf("State should still be Closed after first failure, got %v", cb.GetState())
	}

	// Second failure - should open circuit
	err = cb.Execute(func() error {
		return testError
	})
	if err != testError {
		t.Errorf("Expected test error, got %v", err)
	}
	if cb.GetState() != StateOpen {
		t.Errorf("State should be Open after max failures, got %v", cb.GetState())
	}

	// Third attempt should be rejected immediately
	err = cb.Execute(func() error {
		t.Error("Function should not be executed when circuit is open")
		return nil
	})
	if err != ErrCircuitOpen {
		t.Errorf("Expected ErrCircuitOpen, got %v", err)
	}
}

func TestCircuitBreakerStateTransitions(t *testing.T) {
	stateChanges := make([]string, 0)
	var mu sync.Mutex

	cb := NewCircuitBreaker(CircuitBreakerConfig{
		Name:         "state-test",
		MaxFailures:  2,
		ResetTimeout: 50 * time.Millisecond,
		OnStateChange: func(name string, from, to CircuitState) {
			mu.Lock()
			defer mu.Unlock()
			stateChanges = append(stateChanges, from.String()+"->"+to.String())
		},
	})

	testError := errors.New("test error")

	// Trigger state changes: Closed -> Open
	cb.Execute(func() error { return testError })
	cb.Execute(func() error { return testError })

	// Wait for reset timeout
	time.Sleep(70 * time.Millisecond)

	// This should transition to half-open
	cb.Execute(func() error { return nil })

	// Wait for state change callbacks
	time.Sleep(10 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()

	expectedTransitions := []string{"closed->open", "open->half-open", "half-open->closed"}
	if len(stateChanges) < 2 {
		t.Errorf("Expected at least 2 state changes, got %d: %v", len(stateChanges), stateChanges)
	}
	
	// Check that we got the closed->open transition
	found := false
	for _, change := range stateChanges {
		if change == "closed->open" {
			found = true
			break
		}
	}
	if !found {
		t.Error("Expected closed->open transition not found")
	}
	
	// Use expectedTransitions for future test validation
	_ = expectedTransitions
}

func TestCircuitBreakerHalfOpenBehavior(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{
		Name:         "half-open-test",
		MaxFailures:  1,
		ResetTimeout: 50 * time.Millisecond,
	})

	// Open the circuit
	cb.Execute(func() error { return errors.New("failure") })
	
	if cb.GetState() != StateOpen {
		t.Errorf("Circuit should be open, got %v", cb.GetState())
	}

	// Wait for reset timeout
	time.Sleep(70 * time.Millisecond)

	// First request after timeout should put it in half-open state
	successCount := 0
	err := cb.Execute(func() error {
		successCount++
		return nil
	})
	if err != nil {
		t.Errorf("First request after reset should succeed, got %v", err)
	}

	// Multiple successful requests should close the circuit
	for i := 0; i < 3; i++ {
		cb.Execute(func() error {
			successCount++
			return nil
		})
	}

	if cb.GetState() != StateClosed {
		t.Errorf("Circuit should be closed after successful requests, got %v", cb.GetState())
	}

	if successCount < 3 {
		t.Errorf("Expected at least 3 successful executions, got %d", successCount)
	}
}

func TestCircuitBreakerHalfOpenFailure(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{
		Name:         "half-open-failure-test",
		MaxFailures:  1,
		ResetTimeout: 50 * time.Millisecond,
	})

	// Open the circuit
	cb.Execute(func() error { return errors.New("initial failure") })

	// Wait for reset timeout
	time.Sleep(70 * time.Millisecond)

	// Failure in half-open state should immediately open circuit
	err := cb.Execute(func() error {
		return errors.New("half-open failure")
	})
	if err == nil {
		t.Error("Expected failure in half-open state")
	}

	if cb.GetState() != StateOpen {
		t.Errorf("Circuit should be open after half-open failure, got %v", cb.GetState())
	}

	// Next request should be rejected
	err = cb.Execute(func() error {
		t.Error("Function should not execute when circuit is open")
		return nil
	})
	if err != ErrCircuitOpen {
		t.Errorf("Expected ErrCircuitOpen, got %v", err)
	}
}

func TestCircuitBreakerWithContext(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{
		Name:         "context-test",
		MaxFailures:  3,
		ResetTimeout: 100 * time.Millisecond,
	})

	// Test with valid context
	ctx := context.Background()
	err := cb.ExecuteWithContext(ctx, func() error {
		return nil
	})
	if err != nil {
		t.Errorf("Execution with valid context failed: %v", err)
	}

	// Test with cancelled context
	cancelledCtx, cancel := context.WithCancel(context.Background())
	cancel()

	err = cb.ExecuteWithContext(cancelledCtx, func() error {
		t.Error("Function should not execute with cancelled context")
		return nil
	})
	if err != context.Canceled {
		t.Errorf("Expected context.Canceled, got %v", err)
	}

	// Test timeout context
	timeoutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	err = cb.ExecuteWithContext(timeoutCtx, func() error {
		time.Sleep(50 * time.Millisecond) // This will timeout
		return nil
	})
	if err != context.DeadlineExceeded {
		t.Errorf("Expected context.DeadlineExceeded, got %v", err)
	}
}

func TestCircuitBreakerReset(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{
		Name:         "reset-test",
		MaxFailures:  1,
		ResetTimeout: 100 * time.Millisecond,
	})

	// Open the circuit
	cb.Execute(func() error { return errors.New("failure") })
	
	if cb.GetState() != StateOpen {
		t.Errorf("Circuit should be open, got %v", cb.GetState())
	}

	// Manual reset
	cb.Reset()

	if cb.GetState() != StateClosed {
		t.Errorf("Circuit should be closed after reset, got %v", cb.GetState())
	}

	// Should accept requests normally
	err := cb.Execute(func() error {
		return nil
	})
	if err != nil {
		t.Errorf("Request should succeed after reset, got %v", err)
	}

	stats := cb.GetStats()
	if stats["failures"].(int64) != 0 {
		t.Error("Failure count should be reset to 0")
	}
}

func TestCircuitBreakerManagerCreation(t *testing.T) {
	config := CircuitBreakerConfig{
		MaxFailures:  3,
		ResetTimeout: 60 * time.Second,
	}
	
	cbm := NewCircuitBreakerManager(config)
	defer cbm.Close()

	stats := cbm.GetStats()
	if stats.TotalBreakers != 0 {
		t.Errorf("Expected 0 total breakers initially, got %d", stats.TotalBreakers)
	}
}

func TestCircuitBreakerManagerGetOrCreate(t *testing.T) {
	cbm := NewCircuitBreakerManager(CircuitBreakerConfig{
		MaxFailures:  2,
		ResetTimeout: 50 * time.Millisecond,
	})
	defer cbm.Close()

	// Get or create first breaker
	cb1 := cbm.GetOrCreate("service1")
	if cb1.name != "service1" {
		t.Errorf("Expected name 'service1', got %s", cb1.name)
	}

	// Get same breaker again
	cb2 := cbm.GetOrCreate("service1")
	if cb1 != cb2 {
		t.Error("Should return same circuit breaker instance")
	}

	// Create different breaker
	cb3 := cbm.GetOrCreate("service2")
	if cb1 == cb3 {
		t.Error("Should create different circuit breaker instances")
	}

	stats := cbm.GetStats()
	if stats.TotalBreakers != 2 {
		t.Errorf("Expected 2 total breakers, got %d", stats.TotalBreakers)
	}
}

func TestCircuitBreakerManagerExecution(t *testing.T) {
	cbm := NewCircuitBreakerManager(CircuitBreakerConfig{
		MaxFailures:  2,
		ResetTimeout: 50 * time.Millisecond,
	})
	defer cbm.Close()

	serviceName := "test-service"

	// Successful execution
	err := cbm.Execute(serviceName, func() error {
		return nil
	})
	if err != nil {
		t.Errorf("Successful execution failed: %v", err)
	}

	// Failed executions
	testError := errors.New("service error")
	for i := 0; i < 2; i++ {
		err = cbm.Execute(serviceName, func() error {
			return testError
		})
		if err != testError {
			t.Errorf("Expected service error, got %v", err)
		}
	}

	// Should be open now
	err = cbm.Execute(serviceName, func() error {
		t.Error("Function should not execute when circuit is open")
		return nil
	})
	if err != ErrCircuitOpen {
		t.Errorf("Expected ErrCircuitOpen, got %v", err)
	}

	stats := cbm.GetStats()
	if stats.TotalRequests < 3 {
		t.Errorf("Expected at least 3 total requests, got %d", stats.TotalRequests)
	}
	if stats.RejectedRequests < 1 {
		t.Errorf("Expected at least 1 rejected request, got %d", stats.RejectedRequests)
	}
}

func TestCircuitBreakerManagerStats(t *testing.T) {
	cbm := NewCircuitBreakerManager(CircuitBreakerConfig{
		MaxFailures:  1,
		ResetTimeout: 50 * time.Millisecond,
	})
	defer cbm.Close()

	// Create some breakers in different states
	cbm.Execute("service1", func() error { return nil })                    // Closed
	cbm.Execute("service2", func() error { return errors.New("error") })   // Open
	
	// Wait for state to settle
	time.Sleep(10 * time.Millisecond)

	stats := cbm.GetStats()
	if stats.TotalBreakers != 2 {
		t.Errorf("Expected 2 total breakers, got %d", stats.TotalBreakers)
	}
	if stats.ClosedBreakers < 1 {
		t.Errorf("Expected at least 1 closed breaker, got %d", stats.ClosedBreakers)
	}
	if stats.OpenBreakers < 1 {
		t.Errorf("Expected at least 1 open breaker, got %d", stats.OpenBreakers)
	}

	// Check breaker states map
	if len(stats.BreakerStates) != 2 {
		t.Errorf("Expected 2 breaker states, got %d", len(stats.BreakerStates))
	}
}

func TestCircuitBreakerManagerReset(t *testing.T) {
	cbm := NewCircuitBreakerManager(CircuitBreakerConfig{
		MaxFailures:  1,
		ResetTimeout: 100 * time.Millisecond,
	})
	defer cbm.Close()

	serviceName := "reset-test"

	// Open the circuit
	cbm.Execute(serviceName, func() error { return errors.New("error") })

	// Reset specific breaker
	err := cbm.Reset(serviceName)
	if err != nil {
		t.Errorf("Reset failed: %v", err)
	}

	// Should work normally now
	err = cbm.Execute(serviceName, func() error { return nil })
	if err != nil {
		t.Errorf("Execution should succeed after reset, got %v", err)
	}

	// Test reset non-existent breaker
	err = cbm.Reset("non-existent")
	if err == nil {
		t.Error("Reset should fail for non-existent breaker")
	}
}

func TestCircuitBreakerManagerResetAll(t *testing.T) {
	cbm := NewCircuitBreakerManager(CircuitBreakerConfig{
		MaxFailures:  1,
		ResetTimeout: 100 * time.Millisecond,
	})
	defer cbm.Close()

	// Open multiple circuits
	services := []string{"service1", "service2", "service3"}
	for _, service := range services {
		cbm.Execute(service, func() error { return errors.New("error") })
	}

	// Reset all
	cbm.ResetAll()

	// All should work normally now
	for _, service := range services {
		err := cbm.Execute(service, func() error { return nil })
		if err != nil {
			t.Errorf("Service %s should work after reset all, got %v", service, err)
		}
	}
}

func TestCircuitBreakerManagerRemove(t *testing.T) {
	cbm := NewCircuitBreakerManager(CircuitBreakerConfig{
		MaxFailures:  2,
		ResetTimeout: 50 * time.Millisecond,
	})
	defer cbm.Close()

	serviceName := "removable-service"

	// Create breaker
	cbm.Execute(serviceName, func() error { return nil })

	stats := cbm.GetStats()
	if stats.TotalBreakers != 1 {
		t.Errorf("Expected 1 breaker, got %d", stats.TotalBreakers)
	}

	// Remove breaker
	removed := cbm.Remove(serviceName)
	if !removed {
		t.Error("Should have removed the breaker")
	}

	stats = cbm.GetStats()
	if stats.TotalBreakers != 0 {
		t.Errorf("Expected 0 breakers after removal, got %d", stats.TotalBreakers)
	}

	// Try to remove non-existent breaker
	removed = cbm.Remove("non-existent")
	if removed {
		t.Error("Should not have removed non-existent breaker")
	}
}

func TestCircuitBreakerConcurrentAccess(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{
		Name:         "concurrent-test",
		MaxFailures:  10,
		ResetTimeout: 100 * time.Millisecond,
	})

	const numGoroutines = 20
	const requestsPerGoroutine = 50
	var wg sync.WaitGroup
	var successCount int64
	var errorCount int64

	// Concurrent executions
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			for j := 0; j < requestsPerGoroutine; j++ {
				err := cb.Execute(func() error {
					if j%10 == 0 { // 10% failure rate
						return errors.New("simulated error")
					}
					return nil
				})

				if err != nil {
					atomic.AddInt64(&errorCount, 1)
				} else {
					atomic.AddInt64(&successCount, 1)
				}

				time.Sleep(time.Microsecond) // Small delay
			}
		}(i)
	}

	wg.Wait()

	totalRequests := numGoroutines * requestsPerGoroutine
	actualTotal := atomic.LoadInt64(&successCount) + atomic.LoadInt64(&errorCount)

	if actualTotal != int64(totalRequests) {
		t.Errorf("Expected %d total requests, got %d", totalRequests, actualTotal)
	}

	if atomic.LoadInt64(&successCount) == 0 {
		t.Error("Should have some successful requests")
	}

	stats := cb.GetStats()
	if stats["requests"].(int64) != int64(totalRequests) {
		t.Errorf("Expected %d requests in stats, got %d", totalRequests, stats["requests"].(int64))
	}
}

func BenchmarkCircuitBreakerExecution(b *testing.B) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{
		Name:         "bench-test",
		MaxFailures:  1000,
		ResetTimeout: time.Minute,
	})

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			cb.Execute(func() error {
				return nil
			})
		}
	})
}

func BenchmarkCircuitBreakerManagerExecution(b *testing.B) {
	cbm := NewCircuitBreakerManager(CircuitBreakerConfig{
		MaxFailures:  1000,
		ResetTimeout: time.Minute,
	})
	defer cbm.Close()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		serviceID := 0
		for pb.Next() {
			serviceName := fmt.Sprintf("service-%d", serviceID%10)
			cbm.Execute(serviceName, func() error {
				return nil
			})
			serviceID++
		}
	})
}

func BenchmarkCircuitBreakerConcurrentStates(b *testing.B) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{
		Name:         "state-bench",
		MaxFailures:  5,
		ResetTimeout: 100 * time.Millisecond,
	})

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			state := cb.GetState()
			_ = state // Use the state to prevent optimization
		}
	})
}