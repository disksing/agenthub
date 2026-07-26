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
	AgentName          string    `json:"agentName,omitempty"`
	Source             *Source   `json:"source,omitempty"`
	Provider           string    `json:"provider,omitempty"`
	ProviderSessionID  string    `json:"providerSessionId,omitempty"`
	State              string    `json:"state"`
	CurrentTurnID      string    `json:"currentTurnId,omitempty"`
	PendingApprovalIDs []string  `json:"pendingApprovalIds,omitempty"`
	LastEventID        int64     `json:"lastEventId"`
	CreatedAt          time.Time `json:"createdAt"`
	UpdatedAt          time.Time `json:"updatedAt"`
}

// Source is caller-supplied metadata for correlating sessions with the
// application that created them. AgentHub stores these values verbatim and
// does not authenticate them or impose uniqueness.
type Source struct {
	App        string `json:"app,omitempty"`
	InstanceID string `json:"instanceId,omitempty"`
	ExternalID string `json:"externalId,omitempty"`
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
	Title     string
	Cwd       string
	AgentName string
	Source    *Source
}

type StateEventData struct {
	State string `json:"state"`
}

type ApprovalEventData struct {
	ApprovalID string `json:"approvalId"`
}

// AgentRenameEventData is the payload of the session.agent event, appended
// when a configured agent is renamed so the session follows the new name.
type AgentRenameEventData struct {
	AgentName string `json:"agentName"`
}

// ProviderEventData is the payload of the session.provider event. AgentName
// names the agent configuration the session runs with. AgentID is read-only
// compatibility for events recorded before agent ids were removed; it is
// never written by current code.
type ProviderEventData struct {
	AgentName         string `json:"agentName,omitempty"`
	AgentID           string `json:"agentId,omitempty"`
	Provider          string `json:"provider"`
	ProviderSessionID string `json:"providerSessionId,omitempty"`
}

// ResolvedAgentName returns the recorded agent reference, preferring the
// current agentName field and falling back to the legacy agentId.
func (d ProviderEventData) ResolvedAgentName() string {
	if d.AgentName != "" {
		return d.AgentName
	}
	return d.AgentID
}
