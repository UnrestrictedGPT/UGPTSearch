package concurrency

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// ConnectionPool manages HTTP connections for different instances and browser types
type ConnectionPool struct {
	pools       map[string]*instancePool
	mu          sync.RWMutex
	config      ConnectionPoolConfig
	totalConns  int64
	activeConns int64
	metrics     *ConnectionMetrics
}

// ConnectionPoolConfig holds configuration for the connection pool
type ConnectionPoolConfig struct {
	MaxConnsPerInstance     int           // Maximum connections per instance
	MaxIdleConnsPerInstance int           // Maximum idle connections per instance
	IdleConnTimeout         time.Duration // How long idle connections are kept
	TLSHandshakeTimeout     time.Duration // TLS handshake timeout
	ResponseHeaderTimeout   time.Duration // Response header timeout
	DialTimeout             time.Duration // Connection dial timeout
	KeepAlive               time.Duration // Keep-alive duration
	MaxLifetime             time.Duration // Maximum connection lifetime
}

// instancePool manages connections for a specific instance
type instancePool struct {
	clients   map[string]*http.Client // Keyed by browser profile signature
	instance  string
	mu        sync.RWMutex
	config    ConnectionPoolConfig
	lastUsed  time.Time
	connCount int32
}

// ConnectionMetrics tracks connection pool performance
type ConnectionMetrics struct {
	TotalConnections   int64                    `json:"total_connections"`
	ActiveConnections  int64                    `json:"active_connections"`
	IdleConnections    int64                    `json:"idle_connections"`
	ConnectionReuse    int64                    `json:"connection_reuse"`
	ConnectionTimeouts int64                    `json:"connection_timeouts"`
	TLSHandshakeErrors int64                    `json:"tls_handshake_errors"`
	DNSResolutionTime  time.Duration            `json:"dns_resolution_time"`
	ConnectionTime     time.Duration            `json:"connection_time"`
	ResponseTimes      map[string]time.Duration `json:"response_times"` // Keyed by instance
	mu                 sync.RWMutex
}

// NewConnectionPool creates a new connection pool
func NewConnectionPool(config ConnectionPoolConfig) *ConnectionPool {
	if config.MaxConnsPerInstance <= 0 {
		config.MaxConnsPerInstance = 20
	}
	if config.MaxIdleConnsPerInstance <= 0 {
		config.MaxIdleConnsPerInstance = 10
	}
	if config.IdleConnTimeout <= 0 {
		config.IdleConnTimeout = 90 * time.Second
	}
	if config.TLSHandshakeTimeout <= 0 {
		config.TLSHandshakeTimeout = 10 * time.Second
	}
	if config.ResponseHeaderTimeout <= 0 {
		config.ResponseHeaderTimeout = 30 * time.Second
	}
	if config.DialTimeout <= 0 {
		config.DialTimeout = 10 * time.Second
	}
	if config.KeepAlive <= 0 {
		config.KeepAlive = 30 * time.Second
	}
	if config.MaxLifetime <= 0 {
		config.MaxLifetime = 10 * time.Minute
	}

	pool := &ConnectionPool{
		pools:   make(map[string]*instancePool),
		config:  config,
		metrics: &ConnectionMetrics{ResponseTimes: make(map[string]time.Duration)},
	}

	// Start cleanup goroutine
	go pool.cleanupRoutine()

	return pool
}

// GetClient returns an optimized HTTP client for the given instance and browser profile
func (cp *ConnectionPool) GetClient(instance string, browserProfile BrowserProfile) *http.Client {
	cp.mu.Lock()
	defer cp.mu.Unlock()

	// Get or create instance pool
	instPool, exists := cp.pools[instance]
	if !exists {
		instPool = &instancePool{
			clients:  make(map[string]*http.Client),
			instance: instance,
			config:   cp.config,
			lastUsed: time.Now(),
		}
		cp.pools[instance] = instPool
	}

	// Update last used time
	instPool.lastUsed = time.Now()

	return instPool.getClient(browserProfile, cp.metrics)
}

// BrowserProfile represents browser characteristics for connection optimization
type BrowserProfile struct {
	UserAgent       string
	BrowserType     string // "chrome", "firefox", "safari"
	Platform        string // "windows", "macos", "linux"
	HTTP2Enabled    bool
	CompressionType string // "gzip,deflate,br" etc.
}

// getClient gets or creates a client for the browser profile
func (ip *instancePool) getClient(profile BrowserProfile, metrics *ConnectionMetrics) *http.Client {
	ip.mu.Lock()
	defer ip.mu.Unlock()

	// Create a signature for the browser profile
	signature := fmt.Sprintf("%s_%s_%s_%t",
		profile.BrowserType, profile.Platform, profile.CompressionType, profile.HTTP2Enabled)

	client, exists := ip.clients[signature]
	if exists {
		atomic.AddInt64(&metrics.ConnectionReuse, 1)
		return client
	}

	// Create new client optimized for this browser profile
	client = ip.createOptimizedClient(profile, metrics)
	ip.clients[signature] = client
	atomic.AddInt32(&ip.connCount, 1)
	atomic.AddInt64(&metrics.TotalConnections, 1)

	return client
}

// createOptimizedClient creates an HTTP client optimized for the browser profile
func (ip *instancePool) createOptimizedClient(profile BrowserProfile, metrics *ConnectionMetrics) *http.Client {
	// Custom dialer with connection tracking
	dialer := &net.Dialer{
		Timeout:   ip.config.DialTimeout,
		KeepAlive: ip.config.KeepAlive,
	}

	// Wrap dialer to track metrics
	trackedDialer := func(ctx context.Context, network, addr string) (net.Conn, error) {
		start := time.Now()

		conn, err := dialer.DialContext(ctx, network, addr)
		if err != nil {
			return nil, err
		}

		// Track connection metrics
		atomic.AddInt64(&metrics.ActiveConnections, 1)
		metrics.mu.Lock()
		metrics.ConnectionTime = time.Since(start)
		metrics.mu.Unlock()

		// Wrap connection to track when it's closed
		return &trackedConn{Conn: conn, metrics: metrics}, nil
	}

	// Create transport optimized for browser type
	transport := &http.Transport{
		DialContext:           trackedDialer,
		MaxIdleConns:          ip.config.MaxConnsPerInstance,
		MaxIdleConnsPerHost:   ip.config.MaxIdleConnsPerInstance,
		IdleConnTimeout:       ip.config.IdleConnTimeout,
		TLSHandshakeTimeout:   ip.config.TLSHandshakeTimeout,
		ResponseHeaderTimeout: ip.config.ResponseHeaderTimeout,
		ExpectContinueTimeout: 1 * time.Second,
		DisableKeepAlives:     false,
		DisableCompression:    false,
		MaxConnsPerHost:       ip.config.MaxConnsPerInstance,
	}

	// Browser-specific optimizations
	switch profile.BrowserType {
	case "chrome":
		transport.ForceAttemptHTTP2 = profile.HTTP2Enabled
		transport.MaxIdleConnsPerHost = 15 // Chrome is aggressive with connections
		transport.TLSHandshakeTimeout = 8 * time.Second

		// Chrome TLS config
		transport.TLSClientConfig = &tls.Config{
			MinVersion: tls.VersionTLS12,
			MaxVersion: tls.VersionTLS13,
			CipherSuites: []uint16{
				tls.TLS_AES_128_GCM_SHA256,
				tls.TLS_AES_256_GCM_SHA384,
				tls.TLS_CHACHA20_POLY1305_SHA256,
				tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
				tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
				tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
				tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
			},
		}

	case "firefox":
		transport.ForceAttemptHTTP2 = profile.HTTP2Enabled
		transport.MaxIdleConnsPerHost = 8 // Firefox is more conservative
		transport.ResponseHeaderTimeout = 25 * time.Second

		// Firefox TLS config
		transport.TLSClientConfig = &tls.Config{
			MinVersion: tls.VersionTLS12,
			MaxVersion: tls.VersionTLS13,
			CipherSuites: []uint16{
				tls.TLS_AES_128_GCM_SHA256,
				tls.TLS_CHACHA20_POLY1305_SHA256,
				tls.TLS_AES_256_GCM_SHA384,
				tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
				tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
			},
		}

	case "safari":
		transport.ForceAttemptHTTP2 = profile.HTTP2Enabled
		transport.MaxIdleConnsPerHost = 6 // Safari is most conservative
		transport.IdleConnTimeout = 60 * time.Second

		// Safari TLS config
		transport.TLSClientConfig = &tls.Config{
			MinVersion: tls.VersionTLS12,
			MaxVersion: tls.VersionTLS13,
		}

	default:
		// Default configuration
		transport.ForceAttemptHTTP2 = true
	}

	// Set common TLS configuration if not set
	if transport.TLSClientConfig == nil {
		transport.TLSClientConfig = &tls.Config{
			MinVersion: tls.VersionTLS12,
			MaxVersion: tls.VersionTLS13,
		}
	}

	// Create client with browser-specific timeout
	timeout := 45 * time.Second
	switch profile.BrowserType {
	case "chrome":
		timeout = 30 * time.Second
	case "firefox":
		timeout = 60 * time.Second
	case "safari":
		timeout = 45 * time.Second
	}

	client := &http.Client{
		Transport: transport,
		Timeout:   timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return http.ErrUseLastResponse
			}
			return nil
		},
	}

	return client
}

// trackedConn wraps net.Conn to track when connections are closed
type trackedConn struct {
	net.Conn
	metrics *ConnectionMetrics
	closed  int64
}

func (tc *trackedConn) Close() error {
	if atomic.CompareAndSwapInt64(&tc.closed, 0, 1) {
		atomic.AddInt64(&tc.metrics.ActiveConnections, -1)
	}
	return tc.Conn.Close()
}

// cleanupRoutine periodically cleans up unused connections
func (cp *ConnectionPool) cleanupRoutine() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		cp.cleanup()
	}
}

// cleanup removes unused instance pools and connection pools
func (cp *ConnectionPool) cleanup() {
	cp.mu.Lock()
	defer cp.mu.Unlock()

	now := time.Now()
	for instance, pool := range cp.pools {
		// Remove pools that haven't been used recently
		if now.Sub(pool.lastUsed) > 10*time.Minute {
			pool.close()
			delete(cp.pools, instance)
		}
	}
}

// close cleans up an instance pool
func (ip *instancePool) close() {
	ip.mu.Lock()
	defer ip.mu.Unlock()

	// Close all clients (which closes their transports)
	for _, client := range ip.clients {
		if transport, ok := client.Transport.(*http.Transport); ok {
			transport.CloseIdleConnections()
		}
	}

	ip.clients = make(map[string]*http.Client)
}

// GetStats returns connection pool statistics
func (cp *ConnectionPool) GetStats() *ConnectionMetrics {
	cp.metrics.mu.RLock()
	defer cp.metrics.mu.RUnlock()

	// Calculate idle connections
	idleConns := atomic.LoadInt64(&cp.metrics.TotalConnections) - atomic.LoadInt64(&cp.metrics.ActiveConnections)

	return &ConnectionMetrics{
		TotalConnections:   atomic.LoadInt64(&cp.metrics.TotalConnections),
		ActiveConnections:  atomic.LoadInt64(&cp.metrics.ActiveConnections),
		IdleConnections:    idleConns,
		ConnectionReuse:    atomic.LoadInt64(&cp.metrics.ConnectionReuse),
		ConnectionTimeouts: atomic.LoadInt64(&cp.metrics.ConnectionTimeouts),
		TLSHandshakeErrors: atomic.LoadInt64(&cp.metrics.TLSHandshakeErrors),
		DNSResolutionTime:  cp.metrics.DNSResolutionTime,
		ConnectionTime:     cp.metrics.ConnectionTime,
		ResponseTimes:      make(map[string]time.Duration),
	}
}

// Close shuts down the connection pool
func (cp *ConnectionPool) Close() error {
	cp.mu.Lock()
	defer cp.mu.Unlock()

	// Close all instance pools
	for _, pool := range cp.pools {
		pool.close()
	}

	cp.pools = make(map[string]*instancePool)
	return nil
}
