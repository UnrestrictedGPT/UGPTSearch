package handlers

import (
	"compress/flate"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/andybalholm/brotli"
	"UGPTSearch/internal/instances"
)

var httpClient = &http.Client{
	Timeout: 30 * time.Second,
}

var userAgents = []string{
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/119.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:121.0) Gecko/20100101 Firefox/121.0",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.1 Safari/605.1.15",
	"Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
}

var acceptLanguages = []string{
	"en-US,en;q=0.9",
	"en-GB,en;q=0.9",
	"en-US,en;q=0.9,es;q=0.8",
	"en-GB,en;q=0.9,fr;q=0.8",
	"en-US,en;q=0.9,de;q=0.8",
}

func getRandomUserAgent() string {
	return userAgents[rand.Intn(len(userAgents))]
}

func getRandomAcceptLanguage() string {
	return acceptLanguages[rand.Intn(len(acceptLanguages))]
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

func setHumanLikeHeaders(req *http.Request, searchURL string, instance string) {
	req.Header.Set("User-Agent", getRandomUserAgent())
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8")
	req.Header.Set("Accept-Language", getRandomAcceptLanguage())
	req.Header.Set("Accept-Encoding", "gzip, deflate, br")
	req.Header.Set("DNT", "1")
	req.Header.Set("Connection", "keep-alive")
	req.Header.Set("Upgrade-Insecure-Requests", "1")
	req.Header.Set("Sec-Fetch-Dest", "document")
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	req.Header.Set("Sec-Fetch-Site", "none")
	req.Header.Set("Sec-Fetch-User", "?1")
	req.Header.Set("Cache-Control", "max-age=0")
	
	parsedURL, err := url.Parse(searchURL)
	if err == nil {
		req.Header.Set("Referer", fmt.Sprintf("%s://%s/", parsedURL.Scheme, parsedURL.Host))
	}
	
	cookies := instances.Manager.GetCookies(instance)
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
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

		searchURL := fmt.Sprintf("%s?q=%s&format=%s", instance, url.QueryEscape(query), format)

		req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, searchURL, nil)
		if err != nil {
			instances.Manager.MarkError(instance)
			lastErr = err
			continue
		}
		
		setHumanLikeHeaders(req, searchURL, instance)

		resp, err := httpClient.Do(req)
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

		if format == "json" {
			w.Header().Set("Content-Type", "application/json")
		} else {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
		}

		w.Write(body)
		return
	}

	http.Error(w, fmt.Sprintf("All instances failed after %d attempts. Last error: %s", maxRetries, lastErr), http.StatusServiceUnavailable)
}

func SearchJSON(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("q")
	if query == "" {
		http.Error(w, "Missing search query", http.StatusBadRequest)
		return
	}

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

		searchURL := fmt.Sprintf("%s?q=%s&format=json", instance, url.QueryEscape(query))

		req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, searchURL, nil)
		if err != nil {
			instances.Manager.MarkError(instance)
			lastErr = err
			continue
		}
		
		setHumanLikeHeaders(req, searchURL, instance)

		resp, err := httpClient.Do(req)
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

		w.Header().Set("Content-Type", "application/json")
		w.Write(body)
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