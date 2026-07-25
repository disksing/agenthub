package session

import (
	"encoding/json"
	"time"
)

const (
	StateStarting        = "starting"
	StateReady           = "ready"
	StateBusy            = "busy"
	StateWaitingApproval = "waiting_approval"
	StateStopped         = "stopped"
	StateFailed          = "failed"
	StateArchived        = "archived"
)

type Session struct {
	ID                 string    `json:"id"`
	Title              string    `json:"title"`
	Cwd                string    `json:"cwd"`
	AgentID            string    `json:"agentId,omitempty"`
	Provider           string    `json:"provider,omitempty"`
	ProviderSessionID  string    `json:"providerSessionId,omitempty"`
	State              string    `json:"state"`
	CurrentTurnID      string    `json:"currentTurnId,omitempty"`
	PendingApprovalIDs []string  `json:"pendingApprovalIds,omitempty"`
	LastEventID        int64     `json:"lastEventId"`
	CreatedAt          time.Time `json:"createdAt"`
	UpdatedAt          time.Time `json:"updatedAt"`
}

type Event struct {
	ID        int64           `json:"id"`
	Time      time.Time       `json:"time"`
	Type      string          `json:"type"`
	SessionID string          `json:"sessionId"`
	TurnID    string          `json:"turnId,omitempty"`
	Data      json.RawMessage `json:"data,omitempty"`
}

type CreateInput struct {
	Title   string
	Cwd     string
	AgentID string
}

type StateEventData struct {
	State string `json:"state"`
}

type ApprovalEventData struct {
	ApprovalID string `json:"approvalId"`
}

type ProviderEventData struct {
	AgentID           string `json:"agentId"`
	Provider          string `json:"provider"`
	ProviderSessionID string `json:"providerSessionId,omitempty"`
}
