package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func deltaData(text, method string) map[string]any {
	return map[string]any{"text": text, "method": method}
}

func deltaTextOf(t *testing.T, event Event) string {
	t.Helper()
	var data map[string]any
	if err := json.Unmarshal(event.Data, &data); err != nil {
		t.Fatal(err)
	}
	text, _ := data["text"].(string)
	return text
}

func newDeltaSession(t *testing.T, store *Store) Session {
	t.Helper()
	created, err := store.Create(CreateInput{Title: "Deltas", Cwd: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Append(created.ID, "turn.started", "turn_1", nil); err != nil {
		t.Fatal(err)
	}
	return created
}

func TestAppendMergesConsecutiveAssistantDeltas(t *testing.T) {
	root := t.TempDir()
	store, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	created := newDeltaSession(t, store)

	first, err := store.Append(created.ID, "message.assistant.delta", "turn_1", mustJSON(t, deltaData("Hello", "item/agentMessage/delta")))
	if err != nil {
		t.Fatal(err)
	}
	updated, err := store.Get(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{", ", "world", "!"} {
		merged, err := store.Append(created.ID, "message.assistant.delta", "turn_1", mustJSON(t, deltaData(fragment, "item/agentMessage/delta")))
		if err != nil {
			t.Fatal(err)
		}
		if merged.ID != first.ID {
			t.Fatalf("merged delta id = %d, want replacement of %d", merged.ID, first.ID)
		}
		next, err := store.Get(created.ID)
		if err != nil {
			t.Fatal(err)
		}
		if next.LastEventID != first.ID {
			t.Fatalf("merge must not consume a durable id: LastEventID = %d, want %d", next.LastEventID, first.ID)
		}
		if next.UpdatedAt.Before(updated.UpdatedAt) {
			t.Fatal("merged delta must keep session UpdatedAt moving forward")
		}
		updated = next
	}

	events, err := store.EventsAfter(created.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 {
		t.Fatalf("durable events = %d, want session.created + turn.started + one merged delta, got %+v", len(events), events)
	}
	merged := events[2]
	if merged.ID != first.ID || merged.Type != "message.assistant.delta" || merged.TurnID != "turn_1" {
		t.Fatalf("unexpected merged event: %+v", merged)
	}
	if merged.StartTime == nil || !merged.StartTime.Equal(first.Time) {
		t.Fatalf("merged start time = %v, want first fragment time %v", merged.StartTime, first.Time)
	}
	if text := deltaTextOf(t, merged); text != "Hello, world!" {
		t.Fatalf("merged text = %q, want %q", text, "Hello, world!")
	}

	// The durable log must contain exactly the merged line and survive a
	// reload with the same projection.
	raw, err := os.ReadFile(filepath.Join(root, created.ID, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if lines := strings.Count(string(raw), "\n"); lines != 3 {
		t.Fatalf("events.jsonl lines = %d, want 3", lines)
	}
	reopened, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := reopened.EventsAfter(created.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(replayed) != 3 || deltaTextOf(t, replayed[2]) != "Hello, world!" {
		t.Fatalf("replayed events = %+v", replayed)
	}
	if replayed[2].StartTime == nil || !replayed[2].StartTime.Equal(first.Time) {
		t.Fatalf("replayed start time = %v, want first fragment time %v", replayed[2].StartTime, first.Time)
	}
}

func TestAppendMergesReasoningDeltas(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	created := newDeltaSession(t, store)
	if _, err := store.Append(created.ID, "message.reasoning.delta", "turn_1", mustJSON(t, deltaData("think", "thinking_delta"))); err != nil {
		t.Fatal(err)
	}
	merged, err := store.Append(created.ID, "message.reasoning.delta", "turn_1", mustJSON(t, deltaData("ing", "thinking_delta")))
	if err != nil {
		t.Fatal(err)
	}
	if text := deltaTextOf(t, merged); text != "thinking" {
		t.Fatalf("merged reasoning text = %q, want %q", text, "thinking")
	}
}

func TestDeltaMergeBoundaries(t *testing.T) {
	cases := []struct {
		name     string
		lastType string
		lastData map[string]any
		nextType string
		nextTurn string
		nextData map[string]any
	}{
		{
			name:     "different delta kind",
			lastType: "message.assistant.delta",
			lastData: deltaData("answer", "item/agentMessage/delta"),
			nextType: "message.reasoning.delta",
			nextTurn: "turn_1",
			nextData: deltaData("think", "item/reasoning/textDelta"),
		},
		{
			name:     "different turn",
			lastType: "message.assistant.delta",
			lastData: deltaData("answer", "item/agentMessage/delta"),
			nextType: "message.assistant.delta",
			nextTurn: "turn_2",
			nextData: deltaData("more", "item/agentMessage/delta"),
		},
		{
			name:     "different provider method",
			lastType: "message.reasoning.delta",
			lastData: deltaData("summary", "item/reasoning/summaryTextDelta"),
			nextType: "message.reasoning.delta",
			nextTurn: "turn_1",
			nextData: deltaData("full", "item/reasoning/textDelta"),
		},
		{
			name:     "payload shape differs",
			lastType: "message.assistant.delta",
			lastData: deltaData("answer", "item/agentMessage/delta"),
			nextType: "message.assistant.delta",
			nextTurn: "turn_1",
			nextData: map[string]any{"text": "more", "method": "item/agentMessage/delta", "extra": true},
		},
		{
			name:     "text is not a string",
			lastType: "message.assistant.delta",
			lastData: deltaData("answer", "item/agentMessage/delta"),
			nextType: "message.assistant.delta",
			nextTurn: "turn_1",
			nextData: map[string]any{"text": 42, "method": "item/agentMessage/delta"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store, err := Open(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			created := newDeltaSession(t, store)
			last, err := store.Append(created.ID, tc.lastType, "turn_1", mustJSON(t, tc.lastData))
			if err != nil {
				t.Fatal(err)
			}
			next, err := store.Append(created.ID, tc.nextType, tc.nextTurn, mustJSON(t, tc.nextData))
			if err != nil {
				t.Fatal(err)
			}
			if next.ID != last.ID+1 {
				t.Fatalf("non-mergeable delta id = %d, want new event %d", next.ID, last.ID+1)
			}
			if text := deltaTextOf(t, next); text != mustText(t, tc.nextData) {
				t.Fatalf("non-merged event text = %q, want %q", text, mustText(t, tc.nextData))
			}
		})
	}
}

func mustText(t *testing.T, data map[string]any) string {
	t.Helper()
	text, _ := data["text"].(string)
	return text
}

func TestDeltaAfterNonDeltaEventDoesNotMerge(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	created := newDeltaSession(t, store)
	if _, err := store.Append(created.ID, "message.assistant.delta", "turn_1", mustJSON(t, deltaData("first", "item/agentMessage/delta"))); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Append(created.ID, "tool.event", "turn_1", mustJSON(t, map[string]any{"method": "item/started"})); err != nil {
		t.Fatal(err)
	}
	next, err := store.Append(created.ID, "message.assistant.delta", "turn_1", mustJSON(t, deltaData("second", "item/agentMessage/delta")))
	if err != nil {
		t.Fatal(err)
	}
	if next.ID != 5 {
		t.Fatalf("delta after tool.event id = %d, want new event 5", next.ID)
	}
}

func TestDeltaMergeSurvivesReopen(t *testing.T) {
	root := t.TempDir()
	store, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	created := newDeltaSession(t, store)
	if _, err := store.Append(created.ID, "message.assistant.delta", "turn_1", mustJSON(t, deltaData("Hello", "item/agentMessage/delta"))); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	merged, err := reopened.Append(created.ID, "message.assistant.delta", "turn_1", mustJSON(t, deltaData(" again", "item/agentMessage/delta")))
	if err != nil {
		t.Fatal(err)
	}
	if merged.ID != 3 {
		t.Fatalf("merged delta id after reopen = %d, want 3", merged.ID)
	}
	if text := deltaTextOf(t, merged); text != "Hello again" {
		t.Fatalf("merged text after reopen = %q, want %q", text, "Hello again")
	}
	events, err := reopened.EventsAfter(created.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 {
		t.Fatalf("durable events after reopen = %d, want 3", len(events))
	}
}

func TestDeltaMergeStopsAtAccumulatedSizeCap(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	created := newDeltaSession(t, store)
	chunk := strings.Repeat("x", maxMergedDeltaEventBytes)
	first, err := store.Append(created.ID, "message.assistant.delta", "turn_1", mustJSON(t, deltaData(chunk, "item/agentMessage/delta")))
	if err != nil {
		t.Fatal(err)
	}
	next, err := store.Append(created.ID, "message.assistant.delta", "turn_1", mustJSON(t, deltaData(chunk, "item/agentMessage/delta")))
	if err != nil {
		t.Fatal(err)
	}
	if next.ID != first.ID+1 {
		t.Fatalf("oversized delta id = %d, want new event %d", next.ID, first.ID+1)
	}
	events, err := store.EventsAfter(created.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 4 {
		t.Fatalf("durable events = %d, want 4", len(events))
	}
}

func TestDeltaMergePublishesAppendPatch(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	created := newDeltaSession(t, store)
	subscription, _, err := store.Subscribe(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer subscription.Cancel()

	first, err := store.Append(created.ID, "message.assistant.delta", "turn_1", mustJSON(t, deltaData("Hello", "item/agentMessage/delta")))
	if err != nil {
		t.Fatal(err)
	}
	if event := <-subscription.Events(); event.ID != first.ID {
		t.Fatalf("first live delta = %+v", event)
	}
	merged, err := store.Append(created.ID, "message.assistant.delta", "turn_1", mustJSON(t, deltaData("!", "item/agentMessage/delta")))
	if err != nil {
		t.Fatal(err)
	}
	// The stored event accumulates the full text...
	if text := deltaTextOf(t, merged); text != "Hello!" {
		t.Fatalf("stored merged text = %q, want %q", text, "Hello!")
	}
	// ...while live subscribers receive only the fragment as an append
	// patch, so steady-state traffic stays proportional to the fragments.
	patch := <-subscription.Events()
	if patch.ID != first.ID {
		t.Fatalf("patch id = %d, want %d", patch.ID, first.ID)
	}
	var patchData map[string]any
	if err := json.Unmarshal(patch.Data, &patchData); err != nil {
		t.Fatal(err)
	}
	if patchData["append"] != true {
		t.Fatalf("patch missing append flag: %+v", patchData)
	}
	if patchData["text"] != "!" {
		t.Fatalf("patch text = %q, want only the new fragment", patchData["text"])
	}
	if patchData["method"] != "item/agentMessage/delta" {
		t.Fatalf("patch method = %q", patchData["method"])
	}
	if patch.StartTime == nil || !patch.StartTime.Equal(first.Time) {
		t.Fatalf("append patch start time = %v, want first fragment time %v", patch.StartTime, first.Time)
	}
}

func TestDeltaMergeFallsBackWhenTailMismatch(t *testing.T) {
	root := t.TempDir()
	store, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	created := newDeltaSession(t, store)
	if _, err := store.Append(created.ID, "message.assistant.delta", "turn_1", mustJSON(t, deltaData("Hello", "item/agentMessage/delta"))); err != nil {
		t.Fatal(err)
	}

	// Corrupt the durable tail behind the store's back: the rewrite
	// verification must refuse the merge and fall back to a plain append so
	// the log never loses the new fragment.
	path := filepath.Join(root, created.ID, "events.jsonl")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	corrupted := strings.Replace(string(raw), "Hello", "Jello", 1)
	if corrupted == string(raw) {
		t.Fatal("test setup failed to alter the tail line")
	}
	if err := os.WriteFile(path, []byte(corrupted), 0o600); err != nil {
		t.Fatal(err)
	}

	next, err := store.Append(created.ID, "message.assistant.delta", "turn_1", mustJSON(t, deltaData("!", "item/agentMessage/delta")))
	if err != nil {
		t.Fatal(err)
	}
	if next.ID != 4 {
		t.Fatalf("fallback append id = %d, want new event 4", next.ID)
	}
	reopened, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	events, err := reopened.EventsAfter(created.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 4 || deltaTextOf(t, events[2]) != "Jello" || deltaTextOf(t, events[3]) != "!" {
		t.Fatalf("events after fallback = %+v", events)
	}
}
