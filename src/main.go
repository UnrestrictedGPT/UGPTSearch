package main

import (
	"context"
	"flag"
	"log"
	"math/rand"
	"os"
	"os/signal"
	"syscall"
	"time"

	"UGPTSearch/internal/instances"
	"UGPTSearch/internal/server"
	"UGPTSearch/pkg/utils"
)

// Command line flags
var (
	useConcurrentServer = flag.Bool("concurrent", false, "Use the new concurrent server implementation")
	addr                = flag.String("addr", ":8080", "Server address")
	workers             = flag.Int("workers", 0, "Number of worker goroutines (0 = auto)")
	maxQueue            = flag.Int("max-queue", 20000, "Maximum queue size")
	rateLimit           = flag.Int64("rate-limit", 100, "Rate limit per client")
	globalRateLimit     = flag.Int64("global-rate-limit", 1000, "Global rate limit")
	metricsEnabled      = flag.Bool("metrics", true, "Enable metrics collection")
)

func main() {
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	initializeApplication()

	if *useConcurrentServer {
		runConcurrentServer(ctx)
	} else {
		runOriginalServer(ctx)
	}
}

func runConcurrentServer(ctx context.Context) {
	log.Println("Starting with concurrent server implementation")

	// Create server configuration
	config := &server.ConcurrentServerConfig{
		Addr:                    *addr,
		ReadTimeout:             30 * time.Second,
		WriteTimeout:            30 * time.Second,
		IdleTimeout:             60 * time.Second,
		MaxConcurrentRequests:   1000,
		WorkerPoolSize:          *workers,
		QueueSize:               10000,
		MaxQueueSize:            *maxQueue,
		RateLimitPerClient:      *rateLimit,
		RateLimitGlobal:         *globalRateLimit,
		CircuitBreakerThreshold: 5,
		ConnectionPoolMaxConns:  100,
		ConnectionPoolIdleConns: 20,
		MetricsEnabled:          *metricsEnabled,
		HealthCheckInterval:     30 * time.Second,
	}

	// If workers not specified, use default
	if *workers == 0 {
		config = server.DefaultConcurrentServerConfig()
		config.Addr = *addr
		config.MaxQueueSize = *maxQueue
		config.RateLimitPerClient = *rateLimit
		config.RateLimitGlobal = *globalRateLimit
		config.MetricsEnabled = *metricsEnabled
	}

	srv, err := server.NewConcurrentServer(config)
	if err != nil {
		log.Fatalf("Failed to create concurrent server: %v", err)
	}

	srv.SetupRoutes()

	go func() {
		log.Printf("Loaded %d instances", len(instances.Manager.GetInstances()))
		log.Printf("Server configuration:")
		log.Printf("  - Address: %s", config.Addr)
		log.Printf("  - Workers: %d", config.WorkerPoolSize)
		log.Printf("  - Max Queue: %d", config.MaxQueueSize)
		log.Printf("  - Rate Limit (per client): %d req/min", config.RateLimitPerClient)
		log.Printf("  - Rate Limit (global): %d req/min", config.RateLimitGlobal)
		log.Printf("  - Metrics Enabled: %t", config.MetricsEnabled)

		if config.MetricsEnabled {
			log.Printf("  - Metrics endpoint: http://%s/metrics", config.Addr)
			log.Printf("  - Health endpoint: http://%s/health", config.Addr)
			log.Printf("  - Admin stats: http://%s/admin/stats", config.Addr)
		}

		if err := srv.Start(); err != nil {
			log.Fatalf("Server failed: %v", err)
		}
	}()

	<-ctx.Done()

	// Graceful shutdown
	ctxTimeout, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	log.Println("Shutting down concurrent server...")
	if err := srv.Shutdown(ctxTimeout); err != nil {
		log.Printf("Shutdown error: %v", err)
	}
	log.Println("Concurrent server stopped")
}

func runOriginalServer(ctx context.Context) {
	log.Println("Starting with original server implementation")

	srv := server.New(*addr)
	srv.SetupRoutes()

	go func() {
		log.Printf("Loaded %d instances", len(instances.Manager.GetInstances()))
		if err := srv.Start(); err != nil && err.Error() != "http: Server closed" {
			log.Fatalf("Server failed: %v", err)
		}
	}()

	<-ctx.Done()

	ctxTimeout, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctxTimeout); err != nil {
		log.Printf("Shutdown error: %v", err)
	}
	log.Println("Server stopped")
}

func initializeApplication() {
	rand.Seed(time.Now().UnixNano())

	healthyInstances, err := utils.GetHealthyInstances()
	if err != nil {
		log.Printf("Could not fetch healthy instances: %v", err)
		log.Println("Starting with no instances. The service may not be available.")
	} else {
		log.Printf("Successfully fetched %d healthy instances.", len(healthyInstances))
		instances.Manager.SetInstances(healthyInstances)
	}
}
