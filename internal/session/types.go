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
	StateStopping        = "stopping"
	StateStopped         = "stopped"
	StateArchived        = "archived"
)

const (
	StopReasonRequested      = "requested"
	StopReasonCompleted      = "completed"
	StopReasonProviderError  = "provider_error"
	StopReasonStartupError   = "startup_error"
	StopReasonDaemonRecovery = "daemon_recovery"
)

const (
	EventTurnCompleted = "turn.completed"
	EventTurnFailed    = "turn.failed"
	EventTurnCancelled = "turn.cancelled"
)

// TurnTerminalEventData is the provider-independent payload of a canonical
// turn terminal event. A successful completion has an empty payload. Failed
// and cancelled turns may carry a human-readable error or stable reason.
// Provider-native completion payloads remain available on their preceding
// diagnostic event and are never copied into this public payload.
type TurnTerminalEventData struct {
	Error  string `json:"error,omitempty"`
	Reason string `json:"reason,omitempty"`
}

type Session struct {
	ID                 string            `json:"id"`
	Title              string            `json:"title"`
	Cwd                string            `json:"cwd"`
	AgentName          string            `json:"agentName,omitempty"`
	Source             *Source           `json:"source,omitempty"`
	LaunchEnvironment  map[string]string `json:"launchEnvironment,omitempty"`
	Provider           string            `json:"provider,omitempty"`
	ProviderSessionID  string            `json:"providerSessionId,omitempty"`
	State              string            `json:"state"`
	StopReason         string            `json:"stopReason,omitempty"`
	CurrentTurnID      string            `json:"currentTurnId,omitempty"`
	PendingApprovalIDs []string          `json:"pendingApprovalIds,omitempty"`
	LastEventID        int64             `json:"lastEventId"`
	CreatedAt          time.Time         `json:"createdAt"`
	UpdatedAt          time.Time         `json:"updatedAt"`
}

// Source is caller-supplied metadata for correlating sessions with the
// application that created them. AgentHub stores these values verbatim and
// does not authenticate them or impose uniqueness.
type Source struct {
	App        string `json:"app,omitempty"`
	InstanceID string `json:"instanceId,omitempty"`
	ExternalID string `json:"externalId,omitempty"`
}

// Event is a durable canonical session event. StartTime is populated for a
// delta event folded with at least one following fragment; it preserves the
// first fragment timestamp while Time continues to track the newest fragment.
type Event struct {
	ID        int64           `json:"id"`
	Time      time.Time       `json:"time"`
	StartTime *time.Time      `json:"startTime,omitempty"`
	Type      string          `json:"type"`
	SessionID string          `json:"sessionId"`
	TurnID    string          `json:"turnId,omitempty"`
	Data      json.RawMessage `json:"data,omitempty"`
}

// EventPage is a stable snapshot page of a session's durable event log.
// After and NextAfter use exclusive cursor semantics: a subsequent request
// passes NextAfter as its After value. Before and NextBefore are the
// backward counterpart, populated only by backward pages (EventsPageBefore):
// a subsequent backward request passes NextBefore as its Before value. They
// are zero on forward pages and omitted from their JSON encoding.
type EventPage struct {
	Events        []Event `json:"events"`
	After         int64   `json:"after"`
	Limit         int     `json:"limit"`
	NextAfter     int64   `json:"nextAfter"`
	HasMore       bool    `json:"hasMore"`
	Before        int64   `json:"before,omitempty"`
	NextBefore    int64   `json:"nextBefore,omitempty"`
	HasMoreBefore bool    `json:"hasMoreBefore,omitempty"`
	LatestCursor  int64   `json:"latestCursor"`
}

type CreateInput struct {
	Title             string
	Cwd               string
	AgentName         string
	Source            *Source
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
	State  string `json:"state"`
	Reason string `json:"reason,omitempty"`
}

// ProviderProcessEventData is durable evidence that an adapter started an OS
// process group. It lets a replacement daemon terminate and confirm the old
// group after an ungraceful daemon exit before publishing stopped.
type ProviderProcessEventData struct {
	PID            int `json:"pid"`
	ProcessGroupID int `json:"processGroupId"`
}

type ApprovalEventData struct {
	ApprovalID string `json:"approvalId"`
}

// AgentRenameEventData is the payload of the session.agent event, appended
// when a configured agent is renamed so the session follows the new name.
type AgentRenameEventData struct {
	AgentName string `json:"agentName"`
}

// LaunchEnvironmentEventData is the payload of the
// session.launch-environment event. It carries the session's full effective
// launch environment after an overlay was merged, so replay replaces the
// projected map with the payload verbatim instead of re-applying the
// overlay. The historical session.created snapshot is never rewritten.
type LaunchEnvironmentEventData struct {
	Environment map[string]string `json:"environment"`
}

// ProviderEventData is the payload of the session.provider event. AgentName
// names the agent configuration the session runs with.
type ProviderEventData struct {
	AgentName         string `json:"agentName,omitempty"`
	Provider          string `json:"provider"`
	ProviderSessionID string `json:"providerSessionId,omitempty"`
}
