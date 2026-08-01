package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/disksing/agenthub/internal/session"
)

type backwardPage struct {
	Events []session.Event `json:"events"`
	Page   struct {
		After         int64 `json:"after"`
		Limit         int   `json:"limit"`
		NextAfter     int64 `json:"nextAfter"`
		HasMore       bool  `json:"hasMore"`
		Before        int64 `json:"before"`
		NextBefore    int64 `json:"nextBefore"`
		HasMoreBefore bool  `json:"hasMoreBefore"`
	} `json:"page"`
	LatestCursor int64 `json:"latestCursor"`
}

func getBackwardPage(t *testing.T, base, id, query string) backwardPage {
	t.Helper()
	response, err := http.Get(base + "/v1/sessions/" + id + "/events?" + query)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("%s: status = %d", query, response.StatusCode)
	}
	var page backwardPage
	if err := json.NewDecoder(response.Body).Decode(&page); err != nil {
		t.Fatal(err)
	}
	return page
}

func pageIDs(page backwardPage) []int64 {
	ids := make([]int64, 0, len(page.Events))
	for _, event := range page.Events {
		ids = append(ids, event.ID)
	}
	return ids
}

func TestEventsBackwardPagination(t *testing.T) {
	store, created := seedEventStore(t, 25)
	server := httptest.NewServer(New(store, "test", time.Now()).Handler())
	defer server.Close()

	latest := getBackwardPage(t, server.URL, created.ID, "latest=true&limit=10")
	if got, want := pageIDs(latest), []int64{16, 17, 18, 19, 20, 21, 22, 23, 24, 25}; !equalIDs(got, want) {
		t.Fatalf("latest ids = %v, want %v", got, want)
	}
	if latest.Page.Before != 26 || latest.Page.NextBefore != 16 || !latest.Page.HasMoreBefore {
		t.Fatalf("latest page metadata = %+v", latest.Page)
	}
	// Forward metadata stays populated so the tail page can hand nextAfter
	// to a live stream.
	if latest.Page.NextAfter != 25 || latest.Page.HasMore || latest.LatestCursor != 25 {
		t.Fatalf("latest page forward metadata = %+v latest=%d", latest.Page, latest.LatestCursor)
	}

	// latest=true is exactly before=head+1.
	explicit := getBackwardPage(t, server.URL, created.ID, "before=26&limit=10")
	if !equalIDs(pageIDs(explicit), pageIDs(latest)) || explicit.Page != latest.Page {
		t.Fatalf("before=head+1 page = %+v, want %+v", explicit, latest)
	}

	// A cursor past the durable head clamps instead of erroring.
	clamped := getBackwardPage(t, server.URL, created.ID, "before=1000&limit=10")
	if clamped.Page.Before != 26 || !equalIDs(pageIDs(clamped), pageIDs(latest)) {
		t.Fatalf("clamped page = %+v", clamped)
	}

	// Page back to the log start with nextBefore.
	middle := getBackwardPage(t, server.URL, created.ID, "before=16&limit=10")
	if got, want := pageIDs(middle), []int64{15, 14, 13, 12, 11, 10, 9, 8, 7, 6}; equalIDs(got, want) {
		t.Fatal("events must stay in ascending id order")
	}
	if got, want := pageIDs(middle), []int64{6, 7, 8, 9, 10, 11, 12, 13, 14, 15}; !equalIDs(got, want) {
		t.Fatalf("middle ids = %v, want %v", got, want)
	}
	if middle.Page.NextBefore != 6 || !middle.Page.HasMoreBefore {
		t.Fatalf("middle page metadata = %+v", middle.Page)
	}
	first := getBackwardPage(t, server.URL, created.ID, "before=6&limit=10")
	if got, want := pageIDs(first), []int64{1, 2, 3, 4, 5}; !equalIDs(got, want) {
		t.Fatalf("first ids = %v, want %v", got, want)
	}
	if first.Page.NextBefore != 1 || first.Page.HasMoreBefore {
		t.Fatalf("first page must end backward pagination: %+v", first.Page)
	}

	// before=1 is a valid empty page, not an error.
	empty := getBackwardPage(t, server.URL, created.ID, "before=1&limit=10")
	if len(empty.Events) != 0 || empty.Page.HasMoreBefore || empty.Page.Before != 1 {
		t.Fatalf("before=1 page = %+v", empty)
	}
}

func equalIDs(got, want []int64) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range want {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func TestEventsBackwardCursorValidation(t *testing.T) {
	store, created := seedEventStore(t, 5)
	server := httptest.NewServer(New(store, "test", time.Now()).Handler())
	defer server.Close()
	for _, query := range []string{
		"before=0",
		"before=-1",
		"before=abc",
		"latest=banana",
		"after=1&before=5",
		"after=1&latest=true",
		"before=5&latest=true",
		"stream=true&before=5",
		"stream=true&latest=true",
	} {
		response, err := http.Get(server.URL + "/v1/sessions/" + created.ID + "/events?" + query)
		if err != nil {
			t.Fatal(err)
		}
		var body struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
			response.Body.Close()
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusBadRequest || body.Error.Code != "invalid_event_cursor" {
			t.Errorf("%s: status=%d code=%q, want 400 invalid_event_cursor", query, response.StatusCode, body.Error.Code)
		}
	}

	// The Accept header form of streaming rejects backward parameters too.
	request, err := http.NewRequest(http.MethodGet, server.URL+"/v1/sessions/"+created.ID+"/events?latest=true", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Accept", "text/event-stream")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusBadRequest || body.Error.Code != "invalid_event_cursor" {
		t.Fatalf("SSE latest=true: status=%d code=%q, want 400 invalid_event_cursor", response.StatusCode, body.Error.Code)
	}

	// Last-Event-ID is an explicit forward cursor and conflicts with before.
	request, err = http.NewRequest(http.MethodGet, server.URL+"/v1/sessions/"+created.ID+"/events?before=5", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Last-Event-ID", "3")
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("Last-Event-ID + before: status=%d, want 400", response.StatusCode)
	}
}

func TestEventsForwardPageOmitsBackwardFields(t *testing.T) {
	store, created := seedEventStore(t, 10)
	server := httptest.NewServer(New(store, "test", time.Now()).Handler())
	defer server.Close()

	response, err := http.Get(server.URL + "/v1/sessions/" + created.ID + "/events?after=0&limit=5")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var body map[string]any
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	page, ok := body["page"].(map[string]any)
	if !ok {
		t.Fatalf("page missing or not an object: %v", body)
	}
	for _, field := range []string{"before", "nextBefore", "hasMoreBefore"} {
		if _, present := page[field]; present {
			t.Fatalf("forward page must not carry %q: %v", field, page)
		}
	}
}
