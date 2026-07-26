package client

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/disksing/agenthub/internal/session"
)

func TestRequireCapabilitiesRejectsOldAndIncompleteDaemons(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "old daemon without negotiation fields",
			body: `{"version":"0.1.0"}`,
			want: `incompatible AgentHub API version ""`,
		},
		{
			name: "missing capability",
			body: `{"apiVersion":"1","capabilities":["session.source"]}`,
			want: "events.lossless-replay",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()
			err := New(server.URL).RequireCapabilities("1", "session.source", "events.lossless-replay")
			var incompatible *IncompatibleDaemonError
			if !errors.As(err, &incompatible) || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want IncompatibleDaemonError containing %q", err, test.want)
			}
		})
	}
}

func TestRequireCapabilitiesAllowsUnknownAdditions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"apiVersion":"1","capabilities":["session.source","future.feature"]}`))
	}))
	defer server.Close()
	if err := New(server.URL).RequireCapabilities("1", "session.source"); err != nil {
		t.Fatal(err)
	}
}

func TestRequestReturnsStructuredAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":{"code":"runtime_unavailable","message":"runtime is unavailable","retryable":true,"details":{"scope":"runtime"},"requestId":"req_1"}}`))
	}))
	defer server.Close()
	_, err := New(server.URL).Status()
	var apiError *APIError
	if !errors.As(err, &apiError) {
		t.Fatalf("error = %v, want APIError", err)
	}
	if apiError.StatusCode != http.StatusServiceUnavailable || apiError.Code != "runtime_unavailable" || !apiError.Retryable || apiError.RequestID != "req_1" {
		t.Fatalf("APIError = %+v", apiError)
	}
}

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
