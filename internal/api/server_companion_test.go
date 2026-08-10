package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/disksing/agenthub/internal/config"
	"github.com/disksing/agenthub/internal/runtime"
	"github.com/disksing/agenthub/internal/session"
)

func companionTestServer(t *testing.T, cfg config.Config, upstream *httptest.Server) (*httptest.Server, string, *session.Store) {
	t.Helper()
	root := t.TempDir()
	store, err := session.Open(filepath.Join(root, "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(root, "config.json")
	if err := config.Save(configPath, cfg); err != nil {
		t.Fatal(err)
	}
	manager := runtime.New(store, cfg)
	dependencies := Dependencies{Runtime: manager, ConfigPath: configPath}
	if upstream != nil {
		dependencies.QuotaHTTPClient = upstream.Client()
	}
	server := httptest.NewServer(New(store, "test", time.Now(), dependencies).Handler())
	t.Cleanup(server.Close)
	return server, configPath, store
}

func TestConfigAPIKeepsOnWatchPasswordServerSide(t *testing.T) {
	cfg := config.Defaults()
	cfg.OnWatch.Enabled = true
	cfg.OnWatch.AuthMode = "basic"
	cfg.OnWatch.Username = "alice"
	cfg.OnWatch.Password = "secret"
	server, configPath, _ := companionTestServer(t, cfg, nil)

	response, err := http.Get(server.URL + "/v1/config")
	if err != nil {
		t.Fatal(err)
	}
	var fetched struct {
		Config config.Config `json:"config"`
	}
	if err := json.NewDecoder(response.Body).Decode(&fetched); err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if fetched.Config.OnWatch.Password != "" {
		t.Fatal("GET /v1/config exposed the OnWatch password")
	}
	fetched.Config.Companion.BeepVolume = 0.51
	body, _ := json.Marshal(map[string]any{"config": fetched.Config})
	request, _ := http.NewRequest(http.MethodPut, server.URL+"/v1/config", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("PUT config status = %s", response.Status)
	}
	var savedResponse struct {
		Config config.Config `json:"config"`
	}
	if err := json.NewDecoder(response.Body).Decode(&savedResponse); err != nil {
		t.Fatal(err)
	}
	if savedResponse.Config.OnWatch.Password != "" {
		t.Fatal("PUT /v1/config response exposed the OnWatch password")
	}
	stored, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if stored.OnWatch.Password != "secret" || stored.Companion.BeepVolume != 0.51 {
		t.Fatalf("stored config = %+v", stored)
	}
}

func TestQuotaAndOnWatchTestRoutes(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Forwarded-User") != "admin" {
			t.Fatalf("trusted proxy header = %q", r.Header.Get("X-Forwarded-User"))
		}
		if r.URL.Path == "/api/providers" {
			_, _ = w.Write([]byte(`{"providers":["kimi","both"],"provider_labels":{"kimi":"Kimi Code"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"membership":"Allegro","quotas":[{"name":"5h","displayName":"5-hour","utilization":12,"status":"healthy"}]}`))
	}))
	defer upstream.Close()
	cfg := config.Defaults()
	cfg.OnWatch.Enabled = true
	cfg.OnWatch.ServerURL = upstream.URL
	server, _, _ := companionTestServer(t, cfg, upstream)

	response, err := http.Get(server.URL + "/v1/quota")
	if err != nil {
		t.Fatal(err)
	}
	var quotaBody struct {
		Quota struct {
			Connected bool `json:"connected"`
			Providers []struct {
				Provider string `json:"provider"`
				Quotas   []struct {
					Remaining float64 `json:"remainingPercent"`
				} `json:"quotas"`
			} `json:"providers"`
		} `json:"quota"`
	}
	if err := json.NewDecoder(response.Body).Decode(&quotaBody); err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if !quotaBody.Quota.Connected || quotaBody.Quota.Providers[0].Provider != "kimi" || quotaBody.Quota.Providers[0].Quotas[0].Remaining != 88 {
		t.Fatalf("quota response = %+v", quotaBody)
	}

	testSettings := cfg.OnWatch
	testSettings.Password = ""
	testBody, _ := json.Marshal(map[string]any{"onWatch": testSettings})
	request, _ := http.NewRequest(http.MethodPost, server.URL+"/v1/onwatch/test", bytes.NewReader(testBody))
	request.Header.Set("Content-Type", "application/json")
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("test connection status = %s", response.Status)
	}
}

func TestActivitySSEAggregatesAllSessionsPerSecond(t *testing.T) {
	store, err := session.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	first, _ := store.Create(session.CreateInput{Title: "First", Cwd: t.TempDir(), AgentName: "Codex"})
	second, _ := store.Create(session.CreateInput{Title: "Second", Cwd: t.TempDir(), AgentName: "Kimi"})
	providerData, _ := json.Marshal(session.ProviderEventData{Provider: "codex"})
	_, _ = store.Append(first.ID, "session.provider", "", providerData)
	server := httptest.NewServer(New(store, "test", time.Now()).Handler())
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/v1/activity/events", nil)
	request.Header.Set("Accept", "text/event-stream")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("content type = %q", response.Header.Get("Content-Type"))
	}
	_, _ = store.Append(first.ID, "message.assistant.delta", "turn-1", []byte(`{"text":"a"}`))
	_, _ = store.Append(first.ID, session.EventTurnCompleted, "turn-1", []byte(`{}`))
	_, _ = store.Append(second.ID, "tool.event", "turn-2", []byte(`{}`))

	scanner := bufio.NewScanner(response.Body)
	var frame activityFrame
	for scanner.Scan() {
		line := scanner.Text()
		if len(line) > 6 && line[:6] == "data: " {
			if err := json.Unmarshal([]byte(line[6:]), &frame); err != nil {
				t.Fatal(err)
			}
			break
		}
	}
	if frame.Sequence == 0 || len(frame.Sessions) != 2 {
		t.Fatalf("activity frame = %+v, scanner error = %v", frame, scanner.Err())
	}
	byID := map[string]activitySession{}
	for _, value := range frame.Sessions {
		byID[value.SessionID] = value
	}
	if byID[first.ID].EventCount != 2 || !byID[first.ID].Completed || byID[first.ID].Provider != "codex" || byID[first.ID].Title != "First" {
		t.Fatalf("first activity = %+v", byID[first.ID])
	}
	if byID[second.ID].EventCount != 1 || byID[second.ID].Completed {
		t.Fatalf("second activity = %+v", byID[second.ID])
	}
}
