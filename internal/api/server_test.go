package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/disksing/agenthub/internal/config"
	"github.com/disksing/agenthub/internal/runtime"
	"github.com/disksing/agenthub/internal/session"
)

func TestValidateLoopbackAddress(t *testing.T) {
	for _, address := range []string{"127.0.0.1:4646", "[::1]:4646", "localhost:4646"} {
		if err := ValidateLoopbackAddress(address); err != nil {
			t.Fatalf("%s should be allowed: %v", address, err)
		}
	}
	for _, address := range []string{"0.0.0.0:4646", "192.168.1.2:4646"} {
		if err := ValidateLoopbackAddress(address); err == nil {
			t.Fatalf("%s should be rejected", address)
		}
	}
}

func TestSessionAPIUsesEventLog(t *testing.T) {
	store, err := session.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(New(store, "test", time.Now()).Handler())
	defer server.Close()

	body, _ := json.Marshal(map[string]any{
		"title": "API session",
		"cwd":   t.TempDir(),
		"selector": map[string]any{
			"tags": []string{"code", "build"},
		},
	})
	request, _ := http.NewRequest(http.MethodPost, server.URL+"/v1/sessions", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("unexpected status: %s", response.Status)
	}
	var created struct {
		Session session.Session `json:"session"`
	}
	if err := json.NewDecoder(response.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	if created.Session.State != session.StateReady || created.Session.LastEventID != 1 {
		t.Fatalf("unexpected session: %+v", created.Session)
	}

	response, err = http.Get(server.URL + "/v1/sessions/" + created.Session.ID + "/events")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var history struct {
		Events []session.Event `json:"events"`
	}
	if err := json.NewDecoder(response.Body).Decode(&history); err != nil {
		t.Fatal(err)
	}
	if len(history.Events) != 1 || history.Events[0].Type != "session.created" {
		t.Fatalf("unexpected history: %+v", history.Events)
	}
}

func TestMutationRejectsCrossOriginBrowser(t *testing.T) {
	store, err := session.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	handler := New(store, "test", time.Now()).Handler()
	request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:4646/v1/sessions", strings.NewReader(`{}`))
	request.Host = "127.0.0.1:4646"
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "http://evil.example")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("unexpected status: %d", response.Code)
	}
}

func TestAgentsAndConfigAPI(t *testing.T) {
	root := t.TempDir()
	store, err := session.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		Version: 1, DefaultChatAgentID: "agent",
		AgentProviders: []config.Provider{{ID: "provider", Type: "pi", Enabled: true, Command: "missing-test-command"}},
		Agents:         []config.Agent{{ID: "agent", ProviderID: "provider"}},
		AgentProfiles:  []config.Profile{{Key: "fast", AgentID: "agent"}},
	}
	manager := runtime.New(store, cfg)
	server := httptest.NewServer(New(store, "test", time.Now(), Dependencies{Runtime: manager, ConfigPath: filepath.Join(root, "config.json")}).Handler())
	defer server.Close()
	response, err := http.Get(server.URL + "/v1/agents")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("unexpected status: %s", response.Status)
	}
	var body struct {
		Profiles []config.Profile `json:"profiles"`
		Probes   []config.Probe   `json:"probes"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Profiles) != 1 || len(body.Probes) != 1 || body.Probes[0].Available {
		t.Fatalf("unexpected response: %+v", body)
	}
}

func TestSSEReplaysFromCursor(t *testing.T) {
	store, err := session.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	created, err := store.Create(session.CreateInput{Title: "SSE", Cwd: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Append(created.ID, "session.state", "", mustMarshal(t, session.StateEventData{State: session.StateStopped})); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(New(store, "test", time.Now()).Handler())
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/v1/sessions/"+created.ID+"/events", nil)
	request.Header.Set("Accept", "text/event-stream")
	request.Header.Set("Last-Event-ID", "1")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	reader := bufio.NewReader(response.Body)
	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if line != "id: 2\n" {
		t.Fatalf("unexpected SSE cursor replay: %q", line)
	}
	cancel()
}

func mustMarshal(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
