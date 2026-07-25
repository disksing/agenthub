package runtime

import (
	"encoding/json"
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

func TestManagerRoutesRunsPersistsAndResumes(t *testing.T) {
	root := t.TempDir()
	store, err := session.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		Version: 1, DefaultChatAgentID: "slow",
		AgentProviders: []config.Provider{{ID: "provider", Type: "pi", Enabled: true}},
		Agents:         []config.Agent{{ID: "slow", ProviderID: "provider"}, {ID: "fast-agent", ProviderID: "provider"}},
		AgentProfiles:  []config.Profile{{Key: "fast", AgentID: "fast-agent"}},
	}
	value, err := store.Create(session.CreateInput{Cwd: t.TempDir(), Selection: session.Selection{Mode: "auto", RequestedTags: []string{"fast"}}})
	if err != nil {
		t.Fatal(err)
	}
	manager := New(store, cfg)
	var created []*fakeSession
	manager.factory = func(options provider.Options) (provider.Session, error) {
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

func mustJSON(value any) []byte {
	data, _ := json.Marshal(value)
	return data
}
