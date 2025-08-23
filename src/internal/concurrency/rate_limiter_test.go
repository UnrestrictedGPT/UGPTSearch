package concurrency

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRateLimiterCreation(t *testing.T) {
	tests := []struct {
		name   string
		config RateLimiterConfig
		want   RateLimiterConfig
	}{
		{
			name: "default configuration",
			config: RateLimiterConfig{
				DefaultCapacity:   0,
				DefaultRefillRate: 0,
				GlobalCapacity:    0,
				GlobalRefillRate:  0,
				CleanupInterval:   0,
				MaxBuckets:        0,
			},
			want: RateLimiterConfig{
				DefaultCapacity:   100,
				DefaultRefillRate: 10,
				GlobalCapacity:    1000,
				GlobalRefillRate:  100,
				CleanupInterval:   5 * time.Minute,
				MaxBuckets:        10000,
			},
		},
		{
			name: "custom configuration",
			config: RateLimiterConfig{
				DefaultCapacity:   50,
				DefaultRefillRate: 5,
				GlobalCapacity:    500,
				GlobalRefillRate:  50,
				CleanupInterval:   2 * time.Minute,
				MaxBuckets:        5000,
			},
			want: RateLimiterConfig{
				DefaultCapacity:   50,
				DefaultRefillRate: 5,
				GlobalCapacity:    500,
				GlobalRefillRate:  50,
				CleanupInterval:   2 * time.Minute,
				MaxBuckets:        5000,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rl := NewRateLimiter(tt.config)
			defer rl.Close()

			if rl.defaultCapacity != tt.want.DefaultCapacity {
				t.Errorf("defaultCapacity = %d, want %d", rl.defaultCapacity, tt.want.DefaultCapacity)
			}
			if rl.defaultRefillRate != tt.want.DefaultRefillRate {
				t.Errorf("defaultRefillRate = %d, want %d", rl.defaultRefillRate, tt.want.DefaultRefillRate)
			}
			if rl.globalBucket.capacity != tt.want.GlobalCapacity {
				t.Errorf("globalCapacity = %d, want %d", rl.globalBucket.capacity, tt.want.GlobalCapacity)
			}
			if rl.maxBuckets != tt.want.MaxBuckets {
				t.Errorf("maxBuckets = %d, want %d", rl.maxBuckets, tt.want.MaxBuckets)
			}
		})
	}
}

func TestTokenBucketCreation(t *testing.T) {
	tb := NewTokenBucket("test", 100, 10)
	
	if tb.capacity != 100 {
		t.Errorf("capacity = %d, want 100", tb.capacity)
	}
	if tb.refillRate != 10 {
		t.Errorf("refillRate = %d, want 10", tb.refillRate)
	}
	if tb.tokens != 100 {
		t.Errorf("initial tokens = %d, want 100", tb.tokens)
	}
	if tb.identifier != "test" {
		t.Errorf("identifier = %s, want 'test'", tb.identifier)
	}
}

func TestRateLimiterBasicAllow(t *testing.T) {
	rl := NewRateLimiter(RateLimiterConfig{
		DefaultCapacity:   10,
		DefaultRefillRate: 5,
		GlobalCapacity:    100,
		GlobalRefillRate:  50,
	})
	defer rl.Close()

	identifier := "test-client"

	// Should allow initial requests up to capacity
	for i := 0; i < 10; i++ {
		if !rl.Allow(identifier, 1) {
			t.Errorf("Request %d should have been allowed", i)
		}
	}

	// Should reject additional requests
	if rl.Allow(identifier, 1) {
		t.Error("Request should have been rate limited")
	}

	stats := rl.GetStats()
	if stats.TotalRequests != 11 {
		t.Errorf("Expected 11 total requests, got %d", stats.TotalRequests)
	}
	if stats.AllowedRequests != 10 {
		t.Errorf("Expected 10 allowed requests, got %d", stats.AllowedRequests)
	}
	if stats.RateLimitedReqs != 1 {
		t.Errorf("Expected 1 rate limited request, got %d", stats.RateLimitedReqs)
	}
}

func TestRateLimiterGlobalLimit(t *testing.T) {
	rl := NewRateLimiter(RateLimiterConfig{
		DefaultCapacity:   1000,
		DefaultRefillRate: 100,
		GlobalCapacity:    5,
		GlobalRefillRate:  1,
	})
	defer rl.Close()

	// Exhaust global capacity
	for i := 0; i < 5; i++ {
		identifier := fmt.Sprintf("client-%d", i)
		if !rl.Allow(identifier, 1) {
			t.Errorf("Global request %d should have been allowed", i)
		}
	}

	// Should hit global rate limit
	if rl.Allow("another-client", 1) {
		t.Error("Request should have been globally rate limited")
	}

	stats := rl.GetStats()
	if stats.GlobalRateLimited == 0 {
		t.Error("Expected global rate limiting to occur")
	}
}

func TestRateLimiterRefill(t *testing.T) {
	rl := NewRateLimiter(RateLimiterConfig{
		DefaultCapacity:   3,
		DefaultRefillRate: 10, // 10 tokens per second
		GlobalCapacity:    100,
		GlobalRefillRate:  100,
	})
	defer rl.Close()

	identifier := "test-refill"

	// Exhaust capacity
	for i := 0; i < 3; i++ {
		if !rl.Allow(identifier, 1) {
			t.Errorf("Initial request %d should have been allowed", i)
		}
	}

	// Should be rate limited
	if rl.Allow(identifier, 1) {
		t.Error("Request should have been rate limited")
	}

	// Wait for refill (100ms should add 1 token at 10 tokens/second)
	time.Sleep(150 * time.Millisecond)

	// Should allow one more request
	if !rl.Allow(identifier, 1) {
		t.Error("Request should have been allowed after refill")
	}

	// Should be rate limited again
	if rl.Allow(identifier, 1) {
		t.Error("Request should have been rate limited again")
	}
}

func TestRateLimiterMultipleClients(t *testing.T) {
	rl := NewRateLimiter(RateLimiterConfig{
		DefaultCapacity:   5,
		DefaultRefillRate: 2,
		GlobalCapacity:    100,
		GlobalRefillRate:  50,
	})
	defer rl.Close()

	clients := []string{"client1", "client2", "client3"}

	// Each client should have their own bucket
	for _, client := range clients {
		for i := 0; i < 5; i++ {
			if !rl.Allow(client, 1) {
				t.Errorf("Request %d for %s should have been allowed", i, client)
			}
		}

		// Should be rate limited after capacity
		if rl.Allow(client, 1) {
			t.Errorf("Client %s should have been rate limited", client)
		}
	}

	stats := rl.GetStats()
	if stats.ActiveBuckets != int64(len(clients)) {
		t.Errorf("Expected %d active buckets, got %d", len(clients), stats.ActiveBuckets)
	}
}

func TestRateLimiterAllowWithContext(t *testing.T) {
	rl := NewRateLimiter(RateLimiterConfig{
		DefaultCapacity:   10,
		DefaultRefillRate: 5,
	})
	defer rl.Close()

	// Test with valid context
	ctx := context.Background()
	if !rl.AllowWithContext(ctx, "test", 1) {
		t.Error("Request with valid context should be allowed")
	}

	// Test with cancelled context
	cancelledCtx, cancel := context.WithCancel(context.Background())
	cancel()

	if rl.AllowWithContext(cancelledCtx, "test", 1) {
		t.Error("Request with cancelled context should not be allowed")
	}
}

func TestRateLimiterWaitForPermission(t *testing.T) {
	rl := NewRateLimiter(RateLimiterConfig{
		DefaultCapacity:   1,
		DefaultRefillRate: 10, // 10 tokens per second
	})
	defer rl.Close()

	identifier := "wait-test"

	// Exhaust capacity
	if !rl.Allow(identifier, 1) {
		t.Fatal("Initial request should be allowed")
	}

	// Should wait and then be allowed
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	start := time.Now()
	err := rl.WaitForPermission(ctx, identifier, 1)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("WaitForPermission failed: %v", err)
	}

	// Should have waited some time for refill
	if elapsed < 50*time.Millisecond {
		t.Error("Should have waited for token refill")
	}
}

func TestRateLimiterWaitForPermissionTimeout(t *testing.T) {
	rl := NewRateLimiter(RateLimiterConfig{
		DefaultCapacity:   1,
		DefaultRefillRate: 1, // Very slow refill
	})
	defer rl.Close()

	identifier := "timeout-test"

	// Exhaust capacity
	if !rl.Allow(identifier, 1) {
		t.Fatal("Initial request should be allowed")
	}

	// Should timeout
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := rl.WaitForPermission(ctx, identifier, 1)
	if err == nil {
		t.Error("WaitForPermission should have timed out")
	}
	if err != context.DeadlineExceeded {
		t.Errorf("Expected context.DeadlineExceeded, got %v", err)
	}
}

func TestRateLimiterSetBucketParams(t *testing.T) {
	rl := NewRateLimiter(RateLimiterConfig{
		DefaultCapacity:   10,
		DefaultRefillRate: 5,
	})
	defer rl.Close()

	identifier := "param-test"

	// Create bucket by making a request
	rl.Allow(identifier, 1)

	// Change parameters
	rl.SetBucketParams(identifier, 20, 10)

	// Verify parameters were changed by testing capacity
	bucket := rl.getBucket(identifier)
	if bucket.capacity != 20 {
		t.Errorf("Expected capacity 20, got %d", bucket.capacity)
	}
	if bucket.refillRate != 10 {
		t.Errorf("Expected refill rate 10, got %d", bucket.refillRate)
	}
}

func TestRateLimiterMaxBuckets(t *testing.T) {
	rl := NewRateLimiter(RateLimiterConfig{
		DefaultCapacity:   10,
		DefaultRefillRate: 5,
		MaxBuckets:        3,
	})
	defer rl.Close()

	// Create maximum number of buckets
	for i := 0; i < 3; i++ {
		identifier := fmt.Sprintf("client-%d", i)
		rl.Allow(identifier, 1)
	}

	stats := rl.GetStats()
	if stats.ActiveBuckets != 3 {
		t.Errorf("Expected 3 active buckets, got %d", stats.ActiveBuckets)
	}

	// Adding one more should remove the oldest
	rl.Allow("client-4", 1)

	stats = rl.GetStats()
	if stats.ActiveBuckets != 3 {
		t.Errorf("Expected 3 active buckets after overflow, got %d", stats.ActiveBuckets)
	}
}

func TestRateLimiterConcurrentAccess(t *testing.T) {
	rl := NewRateLimiter(RateLimiterConfig{
		DefaultCapacity:   100,
		DefaultRefillRate: 50,
		GlobalCapacity:    1000,
		GlobalRefillRate:  500,
	})
	defer rl.Close()

	const numGoroutines = 10
	const requestsPerGoroutine = 20
	var wg sync.WaitGroup
	var allowed int64

	// Concurrent requests from multiple goroutines
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			identifier := fmt.Sprintf("concurrent-%d", id)

			for j := 0; j < requestsPerGoroutine; j++ {
				if rl.Allow(identifier, 1) {
					atomic.AddInt64(&allowed, 1)
				}
				time.Sleep(time.Millisecond) // Small delay
			}
		}(i)
	}

	wg.Wait()

	// Should have allowed some requests
	if atomic.LoadInt64(&allowed) == 0 {
		t.Error("Should have allowed some concurrent requests")
	}

	stats := rl.GetStats()
	if stats.TotalRequests != numGoroutines*requestsPerGoroutine {
		t.Errorf("Expected %d total requests, got %d", 
			numGoroutines*requestsPerGoroutine, stats.TotalRequests)
	}
}

func TestRateLimiterCleanup(t *testing.T) {
	rl := NewRateLimiter(RateLimiterConfig{
		DefaultCapacity:   10,
		DefaultRefillRate: 5,
		CleanupInterval:   100 * time.Millisecond,
	})
	defer rl.Close()

	// Create some buckets
	for i := 0; i < 5; i++ {
		identifier := fmt.Sprintf("cleanup-test-%d", i)
		rl.Allow(identifier, 1)
	}

	initialStats := rl.GetStats()
	if initialStats.ActiveBuckets != 5 {
		t.Errorf("Expected 5 active buckets, got %d", initialStats.ActiveBuckets)
	}

	// Wait for cleanup to occur
	time.Sleep(500 * time.Millisecond)

	// Buckets should be cleaned up due to inactivity
	stats := rl.GetStats()
	if stats.ActiveBuckets >= 5 {
		t.Errorf("Expected fewer than 5 buckets after cleanup, got %d", stats.ActiveBuckets)
	}
}

func TestBurstLimiter(t *testing.T) {
	bl := NewBurstLimiter(1*time.Second, 5)

	// Should allow up to max requests
	for i := 0; i < 5; i++ {
		if !bl.Allow() {
			t.Errorf("Request %d should have been allowed", i)
		}
	}

	// Should reject additional requests
	if bl.Allow() {
		t.Error("Request should have been burst limited")
	}

	// Wait for window to slide
	time.Sleep(1100 * time.Millisecond)

	// Should allow requests again
	if !bl.Allow() {
		t.Error("Request should have been allowed after window slide")
	}
}

func TestRateLimiterClose(t *testing.T) {
	rl := NewRateLimiter(RateLimiterConfig{
		DefaultCapacity:   10,
		DefaultRefillRate: 5,
	})

	// Create some buckets
	rl.Allow("test1", 1)
	rl.Allow("test2", 1)

	stats := rl.GetStats()
	if stats.ActiveBuckets == 0 {
		t.Error("Should have active buckets before close")
	}

	// Close should clean up resources
	err := rl.Close()
	if err != nil {
		t.Errorf("Close failed: %v", err)
	}

	// Should have no active buckets after close
	stats = rl.GetStats()
	if stats.ActiveBuckets != 0 {
		t.Errorf("Expected 0 active buckets after close, got %d", stats.ActiveBuckets)
	}
}

func BenchmarkRateLimiterAllow(b *testing.B) {
	rl := NewRateLimiter(RateLimiterConfig{
		DefaultCapacity:   1000000,
		DefaultRefillRate: 100000,
		GlobalCapacity:    10000000,
		GlobalRefillRate:  1000000,
	})
	defer rl.Close()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		id := 0
		for pb.Next() {
			identifier := fmt.Sprintf("bench-%d", id%100) // Reuse identifiers
			rl.Allow(identifier, 1)
			id++
		}
	})
}

func BenchmarkTokenBucketConsume(b *testing.B) {
	tb := NewTokenBucket("bench", 1000000, 100000)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tb.consume(1)
	}
}

func BenchmarkRateLimiterConcurrentClients(b *testing.B) {
	rl := NewRateLimiter(RateLimiterConfig{
		DefaultCapacity:   1000,
		DefaultRefillRate: 100,
		GlobalCapacity:    100000,
		GlobalRefillRate:  10000,
	})
	defer rl.Close()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		clientID := 0
		for pb.Next() {
			identifier := fmt.Sprintf("client-%d", clientID%1000)
			rl.Allow(identifier, 1)
			clientID++
		}
	})
}