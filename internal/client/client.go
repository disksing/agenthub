package client

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/disksing/agenthub/internal/daemon"
	"github.com/disksing/agenthub/internal/paths"
	"github.com/disksing/agenthub/internal/session"
)

type Client struct {
	endpoint string
	http     *http.Client
}

type EventPage struct {
	Events []session.Event `json:"events"`
	Page   struct {
		After     int64 `json:"after"`
		Limit     int   `json:"limit"`
		NextAfter int64 `json:"nextAfter"`
		HasMore   bool  `json:"hasMore"`
	} `json:"page"`
	LatestCursor int64 `json:"latestCursor"`
}

type EventCursorGapError struct {
	Expected int64
	Got      int64
}

func (e *EventCursorGapError) Error() string {
	return fmt.Sprintf("event cursor gap: expected %d, got %d; projection stopped", e.Expected, e.Got)
}

func Discover() (*Client, error) {
	if endpoint := strings.TrimRight(strings.TrimSpace(os.Getenv("AGENTHUB_ENDPOINT")), "/"); endpoint != "" {
		return New(endpoint), nil
	}
	resolved, err := paths.Resolve()
	if err != nil {
		return nil, err
	}
	state, err := daemon.ReadState(resolved.ServerFile)
	if err != nil {
		return nil, fmt.Errorf("discover agenthub daemon: %w", err)
	}
	return New(state.Endpoint), nil
}

func New(endpoint string) *Client {
	return &Client{
		endpoint: strings.TrimRight(endpoint, "/"),
		http:     &http.Client{Timeout: 30 * time.Minute},
	}
}

func (c *Client) Status() (map[string]any, error) {
	var result map[string]any
	if err := c.request(http.MethodGet, "/v1/status", nil, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func (c *Client) CreateSession(title, cwd, agentName string) (session.Session, error) {
	return c.CreateSessionWithMessage(title, cwd, agentName, "")
}

func (c *Client) CreateSessionWithMessage(title, cwd, agentName, message string) (session.Session, error) {
	body := map[string]any{
		"title":     title,
		"cwd":       cwd,
		"agentName": agentName,
	}
	if strings.TrimSpace(message) != "" {
		body["initialMessage"] = map[string]any{"text": message}
	}
	var result struct {
		Session session.Session `json:"session"`
	}
	if err := c.request(http.MethodPost, "/v1/sessions", body, &result); err != nil {
		return session.Session{}, err
	}
	return result.Session, nil
}

func (c *Client) Agents() (map[string]any, error) {
	var result map[string]any
	if err := c.request(http.MethodGet, "/v1/agents", nil, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func (c *Client) SendMessage(id, text string, steer bool) (session.Session, error) {
	var result struct {
		Session session.Session `json:"session"`
	}
	if err := c.request(http.MethodPost, "/v1/sessions/"+id+"/messages", map[string]any{"text": text, "steer": steer}, &result); err != nil {
		return session.Session{}, err
	}
	return result.Session, nil
}

func (c *Client) SessionAction(id, action string) (session.Session, error) {
	var result struct {
		Session session.Session `json:"session"`
	}
	if err := c.request(http.MethodPost, "/v1/sessions/"+id+"/"+action, map[string]any{}, &result); err != nil {
		return session.Session{}, err
	}
	return result.Session, nil
}

func (c *Client) ResolveApproval(id, approvalID, decision string) (session.Session, error) {
	var result struct {
		Session session.Session `json:"session"`
	}
	path := "/v1/sessions/" + id + "/approvals/" + approvalID
	if err := c.request(http.MethodPost, path, map[string]any{"decision": decision}, &result); err != nil {
		return session.Session{}, err
	}
	return result.Session, nil
}

func (c *Client) EventsPage(id string, after int64, limit int) (EventPage, error) {
	var result EventPage
	path := fmt.Sprintf("/v1/sessions/%s/events?after=%d&limit=%d", id, after, limit)
	if err := c.request(http.MethodGet, path, nil, &result); err != nil {
		return EventPage{}, err
	}
	return result, nil
}

// EventsAfter catches up through the durable head reported by the first REST
// page. It stops before projecting any non-contiguous event.
func (c *Client) EventsAfter(id string, after int64) ([]session.Event, error) {
	cursor := after
	var target int64 = -1
	var events []session.Event
	for {
		page, err := c.EventsPage(id, cursor, session.MaxEventPageSize)
		if err != nil {
			return nil, err
		}
		if target < 0 {
			target = page.LatestCursor
		}
		for _, event := range page.Events {
			if event.ID > target {
				break
			}
			if event.ID != cursor+1 {
				return nil, &EventCursorGapError{Expected: cursor + 1, Got: event.ID}
			}
			events = append(events, event)
			cursor = event.ID
		}
		if cursor >= target {
			return events, nil
		}
		if len(page.Events) == 0 {
			return nil, &EventCursorGapError{Expected: cursor + 1, Got: 0}
		}
	}
}

func (c *Client) ListSessions(includeArchived bool) ([]session.Session, error) {
	path := "/v1/sessions"
	if includeArchived {
		path += "?includeArchived=true"
	}
	var result struct {
		Sessions []session.Session `json:"sessions"`
	}
	if err := c.request(http.MethodGet, path, nil, &result); err != nil {
		return nil, err
	}
	return result.Sessions, nil
}

// ListArchivedSessions returns only archived sessions.
func (c *Client) ListArchivedSessions() ([]session.Session, error) {
	var result struct {
		Sessions []session.Session `json:"sessions"`
	}
	if err := c.request(http.MethodGet, "/v1/sessions?archived=true", nil, &result); err != nil {
		return nil, err
	}
	return result.Sessions, nil
}

func (c *Client) GetSession(id string) (session.Session, error) {
	var result struct {
		Session session.Session `json:"session"`
	}
	if err := c.request(http.MethodGet, "/v1/sessions/"+id, nil, &result); err != nil {
		return session.Session{}, err
	}
	return result.Session, nil
}

func (c *Client) ArchiveSession(id string) (session.Session, error) {
	var result struct {
		Session session.Session `json:"session"`
	}
	if err := c.request(http.MethodDelete, "/v1/sessions/"+id, map[string]any{}, &result); err != nil {
		return session.Session{}, err
	}
	return result.Session, nil
}

func (c *Client) request(method, path string, body any, target any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(data)
	}
	request, err := http.NewRequest(method, c.endpoint+path, reader)
	if err != nil {
		return err
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := c.http.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(response.Body, 1024*1024))
		var apiError struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(data, &apiError) == nil && apiError.Error.Message != "" {
			return fmt.Errorf("%s: %s", apiError.Error.Code, apiError.Error.Message)
		}
		return fmt.Errorf("agenthub API returned %s", response.Status)
	}
	if target == nil || response.StatusCode == http.StatusNoContent {
		return nil
	}
	if err := json.NewDecoder(response.Body).Decode(target); err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
		return err
	}
	return nil
}
