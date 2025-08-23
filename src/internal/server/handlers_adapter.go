package server

import (
	"compress/flate"
	"compress/gzip"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/andybalholm/brotli"
)

// HandlerAdapter provides compatibility functions for the existing handlers
// This bridges the gap between the new concurrent architecture and existing evasion techniques

// BrowserProfile for connection optimization (simplified from concurrency package)
type BrowserProfile struct {
	UserAgent    string
	BrowserType  string
	Platform     string
	HTTP2Enabled bool
}

// SetHumanLikeHeaders applies evasion techniques from the handlers package
func SetHumanLikeHeaders(req *http.Request, searchURL string, instance string) BrowserProfile {
	// This would call the existing function from handlers package
	// For now, we'll provide a simplified implementation
	profile := BrowserProfile{
		UserAgent:    req.Header.Get("User-Agent"),
		BrowserType:  "chrome",
		Platform:     "windows",
		HTTP2Enabled: true,
	}

	// Apply basic headers
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	}

	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/webp,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Accept-Encoding", "gzip, deflate, br")
	req.Header.Set("Connection", "keep-alive")
	req.Header.Set("Upgrade-Insecure-Requests", "1")

	return profile
}

// AdvancedHeaderRandomization applies additional header randomization
func AdvancedHeaderRandomization(req *http.Request) {
	// This would call the existing function from handlers package
	// For now, we'll provide a basic implementation

	// Vary some headers randomly
	if req.Header.Get("DNT") == "" {
		req.Header.Set("DNT", "1")
	}
}

// GetAdvancedHumanLikeDelay returns a delay to simulate human behavior
func GetAdvancedHumanLikeDelay(query string) time.Duration {
	// This would call the existing function from handlers package
	// For now, return a basic delay
	return time.Duration(500+len(query)*10) * time.Millisecond
}

// DecompressResponse decompresses HTTP response body based on Content-Encoding
func DecompressResponse(resp *http.Response) ([]byte, error) {
	var reader io.Reader = resp.Body

	contentEncoding := resp.Header.Get("Content-Encoding")
	switch strings.ToLower(contentEncoding) {
	case "gzip":
		gzipReader, err := gzip.NewReader(resp.Body)
		if err != nil {
			return nil, err
		}
		defer gzipReader.Close()
		reader = gzipReader
	case "deflate":
		reader = flate.NewReader(resp.Body)
	case "br":
		reader = brotli.NewReader(resp.Body)
	case "":
		// No compression
	}

	return io.ReadAll(reader)
}

// IsMaintenanceMode checks if the response indicates maintenance mode
func IsMaintenanceMode(body []byte) bool {
	bodyStr := strings.ToLower(string(body))
	return strings.Contains(bodyStr, "maintenance") ||
		strings.Contains(bodyStr, "site maintenance") ||
		strings.Contains(bodyStr, "temporarily unavailable")
}
