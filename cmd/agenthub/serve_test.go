package main

// End-to-end tests for `agenthub serve`: the daemon must migrate the legacy
// default layout (macOS Application Support sessions, ~/Library/Logs
// service logs) into the unified ~/.agenthub root before opening the store,
// report the new paths through /v1/status, and refuse to start on a
// conflict. Everything runs under a temporary HOME; no real user data is
// touched.

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/disksing/agenthub/internal/migrate"
)

// freePort reserves a loopback port that is very likely still free when the
// daemon binds it a moment later.
func freePort(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return addr
}

func writeLegacySession(t *testing.T, dir, id string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	event := map[string]any{
		"id":        1,
		"time":      time.Now().UTC(),
		"type":      "session.created",
		"sessionId": id,
		"data": map[string]any{
			"id":        id,
			"title":     "legacy " + id,
			"cwd":       t.TempDir(),
			"agentName": "Codex",
			"state":     "ready",
			"createdAt": time.Now().UTC(),
			"updatedAt": time.Now().UTC(),
		},
	}
	data, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

// legacyHome builds a HOME with the pre-unification default layout.
func legacyHome(t *testing.T, active, archived []string) string {
	t.Helper()
	home := t.TempDir()
	dataDir := filepath.Join(home, "Library", "Application Support", "agenthub")
	for _, id := range active {
		writeLegacySession(t, filepath.Join(dataDir, "sessions", id), id)
	}
	for _, id := range archived {
		writeLegacySession(t, filepath.Join(dataDir, "sessions", "Archive", id), id)
	}
	logsDir := filepath.Join(home, "Library", "Logs", "AgentHub")
	if err := os.MkdirAll(logsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(logsDir, "stdout.log"), []byte("old log\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return home
}

func serveAsync(t *testing.T, addr string) chan error {
	t.Helper()
	done := make(chan error, 1)
	go func() {
		done <- runServe([]string{"--addr", addr})
	}()
	return done
}

func waitForStatus(t *testing.T, addr string) map[string]any {
	t.Helper()
	endpoint := "http://" + addr
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		response, err := http.Get(endpoint + "/v1/status")
		if err == nil {
			var body map[string]any
			_ = json.NewDecoder(response.Body).Decode(&body)
			response.Body.Close()
			return body
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("daemon did not start")
	return nil
}

func stopDaemon(t *testing.T, home string, done chan error) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(home, ".agenthub", "server.json"))
	if err != nil {
		t.Fatal(err)
	}
	var state struct {
		PID int `json:"pid"`
	}
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Kill(state.PID, syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serve returned: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("daemon did not shut down")
	}
}

func TestServeMigratesLegacyLayoutIntoDotAgentHub(t *testing.T) {
	home := legacyHome(t, []string{"ses_aaa111"}, []string{"ses_zzz999"})
	t.Setenv("HOME", home)
	t.Setenv("AGENTHUB_HOME", "")
	t.Setenv("AGENTHUB_CODEX_CLI", "definitely-missing-codex")
	t.Setenv("AGENTHUB_KIMI_CLI", "definitely-missing-kimi")
	t.Setenv("AGENTHUB_PI_CLI", "definitely-missing-pi")
	t.Setenv("AGENTHUB_OPENCODE_CLI", "definitely-missing-opencode")
	addr := freePort(t)
	done := serveAsync(t, addr)

	status := waitForStatus(t, addr)
	root := filepath.Join(home, ".agenthub")
	paths, _ := status["paths"].(map[string]any)
	wantPaths := map[string]string{
		"config":   filepath.Join(root, "config.json"),
		"sessions": filepath.Join(root, "sessions"),
		"archive":  filepath.Join(root, "sessions", "Archive"),
		"logs":     filepath.Join(root, "logs"),
	}
	for key, want := range wantPaths {
		if paths[key] != want {
			t.Errorf("status paths.%s = %v, want %q", key, paths[key], want)
		}
	}

	// Both sessions migrated and are listed: active visible by default,
	// archived hidden unless requested.
	get := func(path string) map[string]any {
		t.Helper()
		response, err := http.Get("http://" + addr + path)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		var body map[string]any
		if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		return body
	}
	sessions := get("/v1/sessions")["sessions"].([]any)
	if len(sessions) != 1 || sessions[0].(map[string]any)["id"] != "ses_aaa111" {
		t.Fatalf("active sessions = %+v", sessions)
	}
	archived := get("/v1/sessions?archived=true")["sessions"].([]any)
	if len(archived) != 1 || archived[0].(map[string]any)["id"] != "ses_zzz999" {
		t.Fatalf("archived sessions = %+v", archived)
	}
	events := get("/v1/sessions/ses_aaa111/events")["events"].([]any)
	if len(events) != 1 || events[0].(map[string]any)["type"] != "session.created" {
		t.Fatalf("events = %+v", events)
	}

	// The legacy store is gone, the service log moved, and the journal
	// recorded completion.
	if _, err := os.Stat(filepath.Join(home, "Library", "Application Support", "agenthub")); !os.IsNotExist(err) {
		t.Error("legacy data directory still exists")
	}
	if data, err := os.ReadFile(filepath.Join(root, "logs", "stdout.log")); err != nil || string(data) != "old log\n" {
		t.Errorf("migrated log = %q, %v", data, err)
	}
	journal, err := os.ReadFile(filepath.Join(root, "migration.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(journal), `"state": "completed"`) {
		t.Errorf("journal not completed: %s", journal)
	}

	stopDaemon(t, home, done)

	// A restart is idempotent: the daemon comes up on the migrated data
	// without touching anything.
	done = serveAsync(t, addr)
	status = waitForStatus(t, addr)
	storeInfo, _ := status["sessionStore"].(map[string]any)
	if storeInfo["sessionCount"].(float64) != 2 {
		t.Fatalf("sessionStore after restart = %+v", storeInfo)
	}
	stopDaemon(t, home, done)
}

func TestServeRefusesToStartOnMigrationConflict(t *testing.T) {
	home := legacyHome(t, []string{"ses_old111"}, nil)
	// The new store already holds a different session: a real conflict.
	writeLegacySession(t, filepath.Join(home, ".agenthub", "sessions", "ses_new222"), "ses_new222")
	t.Setenv("HOME", home)
	t.Setenv("AGENTHUB_HOME", "")
	err := runServe([]string{"--addr", freePort(t)})
	var conflict *migrate.ConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("err = %v", err)
	}
	for _, want := range []string{"ses_old111", "ses_new222", "never merges"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("conflict error missing %q:\n%s", want, err)
		}
	}
	// Both sides are untouched.
	for _, path := range []string{
		filepath.Join(home, "Library", "Application Support", "agenthub", "sessions", "ses_old111"),
		filepath.Join(home, ".agenthub", "sessions", "ses_new222"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("%s: %v", path, err)
		}
	}
}

func TestServeSkipsMigrationUnderAgentHubHome(t *testing.T) {
	home := legacyHome(t, []string{"ses_old111"}, nil)
	isolated := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("AGENTHUB_HOME", isolated)
	t.Setenv("AGENTHUB_CODEX_CLI", "definitely-missing-codex")
	t.Setenv("AGENTHUB_KIMI_CLI", "definitely-missing-kimi")
	t.Setenv("AGENTHUB_PI_CLI", "definitely-missing-pi")
	t.Setenv("AGENTHUB_OPENCODE_CLI", "definitely-missing-opencode")
	addr := freePort(t)
	done := serveAsync(t, addr)
	status := waitForStatus(t, addr)
	paths, _ := status["paths"].(map[string]any)
	if !strings.HasPrefix(fmt.Sprint(paths["sessions"]), isolated) {
		t.Fatalf("isolated sessions path = %v", paths["sessions"])
	}
	// The legacy layout under HOME is untouched.
	if _, err := os.Stat(filepath.Join(home, "Library", "Application Support", "agenthub", "sessions", "ses_old111")); err != nil {
		t.Error("legacy session moved despite AGENTHUB_HOME isolation")
	}
	if _, err := os.Stat(filepath.Join(home, ".agenthub", "migration.json")); !os.IsNotExist(err) {
		t.Error("migration journal written despite AGENTHUB_HOME isolation")
	}
	// Stop via the isolated server.json.
	data, err := os.ReadFile(filepath.Join(isolated, "state", "server.json"))
	if err != nil {
		t.Fatal(err)
	}
	var state struct {
		PID int `json:"pid"`
	}
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Kill(state.PID, syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serve returned: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("daemon did not shut down")
	}
}
