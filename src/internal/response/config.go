package response

import (
	"strconv"
	"strings"
)

// ConfigFromParams creates a Config from URL parameters
func ConfigFromParams(params map[string][]string) Config {
	config := DefaultConfig

	// Parse max results
	if maxResults := getParam(params, "max_results"); maxResults != "" {
		if val, err := strconv.Atoi(maxResults); err == nil && val > 0 && val <= 50 {
			config.MaxResults = val
		}
	}

	// Parse max description length
	if maxDescLen := getParam(params, "max_desc_len"); maxDescLen != "" {
		if val, err := strconv.Atoi(maxDescLen); err == nil && val > 0 && val <= 1000 {
			config.MaxDescriptionLen = val
		}
	}

	// Parse max title length
	if maxTitleLen := getParam(params, "max_title_len"); maxTitleLen != "" {
		if val, err := strconv.Atoi(maxTitleLen); err == nil && val > 0 && val <= 200 {
			config.MaxTitleLen = val
		}
	}

	// Parse minimum quality score
	if minScore := getParam(params, "min_score"); minScore != "" {
		if val, err := strconv.ParseFloat(minScore, 64); err == nil && val >= 0.0 && val <= 5.0 {
			config.MinQualityScore = val
		}
	}

	// Parse truncate indicator
	if indicator := getParam(params, "truncate_indicator"); indicator != "" && len(indicator) <= 10 {
		config.TruncateIndicator = indicator
	}

	return config
}

// getParam gets the first value for a parameter key
func getParam(params map[string][]string, key string) string {
	if values, ok := params[key]; ok && len(values) > 0 {
		return strings.TrimSpace(values[0])
	}
	return ""
}

// Preset configurations for different use cases
var (
	// CompactConfig for minimal responses
	CompactConfig = Config{
		MaxResults:        5,
		MaxDescriptionLen: 100,
		MaxTitleLen:       50,
		MinQualityScore:   1.0,
		TruncateIndicator: "...",
	}

	// DetailedConfig for comprehensive responses
	DetailedConfig = Config{
		MaxResults:        20,
		MaxDescriptionLen: 500,
		MaxTitleLen:       150,
		MinQualityScore:   0.0,
		TruncateIndicator: " [truncated]",
	}

	// APIConfig optimized for API consumers
	APIConfig = Config{
		MaxResults:        15,
		MaxDescriptionLen: 300,
		MaxTitleLen:       100,
		MinQualityScore:   0.5,
		TruncateIndicator: "...",
	}
)

// GetPresetConfig returns a preset configuration by name
func GetPresetConfig(name string) Config {
	switch strings.ToLower(name) {
	case "compact":
		return CompactConfig
	case "detailed":
		return DetailedConfig
	case "api":
		return APIConfig
	default:
		return DefaultConfig
	}
}
