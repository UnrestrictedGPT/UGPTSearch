package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"UGPTSearch/Utils"
)

// InstanceManager handles the list of Searx instances
type InstanceManager struct {
	instances []string
	mu        sync.RWMutex
}

// NewInstanceManager creates a new instance manager
func NewInstanceManager() *InstanceManager {
	return &InstanceManager{
		instances: make([]string, 0),
	}
}

// AddInstance adds a new instance to the list
func (im *InstanceManager) AddInstance(instance string) {
	im.mu.Lock()
	defer im.mu.Unlock()
	im.instances = append(im.instances, instance)
}

// GetRandomInstance returns a random instance from the list
func (im *InstanceManager) GetRandomInstance() (string, error) {
	im.mu.RLock()
	defer im.mu.RUnlock()
	
	if len(im.instances) == 0 {
		return "", fmt.Errorf("no instances available")
	}
	
	return im.instances[rand.Intn(len(im.instances))], nil
}

// SetInstances replaces all instances with a new list
func (im *InstanceManager) SetInstances(instances []string) {
	im.mu.Lock()
	defer im.mu.Unlock()
	im.instances = instances
}

// GetInstances returns a copy of the current instances
func (im *InstanceManager) GetInstances() []string {
	im.mu.RLock()
	defer im.mu.RUnlock()
	
	instances := make([]string, len(im.instances))
	copy(instances, im.instances)
	return instances
}

// LoadInstancesFromFile loads instances from a JSON file
func (im *InstanceManager) LoadInstancesFromFile(filename string) error {
	file, err := os.Open(filename)
	if err != nil {
		return fmt.Errorf("failed to open instances file: %w", err)
	}
	defer file.Close()

	var instances []string
	decoder := json.NewDecoder(file)
	if err := decoder.Decode(&instances); err != nil {
		return fmt.Errorf("failed to decode instances file: %w", err)
	}

	im.SetInstances(instances)
	log.Printf("Loaded %d instances from %s", len(instances), filename)
	return nil
}

// FetchInstancesFromURL fetches instances from a remote URL
func (im *InstanceManager) FetchInstancesFromURL(url string) error {
	client := &http.Client{
		Timeout: 10 * time.Second,
	}
	
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to fetch instances: %w", err)
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to fetch instances: status code %d", resp.StatusCode)
	}
	
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response body: %w", err)
	}
	
	var instances []string
	if err := json.Unmarshal(body, &instances); err != nil {
		return fmt.Errorf("failed to unmarshal instances: %w", err)
	}
	
	im.SetInstances(instances)
	log.Printf("Fetched %d instances from %s", len(instances), url)
	return nil
}

var (
	instanceManager *InstanceManager
	httpClient      = &http.Client{
		Timeout: 10 * time.Second,
	}
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Initialize random seed
	rand.Seed(time.Now().UnixNano())

	// Initialize instance manager
	instanceManager = NewInstanceManager()

	// Fetch healthy instances from searx.space
	healthyInstances, err := Utils.GetHealthyInstances()
	if err != nil {
		log.Printf("Could not fetch healthy instances: %v", err)
		log.Println("Starting with no instances. The service may not be available.")
	} else {
		log.Printf("Successfully fetched %d healthy instances.", len(healthyInstances))
		instanceManager.SetInstances(healthyInstances)
	}

	// Set up HTTP handlers
	http.HandleFunc("/search", loggingMiddleware(searchHandler))
	http.HandleFunc("/instances", loggingMiddleware(instancesHandler))

	// Create server with timeouts
	server := &http.Server{
		Addr:         ":8080",
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  30 * time.Second,
	}

	// Start server in a goroutine
	go func() {
		log.Printf("Starting server on %s", server.Addr)
		log.Printf("Loaded %d instances", len(instanceManager.GetInstances()))
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server failed: %v", err)
		}
	}()

	// Wait for interrupt signal
	<-ctx.Done()
	log.Println("Shutting down gracefully...")

	// Attempt graceful shutdown
	ctxTimeout, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(ctxTimeout); err != nil {
		log.Printf("Shutdown error: %v", err)
	}
	log.Println("Server stopped")
}

func loggingMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		log.Printf("[%s] %s %s", r.Method, r.URL.Path, time.Since(start))
		next(w, r)
	}
}

func searchHandler(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("q")
	if query == "" {
		http.Error(w, "Missing search query", http.StatusBadRequest)
		return
	}

	format := r.URL.Query().Get("format")
	if format == "" {
		format = "json"
	}

	instance, err := instanceManager.GetRandomInstance()
	if err != nil {
		http.Error(w, fmt.Sprintf("No instances available: %s", err), http.StatusServiceUnavailable)
		return
	}

	searchURL := fmt.Sprintf("%s?q=%s&format=%s", instance, url.QueryEscape(query), format)

	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, searchURL, nil)
	if err != nil {
		http.Error(w, "Invalid request", http.StatusInternalServerError)
		return
	}
	req.Header.Set("User-Agent", "UGPTSearch/1.0")

	resp, err := httpClient.Do(req)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to fetch from instance: %s", err), http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		http.Error(w, "Failed to read response body", http.StatusInternalServerError)
		return
	}

	if format == "json" {
		w.Header().Set("Content-Type", "application/json")
	} else {
		w.Header().Set("Content-Type", "text/plain")
	}

	w.Write(body)
}

func instancesHandler(w http.ResponseWriter, r *http.Request) {
	instances := instanceManager.GetInstances()
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"instances": instances,
		"count":     len(instances),
	})
}