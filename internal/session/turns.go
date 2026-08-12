package session

import (
	"encoding/json"
	"errors"
	"sort"
	"strings"
)

const turnPreviewLimit = 280

// TurnsPage rebuilds a compact Turn index from the durable event source of
// truth. after and before are exclusive FirstEventID cursors; latest selects
// the newest page. Archived Sessions use the same path and remain readable.
func (s *Store) TurnsPage(id string, after, before int64, latest bool, limit int) (TurnPage, error) {
	if after < 0 || before < 0 || (after > 0 && (before > 0 || latest)) || (before > 0 && latest) {
		return TurnPage{}, errors.New("invalid turn cursor")
	}
	if limit <= 0 {
		limit = DefaultEventPageSize
	}
	if limit > MaxEventPageSize {
		limit = MaxEventPageSize
	}
	state, err := s.state(id)
	if err != nil {
		return TurnPage{}, err
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if err := s.ensureEventsLocked(state, id); err != nil {
		return TurnPage{}, err
	}
	turns := buildTurnSummaries(state.events)
	latestCursor := int64(0)
	if len(turns) > 0 {
		latestCursor = turns[len(turns)-1].FirstEventID
	}
	page := TurnPage{Turns: []TurnSummary{}, After: after, Before: before, Limit: limit, LatestCursor: latestCursor, NextAfter: after}
	if latest {
		before = latestCursor + 1
		page.Before = before
	}
	if before > 0 {
		end := sort.Search(len(turns), func(i int) bool { return turns[i].FirstEventID >= before })
		start := end - limit
		if start < 0 {
			start = 0
		}
		page.Turns = cloneTurnSummaries(turns[start:end])
		page.HasMoreBefore = start > 0
		if len(page.Turns) > 0 {
			page.NextBefore = page.Turns[0].FirstEventID
			page.NextAfter = page.Turns[len(page.Turns)-1].FirstEventID
		}
		page.HasMore = end < len(turns)
		return page, nil
	}
	start := sort.Search(len(turns), func(i int) bool { return turns[i].FirstEventID > after })
	end := start + limit
	if end > len(turns) {
		end = len(turns)
	}
	page.Turns = cloneTurnSummaries(turns[start:end])
	page.HasMore = end < len(turns)
	if len(page.Turns) > 0 {
		page.NextAfter = page.Turns[len(page.Turns)-1].FirstEventID
	}
	return page, nil
}

func buildTurnSummaries(events []Event) []TurnSummary {
	turns := make([]TurnSummary, 0)
	byID := make(map[string]int)
	for _, event := range events {
		if event.TurnID == "" {
			continue
		}
		index, ok := byID[event.TurnID]
		if !ok {
			index = len(turns)
			byID[event.TurnID] = index
			turns = append(turns, TurnSummary{
				ID: event.TurnID, Status: "active", StartedAt: event.Time,
				FirstEventID: event.ID, LastEventID: event.ID,
			})
		}
		turn := &turns[index]
		turn.LastEventID = event.ID
		turn.EventCount++
		switch event.Type {
		case EventMessageInput:
			if turn.TriggerEventID == 0 {
				var input MessageInput
				if json.Unmarshal(event.Data, &input) == nil {
					turn.TriggerEventID = event.ID
					turn.TriggerPreview = preview(input.Text)
					turn.TriggerRole = input.Role
					turn.TriggerSender = cloneMessageSender(input.Sender)
				}
			}
		case "message.assistant.delta":
			var data struct {
				Text string `json:"text"`
			}
			if json.Unmarshal(event.Data, &data) == nil {
				turn.FinalReplyEventID = event.ID
				turn.FinalReplyPreview = preview(turn.FinalReplyPreview + data.Text)
			}
		case "tool.event":
			turn.ToolEventCount++
		case EventTurnCompleted, EventTurnFailed, EventTurnCancelled:
			turn.Status = strings.TrimPrefix(event.Type, "turn.")
			completed := event.Time
			turn.CompletedAt = &completed
		}
	}
	return turns
}

func preview(value string) string {
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if len(runes) <= turnPreviewLimit {
		return value
	}
	return string(runes[:turnPreviewLimit]) + "…"
}

func cloneMessageSender(value *MessageSender) *MessageSender {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneTurnSummaries(values []TurnSummary) []TurnSummary {
	cloned := append([]TurnSummary(nil), values...)
	for i := range cloned {
		cloned[i].TriggerSender = cloneMessageSender(cloned[i].TriggerSender)
		if cloned[i].CompletedAt != nil {
			value := *cloned[i].CompletedAt
			cloned[i].CompletedAt = &value
		}
	}
	return cloned
}

// HasMessageID reports whether a canonical input with the stable message ID
// is already durable. It supports safe delivery retry after a lost response.
func (s *Store) HasMessageID(id, messageID string) (bool, error) {
	_, found, err := s.MessageByID(id, messageID)
	return found, err
}

// MessageByID returns the canonical durable input for a caller-stable ID.
func (s *Store) MessageByID(id, messageID string) (MessageInput, bool, error) {
	messageID = strings.TrimSpace(messageID)
	if messageID == "" {
		return MessageInput{}, false, nil
	}
	state, err := s.state(id)
	if err != nil {
		return MessageInput{}, false, err
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if err := s.ensureEventsLocked(state, id); err != nil {
		return MessageInput{}, false, err
	}
	for _, event := range state.events {
		if event.Type != EventMessageInput {
			continue
		}
		var input MessageInput
		if json.Unmarshal(event.Data, &input) == nil && input.MessageID == messageID {
			return input, true, nil
		}
	}
	return MessageInput{}, false, nil
}
