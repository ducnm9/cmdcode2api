package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// modelsUpstream serves a minimal /provider/v1/models response and records how
// many times it was hit, so tests can assert both the parsed catalog and the
// request behavior.
func modelsUpstream(t *testing.T, body string) (*httptest.Server, *int) {
	t.Helper()
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/provider/v1/models" {
			http.NotFound(w, r)
			return
		}
		hits++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func TestRefreshModelsGuardPaths(t *testing.T) {
	t.Cleanup(func() { setModelCatalog(nil) })

	// nil pool.
	if _, err := RefreshModels("http://unused", nil); err == nil {
		t.Fatal("nil pool: want error, got nil")
	}

	// Empty pool: no accounts configured.
	empty := NewAccountPool(nil)
	if _, err := RefreshModels("http://unused", empty); err == nil {
		t.Fatal("empty pool: want error, got nil")
	}

	// Pool with only a disabled account.
	disabled := NewAccountPool(nil)
	if _, err := disabled.Add("acc", "cc-disabled", false); err != nil {
		t.Fatal(err)
	}
	if _, err := RefreshModels("http://unused", disabled); err == nil {
		t.Fatal("disabled-only pool: want error, got nil")
	}
}

func TestRefreshModelsHappyPathUsesPrimaryKey(t *testing.T) {
	t.Cleanup(func() { setModelCatalog(nil) })

	var seenAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"a/model-x","context_length":128000},{"id":"b/model-y","context_length":8000}]}`))
	}))
	t.Cleanup(srv.Close)

	pool := NewAccountPool(nil)
	if _, err := pool.Add("primary", "cc-primary-key", true); err != nil {
		t.Fatal(err)
	}

	count, err := RefreshModels(srv.URL, pool)
	if err != nil {
		t.Fatalf("RefreshModels: %v", err)
	}
	if count != 2 {
		t.Fatalf("loaded = %d, want 2", count)
	}
	if seenAuth != "Bearer cc-primary-key" {
		t.Fatalf("auth = %q, want primary account key", seenAuth)
	}
	cat := modelCatalogSnapshot()
	if len(cat) != 2 || cat[0].ID != "a/model-x" {
		t.Fatalf("catalog = %+v", cat)
	}
}

func TestRefreshModelsFailureKeepsCurrentCatalog(t *testing.T) {
	setModelCatalog([]ModelInfo{{ID: "keep/me", Object: "model"}})
	t.Cleanup(func() { setModelCatalog(nil) })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "upstream down", http.StatusBadGateway)
	}))
	t.Cleanup(srv.Close)

	pool := NewAccountPool(nil)
	if _, err := pool.Add("primary", "cc-key", true); err != nil {
		t.Fatal(err)
	}

	if _, err := RefreshModels(srv.URL, pool); err == nil {
		t.Fatal("want error on upstream 502, got nil")
	}
	// A transient failure must not wipe a working catalog.
	cat := modelCatalogSnapshot()
	if len(cat) != 1 || cat[0].ID != "keep/me" {
		t.Fatalf("catalog mutated on failure: %+v", cat)
	}
}

func TestRunModelRefreshStopsOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cfg := &Config{}
	cfg.SetUpstreamBaseURL("http://unused")
	pool := NewAccountPool(nil)

	done := make(chan struct{})
	go func() {
		RunModelRefresh(ctx, cfg, pool)
		close(done)
	}()

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("RunModelRefresh did not return after context cancel")
	}
}

func TestAdminModelsRefreshEndpoint(t *testing.T) {
	srv, pool, _, cfg, _, _ := newAdminTestEnv(t)
	setModelCatalog(nil)
	t.Cleanup(func() { setModelCatalog(nil) })

	// No accounts yet: the primary lookup fails, surfaced as 502.
	resp, _ := adminRequest(t, srv, "POST", "/admin/api/models/refresh", "admin-pass-123", nil)
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("no-account refresh status = %d, want 502", resp.StatusCode)
	}

	// Point the upstream at a catalog server and add an enabled account.
	up, hits := modelsUpstream(t, `{"object":"list","data":[{"id":"m/one","context_length":4096}]}`)
	cfg.SetUpstreamBaseURL(up.URL)
	if _, err := pool.Add("main", "cc-key", true); err != nil {
		t.Fatal(err)
	}

	resp, payload := adminRequest(t, srv, "POST", "/admin/api/models/refresh", "admin-pass-123", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("refresh status = %d, want 200: %v", resp.StatusCode, payload)
	}
	if payload["loaded"].(float64) != 1 {
		t.Fatalf("loaded = %v, want 1", payload["loaded"])
	}
	if *hits != 1 {
		t.Fatalf("upstream hits = %d, want 1", *hits)
	}
}
