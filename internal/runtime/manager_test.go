package runtime

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/disksing/agenthub/internal/config"
	"github.com/disksing/agenthub/internal/provider"
	"github.com/disksing/agenthub/internal/session"
)

type fakeSession struct {
	hooks    provider.Hooks
	resumeID string
	prompts  []string
	mu       sync.Mutex
}

func (f *fakeSession) Start(resumeID string) error {
	f.resumeID = resumeID
	f.hooks.NativeID("native-session")
	return nil
}
func (f *fakeSession) Prompt(text string, _ bool) error {
	f.mu.Lock()
	f.prompts = append(f.prompts, text)
	f.mu.Unlock()
	f.hooks.Event(provider.Event{Type: "message.assistant.delta", Data: map[string]any{"text": "answer"}})
	f.hooks.Event(provider.Event{Type: "provider.turn.completed", TurnDone: true})
	return nil
}
func (f *fakeSession) Interrupt() error          { return nil }
func (f *fakeSession) Approve(_, _ string) error { return nil }
func (f *fakeSession) Close() error              { return nil }

func testConfig() config.Config {
	return config.Config{
		Version:        1,
		AgentProviders: []config.Provider{{ID: "provider", Type: "pi", Enabled: true}},
		Agents:         []config.Agent{{ID: "slow", ProviderID: "provider"}, {ID: "fast-agent", ProviderID: "provider"}},
	}
}

func TestManagerRunsExplicitAgentAndResumes(t *testing.T) {
	root := t.TempDir()
	store, err := session.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	cfg := testConfig()
	value, err := store.Create(session.CreateInput{Cwd: t.TempDir(), AgentID: "fast-agent"})
	if err != nil {
		t.Fatal(err)
	}
	manager := New(store, cfg)
	var created []*fakeSession
	manager.factory = func(options provider.Options) (provider.Session, error) {
		if options.Agent.ID != "fast-agent" || options.Provider.ID != "provider" {
			t.Errorf("factory received wrong agent/provider: %+v %+v", options.Agent, options.Provider)
		}
		value := &fakeSession{hooks: options.Hooks}
		created = append(created, value)
		return value, nil
	}
	result, err := manager.Send(value.ID, "question", false)
	if err != nil {
		t.Fatal(err)
	}
	if result.AgentID != "fast-agent" || result.ProviderSessionID != "native-session" || result.State != session.StateReady {
		t.Fatalf("unexpected result: %+v", result)
	}
	events, err := store.EventsAfter(value.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var types []string
	for _, event := range events {
		types = append(types, event.Type)
	}
	expected := []string{"session.created", "session.state", "session.provider", "session.state", "message.user", "turn.started", "message.assistant.delta", "provider.turn.completed", "turn.completed"}
	if string(mustJSON(types)) != string(mustJSON(expected)) {
		t.Fatalf("event types = %v", types)
	}

	manager.Close()
	reopened, err := session.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	resumed := New(reopened, cfg)
	var second *fakeSession
	resumed.factory = func(options provider.Options) (provider.Session, error) {
		second = &fakeSession{hooks: options.Hooks}
		return second, nil
	}
	if _, err := resumed.Send(value.ID, "again", false); err != nil {
		t.Fatal(err)
	}
	if second.resumeID != "native-session" {
		t.Fatalf("resume id = %q", second.resumeID)
	}
}

func TestManagerRejectsUnknownAgent(t *testing.T) {
	store, err := session.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	value, err := store.Create(session.CreateInput{Cwd: t.TempDir(), AgentID: "ghost"})
	if err != nil {
		t.Fatal(err)
	}
	manager := New(store, testConfig())
	manager.factory = func(options provider.Options) (provider.Session, error) {
		return &fakeSession{hooks: options.Hooks}, nil
	}
	if _, err := manager.Start(value.ID); err == nil || !strings.Contains(err.Error(), "unknown agent") {
		t.Fatalf("expected an unknown agent error, got %v", err)
	}
}

// Sessions created before explicit agent selection (auto routing) and never
// started have no determinable agent. They must fail clearly instead of being
// guessed onto some configured agent.
func TestManagerRejectsLegacySessionWithoutAgent(t *testing.T) {
	store, err := session.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	value, err := store.Create(session.CreateInput{Cwd: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	manager := New(store, testConfig())
	manager.factory = func(options provider.Options) (provider.Session, error) {
		return &fakeSession{hooks: options.Hooks}, nil
	}
	if _, err := manager.Start(value.ID); err == nil || !strings.Contains(err.Error(), "no agent") {
		t.Fatalf("expected a clear missing-agent error, got %v", err)
	}
}

func mustJSON(value any) []byte {
	data, _ := json.Marshal(value)
	return data
}
