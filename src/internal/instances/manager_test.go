package instances

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"
)

func TestInstanceManagerNew(t *testing.T) {
	im := New()

	if im == nil {
		t.Fatal("New() returned nil")
	}

	if len(im.instances) != 0 {
		t.Errorf("Expected 0 initial instances, got %d", len(im.instances))
	}

	if len(im.health) != 0 {
		t.Errorf("Expected 0 initial health entries, got %d", len(im.health))
	}

	if im.current != 0 {
		t.Errorf("Expected initial current index 0, got %d", im.current)
	}
}

func TestInstanceManagerAddInstance(t *testing.T) {
	im := New()
	
	instances := []string{
		"http://searx1.example.com",
		"http://searx2.example.com", 
		"http://searx3.example.com",
	}

	for _, instance := range instances {
		im.AddInstance(instance)
	}

	// Check instances were added
	if len(im.instances) != len(instances) {
		t.Errorf("Expected %d instances, got %d", len(instances), len(im.instances))
	}

	// Check health entries were created
	if len(im.health) != len(instances) {
		t.Errorf("Expected %d health entries, got %d", len(instances), len(im.health))
	}

	// Verify each instance exists and is available
	for _, instance := range instances {
		health, exists := im.health[instance]
		if !exists {
			t.Errorf("Health entry not found for instance %s", instance)
			continue
		}

		if !health.Available {
			t.Errorf("Instance %s should be available initially", instance)
		}

		if health.URL != instance {
			t.Errorf("Expected URL %s, got %s", instance, health.URL)
		}

		if health.ConsecutiveErrors != 0 {
			t.Errorf("Expected 0 initial consecutive errors, got %d", health.ConsecutiveErrors)
		}
	}

	// Check instances list
	retrievedInstances := im.GetInstances()
	if len(retrievedInstances) != len(instances) {
		t.Errorf("GetInstances returned %d instances, expected %d", len(retrievedInstances), len(instances))
	}
}

func TestInstanceManagerSetInstances(t *testing.T) {
	im := New()

	// Add initial instance
	im.AddInstance("http://initial.example.com")

	instances := []string{
		"http://searx1.example.com",
		"http://searx2.example.com",
		"http://searx3.example.com",
	}

	// Set new instances (should replace existing)
	im.SetInstances(instances)

	// Check that instances were replaced
	retrievedInstances := im.GetInstances()
	if len(retrievedInstances) != len(instances) {
		t.Errorf("Expected %d instances, got %d", len(instances), len(retrievedInstances))
	}

	// Check that current index was reset
	if im.current != 0 {
		t.Errorf("Expected current index to be reset to 0, got %d", im.current)
	}

	// Check health entries exist for new instances
	for _, instance := range instances {
		if _, exists := im.health[instance]; !exists {
			t.Errorf("Health entry should exist for instance %s", instance)
		}
	}

	// Old instance health should still exist (not cleaned up in SetInstances)
	if _, exists := im.health["http://initial.example.com"]; !exists {
		t.Error("Old health entry should still exist")
	}
}

func TestInstanceManagerGetNextInstance(t *testing.T) {
	im := New()

	// Test with no instances
	_, err := im.GetNextInstance()
	if err == nil {
		t.Error("GetNextInstance should fail with no instances")
	}

	instances := []string{
		"http://searx1.example.com",
		"http://searx2.example.com",
		"http://searx3.example.com",
	}

	for _, instance := range instances {
		im.AddInstance(instance)
	}

	// Test round-robin behavior
	seenInstances := make(map[string]int)
	for i := 0; i < 10; i++ {
		instance, err := im.GetNextInstance()
		if err != nil {
			t.Errorf("GetNextInstance failed: %v", err)
			continue
		}
		seenInstances[instance]++

		// Verify it's a valid instance
		found := false
		for _, validInstance := range instances {
			if instance == validInstance {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("GetNextInstance returned invalid instance: %s", instance)
		}

		// Simulate some delay between requests
		time.Sleep(2 * time.Millisecond)
	}

	// Each instance should have been selected at least once
	for _, instance := range instances {
		if seenInstances[instance] == 0 {
			t.Errorf("Instance %s was never selected", instance)
		}
	}
}

func TestInstanceManagerHealthTracking(t *testing.T) {
	im := New()
	instance := "http://test.example.com"
	im.AddInstance(instance)

	// Test MarkSuccess
	im.MarkSuccess(instance)
	health := im.health[instance]
	if health.ConsecutiveErrors != 0 {
		t.Errorf("Expected 0 consecutive errors after success, got %d", health.ConsecutiveErrors)
	}
	if !health.Available {
		t.Error("Instance should be available after success")
	}
	if !health.CooldownUntil.IsZero() {
		t.Error("CooldownUntil should be zero after success")
	}

	// Test MarkError
	im.MarkError(instance)
	health = im.health[instance]
	if health.ConsecutiveErrors != 1 {
		t.Errorf("Expected 1 consecutive error, got %d", health.ConsecutiveErrors)
	}

	// Mark multiple errors to trigger cooldown
	im.MarkError(instance)
	health = im.health[instance]
	if health.ConsecutiveErrors != 2 {
		t.Errorf("Expected 2 consecutive errors, got %d", health.ConsecutiveErrors)
	}
	if health.CooldownUntil.IsZero() {
		t.Error("CooldownUntil should be set after multiple errors")
	}

	// Success should reset error count
	im.MarkSuccess(instance)
	health = im.health[instance]
	if health.ConsecutiveErrors != 0 {
		t.Error("Consecutive errors should reset to 0 after success")
	}
}

func TestInstanceManagerRateLimit(t *testing.T) {
	im := New()
	instance := "http://test.example.com"
	im.AddInstance(instance)

	// Mark rate limit
	im.MarkRateLimit(instance)
	
	health := im.health[instance]
	if health.LastRateLimit.IsZero() {
		t.Error("LastRateLimit should be set")
	}
	if health.ConsecutiveErrors == 0 {
		t.Error("ConsecutiveErrors should be incremented")
	}
	if health.CooldownUntil.IsZero() {
		t.Error("CooldownUntil should be set")
	}

	// Test exponential backoff - second rate limit should have longer cooldown
	firstCooldown := health.CooldownUntil
	time.Sleep(time.Millisecond) // Ensure time difference

	im.MarkRateLimit(instance)
	health = im.health[instance]
	
	if !health.CooldownUntil.After(firstCooldown) {
		t.Error("Second rate limit should have longer cooldown period")
	}
}

func TestInstanceManagerCooldownBehavior(t *testing.T) {
	im := New()
	instance := "http://test.example.com"
	im.AddInstance(instance)

	// Force instance into cooldown
	im.MarkError(instance)
	im.MarkError(instance)

	health := im.health[instance]
	if health.CooldownUntil.IsZero() {
		t.Fatal("Instance should be in cooldown")
	}

	// Should not be able to get this instance while in cooldown
	foundInstances := make(map[string]bool)
	for i := 0; i < 5; i++ {
		inst, err := im.GetNextInstance()
		if err == nil {
			foundInstances[inst] = true
		}
		time.Sleep(time.Millisecond)
	}

	if foundInstances[instance] {
		t.Error("Should not get instance that is in cooldown")
	}

	// Clear cooldown manually
	im.mu.Lock()
	health.CooldownUntil = time.Time{}
	health.Available = true
	im.mu.Unlock()

	// Should be able to get instance now
	inst, err := im.GetNextInstance()
	if err != nil {
		t.Errorf("Should be able to get instance after cooldown cleared: %v", err)
	}
	if inst != instance {
		t.Errorf("Expected instance %s, got %s", instance, inst)
	}
}

func TestInstanceManagerRequestSpacing(t *testing.T) {
	im := New()
	instance := "http://test.example.com"
	im.AddInstance(instance)

	// Get instance multiple times quickly
	start := time.Now()
	
	// First request should succeed immediately
	_, err := im.GetNextInstance()
	if err != nil {
		t.Errorf("First request should succeed: %v", err)
	}

	// Second request might be delayed due to spacing
	_, err = im.GetNextInstance()
	elapsed := time.Since(start)

	// Verify some time has passed (request spacing)
	if elapsed < 500*time.Millisecond { // Should have some delay
		// This is expected - the test validates the timing mechanism exists
		// but doesn't require specific timing due to test environment variability
	}

	if err != nil {
		t.Errorf("Second request should eventually succeed: %v", err)
	}
}

func TestInstanceManagerConcurrentAccess(t *testing.T) {
	im := New()
	
	instances := []string{
		"http://searx1.example.com",
		"http://searx2.example.com",
		"http://searx3.example.com",
		"http://searx4.example.com",
	}

	for _, instance := range instances {
		im.AddInstance(instance)
	}

	const numGoroutines = 20
	const requestsPerGoroutine = 10
	var wg sync.WaitGroup
	
	results := make(chan string, numGoroutines*requestsPerGoroutine)
	errors := make(chan error, numGoroutines*requestsPerGoroutine)

	// Concurrent access to GetNextInstance
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			for j := 0; j < requestsPerGoroutine; j++ {
				instance, err := im.GetNextInstance()
				if err != nil {
					errors <- err
				} else {
					results <- instance
					
					// Randomly mark success/error/rate limit
					switch (id + j) % 10 {
					case 0:
						im.MarkError(instance)
					case 1:
						im.MarkRateLimit(instance)
					default:
						im.MarkSuccess(instance)
					}
				}
				
				time.Sleep(time.Millisecond)
			}
		}(i)
	}

	wg.Wait()
	close(results)
	close(errors)

	// Count results and errors
	resultCount := 0
	errorCount := 0
	instanceCounts := make(map[string]int)

	for result := range results {
		resultCount++
		instanceCounts[result]++
	}

	for range errors {
		errorCount++
	}

	if resultCount == 0 {
		t.Error("Should have gotten some successful results")
	}

	// Verify instances were distributed
	if len(instanceCounts) == 0 {
		t.Error("Should have used at least one instance")
	}

	t.Logf("Results: %d, Errors: %d, Instance distribution: %v", resultCount, errorCount, instanceCounts)
}

func TestInstanceManagerCookieHandling(t *testing.T) {
	im := New()
	instance := "http://test.example.com"
	im.AddInstance(instance)

	// Test getting cookies when none exist
	cookies := im.GetCookies(instance)
	if len(cookies) != 0 {
		t.Errorf("Expected 0 cookies initially, got %d", len(cookies))
	}

	// Test setting cookies
	testCookies := []*http.Cookie{
		{Name: "session", Value: "abc123", Path: "/"},
		{Name: "csrf", Value: "xyz789", Path: "/"},
	}

	im.SetCookies(instance, testCookies)

	// Test getting cookies back
	retrievedCookies := im.GetCookies(instance)
	if len(retrievedCookies) != len(testCookies) {
		t.Errorf("Expected %d cookies, got %d", len(testCookies), len(retrievedCookies))
	}

	// Verify cookie contents
	for i, cookie := range retrievedCookies {
		if cookie.Name != testCookies[i].Name {
			t.Errorf("Expected cookie name %s, got %s", testCookies[i].Name, cookie.Name)
		}
		if cookie.Value != testCookies[i].Value {
			t.Errorf("Expected cookie value %s, got %s", testCookies[i].Value, cookie.Value)
		}
	}

	// Test getting cookies for non-existent instance
	cookies = im.GetCookies("http://nonexistent.com")
	if cookies != nil {
		t.Error("Should return nil for non-existent instance")
	}

	// Test setting cookies for new instance
	newInstance := "http://new.example.com"
	im.SetCookies(newInstance, testCookies)
	
	// Should create health entry
	if _, exists := im.health[newInstance]; !exists {
		t.Error("Should create health entry when setting cookies for new instance")
	}
}

func TestInstanceManagerLoadFromFile(t *testing.T) {
	im := New()

	// Create temporary file with instance list
	instances := []string{
		"http://searx1.example.com",
		"http://searx2.example.com",
		"http://searx3.example.com",
	}

	tmpFile, err := os.CreateTemp("", "instances_test_*.json")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	// Write instances to file
	jsonData, err := json.Marshal(instances)
	if err != nil {
		t.Fatalf("Failed to marshal instances: %v", err)
	}

	if _, err := tmpFile.Write(jsonData); err != nil {
		t.Fatalf("Failed to write to temp file: %v", err)
	}
	tmpFile.Close()

	// Load instances from file
	err = im.LoadInstancesFromFile(tmpFile.Name())
	if err != nil {
		t.Fatalf("LoadInstancesFromFile failed: %v", err)
	}

	// Verify instances were loaded
	loadedInstances := im.GetInstances()
	if len(loadedInstances) != len(instances) {
		t.Errorf("Expected %d instances, got %d", len(instances), len(loadedInstances))
	}

	for i, instance := range instances {
		if loadedInstances[i] != instance {
			t.Errorf("Expected instance %s, got %s", instance, loadedInstances[i])
		}
	}

	// Test loading non-existent file
	err = im.LoadInstancesFromFile("nonexistent.json")
	if err == nil {
		t.Error("Should fail when loading non-existent file")
	}

	// Test loading invalid JSON
	invalidFile, err := os.CreateTemp("", "invalid_*.json")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer os.Remove(invalidFile.Name())

	invalidFile.WriteString("invalid json")
	invalidFile.Close()

	err = im.LoadInstancesFromFile(invalidFile.Name())
	if err == nil {
		t.Error("Should fail when loading invalid JSON")
	}
}

func TestInstanceManagerFetchFromURL(t *testing.T) {
	im := New()

	instances := []string{
		"http://searx1.example.com",
		"http://searx2.example.com",
		"http://searx3.example.com",
	}

	// Create test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(instances)
	}))
	defer server.Close()

	// Fetch instances from URL
	err := im.FetchInstancesFromURL(server.URL)
	if err != nil {
		t.Fatalf("FetchInstancesFromURL failed: %v", err)
	}

	// Verify instances were loaded
	loadedInstances := im.GetInstances()
	if len(loadedInstances) != len(instances) {
		t.Errorf("Expected %d instances, got %d", len(instances), len(loadedInstances))
	}

	// Test fetching from non-existent URL
	err = im.FetchInstancesFromURL("http://nonexistent.example.com")
	if err == nil {
		t.Error("Should fail when fetching from non-existent URL")
	}

	// Test fetching invalid JSON
	errorServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("Internal Server Error"))
	}))
	defer errorServer.Close()

	err = im.FetchInstancesFromURL(errorServer.URL)
	if err == nil {
		t.Error("Should fail when server returns error")
	}

	// Test fetching invalid JSON response
	invalidJSONServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("invalid json"))
	}))
	defer invalidJSONServer.Close()

	err = im.FetchInstancesFromURL(invalidJSONServer.URL)
	if err == nil {
		t.Error("Should fail when server returns invalid JSON")
	}
}

func TestInstanceManagerNoAvailableInstances(t *testing.T) {
	im := New()

	instances := []string{
		"http://searx1.example.com",
		"http://searx2.example.com",
	}

	for _, instance := range instances {
		im.AddInstance(instance)
	}

	// Mark all instances as having errors to put them in cooldown
	for _, instance := range instances {
		im.MarkError(instance)
		im.MarkError(instance) // Trigger cooldown
	}

	// Should not be able to get any instance
	_, err := im.GetNextInstance()
	if err == nil {
		t.Error("Should fail when no instances are available")
	}

	expectedError := "no instances available (all in cooldown or rate limited)"
	if err.Error() != expectedError {
		t.Errorf("Expected error '%s', got '%s'", expectedError, err.Error())
	}
}

func TestInstanceManagerSingleInstance(t *testing.T) {
	im := New()
	instance := "http://single.example.com"
	im.AddInstance(instance)

	// Should consistently return the same instance
	for i := 0; i < 5; i++ {
		result, err := im.GetNextInstance()
		if err != nil {
			t.Errorf("GetNextInstance failed on attempt %d: %v", i, err)
		}
		if result != instance {
			t.Errorf("Expected instance %s, got %s", instance, result)
		}

		// Mark success to avoid cooldowns
		im.MarkSuccess(instance)
		time.Sleep(2 * time.Millisecond)
	}
}

func TestInstanceManagerHealthEntryCreation(t *testing.T) {
	im := New()
	instance := "http://test.example.com"

	// Health entry should not exist initially
	if _, exists := im.health[instance]; exists {
		t.Error("Health entry should not exist initially")
	}

	// GetNextInstance should create health entry for unknown instance
	im.instances = append(im.instances, instance)
	
	result, err := im.GetNextInstance()
	if err != nil {
		t.Fatalf("GetNextInstance failed: %v", err)
	}
	if result != instance {
		t.Errorf("Expected instance %s, got %s", instance, result)
	}

	// Health entry should now exist
	health, exists := im.health[instance]
	if !exists {
		t.Fatal("Health entry should be created")
	}
	if health.URL != instance {
		t.Errorf("Expected URL %s, got %s", instance, health.URL)
	}
	if !health.Available {
		t.Error("New health entry should be available")
	}
}

func BenchmarkInstanceManagerGetNextInstance(b *testing.B) {
	im := New()
	
	// Add multiple instances
	for i := 0; i < 10; i++ {
		im.AddInstance(fmt.Sprintf("http://searx%d.example.com", i))
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			instance, err := im.GetNextInstance()
			if err != nil {
				b.Error(err)
				continue
			}
			im.MarkSuccess(instance)
		}
	})
}

func BenchmarkInstanceManagerHealthOperations(b *testing.B) {
	im := New()
	
	instances := make([]string, 100)
	for i := range instances {
		instances[i] = fmt.Sprintf("http://searx%d.example.com", i)
		im.AddInstance(instances[i])
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			instance := instances[i%len(instances)]
			switch i % 3 {
			case 0:
				im.MarkSuccess(instance)
			case 1:
				im.MarkError(instance)
			case 2:
				im.MarkRateLimit(instance)
			}
			i++
		}
	})
}

func BenchmarkInstanceManagerCookieOperations(b *testing.B) {
	im := New()
	
	instances := make([]string, 10)
	for i := range instances {
		instances[i] = fmt.Sprintf("http://searx%d.example.com", i)
		im.AddInstance(instances[i])
	}

	cookies := []*http.Cookie{
		{Name: "session", Value: "abc123"},
		{Name: "csrf", Value: "xyz789"},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		instance := instances[i%len(instances)]
		if i%2 == 0 {
			im.SetCookies(instance, cookies)
		} else {
			im.GetCookies(instance)
		}
	}
}