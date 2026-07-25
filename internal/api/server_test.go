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

func newGuardedTestServer(t *testing.T) (*httptest.Server, *ListenAddress) {
	t.Helper()
	store, err := session.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	listen := resolveForTest(t, "192.168.1.10:4646", nil, testLANIPv4)
	server := httptest.NewServer(New(store, "test", time.Now(), Dependencies{Listen: listen}).Handler())
	t.Cleanup(server.Close)
	return server, listen
}

func TestHostGuardRejectsForeignHost(t *testing.T) {
	server, _ := newGuardedTestServer(t)
	for host, want := range map[string]int{
		"192.168.1.10:4646": http.StatusOK,
		"127.0.0.1:4646":    http.StatusOK,
		"localhost:4646":    http.StatusOK,
		"myhost:4646":       http.StatusOK,
		"evil.example":      http.StatusForbidden,
		"192.168.1.11:4646": http.StatusForbidden,
	} {
		request, _ := http.NewRequest(http.MethodGet, server.URL+"/v1/health", nil)
		request.Host = host
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != want {
			t.Fatalf("Host %q: status = %d, want %d", host, response.StatusCode, want)
		}
	}
}

func TestMutationAcceptsSameOriginLANBrowser(t *testing.T) {
	server, _ := newGuardedTestServer(t)
	body, _ := json.Marshal(map[string]any{"title": "LAN", "cwd": t.TempDir()})
	request, _ := http.NewRequest(http.MethodPost, server.URL+"/v1/sessions", bytes.NewReader(body))
	request.Host = "192.168.1.10:4646"
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "http://192.168.1.10:4646")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("unexpected status: %s", response.Status)
	}
}

func TestMutationRejectsCrossOriginOnLANListener(t *testing.T) {
	server, _ := newGuardedTestServer(t)
	request, _ := http.NewRequest(http.MethodPost, server.URL+"/v1/sessions", strings.NewReader(`{}`))
	request.Host = "192.168.1.10:4646"
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "http://evil.example")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("unexpected status: %s", response.Status)
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

func newConfigTestServer(t *testing.T) *httptest.Server {
	t.Helper()
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
	t.Cleanup(server.Close)
	return server
}

func TestAgentsAndConfigAPI(t *testing.T) {
	server := newConfigTestServer(t)
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

func putConfig(t *testing.T, server *httptest.Server, body string) (int, string) {
	t.Helper()
	request, err := http.NewRequest(http.MethodPut, server.URL+"/v1/config", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var parsed struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(response.Body).Decode(&parsed); err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, parsed.Error.Code
}

func TestPutConfigRoundTrip(t *testing.T) {
	server := newConfigTestServer(t)
	updated := `{"config":{
		"version": 1,
		"defaultChatAgentId": "agent-b",
		"agentProviders": [
			{"id": "provider", "name": "Pi", "type": "pi", "enabled": true, "command": "missing-test-command"},
			{"id": "second", "name": "Kimi", "type": "kimi", "enabled": false}
		],
		"agents": [
			{"id": "agent", "name": "Pi Agent", "providerId": "provider"},
			{"id": "agent-b", "name": "Pi Agent B", "providerId": "provider", "options": {"model": "m"}}
		],
		"agentProfiles": [{"key": "fast", "agentId": "agent-b", "description": "fast lane"}]
	}}`
	status, code := putConfig(t, server, updated)
	if status != http.StatusOK || code != "" {
		t.Fatalf("PUT /v1/config: status = %d, code = %q", status, code)
	}

	response, err := http.Get(server.URL + "/v1/config")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var configBody struct {
		Config config.Config `json:"config"`
	}
	if err := json.NewDecoder(response.Body).Decode(&configBody); err != nil {
		t.Fatal(err)
	}
	cfg := configBody.Config
	if cfg.DefaultChatAgentID != "agent-b" || len(cfg.AgentProviders) != 2 || len(cfg.Agents) != 2 || len(cfg.AgentProfiles) != 1 {
		t.Fatalf("unexpected config after save: %+v", cfg)
	}
	if cfg.Agents[1].Options["model"] != "m" || cfg.AgentProfiles[0].Description != "fast lane" {
		t.Fatalf("saved fields lost: %+v", cfg)
	}

	response, err = http.Get(server.URL + "/v1/agents")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var agentsBody struct {
		DefaultChatAgentID string           `json:"defaultChatAgentId"`
		Agents             []config.Agent   `json:"agents"`
		Probes             []config.Probe   `json:"probes"`
		Profiles           []config.Profile `json:"profiles"`
	}
	if err := json.NewDecoder(response.Body).Decode(&agentsBody); err != nil {
		t.Fatal(err)
	}
	if agentsBody.DefaultChatAgentID != "agent-b" || len(agentsBody.Agents) != 2 {
		t.Fatalf("GET /v1/agents does not reflect saved config: %+v", agentsBody)
	}
	// Only enabled providers are probed; the disabled second one is absent.
	if len(agentsBody.Probes) != 1 || agentsBody.Probes[0].ProviderID != "provider" {
		t.Fatalf("unexpected probes after save: %+v", agentsBody.Probes)
	}
	if len(agentsBody.Profiles) != 1 || agentsBody.Profiles[0].AgentID != "agent-b" {
		t.Fatalf("unexpected profiles after save: %+v", agentsBody.Profiles)
	}
}

func TestPutConfigRejectsInvalidConfig(t *testing.T) {
	server := newConfigTestServer(t)
	cases := map[string]string{
		"duplicate provider id": `{"config":{"agentProviders":[
			{"id":"p","type":"pi","enabled":true},
			{"id":"p","type":"kimi","enabled":true}]}}`,
		"unsupported provider type": `{"config":{"agentProviders":[
			{"id":"p","type":"bogus","enabled":true}]}}`,
		"dangling agent provider": `{"config":{"agentProviders":[
			{"id":"p","type":"pi","enabled":true}],
			"agents":[{"id":"a","providerId":"ghost"}]}}`,
	}
	for name, body := range cases {
		status, code := putConfig(t, server, body)
		if status != http.StatusUnprocessableEntity || code != "invalid_config" {
			t.Errorf("%s: status = %d, code = %q, want 422 invalid_config", name, status, code)
		}
	}
	// A failed save must not change the existing config.
	response, err := http.Get(server.URL + "/v1/config")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var configBody struct {
		Config config.Config `json:"config"`
	}
	if err := json.NewDecoder(response.Body).Decode(&configBody); err != nil {
		t.Fatal(err)
	}
	if configBody.Config.DefaultChatAgentID != "agent" || len(configBody.Config.Agents) != 1 {
		t.Fatalf("config changed after rejected saves: %+v", configBody.Config)
	}
}

func TestPutConfigRejectsInvalidRequest(t *testing.T) {
	server := newConfigTestServer(t)
	cases := map[string]string{
		"malformed JSON":               `{"config":`,
		"wrong config type":            `{"config":"nope"}`,
		"unknown field without config": `{"conf":{}}`,
	}
	for name, body := range cases {
		status, code := putConfig(t, server, body)
		if status != http.StatusBadRequest || code != "invalid_request" {
			t.Errorf("%s: status = %d, code = %q, want 400 invalid_request", name, status, code)
		}
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

// Every event — including types a consumer has never heard of — must arrive
// on the default SSE message channel, so no event is silently dropped just
// because the client did not subscribe to its type name.
func TestSSEStreamsUnknownEventTypes(t *testing.T) {
	store, err := session.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	created, err := store.Create(session.CreateInput{Title: "SSE", Cwd: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Append(created.ID, "provider.some.future.event", "", mustMarshal(t, map[string]any{"novel": true})); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(New(store, "test", time.Now()).Handler())
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/v1/sessions/"+created.ID+"/events?stream=true", nil)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()

	reader := bufio.NewReader(response.Body)
	var frames []string
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if line == "\n" {
			if len(frames) > 0 {
				break
			}
			continue
		}
		frames = append(frames, strings.TrimSuffix(line, "\n"))
		if strings.HasPrefix(frames[len(frames)-1], "data: ") {
			// The data line is the last line of a frame.
			if _, err := reader.ReadString('\n'); err != nil {
				t.Fatal(err)
			}
			break
		}
	}
	if len(frames) != 2 || frames[0] != "id: 1" || !strings.HasPrefix(frames[1], "data: ") {
		t.Fatalf("unexpected SSE frame: %q", frames)
	}
	var event session.Event
	if err := json.Unmarshal([]byte(strings.TrimPrefix(frames[1], "data: ")), &event); err != nil {
		t.Fatal(err)
	}
	if event.Type != "session.created" {
		t.Fatalf("first replayed event type = %q, want session.created", event.Type)
	}

	// The custom event must also be framed without an `event:` name field.
	frames = frames[:0]
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if line == "\n" {
			break
		}
		frames = append(frames, strings.TrimSuffix(line, "\n"))
	}
	if len(frames) != 2 || frames[0] != "id: 2" || !strings.HasPrefix(frames[1], "data: ") {
		t.Fatalf("unknown event frame must use the default message channel: %q", frames)
	}
	if err := json.Unmarshal([]byte(strings.TrimPrefix(frames[1], "data: ")), &event); err != nil {
		t.Fatal(err)
	}
	if event.Type != "provider.some.future.event" {
		t.Fatalf("second event type = %q, want provider.some.future.event", event.Type)
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
