package session

import (
	"encoding/json"
	"errors"
	"math"
	"testing"
)

// seedBackwardEvents appends n plain events after the session.created event,
// so the durable head is n+1.
func seedBackwardEvents(t *testing.T, store *Store, id string, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		if _, err := store.Append(id, "test.event", "", nil); err != nil {
			t.Fatal(err)
		}
	}
}

func eventIDs(events []Event) []int64 {
	ids := make([]int64, 0, len(events))
	for _, event := range events {
		ids = append(ids, event.ID)
	}
	return ids
}

func assertIDs(t *testing.T, events []Event, want ...int64) {
	t.Helper()
	got := eventIDs(events)
	if len(got) != len(want) {
		t.Fatalf("ids = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ids = %v, want %v", got, want)
		}
	}
}

func TestEventsPageBeforeReadsTailAscending(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	created, err := store.Create(CreateInput{Title: "Tail", Cwd: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	seedBackwardEvents(t, store, created.ID, 10) // head = 11

	// latest=true arrives at the store as the maximum cursor; it must equal
	// before=head+1.
	for _, before := range []int64{math.MaxInt64, 12} {
		page, err := store.EventsPageBefore(created.ID, before, 3)
		if err != nil {
			t.Fatal(err)
		}
		assertIDs(t, page.Events, 9, 10, 11)
		if page.Before != 12 || page.NextBefore != 9 || !page.HasMoreBefore {
			t.Fatalf("unexpected backward page metadata: %+v", page)
		}
		if page.Limit != 3 || page.LatestCursor != 11 {
			t.Fatalf("unexpected page: %+v", page)
		}
		// Forward metadata stays valid so the tail page can hand NextAfter
		// to a live stream.
		if page.NextAfter != 11 || page.HasMore {
			t.Fatalf("tail page must not report forward progress: %+v", page)
		}
	}
}

func TestEventsPageBeforeWalksEntireHistory(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	created, err := store.Create(CreateInput{Title: "Walk", Cwd: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	seedBackwardEvents(t, store, created.ID, 24) // head = 25

	var walked []int64
	before := int64(math.MaxInt64)
	pages := 0
	for {
		page, err := store.EventsPageBefore(created.ID, before, 10)
		if err != nil {
			t.Fatal(err)
		}
		pages++
		// Prepend: walked must end up in ascending order with no gaps or
		// duplicates.
		walked = append(eventIDs(page.Events), walked...)
		if !page.HasMoreBefore {
			break
		}
		before = page.NextBefore
		if pages > 10 {
			t.Fatal("backward pagination did not terminate")
		}
	}
	want := make([]int64, 0, 25)
	for id := int64(1); id <= 25; id++ {
		want = append(want, id)
	}
	if len(walked) != len(want) {
		t.Fatalf("walked ids = %v, want %v", walked, want)
	}
	for i := range want {
		if walked[i] != want[i] {
			t.Fatalf("walked ids = %v, want %v", walked, want)
		}
	}
	if pages != 3 {
		t.Fatalf("pages = %d, want 3 (10 + 10 + 5)", pages)
	}
}

func TestEventsPageBeforeClampsAheadCursor(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	created, err := store.Create(CreateInput{Title: "Clamp", Cwd: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	seedBackwardEvents(t, store, created.ID, 4) // head = 5

	page, err := store.EventsPageBefore(created.ID, 1000, 2)
	if err != nil {
		t.Fatal(err)
	}
	assertIDs(t, page.Events, 4, 5)
	if page.Before != 6 {
		t.Fatalf("before must clamp to head+1 = 6, got %d", page.Before)
	}
	if page.NextBefore != 4 || !page.HasMoreBefore {
		t.Fatalf("unexpected backward page metadata: %+v", page)
	}
}

func TestEventsPageBeforeAtLogStart(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	created, err := store.Create(CreateInput{Title: "Start", Cwd: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	seedBackwardEvents(t, store, created.ID, 2)

	page, err := store.EventsPageBefore(created.ID, 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Events) != 0 {
		t.Fatalf("before=1 must be empty, got %v", eventIDs(page.Events))
	}
	if page.Before != 1 || page.NextBefore != 0 || page.HasMoreBefore {
		t.Fatalf("unexpected empty page metadata: %+v", page)
	}
	// Forward metadata still describes the log: newer events exist after
	// cursor 0.
	if !page.HasMore || page.NextAfter != 0 {
		t.Fatalf("unexpected forward metadata on empty page: %+v", page)
	}

	// A page ending exactly at the log start has no older events.
	page, err = store.EventsPageBefore(created.ID, 3, 10)
	if err != nil {
		t.Fatal(err)
	}
	assertIDs(t, page.Events, 1, 2)
	if page.NextBefore != 1 || page.HasMoreBefore {
		t.Fatalf("page covering the log start must end backward pagination: %+v", page)
	}
}

func TestEventsPageBeforeNormalizesLimit(t *testing.T) {
	root := t.TempDir()
	store, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	created, err := store.Create(CreateInput{Title: "Limit", Cwd: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	events := make([]Event, 0, 1100)
	for id := int64(2); id <= 1101; id++ {
		events = append(events, Event{ID: id, Type: "test.event"})
	}
	appendRawEvents(t, root, created.ID, events...)
	store = reopen(t, store, root)

	page, err := store.EventsPageBefore(created.ID, math.MaxInt64, 0)
	if err != nil {
		t.Fatal(err)
	}
	if page.Limit != DefaultEventPageSize || len(page.Events) != DefaultEventPageSize {
		t.Fatalf("default limit: limit=%d events=%d", page.Limit, len(page.Events))
	}
	assertIDs(t, page.Events[:1], 1101-DefaultEventPageSize+1)
	if page.Events[len(page.Events)-1].ID != 1101 {
		t.Fatalf("default page must end at the head: %+v", page.Events[len(page.Events)-1])
	}

	page, err = store.EventsPageBefore(created.ID, math.MaxInt64, MaxEventPageSize+5000)
	if err != nil {
		t.Fatal(err)
	}
	if page.Limit != MaxEventPageSize || len(page.Events) != MaxEventPageSize {
		t.Fatalf("clamped limit: limit=%d events=%d", page.Limit, len(page.Events))
	}
	if page.Events[0].ID != 1101-MaxEventPageSize+1 || page.Events[len(page.Events)-1].ID != 1101 {
		t.Fatalf("max page window wrong: %d..%d", page.Events[0].ID, page.Events[len(page.Events)-1].ID)
	}
}

func TestEventsPageBeforeUnknownSession(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.EventsPageBefore("ses_missing", 10, 10); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestEventsPageBeforeLazyLoadsSnapshotEvents(t *testing.T) {
	root := t.TempDir()
	store, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	created, err := store.Create(CreateInput{Title: "Lazy", Cwd: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	seedBackwardEvents(t, store, created.ID, 9) // head = 10

	reopened := reopen(t, store, root)
	state := reopened.sessions[created.ID]
	if state == nil {
		t.Fatal("session missing after reopen")
	}
	if state.eventsLoaded {
		t.Fatal("session should open from its trusted snapshot without events")
	}
	page, err := reopened.EventsPageBefore(created.ID, math.MaxInt64, 4)
	if err != nil {
		t.Fatal(err)
	}
	assertIDs(t, page.Events, 7, 8, 9, 10)
	if page.Before != 11 || page.NextBefore != 7 || !page.HasMoreBefore {
		t.Fatalf("unexpected lazy backward page: %+v", page)
	}
}

func TestEventsPageBeforeServesMergedDelta(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	created := newDeltaSession(t, store)
	if _, err := store.Append(created.ID, "message.assistant.delta", "turn_1", mustJSON(t, deltaData("Hello", "item/agentMessage/delta"))); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Append(created.ID, "message.assistant.delta", "turn_1", mustJSON(t, deltaData(", world", "item/agentMessage/delta"))); err != nil {
		t.Fatal(err)
	}

	page, err := store.EventsPageBefore(created.ID, math.MaxInt64, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Events) != 1 {
		t.Fatalf("tail page = %v, want the merged delta event", eventIDs(page.Events))
	}
	if text := deltaTextOf(t, page.Events[0]); text != "Hello, world" {
		t.Fatalf("backward page must serve the full merged event, got %q", text)
	}
	var data map[string]any
	if err := json.Unmarshal(page.Events[0].Data, &data); err != nil {
		t.Fatal(err)
	}
	if _, patched := data["append"]; patched {
		t.Fatal("history pages must never carry the live append patch flag")
	}
}
