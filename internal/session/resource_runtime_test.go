package session

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestCreateOrGetIsDurablyIdempotent(t *testing.T) {
	root := t.TempDir()
	store, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	input := CreateInput{
		Title: "Resource generation", Cwd: t.TempDir(), AgentName: "Codex",
		IdempotencyKey: "generation-01H", Provider: "codex",
		InputCapabilities: InputCapabilities{Steer: true},
		Source: &Source{App: "forge", InstanceID: "workspace-1", ExternalID: "project1.task2/1", Metadata: map[string]string{
			"resourceId": "project1.task2", "generationId": "generation-01H",
		}},
	}
	first, created, err := store.CreateOrGet(input)
	if err != nil || !created {
		t.Fatalf("first create = %+v, %v, %v", first, created, err)
	}
	second, created, err := store.CreateOrGet(input)
	if err != nil || created || second.ID != first.ID {
		t.Fatalf("retry = %+v, %v, %v; want %s", second, created, err, first.ID)
	}
	conflict := input
	conflict.AgentName = "Pi"
	if _, _, err := store.CreateOrGet(conflict); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("conflict error = %v", err)
	}
	reopened, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	third, created, err := reopened.CreateOrGet(input)
	if err != nil || created || third.ID != first.ID || third.Source.Metadata["resourceId"] != "project1.task2" {
		t.Fatalf("restart retry = %+v, %v, %v", third, created, err)
	}
}

func TestTurnIndexUsesStableEventReferencesAndArchivedHistory(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	created, err := store.Create(CreateInput{Cwd: t.TempDir(), AgentName: "Agent"})
	if err != nil {
		t.Fatal(err)
	}
	appendEvent := func(eventType, turnID string, value any) {
		t.Helper()
		var data []byte
		if value != nil {
			data, _ = json.Marshal(value)
		}
		if _, err := store.Append(created.ID, eventType, turnID, data); err != nil {
			t.Fatal(err)
		}
	}
	appendEvent(EventMessageInput, "turn_1", MessageInput{Text: "first request", Role: MessageRoleUser, MessageID: "msg_1"})
	appendEvent("turn.started", "turn_1", nil)
	appendEvent("tool.event", "turn_1", map[string]any{"method": "read"})
	appendEvent("message.assistant.delta", "turn_1", map[string]any{"text": "first reply"})
	appendEvent(EventTurnCompleted, "turn_1", nil)
	appendEvent(EventMessageInput, "turn_2", MessageInput{Text: "second request", Role: MessageRoleAgent})
	appendEvent("turn.started", "turn_2", nil)

	firstPage, err := store.TurnsPage(created.ID, 0, 0, false, 1)
	if err != nil || len(firstPage.Turns) != 1 || !firstPage.HasMore {
		t.Fatalf("first page = %+v, %v", firstPage, err)
	}
	first := firstPage.Turns[0]
	if first.ID != "turn_1" || first.Status != "completed" || first.TriggerEventID == 0 || first.FinalReplyEventID == 0 || first.ToolEventCount != 1 {
		t.Fatalf("first summary = %+v", first)
	}
	secondPage, err := store.TurnsPage(created.ID, firstPage.NextAfter, 0, false, 1)
	if err != nil || len(secondPage.Turns) != 1 || secondPage.Turns[0].ID != "turn_2" || secondPage.Turns[0].Status != "active" {
		t.Fatalf("second page = %+v, %v", secondPage, err)
	}
	if accepted, err := store.HasMessageID(created.ID, "msg_1"); err != nil || !accepted {
		t.Fatalf("message id lookup = %v, %v", accepted, err)
	}
	appendEvent(EventTurnCancelled, "turn_2", TurnTerminalEventData{Reason: "stop"})
	appendEvent("session.state", "", StateEventData{State: StateStopped})
	if _, err := store.Archive(created.ID); err != nil {
		t.Fatal(err)
	}
	latest, err := store.TurnsPage(created.ID, 0, 0, true, 1)
	if err != nil || len(latest.Turns) != 1 || latest.Turns[0].ID != "turn_2" || latest.Turns[0].Status != "cancelled" {
		t.Fatalf("archived latest = %+v, %v", latest, err)
	}
}
