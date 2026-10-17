package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

// modelRefreshInterval is how often the catalog is re-fetched from the CC API
// so newly released upstream models appear without a server restart.
const modelRefreshInterval = 30 * time.Minute

// modelCatalog holds the current upstream model list. It is read by request
// handlers (/v1/models, admin, resolveModelName) and written by the periodic
// refresh and account-add paths, so every access goes through catalogMu to
// stay race-free (CI runs -race).
var (
	catalogMu    sync.RWMutex
	modelCatalog []ModelInfo
)

// setModelCatalog replaces the catalog atomically.
func setModelCatalog(catalog []ModelInfo) {
	catalogMu.Lock()
	modelCatalog = catalog
	catalogMu.Unlock()
}

// modelCatalogSnapshot returns a copy-safe view of the catalog for readers.
// The returned slice must not be mutated.
func modelCatalogSnapshot() []ModelInfo {
	catalogMu.RLock()
	defer catalogMu.RUnlock()
	return modelCatalog
}

// modelCatalogLen reports the number of loaded models.
func modelCatalogLen() int {
	catalogMu.RLock()
	defer catalogMu.RUnlock()
	return len(modelCatalog)
}

// FetchProviderModels pulls the model list from the CC API and populates the
// catalog. Failures are logged and leave the previous catalog untouched.
func FetchProviderModels(baseURL, apiKey string) {
	if _, err := fetchProviderModels(baseURL, apiKey); err != nil {
		log.Printf("[WARN] fetch models: %v (keeping current catalog)", err)
	}
}

// fetchProviderModels fetches and replaces the catalog, returning the loaded
// count. On any error the catalog is left unchanged so a transient upstream
// failure never wipes a working list.
func fetchProviderModels(baseURL, apiKey string) (int, error) {
	url := baseURL + "/provider/v1/models"

	client := &http.Client{Timeout: 15 * time.Second}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return 0, fmt.Errorf("build request failed: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return 0, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}

	var list CCProviderModelList
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return 0, fmt.Errorf("decode response failed: %w", err)
	}

	catalog := make([]ModelInfo, 0, len(list.Data))
	for _, m := range list.Data {
		catalog = append(catalog, ModelInfo{
			ID:            m.ID,
			Object:        "model",
			Created:       1700000000,
			OwnedBy:       "commandcode",
			ContextWindow: m.ContextLength,
		})
	}
	setModelCatalog(catalog)
	log.Printf("models: %d loaded from %s", len(catalog), url)
	return len(catalog), nil
}

// RefreshModels re-fetches the catalog using the pool's primary account,
// regardless of whether the catalog is already populated. Returns the loaded
// count on success. It is used by the periodic refresh loop and the admin
// refresh endpoint.
func RefreshModels(baseURL string, pool *AccountPool) (int, error) {
	if pool == nil {
		return 0, fmt.Errorf("no account pool")
	}
	primary := pool.Primary()
	if primary == nil {
		return 0, fmt.Errorf("no accounts configured")
	}
	if !primary.Enabled {
		return 0, fmt.Errorf("no enabled accounts")
	}
	return fetchProviderModels(baseURL, primary.APIKey)
}

// RunModelRefresh re-fetches the catalog on a fixed interval until ctx is
// cancelled. The initial fetch happens at startup elsewhere, so this only runs
// subsequent refreshes. The base URL is read from cfg on every tick (not
// captured once) so an admin editing base_url live is honored by the loop,
// matching the on-demand refresh path.
func RunModelRefresh(ctx context.Context, cfg *Config, pool *AccountPool) {
	ticker := time.NewTicker(modelRefreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := RefreshModels(cfg.UpstreamBaseURL(), pool); err != nil {
				log.Printf("[WARN] periodic model refresh: %v (keeping current catalog)", err)
			}
		}
	}
}

func availableModels() []string {
	catalogMu.RLock()
	defer catalogMu.RUnlock()
	out := make([]string, 0, len(modelCatalog))
	for _, model := range modelCatalog {
		out = append(out, model.ID)
	}
	return out
}

func isModelExcluded(model string, excludes []string) bool {
	if len(excludes) == 0 {
		return false
	}
	candidates := []string{model}
	if idx := strings.LastIndex(model, "/"); idx >= 0 {
		candidates = append(candidates, model[idx+1:])
	}
	for _, c := range candidates {
		if c == "" {
			continue
		}
		for _, e := range excludes {
			e = strings.TrimSpace(e)
			if e == "" {
				continue
			}
			if strings.HasPrefix(c, e) {
				return true
			}
		}
	}
	return false
}
