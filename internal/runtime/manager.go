package runtime

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/disksing/agenthub/internal/config"
	"github.com/disksing/agenthub/internal/provider"
	"github.com/disksing/agenthub/internal/session"
)

type Manager struct {
	store *session.Store

	mu      sync.Mutex
	cfg     config.Config
	running map[string]*active
	factory func(provider.Options) (provider.Session, error)
}

type active struct {
	mu         sync.Mutex
	adapter    provider.Session
	turnID     string
	ready      chan struct{}
	startErr   error
	stopReason string
	finalized  bool
	finalize   sync.Once
	// replies holds custom text replies queued while the owning turn is still
	// open. Providers cannot accept free text inside an approval response, so
	// each reply dismisses its question immediately and is delivered as a
	// regular user message once the turn closes.
	replies []string
}

func (a *active) turn() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.turnID
}

func (a *active) setTurn(value string) {
	a.mu.Lock()
	a.turnID = value
	a.mu.Unlock()
}

func (a *active) waitReady() error {
	<-a.ready
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.startErr
}

func (a *active) finishStart(err error) {
	a.mu.Lock()
	a.startErr = err
	a.mu.Unlock()
	close(a.ready)
}

func New(store *session.Store, cfg config.Config) *Manager {
	manager := &Manager{store: store, cfg: cfg, running: make(map[string]*active), factory: provider.New}
	for _, value := range store.List(false) {
		manager.recover(value)
	}
	return manager
}

func (a *active) markStopping(reason string) {
	a.mu.Lock()
	if a.stopReason == "" {
		a.stopReason = reason
	}
	a.mu.Unlock()
}

func (a *active) outcome(processErr error) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	switch {
	case a.stopReason != "":
		return a.stopReason, nil
	case a.startErr != nil:
		return session.StopReasonStartupError, a.startErr
	case processErr != nil:
		return session.StopReasonProviderError, processErr
	default:
		return session.StopReasonCompleted, nil
	}
}

func (a *active) withEvent(fn func(turnID string)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.finalized {
		return
	}
	fn(a.turnID)
}

func (a *active) beginFinalizing() {
	a.mu.Lock()
	a.finalized = true
	a.mu.Unlock()
}

func (m *Manager) Config() config.Config {
	m.mu.Lock()
	defer m.mu.Unlock()
	return cloneConfig(m.cfg)
}

func (m *Manager) SetConfig(cfg config.Config) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	m.cfg = cloneConfig(cfg)
	m.mu.Unlock()
	return nil
}

func (m *Manager) Start(id string) (session.Session, error) {
	if _, err := m.ensure(id); err != nil {
		return session.Session{}, err
	}
	return m.store.Get(id)
}

func (m *Manager) Send(id, text string, steer bool) (session.Session, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return session.Session{}, errors.New("message text is required")
	}
	value, err := m.store.Get(id)
	if err != nil {
		return session.Session{}, err
	}
	if value.State == session.StateArchived {
		return session.Session{}, session.ErrArchived
	}
	if value.State == session.StateStopping {
		return session.Session{}, errors.New("session provider is stopping")
	}
	current := value.CurrentTurnID
	if current != "" && !steer {
		return session.Session{}, errors.New("session already has an active turn; set steer=true or wait")
	}
	run, err := m.ensure(id)
	if err != nil {
		return session.Session{}, err
	}
	turnID := current
	if turnID == "" {
		turnID, err = session.NewID("turn")
		if err != nil {
			return session.Session{}, err
		}
		run.setTurn(turnID)
		_, _ = m.store.Append(id, "message.user", turnID, marshal(map[string]any{"text": text}))
		if _, err := m.store.Append(id, "turn.started", turnID, marshal(map[string]any{"text": text})); err != nil {
			return session.Session{}, err
		}
	} else {
		_, _ = m.store.Append(id, "message.user.steer", turnID, marshal(map[string]any{"text": text}))
	}
	if err := run.adapter.Prompt(text, steer); err != nil {
		_, _ = m.store.Append(id, session.EventTurnFailed, turnID, marshal(session.TurnTerminalEventData{Error: err.Error()}))
		run.setTurn("")
		return session.Session{}, err
	}
	return m.store.Get(id)
}

func (m *Manager) Interrupt(id string) error {
	m.mu.Lock()
	run := m.running[id]
	m.mu.Unlock()
	if run == nil {
		return errors.New("session provider is not running")
	}
	if err := run.adapter.Interrupt(); err != nil {
		return err
	}
	if turnID := run.turn(); turnID != "" {
		_, _ = m.store.Append(id, session.EventTurnCancelled, turnID, marshal(session.TurnTerminalEventData{Reason: "interrupted"}))
		run.setTurn("")
	}
	return nil
}

func (m *Manager) Stop(id string) error {
	m.mu.Lock()
	run := m.running[id]
	value, err := m.store.Get(id)
	if err != nil {
		m.mu.Unlock()
		return err
	}
	if value.State == session.StateArchived {
		m.mu.Unlock()
		// Archived sessions are read-only; stopping is a no-op.
		return nil
	}
	if value.State == session.StateStopped && run == nil {
		m.mu.Unlock()
		return nil
	}
	if run != nil {
		run.markStopping(session.StopReasonRequested)
	}
	if value.State != session.StateStopping {
		if _, err = m.store.Append(id, "session.state", "", marshal(session.StateEventData{State: session.StateStopping})); err != nil {
			m.mu.Unlock()
			return err
		}
	}
	if run == nil {
		m.convergeStored(id, session.StopReasonRequested, nil)
		m.mu.Unlock()
		return nil
	}
	m.mu.Unlock()
	closeErr := run.adapter.Close()
	_ = run.waitReady()
	if closeErr == nil {
		m.convergeActive(id, run, nil)
	} else {
		_, _ = m.store.Append(id, "provider.error", run.turn(), marshal(map[string]any{
			"message": closeErr.Error(), "reason": "process_cleanup_error",
		}))
	}
	return closeErr
}

// IsRunning reports whether the provider process for a session is
// currently running under this daemon.
func (m *Manager) IsRunning(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.running[id] != nil
}

// ApprovalReply is the public reply to a pending approval. Exactly one mode
// applies: Text sends a custom free-text reply (the question is dismissed and
// the text is delivered as the next user message once the current turn
// closes), OptionID selects one of the options offered by the request, and
// Decision selects a coarse outcome.
type ApprovalReply struct {
	Decision string
	OptionID string
	Text     string
}

func (m *Manager) Approve(id, approvalID string, reply ApprovalReply) error {
	m.mu.Lock()
	run := m.running[id]
	m.mu.Unlock()
	if run == nil {
		return errors.New("session provider is not running; approval cannot survive daemon restart")
	}
	if reply.Text != "" {
		// No provider protocol carries free text inside an approval response,
		// so a custom reply dismisses the question and is queued for delivery
		// as a regular user message when the turn closes (see providerEvent).
		if err := run.adapter.Approve(approvalID, provider.ApprovalResolution{Decision: "cancel"}); err != nil {
			return err
		}
		run.mu.Lock()
		run.replies = append(run.replies, reply.Text)
		run.mu.Unlock()
		_, err := m.store.Append(id, "approval.resolved", run.turn(), marshal(map[string]any{
			"approvalId": approvalID, "decision": "text", "text": reply.Text,
		}))
		return err
	}
	resolution := provider.ApprovalResolution{Decision: reply.Decision, OptionID: reply.OptionID}
	if err := run.adapter.Approve(approvalID, resolution); err != nil {
		return err
	}
	data := map[string]any{"approvalId": approvalID, "decision": reply.Decision}
	if reply.OptionID != "" {
		data["optionId"] = reply.OptionID
	}
	_, err := m.store.Append(id, "approval.resolved", run.turn(), marshal(data))
	return err
}

func (m *Manager) Close() {
	m.mu.Lock()
	ids := make([]string, 0, len(m.running))
	for id := range m.running {
		ids = append(ids, id)
	}
	m.mu.Unlock()
	for _, id := range ids {
		_ = m.Stop(id)
	}
}

func (m *Manager) ensure(id string) (*active, error) {
	m.mu.Lock()
	if run := m.running[id]; run != nil {
		m.mu.Unlock()
		if err := run.waitReady(); err != nil {
			return nil, err
		}
		return run, nil
	}
	value, err := m.store.Get(id)
	if err != nil {
		m.mu.Unlock()
		return nil, err
	}
	if value.State == session.StateArchived {
		m.mu.Unlock()
		return nil, session.ErrArchived
	}
	if value.State == session.StateStopping {
		m.mu.Unlock()
		return nil, errors.New("session provider is stopping")
	}
	_, _ = m.store.Append(id, "session.state", "", marshal(session.StateEventData{State: session.StateStarting}))
	cfg := cloneConfig(m.cfg)
	if value.AgentName == "" {
		err := fmt.Errorf("session %s has no agent: it was created before explicit agent selection and cannot be started; create a new session with an explicit agent", id)
		m.convergeStored(id, session.StopReasonStartupError, err)
		m.mu.Unlock()
		return nil, err
	}
	agent, providerConfig, err := resolveAgent(cfg, value.AgentName)
	if err != nil {
		err = fmt.Errorf("session %s: %w", id, err)
		m.convergeStored(id, session.StopReasonStartupError, err)
		m.mu.Unlock()
		return nil, err
	}
	run := &active{turnID: value.CurrentTurnID, ready: make(chan struct{})}
	adapter, err := m.factory(provider.Options{
		ID: id, Cwd: value.Cwd, Title: value.Title, Agent: agent, Provider: providerConfig,
		Environment: cloneEnvironment(value.LaunchEnvironment),
		Hooks: provider.Hooks{
			NativeID: func(nativeID string) {
				run.withEvent(func(_ string) {
					_, _ = m.store.Append(id, "session.provider", "", marshal(session.ProviderEventData{
						AgentName: agent.Name, Provider: providerConfig.Type, ProviderSessionID: nativeID,
					}))
				})
			},
			Event: func(event provider.Event) { m.providerEvent(id, run, event) },
			Approval: func(approvalID, method string, params json.RawMessage) {
				run.withEvent(func(turnID string) {
					_, _ = m.store.Append(id, "approval.requested", turnID, marshal(map[string]any{
						"approvalId": approvalID, "method": method, "params": params,
					}))
				})
			},
			ProcessStart: func(info provider.ProcessInfo) error {
				_, err := m.store.Append(id, "provider.process.started", "", marshal(session.ProviderProcessEventData{
					PID: info.PID, ProcessGroupID: info.ProcessGroupID,
				}))
				return err
			},
			ProcessEnd: func(processErr error) {
				_ = run.waitReady()
				// Wait confirms the group leader. Close also eliminates and
				// probes the full process group before stopped is publishable.
				if cleanupErr := run.adapter.Close(); cleanupErr != nil {
					_, _ = m.store.Append(id, "session.state", "", marshal(session.StateEventData{State: session.StateStopping}))
					_, _ = m.store.Append(id, "provider.error", run.turn(), marshal(map[string]any{
						"message": cleanupErr.Error(), "reason": "process_cleanup_error",
					}))
					return
				}
				m.convergeActive(id, run, processErr)
			},
		},
	})
	if err != nil {
		m.convergeStored(id, session.StopReasonStartupError, err)
		m.mu.Unlock()
		return nil, err
	}
	run.adapter = adapter
	m.running[id] = run
	m.mu.Unlock()

	if err := adapter.Start(value.ProviderSessionID); err != nil {
		run.finishStart(err)
		_ = adapter.Close()
		m.convergeActive(id, run, err)
		return nil, err
	}
	_, _ = m.store.Append(id, "session.state", "", marshal(session.StateEventData{State: session.StateReady}))
	run.finishStart(nil)
	return run, nil
}

func (m *Manager) providerEvent(id string, run *active, event provider.Event) {
	var replies []string
	run.withEvent(func(turnID string) {
		if event.Type != "" {
			_, _ = m.store.Append(id, event.Type, turnID, marshal(event.Data))
		}
		if !event.TurnDone || turnID == "" {
			return
		}
		eventType := session.EventTurnCompleted
		terminal := session.TurnTerminalEventData{}
		approvalReason := session.StopReasonCompleted
		if event.TurnFailed {
			eventType = session.EventTurnFailed
			terminal.Error = providerEventMessage(event.Data)
			approvalReason = session.StopReasonProviderError
		}
		// A canonical turn terminal is also the closure boundary for every
		// approval belonging to that turn. In particular, an RPC waiter can
		// observe a crashed provider before the process Wait callback runs.
		// Close approvals here so clients never see turn.failed followed by
		// a still-pending approval while ProcessEnd catches up.
		if value, err := m.store.Get(id); err == nil {
			for _, approvalID := range value.PendingApprovalIDs {
				_, _ = m.store.Append(id, "approval.resolved", turnID, marshal(map[string]any{
					"approvalId": approvalID, "decision": "cancel", "reason": approvalReason,
				}))
			}
		}
		_, _ = m.store.Append(id, eventType, turnID, marshal(terminal))
		run.turnID = ""
		replies = run.replies
		run.replies = nil
	})
	// Replies are delivered from a separate goroutine: providerEvent runs on
	// the provider read loop, and some adapters send prompts synchronously,
	// which would deadlock the loop that feeds their own responses.
	if len(replies) > 0 {
		go func() {
			for _, reply := range replies {
				m.deliverReply(id, reply)
			}
		}()
	}
}

// deliverReply sends a queued custom approval reply as a regular user
// message. The turn that carried the question has already closed, so the
// reply starts a fresh turn; a session that stopped in the meantime is not
// resurrected, and the recorded approval.resolved event keeps the text
// visible for a manual resend.
func (m *Manager) deliverReply(id, text string) {
	value, err := m.store.Get(id)
	if err != nil {
		return
	}
	if value.State == session.StateStopping || value.State == session.StateStopped || value.State == session.StateArchived {
		return
	}
	if _, err := m.Send(id, text, false); err != nil {
		_, _ = m.store.Append(id, "provider.error", "", marshal(map[string]any{
			"message": "could not deliver queued reply: " + err.Error(),
		}))
	}
}

func providerEventMessage(data any) string {
	switch value := data.(type) {
	case map[string]any:
		message, _ := value["message"].(string)
		return message
	case struct{ Message string }:
		return value.Message
	default:
		return ""
	}
}

func (m *Manager) convergeActive(id string, run *active, processErr error) {
	run.finalize.Do(func() {
		run.beginFinalizing()
		reason, cause := run.outcome(processErr)
		m.mu.Lock()
		if m.running[id] == run {
			delete(m.running, id)
		}
		m.convergeStored(id, reason, cause)
		m.mu.Unlock()
		run.setTurn("")
	})
}

// convergeStored is the single terminal path for every confirmed provider
// exit. The event order is stable: error, pending approvals, open turn, and
// finally the stopped boundary with a machine-readable reason.
func (m *Manager) convergeStored(id, reason string, cause error) {
	value, err := m.store.Get(id)
	if err != nil || value.State == session.StateArchived {
		return
	}
	if value.State == session.StateStopped && value.CurrentTurnID == "" && len(value.PendingApprovalIDs) == 0 {
		return
	}
	if cause != nil {
		_, _ = m.store.Append(id, "provider.error", value.CurrentTurnID, marshal(map[string]any{
			"message": cause.Error(), "reason": reason,
		}))
	}
	for _, approvalID := range value.PendingApprovalIDs {
		_, _ = m.store.Append(id, "approval.resolved", value.CurrentTurnID, marshal(map[string]any{
			"approvalId": approvalID, "decision": "cancel", "reason": reason,
		}))
	}
	if value.CurrentTurnID != "" {
		eventType := session.EventTurnCancelled
		data := session.TurnTerminalEventData{Reason: reason}
		if reason == session.StopReasonProviderError || reason == session.StopReasonStartupError {
			eventType = session.EventTurnFailed
			if cause != nil {
				data.Error = cause.Error()
			}
		}
		_, _ = m.store.Append(id, eventType, value.CurrentTurnID, marshal(data))
	}
	_, _ = m.store.Append(id, "session.state", "", marshal(session.StateEventData{
		State: session.StateStopped, Reason: reason,
	}))
}

func (m *Manager) recover(value session.Session) {
	if value.State == session.StateArchived {
		return
	}
	process, open, processErr := m.store.OpenProviderProcess(value.ID)
	needsRecovery := value.State == session.StateStarting ||
		value.State == session.StateBusy ||
		value.State == session.StateWaitingApproval ||
		value.State == session.StateStopping ||
		open ||
		value.CurrentTurnID != "" ||
		len(value.PendingApprovalIDs) > 0
	if !needsRecovery {
		return
	}
	if value.State != session.StateStopping {
		_, _ = m.store.Append(value.ID, "session.state", "", marshal(session.StateEventData{State: session.StateStopping}))
	}
	err := processErr
	if err == nil && open {
		err = provider.TerminateProcessGroup(process.PID, process.ProcessGroupID)
	}
	if err != nil {
		_, _ = m.store.Append(value.ID, "provider.error", value.CurrentTurnID, marshal(map[string]any{
			"message": "daemon recovery could not confirm provider exit: " + err.Error(),
			"reason":  session.StopReasonDaemonRecovery,
		}))
		return
	}
	m.convergeStored(value.ID, session.StopReasonDaemonRecovery, errors.New("provider work was interrupted by daemon restart"))
}

func resolveAgent(cfg config.Config, reference string) (config.Agent, config.Provider, error) {
	return cfg.Agent(reference)
}

func marshal(value any) []byte {
	data, _ := json.Marshal(value)
	return data
}

func cloneEnvironment(value map[string]string) map[string]string {
	if value == nil {
		return nil
	}
	cloned := make(map[string]string, len(value))
	for key, entry := range value {
		cloned[key] = entry
	}
	return cloned
}

func cloneConfig(value config.Config) config.Config {
	data, _ := json.Marshal(value)
	var result config.Config
	_ = json.Unmarshal(data, &result)
	return result
}

func (m *Manager) String() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return fmt.Sprintf("%d running sessions", len(m.running))
}
