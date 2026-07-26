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
	// legacyAgentNames maps agent ids recorded by old sessions to the agent
	// names that replaced them. It is captured when a legacy config file is
	// migrated (see config.LoadLegacyAgentIDs) and lets those sessions start
	// again without guessing.
	legacyAgentNames map[string]string
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

func New(store *session.Store, cfg config.Config, legacyAgentNames ...map[string]string) *Manager {
	manager := &Manager{store: store, cfg: cfg, running: make(map[string]*active), factory: provider.New}
	if len(legacyAgentNames) > 0 {
		manager.legacyAgentNames = legacyAgentNames[0]
	}
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
		_, _ = m.store.Append(id, "turn.failed", turnID, marshal(map[string]any{"error": err.Error()}))
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
		_, _ = m.store.Append(id, "turn.cancelled", turnID, marshal(map[string]any{"reason": "interrupted"}))
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

func (m *Manager) Approve(id, approvalID, decision string) error {
	m.mu.Lock()
	run := m.running[id]
	m.mu.Unlock()
	if run == nil {
		return errors.New("session provider is not running; approval cannot survive daemon restart")
	}
	if err := run.adapter.Approve(approvalID, decision); err != nil {
		return err
	}
	_, err := m.store.Append(id, "approval.resolved", run.turn(), marshal(map[string]any{"approvalId": approvalID, "decision": decision}))
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
	legacyNames := m.legacyAgentNames
	if value.AgentName == "" {
		err := fmt.Errorf("session %s has no agent: it was created before explicit agent selection and cannot be started; create a new session with an explicit agent", id)
		m.convergeStored(id, session.StopReasonStartupError, err)
		m.mu.Unlock()
		return nil, err
	}
	agent, providerConfig, err := resolveAgent(cfg, legacyNames, value.AgentName)
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
	run.withEvent(func(turnID string) {
		if event.Type != "" {
			_, _ = m.store.Append(id, event.Type, turnID, marshal(event.Data))
		}
		if !event.TurnDone || turnID == "" {
			return
		}
		eventType := "turn.completed"
		if event.TurnFailed {
			eventType = "turn.failed"
		}
		_, _ = m.store.Append(id, eventType, turnID, marshal(event.Data))
		run.turnID = ""
	})
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
		eventType := "turn.cancelled"
		data := map[string]any{"reason": reason}
		if reason == session.StopReasonProviderError || reason == session.StopReasonStartupError {
			eventType = "turn.failed"
			if cause != nil {
				data["error"] = cause.Error()
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
		value.State == session.StateFailed ||
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

// resolveAgent finds the agent a session refers to. Current sessions store
// the agent name and resolve directly (case-insensitively). Sessions
// recorded before agent ids were removed store the old id; those resolve
// through the id → name mapping captured during config migration, and only
// when the mapped name still exists. Anything else fails with a clear error
// instead of being guessed onto a different agent.
func resolveAgent(cfg config.Config, legacyNames map[string]string, reference string) (config.Agent, config.Provider, error) {
	agent, providerConfig, err := cfg.Agent(reference)
	if err == nil {
		return agent, providerConfig, nil
	}
	if mapped, ok := legacyNames[reference]; ok {
		if agent, providerConfig, mappedErr := cfg.Agent(mapped); mappedErr == nil {
			return agent, providerConfig, nil
		} else {
			return config.Agent{}, config.Provider{}, fmt.Errorf("agent %q was migrated to %q, which is no longer available: %w", reference, mapped, mappedErr)
		}
	}
	return config.Agent{}, config.Provider{}, err
}

// ResolveLegacyAgentName maps an agent id from a migrated legacy
// configuration to its replacement name, reporting whether the mapping
// exists. It backs the one-time compatibility path for clients that still
// submit agentId when creating a session.
func (m *Manager) ResolveLegacyAgentName(id string) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	name, ok := m.legacyAgentNames[id]
	return name, ok
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
