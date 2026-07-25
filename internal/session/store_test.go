package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStorePersistsOneContinuousEventLog(t *testing.T) {
	root := t.TempDir()
	store, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	created, err := store.Create(CreateInput{
		Title:   "Fix login",
		Cwd:     t.TempDir(),
		AgentID: "codex-build",
	})
	if err != nil {
		t.Fatal(err)
	}
	turnID := "turn_test"
	if _, err := store.Append(created.ID, "turn.started", turnID, nil); err != nil {
		t.Fatal(err)
	}
	approval, _ := json.Marshal(ApprovalEventData{ApprovalID: "apr_test"})
	if _, err := store.Append(created.ID, "approval.requested", turnID, approval); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Append(created.ID, "approval.resolved", turnID, approval); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Append(created.ID, "turn.completed", turnID, nil); err != nil {
		t.Fatal(err)
	}

	value, err := store.Get(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if value.State != StateReady || value.CurrentTurnID != "" || len(value.PendingApprovalIDs) != 0 {
		t.Fatalf("unexpected projection: %+v", value)
	}
	sessionDir := filepath.Join(root, created.ID)
	entries, err := os.ReadDir(sessionDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected only session.json and events.jsonl, got %d entries", len(entries))
	}
	for _, name := range []string{"session.json", "events.jsonl"} {
		if _, err := os.Stat(filepath.Join(sessionDir, name)); err != nil {
			t.Fatalf("missing %s: %v", name, err)
		}
	}

	reopened, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := reopened.Get(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.LastEventID != 5 || reloaded.State != StateReady {
		t.Fatalf("unexpected reloaded projection: %+v", reloaded)
	}
	events, err := reopened.EventsAfter(created.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 5 || events[1].Type != "turn.started" || events[4].Type != "turn.completed" {
		t.Fatalf("unexpected events: %+v", events)
	}
}

func TestStoreRepairsPartialTailAndRebuildsSnapshot(t *testing.T) {
	root := t.TempDir()
	store, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	created, err := store.Create(CreateInput{Title: "Recover", Cwd: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	eventPath := filepath.Join(root, created.ID, "events.jsonl")
	file, err := os.OpenFile(eventPath, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(`{"id":2,"type":"turn.started"`); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, created.ID, "session.json")); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	value, err := reopened.Get(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if value.LastEventID != 1 || value.State != StateReady {
		t.Fatalf("unexpected rebuilt snapshot: %+v", value)
	}
	data, err := os.ReadFile(eventPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 || data[len(data)-1] != '\n' {
		t.Fatalf("event log was not repaired: %q", data)
	}
}

func TestStoreRejectsCorruptCompleteRecord(t *testing.T) {
	root := t.TempDir()
	store, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	created, err := store.Create(CreateInput{Title: "Corrupt", Cwd: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	eventPath := filepath.Join(root, created.ID, "events.jsonl")
	file, err := os.OpenFile(eventPath, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("not-json\n"); err != nil {
		t.Fatal(err)
	}
	_ = file.Close()

	if _, err := Open(root); err == nil {
		t.Fatal("expected a corrupt complete event record to fail")
	}
}

func TestSubscribeReceivesNewEvents(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	created, err := store.Create(CreateInput{Title: "Stream", Cwd: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	events, cancel, err := store.Subscribe(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	if _, err := store.Append(created.ID, "session.state", "", mustJSON(t, StateEventData{State: StateStopped})); err != nil {
		t.Fatal(err)
	}
	event := <-events
	if event.ID != 2 || event.Type != "session.state" {
		t.Fatalf("unexpected live event: %+v", event)
	}
}

func TestProviderMappingIsProjectedAndRebuilt(t *testing.T) {
	root := t.TempDir()
	store, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	created, err := store.Create(CreateInput{Title: "Mapped", Cwd: t.TempDir(), AgentID: "codex-fast"})
	if err != nil {
		t.Fatal(err)
	}
	data := ProviderEventData{
		AgentID: "codex-fast", Provider: "codex", ProviderSessionID: "native-1",
	}
	if _, err := store.Append(created.ID, "session.provider", "", mustJSON(t, data)); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	value, err := reopened.Get(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if value.AgentID != "codex-fast" || value.ProviderSessionID != "native-1" {
		t.Fatalf("unexpected projection: %+v", value)
	}
}

// Events written by the profile-routing era carry selection objects inside
// session.created and session.provider payloads. Replaying them must keep
// working: unknown fields are ignored and the agent mapping recorded by the
// session.provider event is preserved, so the session stays resumable.
func TestLegacySelectionEventsStillReplay(t *testing.T) {
	root := t.TempDir()
	id := "ses_legacy"
	dir := filepath.Join(root, id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	created, err := json.Marshal(map[string]any{
		"id": id, "title": "Legacy", "cwd": t.TempDir(), "state": StateReady,
		"selection":   map[string]any{"mode": "auto", "requestedTags": []string{"fast"}},
		"lastEventId": 1, "createdAt": "2026-01-01T00:00:00Z", "updatedAt": "2026-01-01T00:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	provider, err := json.Marshal(map[string]any{
		"agentId": "codex-fast", "provider": "codex", "providerSessionId": "native-9",
		"selection": map[string]any{"mode": "auto", "requestedTags": []string{"fast"}, "reason": "matched profile fast"},
	})
	if err != nil {
		t.Fatal(err)
	}
	log := ""
	log += fmt.Sprintf("%s\n", mustJSON(t, Event{ID: 1, Time: time.Now().UTC(), Type: "session.created", SessionID: id, Data: created}))
	log += fmt.Sprintf("%s\n", mustJSON(t, Event{ID: 2, Time: time.Now().UTC(), Type: "session.provider", SessionID: id, Data: provider}))
	if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte(log), 0o600); err != nil {
		t.Fatal(err)
	}

	store, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	value, err := store.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if value.AgentID != "codex-fast" || value.Provider != "codex" || value.ProviderSessionID != "native-9" {
		t.Fatalf("legacy agent mapping was not preserved: %+v", value)
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
