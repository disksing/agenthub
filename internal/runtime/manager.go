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
	mu       sync.Mutex
	adapter  provider.Session
	turnID   string
	ready    chan struct{}
	startErr error
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
		if value.State == session.StateBusy || value.State == session.StateWaitingApproval || value.State == session.StateStarting {
			_, _ = store.Append(value.ID, "session.state", "", marshal(session.StateEventData{State: session.StateReady}))
		}
	}
	return manager
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
		_, _ = m.store.Append(id, "session.state", "", marshal(session.StateEventData{State: session.StateFailed}))
		_, _ = m.store.Append(id, "provider.error", "", marshal(map[string]any{"message": err.Error()}))
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
	delete(m.running, id)
	m.mu.Unlock()
	if run != nil {
		_ = run.adapter.Close()
	}
	_, err := m.store.Append(id, "session.state", "", marshal(session.StateEventData{State: session.StateStopped}))
	return err
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
	runs := m.running
	m.running = make(map[string]*active)
	m.mu.Unlock()
	for _, run := range runs {
		_ = run.adapter.Close()
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
	cfg := cloneConfig(m.cfg)
	if value.AgentID == "" {
		m.mu.Unlock()
		return nil, fmt.Errorf("session %s has no agent: it was created before explicit agent selection and cannot be started; create a new session with an explicit agent", id)
	}
	agent, providerConfig, err := cfg.Agent(value.AgentID)
	if err != nil {
		m.mu.Unlock()
		return nil, err
	}
	run := &active{turnID: value.CurrentTurnID, ready: make(chan struct{})}
	adapter, err := m.factory(provider.Options{
		ID: id, Cwd: value.Cwd, Title: value.Title, Agent: agent, Provider: providerConfig,
		Hooks: provider.Hooks{
			NativeID: func(nativeID string) {
				_, _ = m.store.Append(id, "session.provider", "", marshal(session.ProviderEventData{
					AgentID: agent.ID, Provider: providerConfig.Type, ProviderSessionID: nativeID,
				}))
			},
			Event: func(event provider.Event) { m.providerEvent(id, run, event) },
			Approval: func(approvalID, method string, params json.RawMessage) {
				_, _ = m.store.Append(id, "approval.requested", run.turn(), marshal(map[string]any{
					"approvalId": approvalID, "method": method, "params": params,
				}))
			},
			ProcessEnd: func(processErr error) {
				m.mu.Lock()
				if m.running[id] == run {
					delete(m.running, id)
				}
				m.mu.Unlock()
				if processErr != nil {
					_, _ = m.store.Append(id, "provider.error", run.turn(), marshal(map[string]any{"message": processErr.Error()}))
				}
			},
		},
	})
	if err != nil {
		m.mu.Unlock()
		return nil, err
	}
	run.adapter = adapter
	m.running[id] = run
	m.mu.Unlock()

	_, _ = m.store.Append(id, "session.state", "", marshal(session.StateEventData{State: session.StateStarting}))
	if err := adapter.Start(value.ProviderSessionID); err != nil {
		run.finishStart(err)
		m.mu.Lock()
		delete(m.running, id)
		m.mu.Unlock()
		_ = adapter.Close()
		return nil, err
	}
	run.finishStart(nil)
	_, _ = m.store.Append(id, "session.state", "", marshal(session.StateEventData{State: session.StateReady}))
	return run, nil
}

func (m *Manager) providerEvent(id string, run *active, event provider.Event) {
	turnID := run.turn()
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
	run.setTurn("")
}

func marshal(value any) []byte {
	data, _ := json.Marshal(value)
	return data
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
