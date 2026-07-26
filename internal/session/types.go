package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
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
	ID                 string            `json:"id"`
	Title              string            `json:"title"`
	Cwd                string            `json:"cwd"`
	AgentName          string            `json:"agentName,omitempty"`
	LaunchEnvironment  map[string]string `json:"launchEnvironment,omitempty"`
	Provider           string            `json:"provider,omitempty"`
	ProviderSessionID  string            `json:"providerSessionId,omitempty"`
	State              string            `json:"state"`
	CurrentTurnID      string            `json:"currentTurnId,omitempty"`
	PendingApprovalIDs []string          `json:"pendingApprovalIds,omitempty"`
	LastEventID        int64             `json:"lastEventId"`
	CreatedAt          time.Time         `json:"createdAt"`
	UpdatedAt          time.Time         `json:"updatedAt"`
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
	Title             string
	Cwd               string
	AgentName         string
	LaunchEnvironment map[string]string
}

// ValidateLaunchEnvironment checks values before they are persisted and
// passed to an operating-system process. Environment variable names cannot
// be empty or contain '=' or NUL, and values cannot contain NUL.
func ValidateLaunchEnvironment(environment map[string]string) error {
	for key, value := range environment {
		if key == "" {
			return errors.New("environment variable name cannot be empty")
		}
		if strings.ContainsAny(key, "=\x00") {
			return fmt.Errorf("invalid environment variable name %q", key)
		}
		if strings.ContainsRune(value, '\x00') {
			return fmt.Errorf("environment variable %q contains NUL", key)
		}
	}
	return nil
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
