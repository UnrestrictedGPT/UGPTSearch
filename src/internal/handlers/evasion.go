package handlers

import (
	"math/rand"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Advanced evasion techniques for better bot detection bypass

// RequestPattern tracks request patterns to simulate human behavior
type RequestPattern struct {
	mu                   sync.RWMutex
	sessionStart         time.Time
	requestCount         int
	lastRequestTime      time.Time
	searchTerms          []string
	typicalPauseDuration time.Duration
}

var globalPattern = &RequestPattern{
	sessionStart:         time.Now(),
	typicalPauseDuration: 2 * time.Second,
}

// TrackRequest records a new request for pattern analysis
func (rp *RequestPattern) TrackRequest(searchTerm string) {
	rp.mu.Lock()
	defer rp.mu.Unlock()

	rp.requestCount++
	rp.lastRequestTime = time.Now()
	rp.searchTerms = append(rp.searchTerms, searchTerm)

	// Keep only last 10 search terms to avoid memory growth
	if len(rp.searchTerms) > 10 {
		rp.searchTerms = rp.searchTerms[1:]
	}
}

// ShouldDelayRequest determines if we should add artificial delay
func (rp *RequestPattern) ShouldDelayRequest() time.Duration {
	rp.mu.RLock()
	defer rp.mu.RUnlock()

	now := time.Now()
	timeSinceLastRequest := now.Sub(rp.lastRequestTime)
	sessionDuration := now.Sub(rp.sessionStart)

	// No delay if this is the first request or enough time has passed
	if rp.requestCount == 0 || timeSinceLastRequest > 30*time.Second {
		return 0
	}

	// Calculate delay based on session characteristics
	var baseDelay time.Duration

	// New session - slower start
	if sessionDuration < 2*time.Minute {
		baseDelay = time.Duration(1500+rand.Intn(2000)) * time.Millisecond
	} else {
		// Established session - vary based on frequency
		requestsPerMinute := float64(rp.requestCount) / sessionDuration.Minutes()

		if requestsPerMinute > 10 { // Very high frequency - slow down
			baseDelay = time.Duration(3000+rand.Intn(4000)) * time.Millisecond
		} else if requestsPerMinute > 5 { // Moderate frequency
			baseDelay = time.Duration(1000+rand.Intn(2000)) * time.Millisecond
		} else { // Low frequency - can be faster
			baseDelay = time.Duration(500+rand.Intn(1000)) * time.Millisecond
		}
	}

	// Add variability based on "user behavior patterns"
	jitter := float64(baseDelay) * (0.7 + rand.Float64()*0.6) // ±30% jitter
	finalDelay := time.Duration(jitter)

	// Ensure minimum delay for realistic behavior
	minDelay := 200 * time.Millisecond
	if finalDelay < minDelay {
		finalDelay = minDelay
	}

	return finalDelay
}

// GetHumanLikeDelay calculates delay with advanced human behavioral simulation
func GetAdvancedHumanLikeDelay(searchQuery string) time.Duration {
	// Track this request
	globalPattern.TrackRequest(searchQuery)

	// Get base delay from pattern analysis
	patternDelay := globalPattern.ShouldDelayRequest()

	// Add query complexity factor
	queryComplexity := getQueryComplexityFactor(searchQuery)
	complexityDelay := time.Duration(float64(patternDelay) * queryComplexity)

	// Add time-of-day factor (simulate human circadian patterns)
	timeOfDayFactor := getTimeOfDayFactor()
	finalDelay := time.Duration(float64(complexityDelay) * timeOfDayFactor)

	return finalDelay
}

// getQueryComplexityFactor returns multiplier based on query complexity
func getQueryComplexityFactor(query string) float64 {
	baseComplexity := 1.0

	// Simple heuristics for query complexity
	if len(query) > 50 { // Long query - more thinking time
		baseComplexity *= 1.3
	}

	// Count words
	wordCount := len(strings.Fields(query))
	if wordCount > 5 { // Complex multi-word query
		baseComplexity *= 1.2
	}

	// Check for special characters (advanced search)
	if strings.ContainsAny(query, "\"()+-*") {
		baseComplexity *= 1.4
	}

	// Add randomness
	jitter := 0.8 + rand.Float64()*0.4 // ±20% jitter
	return baseComplexity * jitter
}

// getTimeOfDayFactor simulates human activity patterns
func getTimeOfDayFactor() float64 {
	hour := time.Now().Hour()

	switch {
	case hour >= 2 && hour <= 6: // Late night/early morning - slower
		return 1.5 + rand.Float64()*0.5
	case hour >= 7 && hour <= 9: // Morning rush - faster
		return 0.7 + rand.Float64()*0.3
	case hour >= 10 && hour <= 12: // Mid morning - normal
		return 0.9 + rand.Float64()*0.2
	case hour >= 13 && hour <= 14: // Lunch time - variable
		return 0.8 + rand.Float64()*0.6
	case hour >= 15 && hour <= 17: // Afternoon - focused
		return 0.8 + rand.Float64()*0.3
	case hour >= 18 && hour <= 22: // Evening - relaxed
		return 1.1 + rand.Float64()*0.4
	default: // Late evening - slower
		return 1.3 + rand.Float64()*0.4
	}
}

// AdvancedHeaderRandomization provides additional header entropy
func AdvancedHeaderRandomization(req *http.Request) {
	// Add realistic browser extension headers occasionally
	if rand.Float64() < 0.15 { // 15% chance
		extensionHeaders := []struct {
			name  string
			value string
		}{
			{"X-Chrome-UMA-Enabled", "1"},
			{"X-Client-Data", generateClientDataHeader()},
			{"X-Requested-With", "XMLHttpRequest"}, // Sometimes present
		}

		header := extensionHeaders[rand.Intn(len(extensionHeaders))]
		req.Header.Set(header.name, header.value)
	}

	// Vary Accept-Encoding order (fingerprint evasion)
	encodings := []string{"gzip", "deflate", "br", "zstd"}
	rand.Shuffle(len(encodings), func(i, j int) {
		encodings[i], encodings[j] = encodings[j], encodings[i]
	})
	req.Header.Set("Accept-Encoding", strings.Join(encodings, ", "))

	// Add realistic timing headers
	if rand.Float64() < 0.3 { // 30% chance
		req.Header.Set("X-Requested-At", time.Now().Format(time.RFC3339))
	}

	// Simulate mobile network conditions occasionally
	if rand.Float64() < 0.05 { // 5% chance - mobile
		req.Header.Set("Save-Data", "on")
		req.Header.Set("Viewport-Width", "390") // Mobile viewport
	}
}

// generateClientDataHeader creates realistic Chrome client data
func generateClientDataHeader() string {
	// Simplified Chrome client data format (base64-like)
	chars := "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	length := 40 + rand.Intn(20)

	var result strings.Builder
	for i := 0; i < length; i++ {
		result.WriteByte(chars[rand.Intn(len(chars))])
	}

	return result.String()
}

// SessionBasedDelay implements session-aware timing
func SessionBasedDelay() {
	delay := GetAdvancedHumanLikeDelay("")
	if delay > 0 {
		time.Sleep(delay)
	}
}
