package handlers

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"

	"UGPTSearch/internal/instances"
)

var httpClient = &http.Client{
	Timeout: 10 * time.Second,
}

func Search(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("q")
	if query == "" {
		http.Error(w, "Missing search query", http.StatusBadRequest)
		return
	}

	format := r.URL.Query().Get("format")
	if format == "" {
		format = "json"
	}

	useBrowserUA := r.URL.Query().Get("browser_ua") == "true"

	maxRetries := 5
	var lastErr error
	
	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			backoffDelay := time.Duration(math.Pow(2, float64(attempt-1))) * time.Second
			if backoffDelay > 8*time.Second {
				backoffDelay = 8 * time.Second
			}
			time.Sleep(backoffDelay)
		}
		
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
		
		userAgent := "UGPTSearch/1.0"
		if useBrowserUA {
			userAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"
		}
		req.Header.Set("User-Agent", userAgent)

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

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			instances.Manager.MarkError(instance)
			lastErr = err
			continue
		}

		if strings.Contains(string(body), "Site Maintenance") || strings.Contains(string(body), "maintenance") {
			instances.Manager.MarkError(instance)
			lastErr = fmt.Errorf("instance is under maintenance")
			continue
		}

		instances.Manager.MarkSuccess(instance)

		if format == "json" {
			w.Header().Set("Content-Type", "application/json")
		} else {
			w.Header().Set("Content-Type", "text/plain")
		}

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