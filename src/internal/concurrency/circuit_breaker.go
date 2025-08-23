package concurrency

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// CircuitState represents the state of a circuit breaker
type CircuitState int32

const (
	StateClosed CircuitState = iota
	StateOpen
	StateHalfOpen
)

func (s CircuitState) String() string {
	switch s {
	case StateClosed:
		return "closed"
	case StateOpen:
		return "open"
	case StateHalfOpen:
		return "half-open"
	default:
		return "unknown"
	}
}

// CircuitBreaker implements the circuit breaker pattern
type CircuitBreaker struct {
	name          string
	maxFailures   int64
	resetTimeout  time.Duration
	state         int32 // CircuitState
	failures      int64
	requests      int64
	successes     int64
	lastFailTime  int64 // Unix nanoseconds
	mu            sync.RWMutex
	onStateChange func(name string, from, to CircuitState)
}

// CircuitBreakerConfig holds configuration for a circuit breaker
type CircuitBreakerConfig struct {
	Name          string
	MaxFailures   int64         // Number of failures before opening
	ResetTimeout  time.Duration // Time to wait before trying half-open
	OnStateChange func(name string, from, to CircuitState)
}

// CircuitBreakerManager manages multiple circuit breakers
type CircuitBreakerManager struct {
	breakers map[string]*CircuitBreaker
	mu       sync.RWMutex
	config   CircuitBreakerConfig
	stats    *CircuitBreakerStats
}

// CircuitBreakerStats tracks circuit breaker statistics
type CircuitBreakerStats struct {
	TotalBreakers    int64             `json:"total_breakers"`
	OpenBreakers     int64             `json:"open_breakers"`
	HalfOpenBreakers int64             `json:"half_open_breakers"`
	ClosedBreakers   int64             `json:"closed_breakers"`
	TotalRequests    int64             `json:"total_requests"`
	FailedRequests   int64             `json:"failed_requests"`
	RejectedRequests int64             `json:"rejected_requests"`
	BreakerStates    map[string]string `json:"breaker_states"`
}

var (
	ErrCircuitOpen     = errors.New("circuit breaker is open")
	ErrTooManyRequests = errors.New("too many requests when circuit breaker is half-open")
)

// NewCircuitBreaker creates a new circuit breaker
func NewCircuitBreaker(config CircuitBreakerConfig) *CircuitBreaker {
	if config.MaxFailures <= 0 {
		config.MaxFailures = 5
	}
	if config.ResetTimeout <= 0 {
		config.ResetTimeout = 60 * time.Second
	}

	return &CircuitBreaker{
		name:          config.Name,
		maxFailures:   config.MaxFailures,
		resetTimeout:  config.ResetTimeout,
		state:         int32(StateClosed),
		onStateChange: config.OnStateChange,
	}
}

// NewCircuitBreakerManager creates a new circuit breaker manager
func NewCircuitBreakerManager(config CircuitBreakerConfig) *CircuitBreakerManager {
	return &CircuitBreakerManager{
		breakers: make(map[string]*CircuitBreaker),
		config:   config,
		stats:    &CircuitBreakerStats{BreakerStates: make(map[string]string)},
	}
}

// Execute wraps a function call with circuit breaker logic
func (cb *CircuitBreaker) Execute(fn func() error) error {
	return cb.ExecuteWithContext(context.Background(), fn)
}

// ExecuteWithContext wraps a function call with circuit breaker logic and context support
func (cb *CircuitBreaker) ExecuteWithContext(ctx context.Context, fn func() error) error {
	// Check if context is already cancelled
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	// Pre-check circuit state
	if !cb.allowRequest() {
		return ErrCircuitOpen
	}

	atomic.AddInt64(&cb.requests, 1)

	// Execute the function
	err := fn()

	// Record the result
	if err != nil {
		cb.recordFailure()
		return err
	}

	cb.recordSuccess()
	return nil
}

// allowRequest checks if a request should be allowed
func (cb *CircuitBreaker) allowRequest() bool {
	state := CircuitState(atomic.LoadInt32(&cb.state))

	switch state {
	case StateClosed:
		return true
	case StateOpen:
		return cb.shouldAttemptReset()
	case StateHalfOpen:
		// In half-open state, allow limited requests to test if service recovered
		return atomic.LoadInt64(&cb.successes) < 3 // Allow up to 3 test requests
	default:
		return false
	}
}

// shouldAttemptReset checks if enough time has passed to try resetting the circuit
func (cb *CircuitBreaker) shouldAttemptReset() bool {
	lastFail := atomic.LoadInt64(&cb.lastFailTime)
	now := time.Now().UnixNano()

	if now-lastFail >= int64(cb.resetTimeout) {
		cb.setState(StateHalfOpen)
		return true
	}

	return false
}

// recordSuccess records a successful request
func (cb *CircuitBreaker) recordSuccess() {
	atomic.AddInt64(&cb.successes, 1)

	state := CircuitState(atomic.LoadInt32(&cb.state))
	if state == StateHalfOpen {
		// If we're in half-open and have enough successes, close the circuit
		successes := atomic.LoadInt64(&cb.successes)
		if successes >= 3 {
			cb.setState(StateClosed)
			atomic.StoreInt64(&cb.failures, 0)
			atomic.StoreInt64(&cb.successes, 0)
		}
	}
}

// recordFailure records a failed request
func (cb *CircuitBreaker) recordFailure() {
	failures := atomic.AddInt64(&cb.failures, 1)
	atomic.StoreInt64(&cb.lastFailTime, time.Now().UnixNano())

	state := CircuitState(atomic.LoadInt32(&cb.state))

	// Reset success counter on any failure in half-open state
	if state == StateHalfOpen {
		atomic.StoreInt64(&cb.successes, 0)
		cb.setState(StateOpen)
		return
	}

	// Open circuit if too many failures in closed state
	if state == StateClosed && failures >= cb.maxFailures {
		cb.setState(StateOpen)
	}
}

// setState changes the circuit breaker state
func (cb *CircuitBreaker) setState(newState CircuitState) {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	oldState := CircuitState(atomic.LoadInt32(&cb.state))
	if oldState == newState {
		return
	}

	atomic.StoreInt32(&cb.state, int32(newState))

	// Call state change callback if provided
	if cb.onStateChange != nil {
		go cb.onStateChange(cb.name, oldState, newState)
	}
}

// GetState returns the current state of the circuit breaker
func (cb *CircuitBreaker) GetState() CircuitState {
	return CircuitState(atomic.LoadInt32(&cb.state))
}

// GetStats returns statistics for the circuit breaker
func (cb *CircuitBreaker) GetStats() map[string]interface{} {
	return map[string]interface{}{
		"name":      cb.name,
		"state":     cb.GetState().String(),
		"failures":  atomic.LoadInt64(&cb.failures),
		"requests":  atomic.LoadInt64(&cb.requests),
		"successes": atomic.LoadInt64(&cb.successes),
	}
}

// Reset manually resets the circuit breaker to closed state
func (cb *CircuitBreaker) Reset() {
	cb.setState(StateClosed)
	atomic.StoreInt64(&cb.failures, 0)
	atomic.StoreInt64(&cb.requests, 0)
	atomic.StoreInt64(&cb.successes, 0)
}

// GetOrCreate gets an existing circuit breaker or creates a new one
func (cbm *CircuitBreakerManager) GetOrCreate(name string) *CircuitBreaker {
	cbm.mu.RLock()
	cb, exists := cbm.breakers[name]
	cbm.mu.RUnlock()

	if exists {
		return cb
	}

	cbm.mu.Lock()
	defer cbm.mu.Unlock()

	// Double-check after acquiring write lock
	cb, exists = cbm.breakers[name]
	if exists {
		return cb
	}

	// Create new circuit breaker
	config := cbm.config
	config.Name = name
	cb = NewCircuitBreaker(config)
	cbm.breakers[name] = cb

	atomic.AddInt64(&cbm.stats.TotalBreakers, 1)
	return cb
}

// Execute executes a function through the circuit breaker for the given name
func (cbm *CircuitBreakerManager) Execute(name string, fn func() error) error {
	return cbm.ExecuteWithContext(context.Background(), name, fn)
}

// ExecuteWithContext executes a function through the circuit breaker with context support
func (cbm *CircuitBreakerManager) ExecuteWithContext(ctx context.Context, name string, fn func() error) error {
	cb := cbm.GetOrCreate(name)

	atomic.AddInt64(&cbm.stats.TotalRequests, 1)

	err := cb.ExecuteWithContext(ctx, fn)
	if err != nil {
		if errors.Is(err, ErrCircuitOpen) || errors.Is(err, ErrTooManyRequests) {
			atomic.AddInt64(&cbm.stats.RejectedRequests, 1)
		} else {
			atomic.AddInt64(&cbm.stats.FailedRequests, 1)
		}
	}

	return err
}

// GetStats returns statistics for all circuit breakers
func (cbm *CircuitBreakerManager) GetStats() *CircuitBreakerStats {
	cbm.mu.RLock()
	defer cbm.mu.RUnlock()

	stats := &CircuitBreakerStats{
		TotalBreakers:    atomic.LoadInt64(&cbm.stats.TotalBreakers),
		TotalRequests:    atomic.LoadInt64(&cbm.stats.TotalRequests),
		FailedRequests:   atomic.LoadInt64(&cbm.stats.FailedRequests),
		RejectedRequests: atomic.LoadInt64(&cbm.stats.RejectedRequests),
		BreakerStates:    make(map[string]string),
	}

	var openCount, halfOpenCount, closedCount int64

	for name, cb := range cbm.breakers {
		state := cb.GetState()
		stats.BreakerStates[name] = state.String()

		switch state {
		case StateOpen:
			openCount++
		case StateHalfOpen:
			halfOpenCount++
		case StateClosed:
			closedCount++
		}
	}

	stats.OpenBreakers = openCount
	stats.HalfOpenBreakers = halfOpenCount
	stats.ClosedBreakers = closedCount

	return stats
}

// Reset resets a specific circuit breaker
func (cbm *CircuitBreakerManager) Reset(name string) error {
	cbm.mu.RLock()
	cb, exists := cbm.breakers[name]
	cbm.mu.RUnlock()

	if !exists {
		return fmt.Errorf("circuit breaker '%s' not found", name)
	}

	cb.Reset()
	return nil
}

// ResetAll resets all circuit breakers
func (cbm *CircuitBreakerManager) ResetAll() {
	cbm.mu.RLock()
	breakers := make([]*CircuitBreaker, 0, len(cbm.breakers))
	for _, cb := range cbm.breakers {
		breakers = append(breakers, cb)
	}
	cbm.mu.RUnlock()

	for _, cb := range breakers {
		cb.Reset()
	}
}

// Remove removes a circuit breaker
func (cbm *CircuitBreakerManager) Remove(name string) bool {
	cbm.mu.Lock()
	defer cbm.mu.Unlock()

	_, exists := cbm.breakers[name]
	if exists {
		delete(cbm.breakers, name)
		atomic.AddInt64(&cbm.stats.TotalBreakers, -1)
	}

	return exists
}

// Close shuts down the circuit breaker manager
func (cbm *CircuitBreakerManager) Close() error {
	cbm.mu.Lock()
	defer cbm.mu.Unlock()

	cbm.breakers = make(map[string]*CircuitBreaker)
	return nil
}
