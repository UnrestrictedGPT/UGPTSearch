package concurrency

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// TokenBucket implements a token bucket rate limiter
type TokenBucket struct {
	capacity   int64 // Maximum number of tokens
	tokens     int64 // Current number of tokens
	refillRate int64 // Tokens per second
	lastRefill int64 // Last refill timestamp (nanoseconds)
	mu         sync.Mutex
	identifier string // Bucket identifier (e.g., client IP, instance URL)
}

// RateLimiter manages multiple token buckets for different clients/instances
type RateLimiter struct {
	buckets           map[string]*TokenBucket
	globalBucket      *TokenBucket
	mu                sync.RWMutex
	defaultCapacity   int64
	defaultRefillRate int64
	cleanupInterval   time.Duration
	maxBuckets        int
	stats             *RateLimiterStats
	stopCleanup       chan struct{}
	cleanupWg         sync.WaitGroup
}

// RateLimiterStats tracks rate limiter performance
type RateLimiterStats struct {
	TotalRequests     int64 `json:"total_requests"`
	AllowedRequests   int64 `json:"allowed_requests"`
	RateLimitedReqs   int64 `json:"rate_limited_requests"`
	GlobalRateLimited int64 `json:"global_rate_limited"`
	BucketsCount      int64 `json:"buckets_count"`
	ActiveBuckets     int64 `json:"active_buckets"`
}

// RateLimiterConfig holds configuration for the rate limiter
type RateLimiterConfig struct {
	DefaultCapacity   int64         // Default bucket capacity
	DefaultRefillRate int64         // Default refill rate (tokens per second)
	GlobalCapacity    int64         // Global rate limit capacity
	GlobalRefillRate  int64         // Global refill rate
	CleanupInterval   time.Duration // How often to clean up unused buckets
	MaxBuckets        int           // Maximum number of buckets to track
}

// NewRateLimiter creates a new rate limiter with the given configuration
func NewRateLimiter(config RateLimiterConfig) *RateLimiter {
	if config.DefaultCapacity <= 0 {
		config.DefaultCapacity = 100 // 100 requests per bucket
	}
	if config.DefaultRefillRate <= 0 {
		config.DefaultRefillRate = 10 // 10 requests per second
	}
	if config.GlobalCapacity <= 0 {
		config.GlobalCapacity = 1000 // 1000 requests globally
	}
	if config.GlobalRefillRate <= 0 {
		config.GlobalRefillRate = 100 // 100 requests per second globally
	}
	if config.CleanupInterval <= 0 {
		config.CleanupInterval = 5 * time.Minute
	}
	if config.MaxBuckets <= 0 {
		config.MaxBuckets = 10000 // Maximum 10k different clients/instances
	}

	rl := &RateLimiter{
		buckets:           make(map[string]*TokenBucket),
		globalBucket:      NewTokenBucket("global", config.GlobalCapacity, config.GlobalRefillRate),
		defaultCapacity:   config.DefaultCapacity,
		defaultRefillRate: config.DefaultRefillRate,
		cleanupInterval:   config.CleanupInterval,
		maxBuckets:        config.MaxBuckets,
		stats:             &RateLimiterStats{},
		stopCleanup:       make(chan struct{}),
	}

	// Start cleanup routine
	rl.cleanupWg.Add(1)
	go rl.cleanupRoutine()

	return rl
}

// NewTokenBucket creates a new token bucket
func NewTokenBucket(identifier string, capacity, refillRate int64) *TokenBucket {
	now := time.Now().UnixNano()
	return &TokenBucket{
		capacity:   capacity,
		tokens:     capacity, // Start full
		refillRate: refillRate,
		lastRefill: now,
		identifier: identifier,
	}
}

// Allow checks if a request is allowed for the given identifier
func (rl *RateLimiter) Allow(identifier string, tokens int64) bool {
	atomic.AddInt64(&rl.stats.TotalRequests, 1)

	// Check global rate limit first
	if !rl.globalBucket.consume(tokens) {
		atomic.AddInt64(&rl.stats.GlobalRateLimited, 1)
		return false
	}

	// Get or create bucket for identifier
	bucket := rl.getBucket(identifier)
	if bucket.consume(tokens) {
		atomic.AddInt64(&rl.stats.AllowedRequests, 1)
		return true
	}

	atomic.AddInt64(&rl.stats.RateLimitedReqs, 1)
	return false
}

// AllowWithContext checks if a request is allowed with context support
func (rl *RateLimiter) AllowWithContext(ctx context.Context, identifier string, tokens int64) bool {
	select {
	case <-ctx.Done():
		return false
	default:
		return rl.Allow(identifier, tokens)
	}
}

// WaitForPermission waits until tokens become available or context is cancelled
func (rl *RateLimiter) WaitForPermission(ctx context.Context, identifier string, tokens int64) error {
	for {
		if rl.Allow(identifier, tokens) {
			return nil
		}

		// Calculate wait time based on refill rate
		bucket := rl.getBucket(identifier)
		waitTime := time.Duration(float64(tokens)/float64(bucket.refillRate)) * time.Second

		// Cap wait time to prevent excessive delays
		if waitTime > 10*time.Second {
			waitTime = 10 * time.Second
		}

		select {
		case <-time.After(waitTime):
			continue
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// getBucket gets or creates a bucket for the given identifier
func (rl *RateLimiter) getBucket(identifier string) *TokenBucket {
	rl.mu.RLock()
	bucket, exists := rl.buckets[identifier]
	rl.mu.RUnlock()

	if exists {
		return bucket
	}

	rl.mu.Lock()
	defer rl.mu.Unlock()

	// Double-check after acquiring write lock
	bucket, exists = rl.buckets[identifier]
	if exists {
		return bucket
	}

	// Check if we've reached maximum buckets
	if len(rl.buckets) >= rl.maxBuckets {
		// Remove oldest bucket (simple LRU-like behavior)
		oldestTime := time.Now().UnixNano()
		var oldestID string

		for id, b := range rl.buckets {
			if b.lastRefill < oldestTime {
				oldestTime = b.lastRefill
				oldestID = id
			}
		}

		if oldestID != "" {
			delete(rl.buckets, oldestID)
		}
	}

	// Create new bucket
	bucket = NewTokenBucket(identifier, rl.defaultCapacity, rl.defaultRefillRate)
	rl.buckets[identifier] = bucket
	atomic.AddInt64(&rl.stats.BucketsCount, 1)

	return bucket
}

// consume attempts to consume tokens from the bucket
func (tb *TokenBucket) consume(tokens int64) bool {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	tb.refill()

	if tb.tokens >= tokens {
		tb.tokens -= tokens
		return true
	}

	return false
}

// refill adds tokens to the bucket based on time elapsed
func (tb *TokenBucket) refill() {
	now := time.Now().UnixNano()
	elapsed := now - tb.lastRefill
	tb.lastRefill = now

	if elapsed <= 0 {
		return
	}

	// Calculate tokens to add based on elapsed time
	tokensToAdd := (elapsed * tb.refillRate) / int64(time.Second)

	tb.tokens += tokensToAdd
	if tb.tokens > tb.capacity {
		tb.tokens = tb.capacity
	}
}

// GetTokens returns current token count (for monitoring)
func (tb *TokenBucket) GetTokens() int64 {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	tb.refill()
	return tb.tokens
}

// cleanupRoutine periodically cleans up unused buckets
func (rl *RateLimiter) cleanupRoutine() {
	defer rl.cleanupWg.Done()

	ticker := time.NewTicker(rl.cleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			rl.cleanup()
		case <-rl.stopCleanup:
			return
		}
	}
}

// cleanup removes buckets that haven't been used recently
func (rl *RateLimiter) cleanup() {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now().UnixNano()
	cutoff := now - int64(rl.cleanupInterval*3) // Remove buckets unused for 3x cleanup interval

	removed := 0
	for identifier, bucket := range rl.buckets {
		if bucket.lastRefill < cutoff {
			delete(rl.buckets, identifier)
			removed++
		}
	}

	if removed > 0 {
		atomic.AddInt64(&rl.stats.BucketsCount, -int64(removed))
	}
}

// GetStats returns rate limiter statistics
func (rl *RateLimiter) GetStats() RateLimiterStats {
	rl.mu.RLock()
	activeBuckets := int64(len(rl.buckets))
	rl.mu.RUnlock()

	return RateLimiterStats{
		TotalRequests:     atomic.LoadInt64(&rl.stats.TotalRequests),
		AllowedRequests:   atomic.LoadInt64(&rl.stats.AllowedRequests),
		RateLimitedReqs:   atomic.LoadInt64(&rl.stats.RateLimitedReqs),
		GlobalRateLimited: atomic.LoadInt64(&rl.stats.GlobalRateLimited),
		BucketsCount:      atomic.LoadInt64(&rl.stats.BucketsCount),
		ActiveBuckets:     activeBuckets,
	}
}

// SetBucketParams allows dynamic adjustment of bucket parameters
func (rl *RateLimiter) SetBucketParams(identifier string, capacity, refillRate int64) {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	bucket, exists := rl.buckets[identifier]
	if exists {
		bucket.mu.Lock()
		bucket.capacity = capacity
		bucket.refillRate = refillRate
		// Adjust current tokens if capacity was reduced
		if bucket.tokens > capacity {
			bucket.tokens = capacity
		}
		bucket.mu.Unlock()
	}
}

// Close shuts down the rate limiter
func (rl *RateLimiter) Close() error {
	close(rl.stopCleanup)
	rl.cleanupWg.Wait()

	rl.mu.Lock()
	rl.buckets = make(map[string]*TokenBucket)
	rl.mu.Unlock()

	return nil
}

// BurstLimiter provides burst protection with sliding window
type BurstLimiter struct {
	windowSize  time.Duration
	maxRequests int64
	requests    []int64 // timestamps
	mu          sync.Mutex
}

// NewBurstLimiter creates a new burst limiter
func NewBurstLimiter(windowSize time.Duration, maxRequests int64) *BurstLimiter {
	return &BurstLimiter{
		windowSize:  windowSize,
		maxRequests: maxRequests,
		requests:    make([]int64, 0),
	}
}

// Allow checks if a request is allowed within the sliding window
func (bl *BurstLimiter) Allow() bool {
	bl.mu.Lock()
	defer bl.mu.Unlock()

	now := time.Now().UnixNano()
	cutoff := now - int64(bl.windowSize)

	// Remove old requests outside the window
	newRequests := bl.requests[:0]
	for _, timestamp := range bl.requests {
		if timestamp > cutoff {
			newRequests = append(newRequests, timestamp)
		}
	}
	bl.requests = newRequests

	// Check if we can allow this request
	if int64(len(bl.requests)) >= bl.maxRequests {
		return false
	}

	// Add current request
	bl.requests = append(bl.requests, now)
	return true
}
