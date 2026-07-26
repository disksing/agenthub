package client

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/disksing/agenthub/internal/session"
)

func TestEventsAfterPagesToInitialDurableHead(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
		latest := int64(2500)
		end := min(after+1000, latest)
		events := make([]session.Event, 0, end-after)
		for id := after + 1; id <= end; id++ {
			events = append(events, session.Event{ID: id, Type: "provider.test"})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"events":       events,
			"latestCursor": latest,
			"page": map[string]any{
				"after": after, "limit": 1000, "nextAfter": end, "hasMore": end < latest,
			},
		})
	}))
	defer server.Close()
	events, err := New(server.URL).EventsAfter("ses_test", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2500 || events[0].ID != 1 || events[2499].ID != 2500 {
		t.Fatalf("events = %d (%d..%d)", len(events), events[0].ID, events[len(events)-1].ID)
	}
}

func TestEventsAfterStopsOnCursorGap(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"events":       []session.Event{{ID: 1}, {ID: 3}},
			"latestCursor": 3,
			"page":         map[string]any{"after": 0, "limit": 1000, "nextAfter": 3, "hasMore": false},
		})
	}))
	defer server.Close()
	_, err := New(server.URL).EventsAfter("ses_test", 0)
	var gap *EventCursorGapError
	if !errors.As(err, &gap) || gap.Expected != 2 || gap.Got != 3 {
		t.Fatalf("gap error = %#v", err)
	}
}
