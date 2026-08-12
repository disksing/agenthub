package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func appendProjectionEvent(t *testing.T, store *Store, id, eventType, turnID string, data any) Event {
	t.Helper()
	var raw []byte
	if data != nil {
		var err error
		raw, err = json.Marshal(data)
		if err != nil {
			t.Fatal(err)
		}
	}
	event, err := store.Append(id, eventType, turnID, raw)
	if err != nil {
		t.Fatal(err)
	}
	return event
}

func TestClosedTurnProjectionPreservesMessagesAndCollapsesDetailRanges(t *testing.T) {
	root := t.TempDir()
	store, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	created, err := store.Create(CreateInput{Cwd: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	turnID := "turn_projection"
	input := appendProjectionEvent(t, store, created.ID, EventMessageInput, turnID, MessageInput{
		Text: "first", Role: MessageRoleUser, MessageID: "message-1",
	})
	started := appendProjectionEvent(t, store, created.ID, "turn.started", turnID, nil)
	appendProjectionEvent(t, store, created.ID, "message.reasoning.delta", turnID, map[string]any{"text": "one", "method": "first"})
	appendProjectionEvent(t, store, created.ID, "message.reasoning.delta", turnID, map[string]any{"text": "two", "method": "second"})
	appendProjectionEvent(t, store, created.ID, "provider.metadata", turnID, map[string]any{"noise": true})
	appendProjectionEvent(t, store, created.ID, EventMessageInput, turnID, MessageInput{
		Text: "steer", Role: MessageRoleAgent, Steer: true,
		Sender: &MessageSender{Name: "planner"},
	})
	appendProjectionEvent(t, store, created.ID, "tool.event", turnID, map[string]any{"raw": map[string]any{"item": map[string]any{"id": "call-1"}}})
	appendProjectionEvent(t, store, created.ID, "tool.event", turnID, map[string]any{"raw": map[string]any{"itemId": "call-1"}})
	appendProjectionEvent(t, store, created.ID, "tool.event", turnID, map[string]any{"toolCallId": "call-2"})
	appendProjectionEvent(t, store, created.ID, "approval.requested", turnID, map[string]any{"approvalId": "approval-1", "question": "Continue?"})
	appendProjectionEvent(t, store, created.ID, "provider.error", turnID, map[string]any{"message": "retrying"})
	appendProjectionEvent(t, store, created.ID, "future.visible", turnID, map[string]any{"label": "future"})
	appendProjectionEvent(t, store, created.ID, "message.assistant.delta", turnID, map[string]any{"text": "final "})
	appendProjectionEvent(t, store, created.ID, "message.assistant.delta", turnID, map[string]any{"text": "answer"})
	terminal := appendProjectionEvent(t, store, created.ID, EventTurnCompleted, turnID, map[string]any{})

	page, err := store.TurnsPage(created.ID, 0, 0, true, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Turns) != 1 || page.LatestEventID != terminal.ID {
		t.Fatalf("turn page = %+v", page)
	}
	turn := page.Turns[0]
	if turn.StartEventID != input.ID || turn.FirstEventID != input.ID || turn.TurnStartedEventID != started.ID ||
		turn.EndEventID != terminal.ID || !turn.Closed || turn.Status != "completed" {
		t.Fatalf("turn boundaries = %+v", turn)
	}
	var messages []TurnItem
	var thinking, tools *TurnItem
	var unknown *TurnItem
	for index := range turn.Items {
		item := &turn.Items[index]
		switch item.Type {
		case "message":
			messages = append(messages, *item)
		case "thinking":
			thinking = item
		case "tool":
			tools = item
		case "unknown":
			unknown = item
		}
	}
	if len(messages) != 3 || messages[0].Text != "first" || messages[1].Text != "steer" ||
		messages[1].Role != MessageRoleAgent || messages[1].Sender == nil || messages[1].Sender.Name != "planner" ||
		messages[2].Text != "final answer" {
		t.Fatalf("message items = %+v", messages)
	}
	if thinking == nil || thinking.Count != 2 || thinking.Text != "" || thinking.StartEventID == thinking.EndEventID {
		t.Fatalf("thinking item = %+v", thinking)
	}
	if tools == nil || tools.Count != 2 || tools.StartEventID == tools.EndEventID {
		t.Fatalf("tool item = %+v", tools)
	}
	if unknown == nil || unknown.Text != "future.visible" || !strings.Contains(string(unknown.Data), "future") {
		t.Fatalf("unknown item = %+v", unknown)
	}
	if _, err := os.Stat(filepath.Join(root, created.ID, "turns.jsonl")); err != nil {
		t.Fatal(err)
	}
}

func TestClosedTurnPageUsesMaterializedFileWithoutLoadingEvents(t *testing.T) {
	root := t.TempDir()
	store, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	created, err := store.Create(CreateInput{Cwd: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	appendProjectionEvent(t, store, created.ID, EventMessageInput, "turn_fast", MessageInput{Text: "hello", Role: MessageRoleUser})
	appendProjectionEvent(t, store, created.ID, "turn.started", "turn_fast", nil)
	appendProjectionEvent(t, store, created.ID, EventTurnCompleted, "turn_fast", map[string]any{})

	reopened, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	state := reopened.sessions[created.ID]
	if state.eventsLoaded {
		t.Fatal("trusted Session snapshot unexpectedly loaded Events")
	}
	page, err := reopened.TurnsPage(created.ID, 0, 0, true, 1)
	if err != nil || len(page.Turns) != 1 {
		t.Fatalf("page = %+v, err=%v", page, err)
	}
	if state.eventsLoaded {
		t.Fatal("closed materialized Turn query scanned events.jsonl")
	}
}

func TestAssistantProjectionKeepsLogicalVisibleBoundaries(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	created, err := store.Create(CreateInput{Cwd: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	turnID := "turn_boundaries"
	appendProjectionEvent(t, store, created.ID, EventMessageInput, turnID, MessageInput{Text: "go", Role: MessageRoleUser})
	appendProjectionEvent(t, store, created.ID, "turn.started", turnID, nil)
	appendProjectionEvent(t, store, created.ID, "message.assistant.delta", turnID, map[string]any{"text": "one"})
	appendProjectionEvent(t, store, created.ID, "provider.metadata", turnID, map[string]any{"noise": true})
	appendProjectionEvent(t, store, created.ID, "message.assistant.delta", turnID, map[string]any{"text": "two"})
	appendProjectionEvent(t, store, created.ID, "message.reasoning.delta", turnID, map[string]any{"text": "boundary"})
	appendProjectionEvent(t, store, created.ID, "message.assistant.delta", turnID, map[string]any{"text": "three"})
	appendProjectionEvent(t, store, created.ID, EventTurnCompleted, turnID, map[string]any{})

	turn, err := store.Turn(created.ID, turnID)
	if err != nil {
		t.Fatal(err)
	}
	var replies []string
	for _, item := range turn.Items {
		if item.Type == "message" && item.Role == MessageRoleAssistant {
			replies = append(replies, item.Text)
		}
	}
	if len(replies) != 2 || replies[0] != "onetwo" || replies[1] != "three" {
		t.Fatalf("assistant logical messages = %#v", replies)
	}
}

func TestTurnProjectionRebuildsAfterDeletionAndTerminalCrashWindow(t *testing.T) {
	root := t.TempDir()
	store, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	created, err := store.Create(CreateInput{Cwd: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	appendProjectionEvent(t, store, created.ID, EventMessageInput, "turn_rebuild", MessageInput{Text: "hello", Role: MessageRoleUser})
	appendProjectionEvent(t, store, created.ID, "turn.started", "turn_rebuild", nil)
	appendRawEvent(t, root, created.ID, Event{ID: 4, Type: EventTurnFailed, TurnID: "turn_rebuild", Data: json.RawMessage(`{"error":"boom"}`)})

	reopened, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	page, err := reopened.TurnsPage(created.ID, 0, 0, true, 10)
	if err != nil || len(page.Turns) != 1 || !page.Turns[0].Closed || page.Turns[0].Status != "failed" {
		t.Fatalf("crash-window repair page = %+v, err=%v", page, err)
	}
	projection := filepath.Join(root, created.ID, "turns.jsonl")
	if err := os.Remove(projection); err != nil {
		t.Fatal(err)
	}
	page, err = reopened.TurnsPage(created.ID, 0, 0, true, 10)
	if err != nil || len(page.Turns) != 1 || page.Turns[0].EndEventID != 4 {
		t.Fatalf("deleted projection repair page = %+v, err=%v", page, err)
	}
}

func TestArchivedSessionTurnProjectionRemainsReadable(t *testing.T) {
	root := t.TempDir()
	store, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	created, err := store.Create(CreateInput{Cwd: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	appendProjectionEvent(t, store, created.ID, EventMessageInput, "turn_archive", MessageInput{Text: "hello", Role: MessageRoleUser})
	appendProjectionEvent(t, store, created.ID, "turn.started", "turn_archive", nil)
	appendProjectionEvent(t, store, created.ID, EventTurnCompleted, "turn_archive", map[string]any{})
	appendProjectionEvent(t, store, created.ID, "session.state", "", StateEventData{State: StateStopped, Reason: StopReasonRequested})
	if _, err := store.Archive(created.ID); err != nil {
		t.Fatal(err)
	}
	turn, err := store.Turn(created.ID, "turn_archive")
	if err != nil || !turn.Closed {
		t.Fatalf("archived turn = %+v, err=%v", turn, err)
	}
}
