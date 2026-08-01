package main

// End-to-end tests for `agenthub serve`. Everything runs under temporary
// roots; no real user data is touched.

import (
	"bufio"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

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

func writeSessionEvents(t *testing.T, dir, id string, provider bool) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	events := []map[string]any{
		{
			"id": 1, "time": now, "type": "session.created", "sessionId": id,
			"data": map[string]any{
				"id": id, "title": "session " + id, "cwd": t.TempDir(), "agentName": "Codex",
				"state": "ready", "createdAt": now, "updatedAt": now,
			},
		},
	}
	if provider {
		events = append(events, map[string]any{
			"id": 2, "time": now, "type": "session.provider", "sessionId": id,
			"data": map[string]any{
				"agentName": "Codex", "provider": "codex", "providerSessionId": "thread-test-42",
			},
		})
	}
	var data []byte
	for _, event := range events {
		line, err := json.Marshal(event)
		if err != nil {
			t.Fatal(err)
		}
		data = append(data, append(line, '\n')...)
	}
	if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), data, 0o600); err != nil {
		t.Fatal(err)
	}
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

func stopDaemon(t *testing.T, root string, done chan error) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, "state", "server.json"))
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

// SIGTERM must stop the daemon promptly and cleanly even while an SSE client
// keeps an event stream open.
func TestServeSIGTERMClosesSSEAndExitsCleanly(t *testing.T) {
	home := t.TempDir()
	isolated := t.TempDir()
	writeSessionEvents(t, filepath.Join(isolated, "data", "sessions", "ses_sse001"), "ses_sse001", false)
	t.Setenv("HOME", home)
	t.Setenv("AGENTHUB_HOME", isolated)
	t.Setenv("AGENTHUB_CODEX_CLI", "definitely-missing-codex")
	t.Setenv("AGENTHUB_KIMI_CLI", "definitely-missing-kimi")
	t.Setenv("AGENTHUB_PI_CLI", "definitely-missing-pi")
	t.Setenv("AGENTHUB_OPENCODE_CLI", "definitely-missing-opencode")
	addr := freePort(t)
	done := serveAsync(t, addr)
	waitForStatus(t, addr)

	response, err := http.Get("http://" + addr + "/v1/sessions/ses_sse001/events?stream=true")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	reader := bufio.NewReader(response.Body)
	if line, err := reader.ReadString('\n'); err != nil || !strings.HasPrefix(line, "id: ") {
		t.Fatalf("expected replayed SSE event, got %q (%v)", line, err)
	}

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
	started := time.Now()
	if err := syscall.Kill(state.PID, syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serve returned: %v", err)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("daemon did not shut down promptly with an open SSE stream")
	}
	if elapsed := time.Since(started); elapsed >= 5*time.Second {
		t.Fatalf("shutdown took %v; SSE stream blocked the graceful shutdown", elapsed)
	}
	streamEnded := make(chan error, 1)
	go func() {
		for {
			if _, err := reader.ReadString('\n'); err != nil {
				streamEnded <- err
				return
			}
		}
	}()
	select {
	case err := <-streamEnded:
		if err != io.EOF {
			t.Fatalf("SSE stream ended with %v, want clean EOF", err)
		}
	case <-time.After(2 * time.Second):
		_ = response.Body.Close()
		t.Fatal("SSE stream did not end after daemon shutdown")
	}
}
