package instances

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"time"
)

type InstanceHealth struct {
	URL               string
	Available         bool
	LastRateLimit     time.Time
	LastRequest       time.Time
	ConsecutiveErrors int
	CooldownUntil     time.Time
}

type InstanceManager struct {
	instances []string
	health    map[string]*InstanceHealth
	current   int
	mu        sync.RWMutex
}

var Manager *InstanceManager

func init() {
	Manager = New()
}

func New() *InstanceManager {
	return &InstanceManager{
		instances: make([]string, 0),
		health:    make(map[string]*InstanceHealth),
		current:   0,
	}
}

func (im *InstanceManager) AddInstance(instance string) {
	im.mu.Lock()
	defer im.mu.Unlock()
	im.instances = append(im.instances, instance)
	if im.health[instance] == nil {
		im.health[instance] = &InstanceHealth{
			URL:       instance,
			Available: true,
		}
	}
}

func (im *InstanceManager) GetNextInstance() (string, error) {
	im.mu.Lock()
	defer im.mu.Unlock()
	
	if len(im.instances) == 0 {
		return "", fmt.Errorf("no instances available")
	}
	
	now := time.Now()
	attempts := 0
	
	for attempts < len(im.instances) {
		instance := im.instances[im.current]
		im.current = (im.current + 1) % len(im.instances)
		attempts++
		
		health := im.health[instance]
		if health == nil {
			health = &InstanceHealth{
				URL:       instance,
				Available: true,
			}
			im.health[instance] = health
		}
		
		if now.Before(health.CooldownUntil) {
			continue
		}
		
		minRequestInterval := 100 * time.Millisecond
		if now.Sub(health.LastRequest) < minRequestInterval {
			continue
		}
		
		health.LastRequest = now
		return instance, nil
	}
	
	return "", fmt.Errorf("no instances available (all in cooldown or rate limited)")
}

func (im *InstanceManager) SetInstances(instances []string) {
	im.mu.Lock()
	defer im.mu.Unlock()
	im.instances = instances
	im.current = 0
	
	for _, instance := range instances {
		if im.health[instance] == nil {
			im.health[instance] = &InstanceHealth{
				URL:       instance,
				Available: true,
			}
		}
	}
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

func (im *InstanceManager) MarkRateLimit(instance string) {
	im.mu.Lock()
	defer im.mu.Unlock()
	
	health := im.health[instance]
	if health == nil {
		health = &InstanceHealth{
			URL:       instance,
			Available: true,
		}
		im.health[instance] = health
	}
	
	health.LastRateLimit = time.Now()
	health.ConsecutiveErrors++
	
	cooldownDuration := time.Duration(health.ConsecutiveErrors) * 30 * time.Second
	if cooldownDuration > 5*time.Minute {
		cooldownDuration = 5 * time.Minute
	}
	
	health.CooldownUntil = time.Now().Add(cooldownDuration)
}

func (im *InstanceManager) MarkSuccess(instance string) {
	im.mu.Lock()
	defer im.mu.Unlock()
	
	health := im.health[instance]
	if health == nil {
		health = &InstanceHealth{
			URL:       instance,
			Available: true,
		}
		im.health[instance] = health
	}
	
	health.ConsecutiveErrors = 0
	health.Available = true
	health.CooldownUntil = time.Time{}
}

func (im *InstanceManager) MarkError(instance string) {
	im.mu.Lock()
	defer im.mu.Unlock()
	
	health := im.health[instance]
	if health == nil {
		health = &InstanceHealth{
			URL:       instance,
			Available: true,
		}
		im.health[instance] = health
	}
	
	health.ConsecutiveErrors++
	
	if health.ConsecutiveErrors >= 3 {
		cooldownDuration := 30 * time.Second
		health.CooldownUntil = time.Now().Add(cooldownDuration)
	}
}