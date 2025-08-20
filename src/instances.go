package main

import (
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"sync"
	"time"
)

type InstanceManager struct {
	instances []string
	mu        sync.RWMutex
}

func NewInstanceManager() *InstanceManager {
	return &InstanceManager{
		instances: make([]string, 0),
	}
}

func (im *InstanceManager) AddInstance(instance string) {
	im.mu.Lock()
	defer im.mu.Unlock()
	im.instances = append(im.instances, instance)
}

func (im *InstanceManager) GetRandomInstance() (string, error) {
	im.mu.RLock()
	defer im.mu.RUnlock()
	
	if len(im.instances) == 0 {
		return "", fmt.Errorf("no instances available")
	}
	
	return im.instances[rand.Intn(len(im.instances))], nil
}

func (im *InstanceManager) SetInstances(instances []string) {
	im.mu.Lock()
	defer im.mu.Unlock()
	im.instances = instances
}

func (im *InstanceManager) GetInstances() []string {
	im.mu.RLock()
	defer im.mu.RUnlock()
	
	instances := make([]string, len(im.instances))
	copy(instances, im.instances)
	return instances
}

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
	return nil
}

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
	return nil
}