package runtime

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/disksing/agenthub/internal/config"
	"github.com/disksing/agenthub/internal/provider"
	"github.com/disksing/agenthub/internal/session"
)

type fakeSession struct {
	hooks        provider.Hooks
	resumeID     string
	prompts      []string
	resolution   provider.ApprovalResolution
	startErr     error
	onClose      func()
	holdTurn     bool
	closeStarted chan struct{}
	closeRelease chan struct{}
	closeOnce    sync.Once
	mu           sync.Mutex
}

type sourceAwareFakeSession struct {
	*fakeSession
	inputs []session.MessageInput
}

func (f *sourceAwareFakeSession) PromptMessage(input session.MessageInput) error {
	f.mu.Lock()
	f.inputs = append(f.inputs, input)
	f.mu.Unlock()
	return f.fakeSession.Prompt(input.Text, input.Steer)
}

func (f *fakeSession) Start(resumeID string) error {
	f.resumeID = resumeID
	if f.startErr != nil {
		return f.startErr
	}
	f.hooks.NativeID("native-session")
	return nil
}
func (f *fakeSession) Prompt(text string, _ bool) error {
	f.mu.Lock()
	f.prompts = append(f.prompts, text)
	f.mu.Unlock()
	f.hooks.Event(provider.Event{Type: "message.assistant.delta", Data: map[string]any{"text": "answer"}})
	if f.holdTurn {
		return nil
	}
	f.hooks.Event(provider.Event{
		Type:     "provider.turn.completed",
		Data:     map[string]any{"nativeTurnId": "provider-private"},
		TurnDone: true,
	})
	return nil
}
func (f *fakeSession) Interrupt() error { return nil }
func (f *fakeSession) Approve(_ string, resolution provider.ApprovalResolution) error {
	f.mu.Lock()
	f.resolution = resolution
	f.mu.Unlock()
	return nil
}
func (f *fakeSession) Close() error {
	if f.closeStarted != nil {
		f.closeOnce.Do(func() { close(f.closeStarted) })
	}
	if f.closeRelease != nil {
		<-f.closeRelease
	}
	if f.onClose != nil {
		f.onClose()
	}
	return nil
}

func testConfig() config.Config {
	return config.Config{
		Version:        1,
		AgentProviders: []config.Provider{{ID: "provider", Type: "pi", Enabled: true}},
		Agents:         []config.Agent{{Name: "Slow", ProviderID: "provider"}, {Name: "Fast Agent", ProviderID: "provider"}},
	}
}

func TestManagerRunsExplicitAgentAndResumes(t *testing.T) {
	root := t.TempDir()
	store, err := session.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	cfg := testConfig()
	value, err := store.Create(session.CreateInput{
		Cwd:               t.TempDir(),
		AgentName:         "Fast Agent",
		LaunchEnvironment: map[string]string{"FORGE_SESSION_ID": "forge-session-resume"},
	})
	if err != nil {
		t.Fatal(err)
	}
	manager := New(store, cfg)
	var created []*fakeSession
	manager.factory = func(options provider.Options) (provider.Session, error) {
		if options.Agent.Name != "Fast Agent" || options.Provider.ID != "provider" {
			t.Errorf("factory received wrong agent/provider: %+v %+v", options.Agent, options.Provider)
		}
		if options.Environment["FORGE_SESSION_ID"] != "forge-session-resume" {
			t.Errorf("factory environment = %+v", options.Environment)
		}
		value := &fakeSession{hooks: options.Hooks}
		created = append(created, value)
		return value, nil
	}
	result, err := manager.Send(value.ID, "question", false)
	if err != nil {
		t.Fatal(err)
	}
	if result.AgentName != "Fast Agent" || result.ProviderSessionID != "native-session" || result.State != session.StateReady {
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
	expected := []string{"session.created", "session.state", "session.provider", "session.state", "message.input", "turn.started", "message.assistant.delta", "provider.turn.completed", "turn.completed"}
	if string(mustJSON(types)) != string(mustJSON(expected)) {
		t.Fatalf("event types = %v", types)
	}
	if got := string(events[len(events)-1].Data); got != "{}" {
		t.Fatalf("canonical turn.completed payload = %s, want provider-independent {}", got)
	}
	if got := string(events[len(events)-2].Data); !strings.Contains(got, "provider-private") {
		t.Fatalf("provider-native diagnostic payload was not preserved: %s", got)
	}

	manager.Close()
	reopened, err := session.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	resumed := New(reopened, cfg)
	var second *fakeSession
	resumed.factory = func(options provider.Options) (provider.Session, error) {
		if options.Environment["FORGE_SESSION_ID"] != "forge-session-resume" {
			t.Errorf("resumed factory environment = %+v", options.Environment)
		}
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

func TestManagerPersistsAndDeliversCanonicalSourceMessage(t *testing.T) {
	root := t.TempDir()
	store, err := session.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	value, err := store.Create(session.CreateInput{Cwd: t.TempDir(), AgentName: "Fast Agent"})
	if err != nil {
		t.Fatal(err)
	}
	manager := New(store, testConfig())
	var adapter *sourceAwareFakeSession
	manager.factory = func(options provider.Options) (provider.Session, error) {
		adapter = &sourceAwareFakeSession{fakeSession: &fakeSession{hooks: options.Hooks}}
		return adapter, nil
	}
	input := session.MessageInput{
		Text:   "wake the worker",
		Role:   session.MessageRoleAgent,
		Sender: &session.MessageSender{Name: "Scheduler", SessionID: "ses_scheduler"},
	}
	if _, err := manager.SendMessage(value.ID, input); err != nil {
		t.Fatal(err)
	}
	if len(adapter.inputs) != 1 || adapter.inputs[0].Role != session.MessageRoleAgent ||
		adapter.inputs[0].Sender == nil || adapter.inputs[0].Sender.SessionID != "ses_scheduler" {
		t.Fatalf("source-aware prompt input = %+v", adapter.inputs)
	}
	events, err := store.EventsAfter(value.ID, 0, 20)
	if err != nil {
		t.Fatal(err)
	}
	var found session.MessageInput
	for _, event := range events {
		if event.Type == session.EventMessageInput {
			if err := json.Unmarshal(event.Data, &found); err != nil {
				t.Fatal(err)
			}
			if string(event.Data) == "" || event.TurnID == "" {
				t.Fatalf("canonical input event = %+v", event)
			}
		}
		if event.Type == "turn.started" && len(event.Data) != 0 {
			t.Fatalf("turn.started must remain lifecycle-only: %s", event.Data)
		}
	}
	if found.Role != session.MessageRoleAgent || found.Text != input.Text || found.Sender == nil ||
		found.Sender.Name != "Scheduler" || found.Sender.SessionID != "ses_scheduler" {
		t.Fatalf("persisted message input = %+v", found)
	}
}

// TestManagerStartSeesUpdatedLaunchEnvironment pins the ordering the resume
// endpoint relies on: the environment overlay is durable before the runtime
// starts, so the provider factory observes the merged environment — the new
// FORGE_SESSION_ID plus every key the overlay did not mention.
func TestManagerStartSeesUpdatedLaunchEnvironment(t *testing.T) {
	store, err := session.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	value, err := store.Create(session.CreateInput{
		Cwd:               t.TempDir(),
		AgentName:         "Fast Agent",
		LaunchEnvironment: map[string]string{"FORGE_SESSION_ID": "forge-old", "KEEP": "original"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateLaunchEnvironment(value.ID, map[string]string{"FORGE_SESSION_ID": "forge-new"}); err != nil {
		t.Fatal(err)
	}
	manager := New(store, testConfig())
	defer manager.Close()
	manager.factory = func(options provider.Options) (provider.Session, error) {
		if options.Environment["FORGE_SESSION_ID"] != "forge-new" {
			t.Errorf("factory FORGE_SESSION_ID = %q, want forge-new (%+v)", options.Environment["FORGE_SESSION_ID"], options.Environment)
		}
		if options.Environment["KEEP"] != "original" {
			t.Errorf("factory lost an overlaid key: %+v", options.Environment)
		}
		return &fakeSession{hooks: options.Hooks}, nil
	}
	started, err := manager.Start(value.ID)
	if err != nil {
		t.Fatal(err)
	}
	if started.LaunchEnvironment["FORGE_SESSION_ID"] != "forge-new" || started.LaunchEnvironment["KEEP"] != "original" {
		t.Fatalf("started session environment = %+v", started.LaunchEnvironment)
	}
}

func TestManagerKeepsParallelSessionEnvironmentsIndependent(t *testing.T) {
	store, err := session.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.Create(session.CreateInput{
		Cwd:               t.TempDir(),
		AgentName:         "Fast Agent",
		LaunchEnvironment: map[string]string{"FORGE_SESSION_ID": "forge-one"},
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.Create(session.CreateInput{
		Cwd:               t.TempDir(),
		AgentName:         "Fast Agent",
		LaunchEnvironment: map[string]string{"FORGE_SESSION_ID": "forge-two"},
	})
	if err != nil {
		t.Fatal(err)
	}
	manager := New(store, testConfig())
	observed := make(map[string]string)
	var observedMu sync.Mutex
	manager.factory = func(options provider.Options) (provider.Session, error) {
		observedMu.Lock()
		observed[options.ID] = options.Environment["FORGE_SESSION_ID"]
		observedMu.Unlock()
		return &fakeSession{hooks: options.Hooks}, nil
	}

	var wait sync.WaitGroup
	errorsByID := make(chan error, 2)
	for _, id := range []string{first.ID, second.ID} {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, err := manager.Start(id)
			errorsByID <- err
		}()
	}
	wait.Wait()
	close(errorsByID)
	for err := range errorsByID {
		if err != nil {
			t.Fatal(err)
		}
	}
	if observed[first.ID] != "forge-one" || observed[second.ID] != "forge-two" {
		t.Fatalf("parallel environments crossed: %+v", observed)
	}
}

func TestManagerRejectsUnknownAgent(t *testing.T) {
	store, err := session.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	value, err := store.Create(session.CreateInput{Cwd: t.TempDir(), AgentName: "ghost"})
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

// When the provider fails to start, the session must preserve a visible
// error and converge to strict stopped, the adapter must be closed (no
// orphaned provider process), and the session must not stay registered.
func TestManagerStartFailureCleansUp(t *testing.T) {
	store, err := session.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	value, err := store.Create(session.CreateInput{Cwd: t.TempDir(), AgentName: "Fast Agent"})
	if err != nil {
		t.Fatal(err)
	}
	manager := New(store, testConfig())
	closed := false
	startErr := errors.New("start Kimi Code ACP: session/new timed out after 2m0s waiting for the provider to respond")
	manager.factory = func(options provider.Options) (provider.Session, error) {
		return &fakeSession{hooks: options.Hooks, startErr: startErr, onClose: func() { closed = true }}, nil
	}
	if _, err := manager.Start(value.ID); err == nil || !strings.Contains(err.Error(), "session/new timed out") {
		t.Fatalf("expected the provider start error, got %v", err)
	}
	if !closed {
		t.Fatal("adapter was not closed after the failed start")
	}
	if manager.IsRunning(value.ID) {
		t.Fatal("session is still registered as running")
	}
	got, err := store.Get(value.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != session.StateStopped || got.StopReason != session.StopReasonStartupError {
		t.Fatalf("unexpected terminal session: %+v", got)
	}
	events, err := store.EventsAfter(value.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var types []string
	for _, event := range events {
		types = append(types, event.Type)
	}
	expected := []string{"session.created", "session.state", "provider.error", "session.state"}
	if string(mustJSON(types)) != string(mustJSON(expected)) {
		t.Fatalf("event types = %v", types)
	}
	// A startup failure carries no active work and stays archivable after
	// strict stopped convergence.
	if _, err := store.Archive(value.ID); err != nil {
		t.Fatalf("failed session should be archivable: %v", err)
	}
}

// A session without an explicit agent cannot be started.
func TestManagerRejectsSessionWithoutAgent(t *testing.T) {
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

func TestManagerTreatsArchivedSessionAsReadOnly(t *testing.T) {
	store, err := session.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	value, err := store.Create(session.CreateInput{Cwd: t.TempDir(), AgentName: "Fast Agent"})
	if err != nil {
		t.Fatal(err)
	}
	manager := New(store, testConfig())
	manager.factory = func(options provider.Options) (provider.Session, error) {
		return &fakeSession{hooks: options.Hooks}, nil
	}
	if err := manager.Stop(value.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Archive(value.ID); err != nil {
		t.Fatal(err)
	}

	if manager.IsRunning(value.ID) {
		t.Fatal("archived session must not be running")
	}
	if _, err := manager.Send(value.ID, "hello", false); !errors.Is(err, session.ErrArchived) {
		t.Fatalf("Send error = %v, want ErrArchived", err)
	}
	if _, err := manager.Start(value.ID); !errors.Is(err, session.ErrArchived) {
		t.Fatalf("Start error = %v, want ErrArchived", err)
	}
	if err := manager.Interrupt(value.ID); err == nil {
		t.Fatal("Interrupt on archived session must fail")
	}
	if err := manager.Approve(value.ID, "apr_1", ApprovalReply{Decision: "accept"}); err == nil {
		t.Fatal("Approve on archived session must fail")
	}
	// Stop stays a safe no-op and appends nothing.
	before, err := store.Get(value.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Stop(value.ID); err != nil {
		t.Fatalf("Stop on archived session = %v", err)
	}
	after, err := store.Get(value.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.LastEventID != before.LastEventID || after.State != session.StateArchived {
		t.Fatalf("Stop mutated archived session: %+v", after)
	}
}

func TestStopPublishesStoppedOnlyAfterCloseConfirmsExit(t *testing.T) {
	store, err := session.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	value, err := store.Create(session.CreateInput{Cwd: t.TempDir(), AgentName: "Fast Agent"})
	if err != nil {
		t.Fatal(err)
	}
	manager := New(store, testConfig())
	closeStarted := make(chan struct{})
	closeRelease := make(chan struct{})
	manager.factory = func(options provider.Options) (provider.Session, error) {
		return &fakeSession{hooks: options.Hooks, closeStarted: closeStarted, closeRelease: closeRelease}, nil
	}
	if _, err := manager.Start(value.ID); err != nil {
		t.Fatal(err)
	}
	stopped := make(chan error, 1)
	go func() { stopped <- manager.Stop(value.ID) }()
	<-closeStarted

	during, err := store.Get(value.ID)
	if err != nil {
		t.Fatal(err)
	}
	if during.State != session.StateStopping || during.StopReason != "" {
		t.Fatalf("session published a terminal boundary too early: %+v", during)
	}
	if !manager.IsRunning(value.ID) {
		t.Fatal("runtime forgot the provider before Close confirmed exit")
	}

	close(closeRelease)
	if err := <-stopped; err != nil {
		t.Fatal(err)
	}
	after, err := store.Get(value.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.State != session.StateStopped || after.StopReason != session.StopReasonRequested {
		t.Fatalf("unexpected stopped projection: %+v", after)
	}
	if manager.IsRunning(value.ID) {
		t.Fatal("provider remains registered after stopped")
	}
}

func TestProviderCrashClosesApprovalAndTurnBeforeStopped(t *testing.T) {
	store, err := session.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	value, err := store.Create(session.CreateInput{Cwd: t.TempDir(), AgentName: "Fast Agent"})
	if err != nil {
		t.Fatal(err)
	}
	manager := New(store, testConfig())
	var adapter *fakeSession
	manager.factory = func(options provider.Options) (provider.Session, error) {
		adapter = &fakeSession{hooks: options.Hooks, holdTurn: true}
		return adapter, nil
	}
	if _, err := manager.Send(value.ID, "question", false); err != nil {
		t.Fatal(err)
	}
	adapter.hooks.Approval("apr_crash", "tool/request", nil)
	adapter.hooks.ProcessEnd(errors.New("provider exited with status 9"))

	got, err := store.Get(value.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != session.StateStopped || got.StopReason != session.StopReasonProviderError ||
		got.CurrentTurnID != "" || len(got.PendingApprovalIDs) != 0 {
		t.Fatalf("crash did not converge safely: %+v", got)
	}
	events, err := store.EventsAfter(value.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var tail []string
	for _, event := range events[len(events)-4:] {
		tail = append(tail, event.Type)
	}
	want := []string{"provider.error", "approval.resolved", "turn.failed", "session.state"}
	if string(mustJSON(tail)) != string(mustJSON(want)) {
		t.Fatalf("terminal event order = %v, want %v", tail, want)
	}
}

func TestProviderTurnFailureClosesApprovalBeforeCanonicalTerminal(t *testing.T) {
	store, err := session.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	value, err := store.Create(session.CreateInput{Cwd: t.TempDir(), AgentName: "Fast Agent"})
	if err != nil {
		t.Fatal(err)
	}
	manager := New(store, testConfig())
	var adapter *fakeSession
	manager.factory = func(options provider.Options) (provider.Session, error) {
		adapter = &fakeSession{hooks: options.Hooks, holdTurn: true}
		return adapter, nil
	}
	if _, err := manager.Send(value.ID, "question", false); err != nil {
		t.Fatal(err)
	}
	adapter.hooks.Approval("apr_rpc", "tool/request", nil)
	adapter.hooks.Event(provider.Event{
		Type: "provider.error", Data: map[string]any{"message": "provider exited before responding"},
		TurnDone: true, TurnFailed: true,
	})

	got, err := store.Get(value.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.CurrentTurnID != "" || len(got.PendingApprovalIDs) != 0 {
		t.Fatalf("turn terminal left work open: %+v", got)
	}
	events, err := store.EventsAfter(value.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var tail []string
	for _, event := range events[len(events)-3:] {
		tail = append(tail, event.Type)
	}
	want := []string{"provider.error", "approval.resolved", "turn.failed"}
	if string(mustJSON(tail)) != string(mustJSON(want)) {
		t.Fatalf("terminal event order = %v, want %v", tail, want)
	}
}

func TestRetryableProviderErrorKeepsTurnBusyUntilCompletion(t *testing.T) {
	store, err := session.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	value, err := store.Create(session.CreateInput{Cwd: t.TempDir(), AgentName: "Fast Agent"})
	if err != nil {
		t.Fatal(err)
	}
	manager := New(store, testConfig())
	var adapter *fakeSession
	manager.factory = func(options provider.Options) (provider.Session, error) {
		adapter = &fakeSession{hooks: options.Hooks, holdTurn: true}
		return adapter, nil
	}
	if _, err := manager.Send(value.ID, "question", false); err != nil {
		t.Fatal(err)
	}
	adapter.hooks.Event(provider.Event{
		Type: "provider.error",
		Data: map[string]any{
			"message":   "Reconnecting... 2/5",
			"details":   "tls handshake eof",
			"willRetry": true,
		},
	})

	during, err := store.Get(value.ID)
	if err != nil {
		t.Fatal(err)
	}
	if during.State != session.StateBusy || during.CurrentTurnID == "" {
		t.Fatalf("retryable error closed the active turn: %+v", during)
	}
	turnID := during.CurrentTurnID
	adapter.hooks.Event(provider.Event{
		Type: "message.assistant.delta",
		Data: map[string]any{"text": "recovered"},
	})
	adapter.hooks.Event(provider.Event{
		Type:     "provider.turn.completed",
		Data:     map[string]any{"nativeTurnId": "provider-private"},
		TurnDone: true,
	})

	after, err := store.Get(value.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.State != session.StateReady || after.CurrentTurnID != "" {
		t.Fatalf("recovered turn did not complete: %+v", after)
	}
	events, err := store.EventsAfter(value.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var retryCount, failedCount, completedCount int
	for _, event := range events {
		switch event.Type {
		case "provider.error":
			retryCount++
			if event.TurnID != turnID {
				t.Fatalf("retry event turn = %q, want %q", event.TurnID, turnID)
			}
		case "message.assistant.delta":
			if event.TurnID != turnID {
				t.Fatalf("post-retry delta turn = %q, want %q", event.TurnID, turnID)
			}
		case session.EventTurnFailed:
			failedCount++
		case session.EventTurnCompleted:
			completedCount++
		}
	}
	if retryCount != 1 || failedCount != 0 || completedCount != 1 {
		t.Fatalf("retry terminal counts: provider.error=%d turn.failed=%d turn.completed=%d",
			retryCount, failedCount, completedCount)
	}
}

func TestCustomApprovalReplyIsDeliveredAfterTurnCloses(t *testing.T) {
	store, err := session.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	value, err := store.Create(session.CreateInput{Cwd: t.TempDir(), AgentName: "Fast Agent"})
	if err != nil {
		t.Fatal(err)
	}
	manager := New(store, testConfig())
	var adapter *fakeSession
	manager.factory = func(options provider.Options) (provider.Session, error) {
		adapter = &fakeSession{hooks: options.Hooks, holdTurn: true}
		return adapter, nil
	}
	if _, err := manager.Send(value.ID, "question", false); err != nil {
		t.Fatal(err)
	}
	adapter.hooks.Approval("apr_text", "session/request_permission", nil)
	if err := manager.Approve(value.ID, "apr_text", ApprovalReply{Text: "custom answer"}); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(value.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.PendingApprovalIDs) != 0 || got.CurrentTurnID == "" {
		t.Fatalf("text reply must resolve the approval and keep the turn open: %+v", got)
	}
	adapter.mu.Lock()
	prompts := len(adapter.prompts)
	adapter.mu.Unlock()
	if prompts != 1 {
		t.Fatalf("reply must wait for the open turn, prompts = %d", prompts)
	}
	adapter.hooks.Event(provider.Event{
		Type: "provider.turn.completed", Data: map[string]any{"nativeTurnId": "provider-private"}, TurnDone: true,
	})
	deadline := time.Now().Add(5 * time.Second)
	for {
		adapter.mu.Lock()
		delivered := len(adapter.prompts) == 2 && adapter.prompts[1] == "custom answer"
		adapter.mu.Unlock()
		if delivered {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("queued reply was not delivered after the turn closed")
		}
		time.Sleep(10 * time.Millisecond)
	}
	events, err := store.EventsAfter(value.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var resolved, userMessage map[string]any
	var order []string
	for _, event := range events {
		order = append(order, event.Type)
		switch event.Type {
		case "approval.resolved":
			_ = json.Unmarshal(event.Data, &resolved)
		case session.EventMessageInput:
			var data session.MessageInput
			if _ = json.Unmarshal(event.Data, &data); data.Text == "custom answer" {
				userMessage = map[string]any{"text": data.Text}
			}
		}
	}
	if resolved["decision"] != "text" || resolved["text"] != "custom answer" {
		t.Fatalf("approval.resolved = %v", resolved)
	}
	if userMessage == nil {
		t.Fatalf("reply was not recorded as a user message: %v", order)
	}
}

func TestCustomApprovalReplyIsNotDeliveredToStoppedSession(t *testing.T) {
	store, err := session.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	value, err := store.Create(session.CreateInput{Cwd: t.TempDir(), AgentName: "Fast Agent"})
	if err != nil {
		t.Fatal(err)
	}
	manager := New(store, testConfig())
	var adapter *fakeSession
	manager.factory = func(options provider.Options) (provider.Session, error) {
		adapter = &fakeSession{hooks: options.Hooks, holdTurn: true}
		return adapter, nil
	}
	if _, err := manager.Send(value.ID, "question", false); err != nil {
		t.Fatal(err)
	}
	adapter.hooks.Approval("apr_text", "session/request_permission", nil)
	if err := manager.Approve(value.ID, "apr_text", ApprovalReply{Text: "custom answer"}); err != nil {
		t.Fatal(err)
	}
	if err := manager.Stop(value.ID); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		adapter.mu.Lock()
		prompts := len(adapter.prompts)
		adapter.mu.Unlock()
		if prompts != 1 {
			t.Fatal("stopped session must not receive queued replies")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestApprovalReplySelectsExplicitOption(t *testing.T) {
	store, err := session.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	value, err := store.Create(session.CreateInput{Cwd: t.TempDir(), AgentName: "Fast Agent"})
	if err != nil {
		t.Fatal(err)
	}
	manager := New(store, testConfig())
	var adapter *fakeSession
	manager.factory = func(options provider.Options) (provider.Session, error) {
		adapter = &fakeSession{hooks: options.Hooks, holdTurn: true}
		return adapter, nil
	}
	if _, err := manager.Send(value.ID, "question", false); err != nil {
		t.Fatal(err)
	}
	adapter.hooks.Approval("apr_option", "session/request_permission", nil)
	if err := manager.Approve(value.ID, "apr_option", ApprovalReply{Decision: "accept", OptionID: "q0_opt_1"}); err != nil {
		t.Fatal(err)
	}
	adapter.mu.Lock()
	resolution := adapter.resolution
	adapter.mu.Unlock()
	if resolution.Decision != "accept" || resolution.OptionID != "q0_opt_1" {
		t.Fatalf("adapter received %+v", resolution)
	}
	events, err := store.EventsAfter(value.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var resolved map[string]any
	for _, event := range events {
		if event.Type == "approval.resolved" {
			_ = json.Unmarshal(event.Data, &resolved)
		}
	}
	if resolved["decision"] != "accept" || resolved["optionId"] != "q0_opt_1" {
		t.Fatalf("approval.resolved = %v", resolved)
	}
}

func TestStopAndNaturalExitRaceProducesOneStoppedBoundary(t *testing.T) {
	for attempt := 0; attempt < 25; attempt++ {
		store, err := session.Open(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		value, err := store.Create(session.CreateInput{Cwd: t.TempDir(), AgentName: "Fast Agent"})
		if err != nil {
			t.Fatal(err)
		}
		manager := New(store, testConfig())
		var adapter *fakeSession
		manager.factory = func(options provider.Options) (provider.Session, error) {
			adapter = &fakeSession{hooks: options.Hooks}
			return adapter, nil
		}
		if _, err := manager.Start(value.ID); err != nil {
			t.Fatal(err)
		}
		var group sync.WaitGroup
		group.Add(2)
		go func() {
			defer group.Done()
			_ = manager.Stop(value.ID)
		}()
		go func() {
			defer group.Done()
			adapter.hooks.ProcessEnd(nil)
		}()
		group.Wait()
		events, err := store.EventsAfter(value.ID, 0, 100)
		if err != nil {
			t.Fatal(err)
		}
		stopped := 0
		for _, event := range events {
			if event.Type != "session.state" {
				continue
			}
			var data session.StateEventData
			if json.Unmarshal(event.Data, &data) == nil && data.State == session.StateStopped {
				stopped++
			}
		}
		if stopped != 1 {
			t.Fatalf("attempt %d produced %d stopped events: %+v", attempt, stopped, events)
		}
		got, err := store.Get(value.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.State != session.StateStopped {
			t.Fatalf("attempt %d did not converge: %+v", attempt, got)
		}
	}
}

func TestDaemonRecoveryKillsRecordedProcessGroupAndClosesWork(t *testing.T) {
	root := t.TempDir()
	store, err := session.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	value, err := store.Create(session.CreateInput{Cwd: t.TempDir(), AgentName: "Fast Agent"})
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(t.TempDir(), "writes")
	cmd := exec.Command("sh", "-c", `trap '' TERM; while :; do printf x >> "$1"; sleep 0.02; done`, "sh", marker)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waited := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(waited)
	}()
	t.Cleanup(func() {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		select {
		case <-waited:
		case <-time.After(2 * time.Second):
		}
	})
	pgid, err := syscall.Getpgid(cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	turnID := "turn_recovery"
	_, _ = store.Append(value.ID, "session.state", "", mustJSON(session.StateEventData{State: session.StateStarting}))
	_, _ = store.Append(value.ID, "provider.process.started", "", mustJSON(session.ProviderProcessEventData{PID: cmd.Process.Pid, ProcessGroupID: pgid}))
	_, _ = store.Append(value.ID, "turn.started", turnID, nil)
	_, _ = store.Append(value.ID, "approval.requested", turnID, mustJSON(session.ApprovalEventData{ApprovalID: "apr_recovery"}))

	_ = New(store, testConfig())
	select {
	case <-waited:
	case <-time.After(5 * time.Second):
		t.Fatal("recovery did not reap the recorded provider process")
	}
	recovered, err := store.Get(value.ID)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.State != session.StateStopped || recovered.StopReason != session.StopReasonDaemonRecovery ||
		recovered.CurrentTurnID != "" || len(recovered.PendingApprovalIDs) != 0 {
		t.Fatalf("unexpected recovered projection: %+v", recovered)
	}
	before, _ := os.Stat(marker)
	time.Sleep(100 * time.Millisecond)
	after, _ := os.Stat(marker)
	if before != nil && after != nil && before.Size() != after.Size() {
		t.Fatal("recovered provider wrote after stopped was published")
	}
}
