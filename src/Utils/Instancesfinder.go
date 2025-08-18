package Utils

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// InstanceDetails holds the specific fields we need to check for a healthy instance.
type InstanceDetails struct {
	NetworkType string `json:"network_type"`
	Generator   string `json:"generator"`
}

// InstancesData is the top-level structure of the JSON response from searx.space.
type InstancesData struct {
	Instances map[string]InstanceDetails `json:"instances"`
}

// GetHealthyInstances fetches instances from searx.space, filters for healthy ones,
// and returns a slice of their base URLs.
func GetHealthyInstances() ([]string, error) {
	resp, err := http.Get("https://searx.space/data/instances.json")
	if err != nil {
		return nil, fmt.Errorf("failed to fetch instances data: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to fetch instances data: status code %d", resp.StatusCode)
	}

	var data InstancesData
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, fmt.Errorf("failed to decode instances JSON: %w", err)
	}

	var healthyInstances []string
	for url, details := range data.Instances {
		if details.NetworkType == "normal" && details.Generator == "searxng" {
			healthyInstances = append(healthyInstances, url)
		}
	}

	return healthyInstances, nil
}
