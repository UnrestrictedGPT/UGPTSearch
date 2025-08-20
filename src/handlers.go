package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

var httpClient = &http.Client{
	Timeout: 10 * time.Second,
}

func searchHandler(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("q")
	if query == "" {
		http.Error(w, "Missing search query", http.StatusBadRequest)
		return
	}

	format := r.URL.Query().Get("format")
	if format == "" {
		format = "json"
	}

	instance, err := instanceManager.GetRandomInstance()
	if err != nil {
		http.Error(w, fmt.Sprintf("No instances available: %s", err), http.StatusServiceUnavailable)
		return
	}

	searchURL := fmt.Sprintf("%s?q=%s&format=%s", instance, url.QueryEscape(query), format)

	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, searchURL, nil)
	if err != nil {
		http.Error(w, "Invalid request", http.StatusInternalServerError)
		return
	}
	req.Header.Set("User-Agent", "UGPTSearch/1.0")

	resp, err := httpClient.Do(req)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to fetch from instance: %s", err), http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		http.Error(w, "Failed to read response body", http.StatusInternalServerError)
		return
	}

	if format == "json" {
		w.Header().Set("Content-Type", "application/json")
	} else {
		w.Header().Set("Content-Type", "text/plain")
	}

	w.Write(body)
}

func instancesHandler(w http.ResponseWriter, r *http.Request) {
	instances := instanceManager.GetInstances()
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"instances": instances,
		"count":     len(instances),
	})
}