package handlers

import (
	"compress/flate"
	"compress/gzip"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	mathrand "math/rand"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"UGPTSearch/internal/instances"
	"UGPTSearch/internal/response"
	"github.com/andybalholm/brotli"
)

// Enhanced HTTP client with realistic browser behavior
var httpClient = createAdvancedHTTPClient()

func createAdvancedHTTPClient() *http.Client {
	// Create custom transport with realistic settings
	transport := &http.Transport{
		// Connection pooling like browsers
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 10,
		IdleConnTimeout:     90 * time.Second,

		// Realistic timeouts
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 20 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,

		// Browser-like connection behavior
		DisableCompression: false, // Enable compression
		DisableKeepAlives:  false, // Enable keep-alive

		// Modern TLS settings
		ForceAttemptHTTP2: true,
	}

	// Create client with browser-like characteristics
	client := &http.Client{
		Transport: transport,
		Timeout:   45 * time.Second, // Slightly longer timeout

		// Don't automatically follow redirects (more control)
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			// Follow up to 3 redirects (like most browsers)
			if len(via) >= 3 {
				return http.ErrUseLastResponse
			}

			// Copy headers from original request (realistic behavior)
			for key, values := range via[0].Header {
				for _, value := range values {
					req.Header.Add(key, value)
				}
			}

			return nil
		},
	}

	return client
}

// Enhanced user agent pools with realistic diversity
type BrowserProfile struct {
	UserAgent       string
	SecChUA         string
	SecChUAMobile   string
	SecChUAPlatform string
	ViewportWidth   int
	ViewportHeight  int
}

var chromeProfiles = []BrowserProfile{
	// Chrome Windows variants
	{"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36", `"Not_A Brand";v="8", "Chromium";v="120", "Google Chrome";v="120"`, "?0", `"Windows"`, 1920, 1080},
	{"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/119.0.0.0 Safari/537.36", `"Google Chrome";v="119", "Chromium";v="119", "Not?A_Brand";v="24"`, "?0", `"Windows"`, 1366, 768},
	{"Mozilla/5.0 (Windows NT 11.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/121.0.0.0 Safari/537.36", `"Not A(Brand";v="99", "Google Chrome";v="121", "Chromium";v="121"`, "?0", `"Windows"`, 1920, 1080},
	{"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/118.0.0.0 Safari/537.36", `"Chromium";v="118", "Google Chrome";v="118", "Not=A?Brand";v="99"`, "?0", `"Windows"`, 1440, 900},

	// Chrome macOS variants
	{"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36", `"Not_A Brand";v="8", "Chromium";v="120", "Google Chrome";v="120"`, "?0", `"macOS"`, 1440, 900},
	{"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_14_6) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/119.0.0.0 Safari/537.36", `"Google Chrome";v="119", "Chromium";v="119", "Not?A_Brand";v="24"`, "?0", `"macOS"`, 1920, 1080},
	{"Mozilla/5.0 (Macintosh; Intel Mac OS X 13_6_0) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/121.0.0.0 Safari/537.36", `"Not A(Brand";v="99", "Google Chrome";v="121", "Chromium";v="121"`, "?0", `"macOS"`, 2560, 1440},

	// Chrome Linux variants
	{"Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36", `"Not_A Brand";v="8", "Chromium";v="120", "Google Chrome";v="120"`, "?0", `"Linux"`, 1920, 1080},
	{"Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/119.0.0.0 Safari/537.36", `"Google Chrome";v="119", "Chromium";v="119", "Not?A_Brand";v="24"`, "?0", `"Linux"`, 1366, 768},
}

var firefoxProfiles = []BrowserProfile{
	// Firefox Windows variants
	{"Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:121.0) Gecko/20100101 Firefox/121.0", "", "", "", 1920, 1080},
	{"Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:120.0) Gecko/20100101 Firefox/120.0", "", "", "", 1366, 768},
	{"Mozilla/5.0 (Windows NT 11.0; Win64; x64; rv:122.0) Gecko/20100101 Firefox/122.0", "", "", "", 1920, 1080},

	// Firefox macOS variants
	{"Mozilla/5.0 (Macintosh; Intel Mac OS X 10.15; rv:121.0) Gecko/20100101 Firefox/121.0", "", "", "", 1440, 900},
	{"Mozilla/5.0 (Macintosh; Intel Mac OS X 13.6; rv:120.0) Gecko/20100101 Firefox/120.0", "", "", "", 1920, 1080},

	// Firefox Linux variants
	{"Mozilla/5.0 (X11; Linux x86_64; rv:121.0) Gecko/20100101 Firefox/121.0", "", "", "", 1920, 1080},
	{"Mozilla/5.0 (X11; Ubuntu; Linux x86_64; rv:120.0) Gecko/20100101 Firefox/120.0", "", "", "", 1366, 768},
}

var safariProfiles = []BrowserProfile{
	// Safari macOS variants
	{"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.1 Safari/605.1.15", "", "", "", 1440, 900},
	{"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Safari/605.1.15", "", "", "", 1920, 1080},
	{"Mozilla/5.0 (Macintosh; Intel Mac OS X 13_6) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.1 Safari/605.1.15", "", "", "", 2560, 1440},
}

// Enhanced language profiles with realistic variations
var acceptLanguages = []string{
	"en-US,en;q=0.9",
	"en-GB,en;q=0.9",
	"en-US,en;q=0.9,es;q=0.8",
	"en-GB,en;q=0.9,fr;q=0.8",
	"en-US,en;q=0.9,de;q=0.8",
	"en-CA,en;q=0.9,fr;q=0.8",
	"en-AU,en;q=0.9",
	"en-US,en;q=0.9,zh;q=0.8",
	"en-GB,en;q=0.9,es;q=0.8,fr;q=0.7",
	"en-US,en;q=0.9,ja;q=0.8",
	"en,en-US;q=0.9",
	"en-US,en;q=0.9,it;q=0.8",
}

// Accept header variations for different content types
var acceptHeaders = []string{
	"text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7",
	"text/html,application/xhtml+xml,application/xml;q=0.9,image/webp,*/*;q=0.8",
	"text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8",
	"text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8",
	"text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8",
}

// Connection header variations
var connectionHeaders = []string{
	"keep-alive",
	"Keep-Alive",
}

// Cache-Control variations
var cacheControlHeaders = []string{
	"max-age=0",
	"no-cache",
	"max-age=0, no-cache",
}

func getRandomBrowserProfile() BrowserProfile {
	// Weighted selection to match real-world browser usage
	rnd := mathrand.Float64()

	switch {
	case rnd < 0.65: // 65% Chrome
		return chromeProfiles[mathrand.Intn(len(chromeProfiles))]
	case rnd < 0.85: // 20% Firefox
		return firefoxProfiles[mathrand.Intn(len(firefoxProfiles))]
	default: // 15% Safari
		return safariProfiles[mathrand.Intn(len(safariProfiles))]
	}
}

func getRandomAcceptLanguage() string {
	return acceptLanguages[mathrand.Intn(len(acceptLanguages))]
}

func getRandomAcceptHeader() string {
	return acceptHeaders[mathrand.Intn(len(acceptHeaders))]
}

func getRandomConnectionHeader() string {
	return connectionHeaders[mathrand.Intn(len(connectionHeaders))]
}

func getRandomCacheControlHeader() string {
	return cacheControlHeaders[mathrand.Intn(len(cacheControlHeaders))]
}

// Generate realistic request timing with human-like patterns
func getHumanLikeDelay() time.Duration {
	// Simulate realistic human browsing patterns
	// Fast clicks: 300-800ms (25%)
	// Normal browsing: 1-4s (50%)
	// Slow browsing/reading: 4-12s (25%)

	rnd := mathrand.Float64()
	switch {
	case rnd < 0.25: // Fast browsing
		return time.Duration(300+mathrand.Intn(500)) * time.Millisecond
	case rnd < 0.75: // Normal browsing
		return time.Duration(1000+mathrand.Intn(3000)) * time.Millisecond
	default: // Slow browsing
		return time.Duration(4000+mathrand.Intn(8000)) * time.Millisecond
	}
}

// Generate random hex string for fingerprinting evasion
func generateRandomHex(length int) string {
	bytes := make([]byte, length/2)
	rand.Read(bytes)
	return hex.EncodeToString(bytes)
}

// Generate realistic TLS JA3 fingerprint variations
func getRandomJA3Fingerprint() string {
	// Common JA3 fingerprints for major browsers
	ja3Fingerprints := []string{
		"769,47-53-5-10-49161-49162-49171-49172-50-56-19-4,0-10-11",
		"771,4865-4866-4867-49195-49199-49196-49200-52393-52392-49171-49172-156-157-47-53,0-23-65281-10-11-35-16-5-13-18-51-45-43-27-17513",
		"772,4865-4867-4866-49195-49199-52393-52392-49196-49200-49162-49161-49171-49172-51-57-47-53,0-23-65281-10-11-35-16-5-51-43-13-45-28-21",
	}
	return ja3Fingerprints[mathrand.Intn(len(ja3Fingerprints))]
}

// getBrowserSpecificClient returns a client optimized for the browser profile
func getBrowserSpecificClient(profile BrowserProfile) *http.Client {
	transport := &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 10,
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
		DisableCompression:  false,
		DisableKeepAlives:   false,
	}

	// Browser-specific transport optimizations
	if strings.Contains(profile.UserAgent, "Chrome") {
		// Chrome typically has aggressive connection pooling
		transport.MaxIdleConnsPerHost = 15
		transport.ForceAttemptHTTP2 = true
	} else if strings.Contains(profile.UserAgent, "Firefox") {
		// Firefox has different connection characteristics
		transport.MaxIdleConnsPerHost = 8
		transport.ResponseHeaderTimeout = 25 * time.Second
	} else if strings.Contains(profile.UserAgent, "Safari") {
		// Safari has more conservative settings
		transport.MaxIdleConnsPerHost = 6
		transport.IdleConnTimeout = 60 * time.Second
	}

	return &http.Client{
		Transport: transport,
		Timeout:   45 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return http.ErrUseLastResponse
			}
			// Copy essential headers on redirect
			for key, values := range via[0].Header {
				if key == "User-Agent" || key == "Accept" || key == "Accept-Language" {
					for _, value := range values {
						req.Header.Add(key, value)
					}
				}
			}
			return nil
		},
	}
}

func decompressResponse(resp *http.Response) ([]byte, error) {
	var reader io.Reader = resp.Body

	contentEncoding := resp.Header.Get("Content-Encoding")
	switch strings.ToLower(contentEncoding) {
	case "gzip":
		gzipReader, err := gzip.NewReader(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("failed to create gzip reader: %w", err)
		}
		defer gzipReader.Close()
		reader = gzipReader
	case "deflate":
		reader = flate.NewReader(resp.Body)
	case "br":
		reader = brotli.NewReader(resp.Body)
	case "":
		// No compression
	default:
		return nil, fmt.Errorf("unsupported content encoding: %s", contentEncoding)
	}

	return io.ReadAll(reader)
}

func setHumanLikeHeaders(req *http.Request, searchURL string, instance string) BrowserProfile {
	profile := getRandomBrowserProfile()

	// Core headers
	req.Header.Set("User-Agent", profile.UserAgent)
	req.Header.Set("Accept", getRandomAcceptHeader())
	req.Header.Set("Accept-Language", getRandomAcceptLanguage())
	req.Header.Set("Accept-Encoding", "gzip, deflate, br, zstd")
	req.Header.Set("Connection", getRandomConnectionHeader())
	req.Header.Set("Cache-Control", getRandomCacheControlHeader())
	req.Header.Set("Upgrade-Insecure-Requests", "1")

	// Browser-specific headers
	if strings.Contains(profile.UserAgent, "Chrome") {
		// Chrome-specific headers
		req.Header.Set("sec-ch-ua", profile.SecChUA)
		req.Header.Set("sec-ch-ua-mobile", profile.SecChUAMobile)
		req.Header.Set("sec-ch-ua-platform", profile.SecChUAPlatform)
		req.Header.Set("Sec-Fetch-Dest", "document")
		req.Header.Set("Sec-Fetch-Mode", "navigate")
		req.Header.Set("Sec-Fetch-Site", "none")
		req.Header.Set("Sec-Fetch-User", "?1")

		// Viewport hints for Chrome
		req.Header.Set("sec-ch-viewport-width", strconv.Itoa(profile.ViewportWidth))
		req.Header.Set("sec-ch-viewport-height", strconv.Itoa(profile.ViewportHeight))

		// Chrome DPR variations
		dpr := []string{"1", "1.25", "1.5", "2"}[mathrand.Intn(4)]
		req.Header.Set("sec-ch-dpr", dpr)

	} else if strings.Contains(profile.UserAgent, "Firefox") {
		// Firefox-specific headers
		req.Header.Set("Sec-Fetch-Dest", "document")
		req.Header.Set("Sec-Fetch-Mode", "navigate")
		req.Header.Set("Sec-Fetch-Site", "none")
		req.Header.Set("Sec-Fetch-User", "?1")

		// Firefox tracking protection
		if mathrand.Float64() < 0.7 { // 70% of Firefox users have DNT enabled
			req.Header.Set("DNT", "1")
		}

	} else if strings.Contains(profile.UserAgent, "Safari") {
		// Safari-specific headers (minimal sec-fetch)
		if mathrand.Float64() < 0.3 { // Safari sometimes doesn't send sec-fetch headers
			req.Header.Set("Sec-Fetch-Dest", "document")
			req.Header.Set("Sec-Fetch-Mode", "navigate")
			req.Header.Set("Sec-Fetch-Site", "none")
		}
	}

	// Randomly omit DNT header (realistic behavior)
	if mathrand.Float64() < 0.4 { // 40% chance to include DNT
		req.Header.Set("DNT", "1")
	}

	// Realistic referer patterns
	parsedURL, err := url.Parse(searchURL)
	if err == nil {
		// Vary referer patterns
		refererPatterns := []string{
			fmt.Sprintf("%s://%s/", parsedURL.Scheme, parsedURL.Host),
			fmt.Sprintf("%s://%s", parsedURL.Scheme, parsedURL.Host),
			"https://www.google.com/",
			"https://duckduckgo.com/",
			"https://www.bing.com/",
		}

		// 80% chance to include referer
		if mathrand.Float64() < 0.8 {
			referer := refererPatterns[mathrand.Intn(len(refererPatterns))]
			req.Header.Set("Referer", referer)
		}
	}

	// Add realistic X-Forwarded-For occasionally (as if behind proxy)
	if mathrand.Float64() < 0.15 { // 15% chance
		xff := fmt.Sprintf("%d.%d.%d.%d",
			mathrand.Intn(255)+1, mathrand.Intn(255), mathrand.Intn(255), mathrand.Intn(255))
		req.Header.Set("X-Forwarded-For", xff)
	}

	// Random order of headers (anti-fingerprinting)
	headerKeys := make([]string, 0, len(req.Header))
	for k := range req.Header {
		headerKeys = append(headerKeys, k)
	}
	mathrand.Shuffle(len(headerKeys), func(i, j int) {
		headerKeys[i], headerKeys[j] = headerKeys[j], headerKeys[i]
	})

	// Cookie handling
	cookies := instances.Manager.GetCookies(instance)
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}

	// Add session tracking cookies occasionally
	if mathrand.Float64() < 0.3 { // 30% chance
		sessionID := generateRandomHex(32)
		sessionCookie := &http.Cookie{
			Name:  "session_id",
			Value: sessionID,
			Path:  "/",
		}
		req.AddCookie(sessionCookie)
	}

	return profile
}

func Search(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("q")
	if query == "" {
		http.Error(w, "Missing search query", http.StatusBadRequest)
		return
	}

	format := r.URL.Query().Get("format")
	if format == "" {
		format = "html"
	}

	// Create response processor with configuration from URL parameters
	config := response.ConfigFromParams(r.URL.Query())
	if preset := r.URL.Query().Get("preset"); preset != "" {
		config = response.GetPresetConfig(preset)
	}
	processor := response.NewProcessor(config)

	maxRetries := 3
	var lastErr error

	for attempt := 0; attempt < maxRetries; attempt++ {

		instance, err := instances.Manager.GetNextInstance()
		if err != nil {
			lastErr = err
			if attempt == maxRetries-1 {
				http.Error(w, fmt.Sprintf("No instances available: %s", err), http.StatusServiceUnavailable)
				return
			}
			continue
		}

		// Always request HTML from SearXNG to avoid triggering bot detection
		// We'll process HTML into the desired format (json/text) ourselves
		searchURL := fmt.Sprintf("%s?q=%s&format=html", instance, url.QueryEscape(query))

		req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, searchURL, nil)
		if err != nil {
			instances.Manager.MarkError(instance)
			lastErr = err
			continue
		}

		profile := setHumanLikeHeaders(req, searchURL, instance)

		// Apply advanced evasion techniques
		AdvancedHeaderRandomization(req)

		// Add human-like delay before request
		delay := GetAdvancedHumanLikeDelay(query)
		if delay > 0 {
			time.Sleep(delay)
		}

		// Use browser-specific client
		browserClient := getBrowserSpecificClient(profile)
		resp, err := browserClient.Do(req)
		if err != nil {
			instances.Manager.MarkError(instance)
			lastErr = err
			continue
		}

		if resp.StatusCode == http.StatusTooManyRequests {
			resp.Body.Close()
			instances.Manager.MarkRateLimit(instance)
			lastErr = fmt.Errorf("instance returned status %d", resp.StatusCode)
			continue
		}

		if resp.StatusCode >= 500 {
			resp.Body.Close()
			instances.Manager.MarkError(instance)
			lastErr = fmt.Errorf("instance returned status %d", resp.StatusCode)
			continue
		}

		body, err := decompressResponse(resp)
		resp.Body.Close()
		if err != nil {
			instances.Manager.MarkError(instance)
			lastErr = fmt.Errorf("failed to decompress response: %w", err)
			continue
		}

		if strings.Contains(string(body), "Site Maintenance") || strings.Contains(string(body), "maintenance") {
			instances.Manager.MarkError(instance)
			lastErr = fmt.Errorf("instance is under maintenance")
			continue
		}

		instances.Manager.MarkSuccess(instance)

		if len(resp.Cookies()) > 0 {
			instances.Manager.SetCookies(instance, resp.Cookies())
		}

		// Process response based on requested format
		switch format {
		case "text":
			// Process HTML and return plaintext
			processedResp, err := processor.ProcessHTMLResponse(body, query, instance)
			if err != nil {
				http.Error(w, fmt.Sprintf("Failed to process response: %s", err), http.StatusInternalServerError)
				return
			}

			plaintext := processor.FormatAsPlaintext(processedResp)
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.Write([]byte(plaintext))
			return

		case "json":
			// Process HTML and return enhanced JSON
			processedResp, err := processor.ProcessHTMLResponse(body, query, instance)
			if err != nil {
				http.Error(w, fmt.Sprintf("Failed to process response: %s", err), http.StatusInternalServerError)
				return
			}

			jsonData, err := processor.FormatAsJSON(processedResp)
			if err != nil {
				http.Error(w, fmt.Sprintf("Failed to format JSON: %s", err), http.StatusInternalServerError)
				return
			}

			w.Header().Set("Content-Type", "application/json")
			w.Write(jsonData)
			return

		default: // "html" or any other format
			// Return original HTML response
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write(body)
			return
		}
	}

	http.Error(w, fmt.Sprintf("All instances failed after %d attempts. Last error: %s", maxRetries, lastErr), http.StatusServiceUnavailable)
}

func SearchJSON(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("q")
	if query == "" {
		http.Error(w, "Missing search query", http.StatusBadRequest)
		return
	}

	// Create response processor with configuration from URL parameters
	config := response.ConfigFromParams(r.URL.Query())
	if preset := r.URL.Query().Get("preset"); preset != "" {
		config = response.GetPresetConfig(preset)
	}
	processor := response.NewProcessor(config)

	maxRetries := 3
	var lastErr error

	for attempt := 0; attempt < maxRetries; attempt++ {
		instance, err := instances.Manager.GetNextInstance()
		if err != nil {
			lastErr = err
			if attempt == maxRetries-1 {
				http.Error(w, fmt.Sprintf("No instances available: %s", err), http.StatusServiceUnavailable)
				return
			}
			continue
		}

		searchURL := fmt.Sprintf("%s?q=%s&format=html", instance, url.QueryEscape(query))

		req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, searchURL, nil)
		if err != nil {
			instances.Manager.MarkError(instance)
			lastErr = err
			continue
		}

		profile := setHumanLikeHeaders(req, searchURL, instance)

		// Apply advanced evasion techniques
		AdvancedHeaderRandomization(req)

		// Add human-like delay before request
		delay := GetAdvancedHumanLikeDelay(query)
		if delay > 0 {
			time.Sleep(delay)
		}

		// Use browser-specific client
		browserClient := getBrowserSpecificClient(profile)
		resp, err := browserClient.Do(req)
		if err != nil {
			instances.Manager.MarkError(instance)
			lastErr = err
			continue
		}

		if resp.StatusCode == http.StatusTooManyRequests {
			resp.Body.Close()
			instances.Manager.MarkRateLimit(instance)
			lastErr = fmt.Errorf("instance returned status %d", resp.StatusCode)
			continue
		}

		if resp.StatusCode >= 500 {
			resp.Body.Close()
			instances.Manager.MarkError(instance)
			lastErr = fmt.Errorf("instance returned status %d", resp.StatusCode)
			continue
		}

		body, err := decompressResponse(resp)
		resp.Body.Close()
		if err != nil {
			instances.Manager.MarkError(instance)
			lastErr = fmt.Errorf("failed to decompress response: %w", err)
			continue
		}

		if strings.Contains(string(body), "Site Maintenance") || strings.Contains(string(body), "maintenance") {
			instances.Manager.MarkError(instance)
			lastErr = fmt.Errorf("instance is under maintenance")
			continue
		}

		instances.Manager.MarkSuccess(instance)

		if len(resp.Cookies()) > 0 {
			instances.Manager.SetCookies(instance, resp.Cookies())
		}

		// Process HTML response for enhanced JSON output
		processedResp, err := processor.ProcessHTMLResponse(body, query, instance)
		if err != nil {
			// Fallback to empty results if processing fails
			fallbackResp := map[string]interface{}{
				"query":          query,
				"results":        []interface{}{},
				"result_count":   0,
				"instance":       instance,
				"processing_error": err.Error(),
				"processed_at":   time.Now(),
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(fallbackResp)
			return
		}

		jsonData, err := processor.FormatAsJSON(processedResp)
		if err != nil {
			// Fallback to simple JSON structure if formatting fails
			fallbackResp := map[string]interface{}{
				"query":        query,
				"results":      []interface{}{},
				"result_count": 0,
				"instance":     instance,
				"format_error": err.Error(),
				"processed_at": time.Now(),
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(fallbackResp)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.Write(jsonData)
		return
	}

	http.Error(w, fmt.Sprintf("All instances failed after %d attempts. Last error: %s", maxRetries, lastErr), http.StatusServiceUnavailable)
}

func Instances(w http.ResponseWriter, r *http.Request) {
	instanceList := instances.Manager.GetInstances()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"instances": instanceList,
		"count":     len(instanceList),
	})
}
