package provider

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/disksing/agenthub/internal/config"
)

type writeCloser struct{ io.Writer }

func (writeCloser) Close() error { return nil }

func TestCodexTranslatesStreamingTurnAndApproval(t *testing.T) {
	var events []Event
	var approvalID string
	options := Options{
		Agent: config.Agent{Options: map[string]string{"approval": "on-request"}},
		Hooks: Hooks{
			Event:    func(event Event) { events = append(events, event) },
			Approval: func(id, _ string, _ json.RawMessage) { approvalID = id },
		},
	}
	value := newCodex("unused", options)
	var output bytes.Buffer
	value.rpc.stdin = writeCloser{&output}
	value.notification("turn/started", json.RawMessage(`{"turn":{"id":"turn-native"}}`))
	value.notification("item/agentMessage/delta", json.RawMessage(`{"delta":"hello"}`))
	value.inbound(json.RawMessage(`42`), "item/commandExecution/requestApproval", json.RawMessage(`{"command":"go test ./..."}`))
	if approvalID != "42" {
		t.Fatalf("approval id = %q", approvalID)
	}
	if err := value.Approve("42", "accept"); err != nil {
		t.Fatal(err)
	}
	value.notification("turn/completed", json.RawMessage(`{"turn":{"id":"turn-native"}}`))
	if len(events) != 3 || events[1].Type != "message.assistant.delta" || !events[2].TurnDone {
		t.Fatalf("unexpected events: %+v", events)
	}
	if !strings.Contains(output.String(), `"decision":"accept"`) {
		t.Fatalf("unexpected approval response: %s", output.String())
	}
}

func TestCodexParsesModelList(t *testing.T) {
	raw := json.RawMessage(`{"data":[{"id":"gpt-5.6-sol","isDefault":true,"supportedReasoningEfforts":[{"reasoningEffort":"low"},{"reasoningEffort":"high"}]},{"id":"gpt-5.5","supportedReasoningEfforts":[{"reasoningEffort":"medium"}]}]}`)
	models := parseCodexModels(raw)
	if len(models) != 2 || !models[0].isDefault || models[0].id != "gpt-5.6-sol" {
		t.Fatalf("unexpected models: %+v", models)
	}
	if len(models[0].efforts) != 2 || models[0].efforts[1] != "high" {
		t.Fatalf("unexpected efforts: %+v", models[0].efforts)
	}
	if got := parseCodexModels(json.RawMessage(`null`)); len(got) != 0 {
		t.Fatalf("expected no models, got %+v", got)
	}
}

func TestCodexChecksReasoningEffort(t *testing.T) {
	models := []codexModel{
		{id: "gpt-5.6-sol", isDefault: true, efforts: []string{"low", "medium", "high"}},
		{id: "gpt-5.5", efforts: []string{"medium"}},
	}
	if err := checkReasoningEffort("high", "", models); err != nil {
		t.Fatalf("default model should accept high: %v", err)
	}
	if err := checkReasoningEffort("medium", "gpt-5.5", models); err != nil {
		t.Fatalf("requested model should accept medium: %v", err)
	}
	err := checkReasoningEffort("bogus", "gpt-5.6-sol", models)
	if err == nil || !strings.Contains(err.Error(), "low, medium, high") {
		t.Fatalf("expected supported values in error, got %v", err)
	}
	if err := checkReasoningEffort("bogus", "unknown-model", models); err != nil {
		t.Fatalf("unknown model should pass through: %v", err)
	}
	if err := checkReasoningEffort("bogus", "", nil); err != nil {
		t.Fatalf("empty catalog should pass through: %v", err)
	}
}

func TestCodexChecksReasoningEffortEcho(t *testing.T) {
	if err := checkReasoningEffortEcho(json.RawMessage(`{"reasoningEffort":"high"}`), "high"); err != nil {
		t.Fatalf("matching echo should pass: %v", err)
	}
	if err := checkReasoningEffortEcho(json.RawMessage(`{"model":"gpt-5.6-sol"}`), "high"); err != nil {
		t.Fatalf("missing echo should pass: %v", err)
	}
	err := checkReasoningEffortEcho(json.RawMessage(`{"reasoningEffort":"medium"}`), "high")
	if err == nil || !strings.Contains(err.Error(), "medium") {
		t.Fatalf("expected echo mismatch error, got %v", err)
	}
}

func TestACPTranslatesMessageAndPermission(t *testing.T) {
	var events []Event
	var approvalID string
	value := newACP("unused", Options{
		Provider: config.Provider{Type: "kimi"},
		Hooks: Hooks{
			Event:    func(event Event) { events = append(events, event) },
			Approval: func(id, _ string, _ json.RawMessage) { approvalID = id },
		},
	})
	var output bytes.Buffer
	value.rpc.stdin = writeCloser{&output}
	value.notification("session/update", json.RawMessage(`{"update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"hi"}}}`))
	value.inbound(json.RawMessage(`"req-1"`), "session/request_permission", json.RawMessage(`{"options":[{"optionId":"once","kind":"allow_once"}]}`))
	if approvalID != "req-1" {
		t.Fatalf("approval id = %q", approvalID)
	}
	if err := value.Approve("req-1", "accept"); err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Type != "message.assistant.delta" {
		t.Fatalf("unexpected events: %+v", events)
	}
	if !strings.Contains(output.String(), `"optionId":"once"`) {
		t.Fatalf("unexpected permission response: %s", output.String())
	}
}

func TestPiTranslatesDeltaAndSettled(t *testing.T) {
	var events []Event
	value := newPi("unused", Options{Hooks: Hooks{Event: func(event Event) { events = append(events, event) }}})
	value.event("message_update", json.RawMessage(`{"assistantMessageEvent":{"type":"text_delta","delta":"ok"}}`))
	value.event("agent_settled", json.RawMessage(`{"type":"agent_settled"}`))
	if len(events) != 2 || events[0].Type != "message.assistant.delta" || !events[1].TurnDone {
		t.Fatalf("unexpected events: %+v", events)
	}
}

func TestCodexLaunchEnvironmentReachesProcessAndStartOrResumeConfig(t *testing.T) {
	t.Setenv("AGENTHUB_PROCESS_ENV", "daemon-value")
	t.Setenv("AGENTHUB_EXPECTED_LAUNCH_ENV", "session-value")
	for _, resumeID := range []string{"", "thread-existing"} {
		t.Run(map[bool]string{true: "resume", false: "start"}[resumeID != ""], func(t *testing.T) {
			var nativeID string
			value := newCodex(helperCLI(t, "codex-session-environment"), Options{
				Cwd:         t.TempDir(),
				Environment: map[string]string{"AGENTHUB_PROCESS_ENV": "session-value"},
				Hooks: Hooks{
					NativeID: func(id string) { nativeID = id },
				},
			})
			if err := value.Start(resumeID); err != nil {
				t.Fatalf("Start(%q): %v", resumeID, err)
			}
			if nativeID != "session-value" {
				t.Fatalf("native id = %q, launch environment did not reach fake Codex", nativeID)
			}
			if err := value.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestACPLaunchEnvironmentOverridesDaemonEnvironment(t *testing.T) {
	t.Setenv("AGENTHUB_PROCESS_ENV", "daemon-value")
	var nativeID string
	options := acpTestOptions(t, &nativeID)
	options.Environment = map[string]string{"AGENTHUB_PROCESS_ENV": "acp-session-value"}
	value := newACP(helperCLI(t, "acp-session-environment"), options)
	if err := value.Start(""); err != nil {
		t.Fatal(err)
	}
	if nativeID != "acp-session-value" {
		t.Fatalf("native id = %q, launch environment did not reach fake ACP", nativeID)
	}
	if err := value.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestPiLaunchEnvironmentOverridesDaemonEnvironment(t *testing.T) {
	t.Setenv("AGENTHUB_PROCESS_ENV", "daemon-value")
	var nativeID string
	value := newPi(helperCLI(t, "pi-session-environment"), Options{
		Cwd:         t.TempDir(),
		Environment: map[string]string{"AGENTHUB_PROCESS_ENV": "pi-session-value"},
		Hooks: Hooks{
			NativeID: func(id string) { nativeID = id },
		},
	})
	if err := value.Start(""); err != nil {
		t.Fatal(err)
	}
	if nativeID != "pi-session-value" {
		t.Fatalf("native id = %q, launch environment did not reach fake Pi", nativeID)
	}
	if err := value.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestJSONRPCDrainsFinalOutputBeforeProcessEnd(t *testing.T) {
	notificationStarted := make(chan struct{})
	releaseNotification := make(chan struct{})
	processEnded := make(chan struct{})
	var notificationOnce sync.Once
	var processEndOnce sync.Once
	value := newJSONRPC(
		helperCLI(t, "jsonrpc-output-then-exit"),
		nil,
		t.TempDir(),
		nil,
		Hooks{ProcessEnd: func(error) { processEndOnce.Do(func() { close(processEnded) }) }},
	)
	value.notify = func(string, json.RawMessage) {
		notificationOnce.Do(func() { close(notificationStarted) })
		<-releaseNotification
	}
	if err := value.start(); err != nil {
		t.Fatal(err)
	}
	waitForSignal(t, notificationStarted, "final JSON-RPC notification")
	select {
	case <-processEnded:
		t.Fatal("ProcessEnd ran before the final JSON-RPC notification completed")
	case <-time.After(100 * time.Millisecond):
	}
	close(releaseNotification)
	waitForSignal(t, processEnded, "JSON-RPC ProcessEnd")
	if err := value.close(); err != nil {
		t.Fatal(err)
	}
}

func TestPiDrainsFinalOutputBeforeProcessEnd(t *testing.T) {
	eventStarted := make(chan struct{})
	releaseEvent := make(chan struct{})
	processEnded := make(chan struct{})
	var eventOnce sync.Once
	var processEndOnce sync.Once
	value := newPi(helperCLI(t, "pi-output-then-exit"), Options{
		Cwd: t.TempDir(),
		Hooks: Hooks{
			Event: func(Event) {
				eventOnce.Do(func() { close(eventStarted) })
				<-releaseEvent
			},
			ProcessEnd: func(error) { processEndOnce.Do(func() { close(processEnded) }) },
		},
	})
	if err := value.Start(""); err != nil {
		t.Fatal(err)
	}
	waitForSignal(t, eventStarted, "final Pi event")
	select {
	case <-processEnded:
		t.Fatal("ProcessEnd ran before the final Pi event completed")
	case <-time.After(100 * time.Millisecond):
	}
	close(releaseEvent)
	waitForSignal(t, processEnded, "Pi ProcessEnd")
	if err := value.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestACPPromptCompletionPrecedesImmediateProcessEnd(t *testing.T) {
	var nativeID string
	var mu sync.Mutex
	var order []string
	processEnded := make(chan struct{})
	options := acpTestOptions(t, &nativeID)
	options.Hooks.Event = func(event Event) {
		if !event.TurnDone {
			return
		}
		mu.Lock()
		order = append(order, "turn")
		mu.Unlock()
	}
	options.Hooks.ProcessEnd = func(error) {
		mu.Lock()
		order = append(order, "process")
		mu.Unlock()
		close(processEnded)
	}
	value := newACP(helperCLI(t, "acp-prompt-exit"), options)
	if err := value.Start(""); err != nil {
		t.Fatal(err)
	}
	if err := value.Prompt("finish", false); err != nil {
		t.Fatal(err)
	}
	waitForSignal(t, processEnded, "ACP ProcessEnd")
	mu.Lock()
	defer mu.Unlock()
	if strings.Join(order, ",") != "turn,process" {
		t.Fatalf("terminal order = %v, want turn before process", order)
	}
}

func waitForSignal(t *testing.T, signal <-chan struct{}, description string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", description)
	}
}

func TestCloseEliminatesDescendantsAfterGroupLeaderExited(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "writes")
	cmd := exec.Command("sh", "-c", `(trap '' TERM; while :; do printf x >> "$1"; sleep 0.02; done) & exit 0`, "sh", marker)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pgid, err := syscall.Getpgid(cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()
	<-done
	t.Cleanup(func() { _ = syscall.Kill(-pgid, syscall.SIGKILL) })
	if !processGroupExists(pgid) {
		t.Fatal("test descendant did not survive its group leader")
	}
	if err := terminateChildProcess(cmd, pgid, nil, done); err != nil {
		t.Fatal(err)
	}
	before, _ := os.Stat(marker)
	time.Sleep(100 * time.Millisecond)
	after, _ := os.Stat(marker)
	if before != nil && after != nil && before.Size() != after.Size() {
		t.Fatal("provider descendant wrote after Close returned")
	}
}
