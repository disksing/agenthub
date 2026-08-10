package companion

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/disksing/agenthub/internal/config"
)

func TestSnapshotNormalizesProviderSchemasAndCaches(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("X-Forwarded-User") != "local-user" {
			t.Fatalf("forwarded user = %q", r.Header.Get("X-Forwarded-User"))
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/providers":
			_, _ = w.Write([]byte(`{"providers":["codex","kimi","grok","opencode","both"],"provider_labels":{"codex":"Codex","kimi":"Kimi","grok":"Grok","opencode":"OpenCode"}}`))
		case "/api/current":
			switch r.URL.Query().Get("provider") {
			case "codex":
				_, _ = w.Write([]byte(`{"capturedAt":"2026-08-10T16:12:24Z","planType":"pro","quotas":[{"displayName":"Weekly","name":"seven_day","remainingPercent":83,"utilization":17,"timeUntilResetSeconds":449168,"status":"healthy"}]}`))
			case "kimi":
				_, _ = w.Write([]byte(`{"capturedAt":"2026-08-10T16:14:23Z","membership":"Allegro","quotas":[{"displayName":"5-hour","name":"5h","utilization":22,"timeUntilResetSeconds":7200,"status":"warning"}]}`))
			case "grok":
				_, _ = w.Write([]byte(`{"capturedAt":"2026-08-10T16:14:23Z","quotas":[{"displayName":"Credits","name":"credits","utilization":78,"status":"danger"}]}`))
			case "opencode":
				_, _ = w.Write([]byte(`{"capturedAt":"2026-08-10T16:12:23Z","planName":"OpenCode Go","quotas":[{"displayName":"5-Hour","name":"five_hour","limit":100,"used":3,"remainingPercent":97,"isStale":true,"status":"healthy"}]}`))
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	settings := config.Defaults().OnWatch
	settings.Enabled = true
	settings.ServerURL = upstream.URL
	settings.Username = "local-user"
	service := NewService(upstream.Client())

	result := service.Snapshot(context.Background(), settings)
	if !result.Connected || len(result.Providers) != 4 {
		t.Fatalf("snapshot = %+v", result)
	}
	if result.Providers[0].Quotas[0].Kind != "7d" || result.Providers[0].Quotas[0].RemainingPercent != 83 {
		t.Fatalf("codex quota = %+v", result.Providers[0])
	}
	if result.Providers[1].Quotas[0].RemainingPercent != 78 || result.Providers[1].Status != "warning" {
		t.Fatalf("kimi quota = %+v", result.Providers[1])
	}
	if result.Providers[2].Quotas[0].Kind != "credits" || result.Providers[2].Quotas[0].RemainingPercent != 22 {
		t.Fatalf("grok quota = %+v", result.Providers[2])
	}
	if !result.Providers[3].Stale || result.Providers[3].PlanLabel != "OpenCode Go" {
		t.Fatalf("opencode quota = %+v", result.Providers[3])
	}
	_ = service.Snapshot(context.Background(), settings)
	if calls.Load() != 5 {
		t.Fatalf("cached request made %d upstream calls, want 5", calls.Load())
	}
}

func TestSnapshotReturnsStaleCacheAfterUpstreamFailure(t *testing.T) {
	failing := atomic.Bool{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if failing.Load() {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		if r.URL.Path == "/api/providers" {
			_, _ = w.Write([]byte(`{"providers":["codex"]}`))
			return
		}
		_, _ = w.Write([]byte(`{"quotas":[{"name":"5h","utilization":10,"status":"healthy"}]}`))
	}))
	defer upstream.Close()
	settings := config.Defaults().OnWatch
	settings.Enabled = true
	settings.ServerURL = upstream.URL
	service := NewService(upstream.Client())
	first := service.Snapshot(context.Background(), settings)
	if !first.Connected {
		t.Fatalf("first snapshot = %+v", first)
	}
	failing.Store(true)
	service.mu.Lock()
	service.expiresAt = time.Time{}
	service.mu.Unlock()
	stale := service.Snapshot(context.Background(), settings)
	if stale.Connected || !stale.Stale || stale.Error == "" || !stale.Providers[0].Stale {
		t.Fatalf("stale snapshot = %+v", stale)
	}
}

func TestBasicAuthAndCatalogResponse(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, password, ok := r.BasicAuth()
		if !ok || user != "alice" || password != "secret" {
			t.Fatalf("basic auth = %q %q %v", user, password, ok)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"providers": []string{"codex", "both"}})
	}))
	defer upstream.Close()
	settings := config.Defaults().OnWatch
	settings.ServerURL = upstream.URL
	settings.AuthMode = "basic"
	settings.Username = "alice"
	settings.Password = "secret"
	catalog, err := NewService(upstream.Client()).TestConnection(context.Background(), settings)
	if err != nil || len(catalog.Providers) != 1 || catalog.Providers[0] != "codex" {
		t.Fatalf("catalog = %+v, %v", catalog, err)
	}
}

func TestWindowPositionMovesLeftAsResetApproaches(t *testing.T) {
	fullWindow := int64(5 * 60 * 60)
	halfWindow := fullWindow / 2
	nearReset := int64(60)
	atReset := int64(0)

	fullPosition := windowPosition("5h", &fullWindow)
	halfPosition := windowPosition("5h", &halfWindow)
	nearPosition := windowPosition("5h", &nearReset)
	resetPosition := windowPosition("5h", &atReset)
	if fullPosition == nil || halfPosition == nil || nearPosition == nil || resetPosition == nil {
		t.Fatal("known reset window did not produce marker positions")
	}
	if *fullPosition != 100 || *halfPosition != 50 || *resetPosition != 0 {
		t.Fatalf("positions = full %.2f, half %.2f, reset %.2f", *fullPosition, *halfPosition, *resetPosition)
	}
	if !(*nearPosition < *halfPosition) {
		t.Fatalf("near-reset position %.2f must be left of half-window position %.2f", *nearPosition, *halfPosition)
	}
}
