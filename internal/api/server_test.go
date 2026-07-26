package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
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
	body, _ := json.Marshal(map[string]any{"title": "LAN", "cwd": t.TempDir(), "agentName": "Agent"})
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
		"title":             "API session",
		"cwd":               t.TempDir(),
		"agentName":         "Agent",
		"launchEnvironment": map[string]string{"FORGE_SESSION_ID": "forge-api"},
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
	if created.Session.LaunchEnvironment["FORGE_SESSION_ID"] != "forge-api" {
		t.Fatalf("launch environment was not persisted: %+v", created.Session)
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
	if !bytes.Contains(history.Events[0].Data, []byte(`"launchEnvironment":{"FORGE_SESSION_ID":"forge-api"}`)) {
		t.Fatalf("session.created did not persist launchEnvironment: %s", history.Events[0].Data)
	}
}

func TestCreateSessionRejectsInvalidLaunchEnvironment(t *testing.T) {
	store, err := session.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(New(store, "test", time.Now()).Handler())
	defer server.Close()
	body, _ := json.Marshal(map[string]any{
		"cwd":               t.TempDir(),
		"agentName":         "Agent",
		"launchEnvironment": map[string]string{"BAD=NAME": "value"},
	})
	request, _ := http.NewRequest(http.MethodPost, server.URL+"/v1/sessions", bytes.NewReader(body))
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
	if response.StatusCode != http.StatusUnprocessableEntity || parsed.Error.Code != "invalid_launch_environment" {
		t.Fatalf("status = %d, code = %q", response.StatusCode, parsed.Error.Code)
	}
}

func TestSessionSourceAPIAndCombinedFilters(t *testing.T) {
	root := t.TempDir()
	store, err := session.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(New(store, "test", time.Now()).Handler())

	create := func(title string, sourceValue any) session.Session {
		t.Helper()
		body := map[string]any{
			"title": title, "cwd": t.TempDir(), "agentName": "Agent",
		}
		if sourceValue != nil {
			body["source"] = sourceValue
		}
		encoded, _ := json.Marshal(body)
		request, _ := http.NewRequest(http.MethodPost, server.URL+"/v1/sessions", bytes.NewReader(encoded))
		request.Header.Set("Content-Type", "application/json")
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusCreated {
			t.Fatalf("create %q status = %s", title, response.Status)
		}
		var result struct {
			Session session.Session `json:"session"`
		}
		if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
			t.Fatal(err)
		}
		return result.Session
	}
	sourceValue := map[string]any{"app": "forge", "instanceId": "mac-1", "externalId": "task-1"}
	forgeOne := create("forge one", sourceValue)
	forgeDuplicate := create("forge duplicate", sourceValue)
	forgeTwo := create("forge two", map[string]any{"app": "forge", "instanceId": "mac-2", "externalId": "task-2"})
	other := create("other", map[string]any{"app": "other", "instanceId": "mac-1", "externalId": "task-1"})
	legacy := create("legacy", nil)

	if forgeOne.Source == nil || forgeOne.Source.App != "forge" {
		t.Fatalf("create response source = %+v", forgeOne.Source)
	}
	fetched := getSession(t, server, forgeOne.ID)
	if fetched.Source == nil || *fetched.Source != (session.Source{App: "forge", InstanceID: "mac-1", ExternalID: "task-1"}) {
		t.Fatalf("GET response source = %+v", fetched.Source)
	}
	stateData, err := json.Marshal(session.StateEventData{State: session.StateStopped})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Append(forgeTwo.ID, "session.state", "", stateData); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Archive(forgeDuplicate.ID); err != nil {
		t.Fatal(err)
	}

	assertList := func(query string, want ...string) {
		t.Helper()
		response, err := http.Get(server.URL + "/v1/sessions?" + query)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		var result struct {
			Sessions []session.Session `json:"sessions"`
		}
		if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
			t.Fatal(err)
		}
		got := make(map[string]bool, len(result.Sessions))
		for _, value := range result.Sessions {
			got[value.ID] = true
			if value.Source == nil {
				t.Fatalf("%s: listed session %s omitted source", query, value.ID)
			}
		}
		if len(got) != len(want) {
			t.Fatalf("%s: ids = %v, want %v", query, got, want)
		}
		for _, id := range want {
			if !got[id] {
				t.Fatalf("%s: missing %s in %v", query, id, got)
			}
		}
	}
	assertList("sourceApp=forge", forgeOne.ID, forgeTwo.ID)
	assertList("sourceInstanceId=mac-1", forgeOne.ID, other.ID)
	assertList("sourceExternalId=task-2", forgeTwo.ID)
	assertList("sourceApp=forge&sourceInstanceId=mac-1&sourceExternalId=task-1", forgeOne.ID)
	assertList("includeArchived=true&sourceApp=forge&sourceInstanceId=mac-1&sourceExternalId=task-1", forgeOne.ID, forgeDuplicate.ID)
	assertList("archived=true&sourceApp=forge&sourceInstanceId=mac-1", forgeDuplicate.ID)
	assertList("sourceApp=forge&sourceExternalId=task-2&state=stopped", forgeTwo.ID)

	for _, id := range []string{legacy.ID, other.ID} {
		if list := listIDs(t, server, "?sourceApp=forge"); list[id] != "" {
			t.Fatalf("unmatched session %s appeared in source filter", id)
		}
	}

	server.Close()
	reopened, err := session.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	value, err := reopened.Get(forgeOne.ID)
	if err != nil {
		t.Fatal(err)
	}
	if value.Source == nil || *value.Source != (session.Source{App: "forge", InstanceID: "mac-1", ExternalID: "task-1"}) {
		t.Fatalf("source after daemon-style reopen = %+v", value.Source)
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
		Version:        1,
		AgentProviders: []config.Provider{{ID: "provider", Type: "pi", Enabled: true, Command: "missing-test-command"}},
		Agents:         []config.Agent{{Name: "Pi Agent", ProviderID: "provider"}},
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
		Agents []config.Agent `json:"agents"`
		Probes []config.Probe `json:"probes"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Agents) != 1 || len(body.Probes) != 1 || body.Probes[0].Available {
		t.Fatalf("unexpected response: %+v", body)
	}
}

func TestCreateSessionRequiresExplicitAgent(t *testing.T) {
	server := newConfigTestServer(t)
	post := func(body string) (int, string) {
		t.Helper()
		request, err := http.NewRequest(http.MethodPost, server.URL+"/v1/sessions", strings.NewReader(body))
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
	cwd := t.TempDir()
	cases := []struct {
		name string
		body string
		want int
		code string
	}{
		{"missing agentName", `{"cwd":"` + cwd + `"}`, http.StatusUnprocessableEntity, "agent_required"},
		{"blank agentName", `{"cwd":"` + cwd + `","agentName":"  "}`, http.StatusUnprocessableEntity, "agent_required"},
		{"unknown agent", `{"cwd":"` + cwd + `","agentName":"ghost"}`, http.StatusUnprocessableEntity, "invalid_agent"},
		// A valid name passes validation (case-insensitively) and only
		// fails later because the test provider cannot start.
		{"agent name matches case-insensitively", `{"cwd":"` + cwd + `","agentName":"pi agent"}`, http.StatusBadGateway, "provider_start_failed"},
		{"unresolvable legacy agentId", `{"cwd":"` + cwd + `","agentId":"agent"}`, http.StatusUnprocessableEntity, "agent_id_removed"},
		{"both reference forms", `{"cwd":"` + cwd + `","agentName":"Pi Agent","agentId":"agent"}`, http.StatusBadRequest, "invalid_request"},
		{"legacy selector field", `{"cwd":"` + cwd + `","agentName":"Pi Agent","selector":{"tags":["fast"]}}`, http.StatusBadRequest, "invalid_request"},
	}
	for _, item := range cases {
		status, code := post(item.body)
		if status != item.want || code != item.code {
			t.Errorf("%s: status = %d, code = %q, want %d %s", item.name, status, code, item.want, item.code)
		}
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
		"agentProviders": [
			{"id": "provider", "name": "Pi", "type": "pi", "enabled": true, "command": "missing-test-command"},
			{"id": "second", "name": "Kimi", "type": "kimi", "enabled": false}
		],
		"agents": [
			{"name": "Pi Agent", "providerId": "provider"},
			{"name": "Pi Agent B", "providerId": "provider", "options": {"model": "m"}}
		]
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
	if len(cfg.AgentProviders) != 2 || len(cfg.Agents) != 2 {
		t.Fatalf("unexpected config after save: %+v", cfg)
	}
	if cfg.Agents[1].Options["model"] != "m" {
		t.Fatalf("saved fields lost: %+v", cfg)
	}

	response, err = http.Get(server.URL + "/v1/agents")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var agentsBody struct {
		Agents []config.Agent `json:"agents"`
		Probes []config.Probe `json:"probes"`
	}
	if err := json.NewDecoder(response.Body).Decode(&agentsBody); err != nil {
		t.Fatal(err)
	}
	if len(agentsBody.Agents) != 2 {
		t.Fatalf("GET /v1/agents does not reflect saved config: %+v", agentsBody)
	}
	// Only enabled providers are probed; the disabled second one is absent.
	if len(agentsBody.Probes) != 1 || agentsBody.Probes[0].ProviderID != "provider" {
		t.Fatalf("unexpected probes after save: %+v", agentsBody.Probes)
	}
}

// Removed profile-routing fields are rejected by the strict config decoder,
// so new writes can never reintroduce them through the API.
func TestPutConfigRejectsRemovedProfileFields(t *testing.T) {
	server := newConfigTestServer(t)
	cases := map[string]string{
		"agentProfiles": `{"config":{"agentProviders":[{"id":"p","type":"pi","enabled":true}],
			"agentProfiles":[{"key":"fast","agentId":"a"}]}}`,
		"defaultChatAgentId": `{"config":{"agentProviders":[{"id":"p","type":"pi","enabled":true}],
			"defaultChatAgentId":"a"}}`,
	}
	for name, body := range cases {
		status, code := putConfig(t, server, body)
		if status != http.StatusBadRequest || code != "invalid_request" {
			t.Errorf("%s: status = %d, code = %q, want 400 invalid_request", name, status, code)
		}
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
			"agents":[{"name":"A","providerId":"ghost"}]}}`,
		"duplicate agent names": `{"config":{"agentProviders":[
			{"id":"p","type":"pi","enabled":true}],
			"agents":[{"name":"Codex","providerId":"p"},{"name":" codex ","providerId":"p"}]}}`,
		"blank agent name": `{"config":{"agentProviders":[
			{"id":"p","type":"pi","enabled":true}],
			"agents":[{"name":"  ","providerId":"p"}]}}`,
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
	if len(configBody.Config.Agents) != 1 {
		t.Fatalf("config changed after rejected saves: %+v", configBody.Config)
	}
}

func TestPutConfigRejectsInvalidRequest(t *testing.T) {
	server := newConfigTestServer(t)
	cases := map[string]string{
		"malformed JSON":               `{"config":`,
		"wrong config type":            `{"config":"nope"}`,
		"unknown field without config": `{"conf":{}}`,
		// The removed agent id must never be written back through the API.
		"legacy agent id field": `{"config":{"agentProviders":[{"id":"p","type":"pi","enabled":true}],
			"agents":[{"id":"a","name":"A","providerId":"p"}]}}`,
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

// When the server starts shutting down, live SSE streams must end on their
// own so http.Server.Shutdown is not blocked until its deadline by clients
// that keep the connection open.
func TestSSEStopsWhenServerCloses(t *testing.T) {
	store, err := session.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	created, err := store.Create(session.CreateInput{Title: "SSE", Cwd: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	closing := make(chan struct{})
	server := httptest.NewServer(New(store, "test", time.Now(), Dependencies{Closing: closing}).Handler())
	defer server.Close()

	response, err := http.Get(server.URL + "/v1/sessions/" + created.ID + "/events?stream=true")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	reader := bufio.NewReader(response.Body)
	if line, err := reader.ReadString('\n'); err != nil || line != "id: 1\n" {
		t.Fatalf("expected replayed first event, got %q (%v)", line, err)
	}

	close(closing)
	deadline := time.After(5 * time.Second)
	errCh := make(chan error, 1)
	go func() {
		for {
			if _, err := reader.ReadString('\n'); err != nil {
				errCh <- err
				return
			}
		}
	}()
	select {
	case err := <-errCh:
		if err != io.EOF {
			t.Fatalf("stream ended with %v, want clean EOF", err)
		}
	case <-deadline:
		t.Fatal("SSE stream did not end after the server started closing")
	}
}

func mustMarshal(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func createTestSession(t *testing.T, server *httptest.Server) session.Session {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"title": "archive api", "cwd": t.TempDir(), "agentName": "Agent"})
	request, _ := http.NewRequest(http.MethodPost, server.URL+"/v1/sessions", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create status: %s", response.Status)
	}
	var created struct {
		Session session.Session `json:"session"`
	}
	if err := json.NewDecoder(response.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	return created.Session
}

func listIDs(t *testing.T, server *httptest.Server, query string) map[string]string {
	t.Helper()
	response, err := http.Get(server.URL + "/v1/sessions" + query)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var listed struct {
		Sessions []session.Session `json:"sessions"`
	}
	if err := json.NewDecoder(response.Body).Decode(&listed); err != nil {
		t.Fatal(err)
	}
	result := make(map[string]string)
	for _, value := range listed.Sessions {
		result[value.ID] = value.State
	}
	return result
}

func deleteSession(t *testing.T, server *httptest.Server, id string) *http.Response {
	t.Helper()
	request, _ := http.NewRequest(http.MethodDelete, server.URL+"/v1/sessions/"+id, bytes.NewReader([]byte("{}")))
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func TestArchiveEndpointMovesAndHidesSession(t *testing.T) {
	root := t.TempDir()
	store, err := session.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(New(store, "test", time.Now()).Handler())
	defer server.Close()
	created := createTestSession(t, server)

	response := deleteSession(t, server, created.ID)
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("archive status: %s", response.Status)
	}
	var archived struct {
		Session session.Session `json:"session"`
	}
	if err := json.NewDecoder(response.Body).Decode(&archived); err != nil {
		t.Fatal(err)
	}
	if archived.Session.State != session.StateArchived {
		t.Fatalf("state = %q, want archived", archived.Session.State)
	}

	// The directory physically moved into Archive/.
	if _, err := os.Stat(filepath.Join(root, created.ID)); !os.IsNotExist(err) {
		t.Fatalf("active directory still exists: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, session.ArchiveDirName, created.ID, "events.jsonl")); err != nil {
		t.Fatalf("archived events missing: %v", err)
	}

	// Hidden by default, visible through the explicit archived view and the
	// includeArchived flag.
	if _, ok := listIDs(t, server, "")[created.ID]; ok {
		t.Fatal("archived session appears in the default list")
	}
	if state := listIDs(t, server, "?archived=true")[created.ID]; state != session.StateArchived {
		t.Fatalf("archived view state = %q", state)
	}
	if state := listIDs(t, server, "?includeArchived=true")[created.ID]; state != session.StateArchived {
		t.Fatalf("includeArchived state = %q", state)
	}

	// Metadata and history stay readable.
	response, err = http.Get(server.URL + "/v1/sessions/" + created.ID)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("get archived status: %s", response.Status)
	}
	response, err = http.Get(server.URL + "/v1/sessions/" + created.ID + "/events")
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
	if history.Events[len(history.Events)-1].Type != "session.archived" {
		t.Fatalf("last event = %q, want session.archived", history.Events[len(history.Events)-1].Type)
	}

	// Repeating the archive is idempotent.
	response = deleteSession(t, server, created.ID)
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("repeat archive status: %s", response.Status)
	}
}

func TestArchiveEndpointErrors(t *testing.T) {
	store, err := session.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(New(store, "test", time.Now()).Handler())
	defer server.Close()

	response := deleteSession(t, server, "ses_missing")
	response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("missing archive status: %s", response.Status)
	}

	// A busy session conflicts and keeps its directory.
	created := createTestSession(t, server)
	if _, err := store.Append(created.ID, "turn.started", "turn_open", nil); err != nil {
		t.Fatal(err)
	}
	response = deleteSession(t, server, created.ID)
	defer response.Body.Close()
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("active archive status: %s", response.Status)
	}
	var failure struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(response.Body).Decode(&failure); err != nil {
		t.Fatal(err)
	}
	if failure.Error.Code != "session_active" {
		t.Fatalf("error code = %q, want session_active", failure.Error.Code)
	}
	value, err := store.Get(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if value.State != session.StateBusy {
		t.Fatalf("state changed to %q after conflict", value.State)
	}
}

func TestArchivedSessionRejectsWrites(t *testing.T) {
	store, err := session.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(New(store, "test", time.Now()).Handler())
	defer server.Close()
	created := createTestSession(t, server)
	response := deleteSession(t, server, created.ID)
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("archive status: %s", response.Status)
	}

	for _, path := range []string{"messages", "resume", "interrupt", "stop", "approvals/apr_1"} {
		request, _ := http.NewRequest(http.MethodPost, server.URL+"/v1/sessions/"+created.ID+"/"+path, bytes.NewReader([]byte("{}")))
		request.Header.Set("Content-Type", "application/json")
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		var failure struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		_ = json.NewDecoder(response.Body).Decode(&failure)
		response.Body.Close()
		if response.StatusCode != http.StatusConflict || failure.Error.Code != "session_archived" {
			t.Fatalf("POST %s: status = %s code = %q, want 409 session_archived", path, response.Status, failure.Error.Code)
		}
	}
}

// newToggleTestServer starts a daemon whose config holds a single built-in
// provider (pi) with a custom command and one agent bound to it.
func newToggleTestServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	root := t.TempDir()
	store, err := session.Open(filepath.Join(root, "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		Version:        1,
		AgentProviders: []config.Provider{{ID: "pi", Name: "Pi Coding Agent", Type: "pi", Enabled: true, Command: "missing-test-command"}},
		Agents:         []config.Agent{{Name: "Pi Agent", ProviderID: "pi"}},
	}
	configPath := filepath.Join(root, "config.json")
	if err := config.Save(configPath, cfg); err != nil {
		t.Fatal(err)
	}
	manager := runtime.New(store, cfg)
	server := httptest.NewServer(New(store, "test", time.Now(), Dependencies{Runtime: manager, ConfigPath: configPath}).Handler())
	t.Cleanup(server.Close)
	return server, configPath
}

func toggleProvider(t *testing.T, server *httptest.Server, id, body string) (int, map[string]any) {
	t.Helper()
	request, err := http.NewRequest(http.MethodPut, server.URL+"/v1/config/providers/"+id, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var parsed map[string]any
	if err := json.NewDecoder(response.Body).Decode(&parsed); err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, parsed
}

func getAgents(t *testing.T, server *httptest.Server) []map[string]any {
	t.Helper()
	response, err := http.Get(server.URL + "/v1/agents")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var body struct {
		Agents []map[string]any `json:"agents"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	return body.Agents
}

func TestToggleProviderDisableEnableRoundTrip(t *testing.T) {
	server, configPath := newToggleTestServer(t)

	// Disable: only the flag flips, the custom command is preserved, and the
	// change is persisted to disk.
	status, body := toggleProvider(t, server, "pi", `{"enabled": false}`)
	if status != http.StatusOK {
		t.Fatalf("disable: status = %d, body = %v", status, body)
	}
	provider := body["provider"].(map[string]any)
	if provider["enabled"] != false || provider["command"] != "missing-test-command" {
		t.Fatalf("disable lost the underlying configuration: %v", provider)
	}
	onDisk, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if onDisk.AgentProviders[0].Enabled || onDisk.AgentProviders[0].Command != "missing-test-command" {
		t.Fatalf("toggle was not persisted: %+v", onDisk.AgentProviders[0])
	}

	// The agent of a disabled provider is reported unavailable and new
	// sessions naming it are rejected even when the client bypasses the UI.
	agents := getAgents(t, server)
	if len(agents) != 1 || agents[0]["available"] != false || agents[0]["unavailableReason"] == nil {
		t.Fatalf("agent of disabled provider should be unavailable: %v", agents)
	}
	request, err := http.NewRequest(http.MethodPost, server.URL+"/v1/sessions", strings.NewReader(
		`{"cwd":"`+t.TempDir()+`","agentName":"Pi Agent"}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var created map[string]any
	if err := json.NewDecoder(response.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusUnprocessableEntity || created["error"].(map[string]any)["code"] != "invalid_agent" {
		t.Fatalf("session creation with a disabled provider: status = %d, body = %v", response.StatusCode, created)
	}

	// Re-enable restores availability without losing the command.
	status, body = toggleProvider(t, server, "pi", `{"enabled": true}`)
	if status != http.StatusOK {
		t.Fatalf("enable: status = %d, body = %v", status, body)
	}
	provider = body["provider"].(map[string]any)
	if provider["enabled"] != true || provider["command"] != "missing-test-command" {
		t.Fatalf("enable lost the underlying configuration: %v", provider)
	}
	agents = getAgents(t, server)
	if len(agents) != 1 || agents[0]["available"] != true {
		t.Fatalf("agent of re-enabled provider should be available: %v", agents)
	}
}

func TestToggleProviderCreatesBuiltinDefault(t *testing.T) {
	server, _ := newToggleTestServer(t)
	status, body := toggleProvider(t, server, "kimi", `{"enabled": true}`)
	if status != http.StatusOK {
		t.Fatalf("status = %d, body = %v", status, body)
	}
	provider := body["provider"].(map[string]any)
	if provider["id"] != "kimi" || provider["type"] != "kimi" || provider["enabled"] != true {
		t.Fatalf("missing built-in provider was not created with defaults: %v", provider)
	}
}

func TestToggleProviderRejectsBadRequests(t *testing.T) {
	server, _ := newToggleTestServer(t)
	cases := []struct {
		name string
		id   string
		body string
		want int
		code string
	}{
		{"unknown provider", "ghost", `{"enabled": true}`, http.StatusNotFound, "unknown_provider"},
		{"non-builtin custom provider", "provider", `{"enabled": false}`, http.StatusNotFound, "unknown_provider"},
		{"missing enabled flag", "pi", `{}`, http.StatusBadRequest, "invalid_request"},
		{"wrong enabled type", "pi", `{"enabled": "yes"}`, http.StatusBadRequest, "invalid_request"},
		{"unknown field", "pi", `{"enabled": true, "command": "x"}`, http.StatusBadRequest, "invalid_request"},
	}
	for _, item := range cases {
		status, body := toggleProvider(t, server, item.id, item.body)
		code, _ := body["error"].(map[string]any)["code"].(string)
		if status != item.want || code != item.code {
			t.Errorf("%s: status = %d, code = %q, want %d %s", item.name, status, code, item.want, item.code)
		}
	}
}

// The session records the canonical configured display name, not the
// spelling the client sent.
func TestCreateSessionPersistsCanonicalAgentName(t *testing.T) {
	server := newConfigTestServer(t)
	body, _ := json.Marshal(map[string]any{"cwd": t.TempDir(), "agentName": " pi AGENT "})
	request, _ := http.NewRequest(http.MethodPost, server.URL+"/v1/sessions", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	// The test provider cannot start; the session still exists.
	if response.StatusCode != http.StatusBadGateway {
		t.Fatalf("unexpected status: %s", response.Status)
	}
	var parsed struct {
		Error struct {
			Details struct {
				SessionID string `json:"sessionId"`
			} `json:"details"`
		} `json:"error"`
	}
	if err := json.NewDecoder(response.Body).Decode(&parsed); err != nil {
		t.Fatal(err)
	}
	value := getSession(t, server, parsed.Error.Details.SessionID)
	if value.AgentName != "Pi Agent" {
		t.Fatalf("session did not record the canonical name: %+v", value)
	}
}

// When session creation fails at the provider, the API must surface the
// real provider error and the session must end in a terminal failed state —
// never stuck in starting.
func TestCreateSessionProviderFailureSurfacesError(t *testing.T) {
	server := newConfigTestServer(t)
	body, _ := json.Marshal(map[string]any{"cwd": t.TempDir(), "agentName": "Pi Agent"})
	request, _ := http.NewRequest(http.MethodPost, server.URL+"/v1/sessions", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusBadGateway {
		t.Fatalf("unexpected status: %s", response.Status)
	}
	var parsed struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
			Details struct {
				SessionID string `json:"sessionId"`
			} `json:"details"`
		} `json:"error"`
	}
	if err := json.NewDecoder(response.Body).Decode(&parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.Error.Code != "provider_start_failed" {
		t.Fatalf("error code = %q", parsed.Error.Code)
	}
	if parsed.Error.Message == "" || parsed.Error.Message == "failed" {
		t.Fatalf("error message must carry the provider cause: %q", parsed.Error.Message)
	}
	value := getSession(t, server, parsed.Error.Details.SessionID)
	if value.State != session.StateFailed {
		t.Fatalf("session state = %q, want failed", value.State)
	}
}

// A deprecated agentId still resolves through the id → name mapping recorded
// by the legacy config migration; the session is created with the name.
func TestCreateSessionResolvesLegacyAgentID(t *testing.T) {
	root := t.TempDir()
	store, err := session.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		Version:        1,
		AgentProviders: []config.Provider{{ID: "provider", Type: "pi", Enabled: true, Command: "missing-test-command"}},
		Agents:         []config.Agent{{Name: "Pi Agent", ProviderID: "provider"}},
	}
	manager := runtime.New(store, cfg, map[string]string{"agent-old": "Pi Agent"})
	server := httptest.NewServer(New(store, "test", time.Now(), Dependencies{Runtime: manager, ConfigPath: filepath.Join(root, "config.json")}).Handler())
	defer server.Close()

	body, _ := json.Marshal(map[string]any{"cwd": t.TempDir(), "agentId": "agent-old"})
	request, _ := http.NewRequest(http.MethodPost, server.URL+"/v1/sessions", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusBadGateway {
		t.Fatalf("unexpected status: %s", response.Status)
	}
	var parsed struct {
		Error struct {
			Details struct {
				SessionID string `json:"sessionId"`
			} `json:"details"`
		} `json:"error"`
	}
	if err := json.NewDecoder(response.Body).Decode(&parsed); err != nil {
		t.Fatal(err)
	}
	value := getSession(t, server, parsed.Error.Details.SessionID)
	if value.AgentName != "Pi Agent" {
		t.Fatalf("legacy agentId did not resolve to the configured name: %+v", value)
	}
}

// Renaming an agent through PUT /v1/config re-points the sessions that
// referenced the old name at the new one via a session.agent event.
func TestPutConfigRenameMigratesSessionReferences(t *testing.T) {
	server := newConfigTestServer(t)
	body, _ := json.Marshal(map[string]any{"cwd": t.TempDir(), "agentName": "Pi Agent"})
	request, _ := http.NewRequest(http.MethodPost, server.URL+"/v1/sessions", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	var created struct {
		Error struct {
			Details struct {
				SessionID string `json:"sessionId"`
			} `json:"details"`
		} `json:"error"`
	}
	_ = json.NewDecoder(response.Body).Decode(&created)
	response.Body.Close()
	sessionID := created.Error.Details.SessionID
	if sessionID == "" {
		t.Fatal("session was not created")
	}

	renamed := `{"config":{
		"version": 1,
		"agentProviders": [
			{"id": "provider", "name": "Pi", "type": "pi", "enabled": true, "command": "missing-test-command"}
		],
		"agents": [{"name": "Pi Agent X", "providerId": "provider"}]
	}}`
	status, code := putConfig(t, server, renamed)
	if status != http.StatusOK || code != "" {
		t.Fatalf("rename save failed: status = %d, code = %q", status, code)
	}
	value := getSession(t, server, sessionID)
	if value.AgentName != "Pi Agent X" {
		t.Fatalf("session did not follow the rename: %+v", value)
	}
	response, err = http.Get(server.URL + "/v1/sessions/" + sessionID + "/events")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var eventsBody struct {
		Events []session.Event `json:"events"`
	}
	if err := json.NewDecoder(response.Body).Decode(&eventsBody); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, event := range eventsBody.Events {
		if event.Type == "session.agent" && strings.Contains(string(event.Data), "Pi Agent X") {
			found = true
		}
	}
	if !found {
		t.Fatal("rename did not append a session.agent event")
	}
}

// A rename that matches several identical new agents is rejected instead of
// guessed; the sessions keep referencing the old name.
func TestPutConfigRejectsAmbiguousRename(t *testing.T) {
	server := newConfigTestServer(t)
	ambiguous := `{"config":{
		"version": 1,
		"agentProviders": [
			{"id": "provider", "name": "Pi", "type": "pi", "enabled": true, "command": "missing-test-command"}
		],
		"agents": [
			{"name": "Pi Agent X", "providerId": "provider"},
			{"name": "Pi Agent Y", "providerId": "provider"}
		]
	}}`
	status, code := putConfig(t, server, ambiguous)
	if status != http.StatusUnprocessableEntity || code != "ambiguous_rename" {
		t.Fatalf("ambiguous rename: status = %d, code = %q", status, code)
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
	if len(configBody.Config.Agents) != 1 || configBody.Config.Agents[0].Name != "Pi Agent" {
		t.Fatalf("config changed after the rejected rename: %+v", configBody.Config)
	}
}

func getSession(t *testing.T, server *httptest.Server, id string) session.Session {
	t.Helper()
	response, err := http.Get(server.URL + "/v1/sessions/" + id)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var body struct {
		Session session.Session `json:"session"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	return body.Session
}

func TestStatusReportsUnifiedDataPaths(t *testing.T) {
	root := t.TempDir()
	store, err := session.Open(filepath.Join(root, "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(New(store, "test", time.Now(), Dependencies{
		ConfigPath: filepath.Join(root, "config.json"),
		LogsDir:    filepath.Join(root, "logs"),
	}).Handler())
	defer server.Close()

	response, err := http.Get(server.URL + "/v1/status")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("unexpected status: %s", response.Status)
	}
	var body struct {
		Paths map[string]string `json:"paths"`
		Store map[string]any    `json:"sessionStore"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"config":   filepath.Join(root, "config.json"),
		"sessions": filepath.Join(root, "sessions"),
		"archive":  filepath.Join(root, "sessions", "Archive"),
		"logs":     filepath.Join(root, "logs"),
	}
	for key, value := range want {
		if body.Paths[key] != value {
			t.Errorf("paths.%s = %q, want %q", key, body.Paths[key], value)
		}
	}
	if body.Store["path"] != want["sessions"] || body.Store["archivePath"] != want["archive"] {
		t.Errorf("sessionStore = %+v", body.Store)
	}
}
