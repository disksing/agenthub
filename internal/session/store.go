package session

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

var ErrNotFound = errors.New("session not found")

type sessionState struct {
	mu      sync.Mutex
	session Session
	events  []Event
}

type Store struct {
	root string

	mu          sync.RWMutex
	sessions    map[string]*sessionState
	subscribers map[string]map[chan Event]struct{}
}

func Open(root string) (*Store, error) {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("create session store: %w", err)
	}
	store := &Store{
		root:        root,
		sessions:    make(map[string]*sessionState),
		subscribers: make(map[string]map[chan Event]struct{}),
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("read session store: %w", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "ses_") {
			continue
		}
		state, err := store.loadSession(entry.Name())
		if err != nil {
			return nil, fmt.Errorf("load %s: %w", entry.Name(), err)
		}
		store.sessions[entry.Name()] = state
	}
	return store, nil
}

func (s *Store) Root() string {
	return s.root
}

func (s *Store) Create(input CreateInput) (Session, error) {
	now := time.Now().UTC()
	id, err := NewID("ses")
	if err != nil {
		return Session{}, err
	}
	if input.Title == "" {
		input.Title = "New Session"
	}
	value := Session{
		ID:        id,
		Title:     input.Title,
		Cwd:       input.Cwd,
		AgentID:   input.AgentID,
		State:     StateReady,
		CreatedAt: now,
		UpdatedAt: now,
	}
	state := &sessionState{session: value}
	dir := s.sessionDir(id)
	if err := os.Mkdir(dir, 0o700); err != nil {
		return Session{}, fmt.Errorf("create session directory: %w", err)
	}

	s.mu.Lock()
	s.sessions[id] = state
	s.mu.Unlock()

	data, err := json.Marshal(value)
	if err != nil {
		s.removeSession(id)
		_ = os.RemoveAll(dir)
		return Session{}, err
	}
	if _, err := s.Append(id, "session.created", "", data); err != nil {
		s.removeSession(id)
		_ = os.RemoveAll(dir)
		return Session{}, err
	}
	return s.Get(id)
}

func (s *Store) Get(id string) (Session, error) {
	state, err := s.state(id)
	if err != nil {
		return Session{}, err
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	return cloneSession(state.session), nil
}

func (s *Store) List(includeArchived bool) []Session {
	s.mu.RLock()
	states := make([]*sessionState, 0, len(s.sessions))
	for _, state := range s.sessions {
		states = append(states, state)
	}
	s.mu.RUnlock()

	values := make([]Session, 0, len(states))
	for _, state := range states {
		state.mu.Lock()
		value := cloneSession(state.session)
		state.mu.Unlock()
		if !includeArchived && value.State == StateArchived {
			continue
		}
		values = append(values, value)
	}
	sort.Slice(values, func(i, j int) bool {
		return values[i].UpdatedAt.After(values[j].UpdatedAt)
	})
	return values
}

func (s *Store) Append(id, eventType, turnID string, data []byte) (Event, error) {
	state, err := s.state(id)
	if err != nil {
		return Event{}, err
	}
	state.mu.Lock()
	event := Event{
		ID:        state.session.LastEventID + 1,
		Time:      time.Now().UTC(),
		Type:      eventType,
		SessionID: id,
		TurnID:    turnID,
		Data:      append(json.RawMessage(nil), data...),
	}
	encoded, err := json.Marshal(event)
	if err != nil {
		state.mu.Unlock()
		return Event{}, err
	}
	encoded = append(encoded, '\n')
	if err := appendDurable(s.eventsPath(id), encoded); err != nil {
		state.mu.Unlock()
		return Event{}, err
	}
	if err := applyEvent(&state.session, event); err != nil {
		state.mu.Unlock()
		return Event{}, fmt.Errorf("project event: %w", err)
	}
	state.events = append(state.events, event)
	if err := writeJSONAtomic(s.snapshotPath(id), state.session); err != nil {
		state.mu.Unlock()
		return Event{}, fmt.Errorf("write session snapshot: %w", err)
	}
	state.mu.Unlock()

	s.publish(event)
	return event, nil
}

func (s *Store) EventsAfter(id string, after int64, limit int) ([]Event, error) {
	state, err := s.state(id)
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 1000 {
		limit = 500
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	events := make([]Event, 0, min(limit, len(state.events)))
	for _, event := range state.events {
		if event.ID <= after {
			continue
		}
		events = append(events, cloneEvent(event))
		if len(events) == limit {
			break
		}
	}
	return events, nil
}

func (s *Store) Subscribe(id string) (<-chan Event, func(), error) {
	if _, err := s.state(id); err != nil {
		return nil, nil, err
	}
	ch := make(chan Event, 256)
	s.mu.Lock()
	if s.subscribers[id] == nil {
		s.subscribers[id] = make(map[chan Event]struct{})
	}
	s.subscribers[id][ch] = struct{}{}
	s.mu.Unlock()
	cancel := func() {
		s.mu.Lock()
		if subscribers := s.subscribers[id]; subscribers != nil {
			if _, ok := subscribers[ch]; ok {
				delete(subscribers, ch)
			}
			if len(subscribers) == 0 {
				delete(s.subscribers, id)
			}
		}
		s.mu.Unlock()
	}
	return ch, cancel, nil
}

func (s *Store) loadSession(id string) (*sessionState, error) {
	events, err := readEventsRepairTail(s.eventsPath(id))
	if err != nil {
		return nil, err
	}
	if len(events) == 0 {
		return nil, errors.New("session has no events")
	}
	var projected Session
	for _, event := range events {
		if event.SessionID != id {
			return nil, fmt.Errorf("event %d belongs to %s", event.ID, event.SessionID)
		}
		if err := applyEvent(&projected, event); err != nil {
			return nil, fmt.Errorf("event %d: %w", event.ID, err)
		}
	}
	if err := writeJSONAtomic(s.snapshotPath(id), projected); err != nil {
		return nil, err
	}
	return &sessionState{session: projected, events: events}, nil
}

func applyEvent(projected *Session, event Event) error {
	switch event.Type {
	case "session.created":
		var created Session
		if err := json.Unmarshal(event.Data, &created); err != nil {
			return err
		}
		*projected = created
	case "session.state":
		var data StateEventData
		if err := json.Unmarshal(event.Data, &data); err != nil {
			return err
		}
		projected.State = data.State
	case "session.provider":
		var data ProviderEventData
		if err := json.Unmarshal(event.Data, &data); err != nil {
			return err
		}
		projected.AgentID = data.AgentID
		projected.Provider = data.Provider
		projected.ProviderSessionID = data.ProviderSessionID
	case "session.archived":
		projected.State = StateArchived
	case "turn.started":
		projected.CurrentTurnID = event.TurnID
		projected.State = StateBusy
	case "turn.completed", "turn.failed", "turn.cancelled":
		if projected.CurrentTurnID == event.TurnID {
			projected.CurrentTurnID = ""
		}
		if projected.State != StateStopped && projected.State != StateArchived {
			projected.State = StateReady
		}
	case "approval.requested":
		var data ApprovalEventData
		if err := json.Unmarshal(event.Data, &data); err != nil {
			return err
		}
		projected.PendingApprovalIDs = appendUnique(projected.PendingApprovalIDs, data.ApprovalID)
		projected.State = StateWaitingApproval
	case "approval.resolved":
		var data ApprovalEventData
		if err := json.Unmarshal(event.Data, &data); err != nil {
			return err
		}
		projected.PendingApprovalIDs = removeString(projected.PendingApprovalIDs, data.ApprovalID)
		if len(projected.PendingApprovalIDs) == 0 {
			if projected.CurrentTurnID != "" {
				projected.State = StateBusy
			} else {
				projected.State = StateReady
			}
		}
	}
	projected.LastEventID = event.ID
	projected.UpdatedAt = event.Time
	return nil
}

func readEventsRepairTail(path string) ([]Event, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if len(data) > 0 && data[len(data)-1] != '\n' {
		lastNewline := bytes.LastIndexByte(data, '\n')
		validLength := lastNewline + 1
		if validLength < 0 {
			validLength = 0
		}
		if err := os.Truncate(path, int64(validLength)); err != nil {
			return nil, fmt.Errorf("repair event log tail: %w", err)
		}
		data = data[:validLength]
	}
	var events []Event
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 64*1024), 8*1024*1024)
	var previousID int64
	for scanner.Scan() {
		var event Event
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			return nil, fmt.Errorf("invalid event log record: %w", err)
		}
		if event.ID != previousID+1 {
			return nil, fmt.Errorf("non-contiguous event id: got %d after %d", event.ID, previousID)
		}
		previousID = event.ID
		events = append(events, event)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return events, nil
}

func appendDurable(path string, data []byte) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func writeJSONAtomic(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	dir := filepath.Dir(path)
	temp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	cleanup := func() {
		_ = temp.Close()
		_ = os.Remove(tempPath)
	}
	if err := temp.Chmod(0o600); err != nil {
		cleanup()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		cleanup()
		return err
	}
	if err := temp.Sync(); err != nil {
		cleanup()
		return err
	}
	if err := temp.Close(); err != nil {
		_ = os.Remove(tempPath)
		return err
	}
	if err := os.Rename(tempPath, path); err != nil {
		_ = os.Remove(tempPath)
		return err
	}
	if dirFile, err := os.Open(dir); err == nil {
		_ = dirFile.Sync()
		_ = dirFile.Close()
	}
	return nil
}

func NewID(prefix string) (string, error) {
	random := make([]byte, 8)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s_%x%s", prefix, time.Now().UTC().UnixMilli(), hex.EncodeToString(random)), nil
}

func (s *Store) state(id string) (*sessionState, error) {
	s.mu.RLock()
	state := s.sessions[id]
	s.mu.RUnlock()
	if state == nil {
		return nil, ErrNotFound
	}
	return state, nil
}

func (s *Store) publish(event Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for ch := range s.subscribers[event.SessionID] {
		select {
		case ch <- cloneEvent(event):
		default:
			delete(s.subscribers[event.SessionID], ch)
			close(ch)
		}
	}
	if len(s.subscribers[event.SessionID]) == 0 {
		delete(s.subscribers, event.SessionID)
	}
}

func (s *Store) removeSession(id string) {
	s.mu.Lock()
	delete(s.sessions, id)
	s.mu.Unlock()
}

func (s *Store) sessionDir(id string) string {
	return filepath.Join(s.root, id)
}

func (s *Store) snapshotPath(id string) string {
	return filepath.Join(s.sessionDir(id), "session.json")
}

func (s *Store) eventsPath(id string) string {
	return filepath.Join(s.sessionDir(id), "events.jsonl")
}

func cloneSession(value Session) Session {
	value.PendingApprovalIDs = append([]string(nil), value.PendingApprovalIDs...)
	return value
}

func cloneEvent(value Event) Event {
	value.Data = append(json.RawMessage(nil), value.Data...)
	return value
}

func appendUnique(values []string, value string) []string {
	for _, current := range values {
		if current == value {
			return values
		}
	}
	return append(values, value)
}

func removeString(values []string, value string) []string {
	result := values[:0]
	for _, current := range values {
		if current != value {
			result = append(result, current)
		}
	}
	return result
}
