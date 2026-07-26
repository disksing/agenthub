package provider

import (
	"errors"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/disksing/agenthub/internal/config"
)

// ACP startup handshake tests. They exercise the full spawn → initialize →
// session/new path against the fake ACP server in TestHelperProcess, without
// a real provider CLI.

func withStartupTimeout(t *testing.T, timeout time.Duration) {
	t.Helper()
	previous := startupRequestTimeout
	startupRequestTimeout = timeout
	t.Cleanup(func() { startupRequestTimeout = previous })
}

func acpTestOptions(t *testing.T, nativeID *string) Options {
	t.Helper()
	return Options{
		Cwd:      t.TempDir(),
		Provider: config.Provider{ID: "kimi", Type: "kimi", Name: "Kimi Code"},
		Agent:    config.Agent{Name: "kimi-k3", Options: map[string]string{"model": "kimi-code/k3", "mode": "build"}},
		Hooks: Hooks{
			NativeID: func(id string) { *nativeID = id },
		},
	}
}

func TestACPStartCompletesHandshake(t *testing.T) {
	var nativeID string
	value := newACP(helperCLI(t, "acp"), acpTestOptions(t, &nativeID))
	if err := value.Start(""); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if nativeID != "session-test" {
		t.Fatalf("native session id = %q", nativeID)
	}
	if err := value.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestACPStartSessionNewTimeout(t *testing.T) {
	withStartupTimeout(t, 2*time.Second)
	var nativeID string
	value := newACP(helperCLI(t, "acp-hang-session-new"), acpTestOptions(t, &nativeID))
	start := time.Now()
	err := value.Start("")
	if err == nil {
		t.Fatal("Start should fail when session/new never responds")
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("startup timeout took too long: %s", elapsed)
	}
	var timeoutErr *RequestTimeoutError
	if !errors.As(err, &timeoutErr) || timeoutErr.Method != "session/new" {
		t.Fatalf("err = %v, want session/new RequestTimeoutError", err)
	}
	if !strings.Contains(err.Error(), "Kimi Code") || !strings.Contains(err.Error(), "working directory") {
		t.Fatalf("error should name the provider and be actionable: %v", err)
	}
	if runtime.GOOS == "darwin" && !strings.Contains(err.Error(), "Privacy") {
		t.Fatalf("darwin error should hint at privacy permission prompts: %v", err)
	}
	if nativeID != "" {
		t.Fatalf("native id must not be recorded on failure: %q", nativeID)
	}
	// Closing after a failed start must kill the stuck provider process.
	pid := value.rpc.cmd.Process.Pid
	if err := value.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if err := syscall.Kill(pid, 0); err != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("provider process still running after Close")
		}
		time.Sleep(20 * time.Millisecond)
	}
	// The timed-out request must not leak its waiting-map entry.
	value.rpc.mu.Lock()
	waiting := len(value.rpc.waiting)
	value.rpc.mu.Unlock()
	if waiting != 0 {
		t.Fatalf("waiting map leaked %d entries", waiting)
	}
}

func TestACPStartProcessExitsBeforeHandshake(t *testing.T) {
	var nativeID string
	command := writeScript(t, `echo 'boom: cannot start' >&2
exit 3`)
	value := newACP(command, acpTestOptions(t, &nativeID))
	err := value.Start("")
	if err == nil || !strings.Contains(err.Error(), "provider exited") {
		t.Fatalf("err = %v, want early-exit error", err)
	}
	if nativeID != "" {
		t.Fatalf("native id must not be recorded on failure: %q", nativeID)
	}
	_ = value.Close()
}

func TestACPStartInitializeProtocolError(t *testing.T) {
	var nativeID string
	value := newACP(helperCLI(t, "acp-init-error"), acpTestOptions(t, &nativeID))
	err := value.Start("")
	if err == nil || !strings.Contains(err.Error(), "auth required") {
		t.Fatalf("err = %v, want the provider protocol error surfaced", err)
	}
	if nativeID != "" {
		t.Fatalf("native id must not be recorded on failure: %q", nativeID)
	}
	_ = value.Close()
}

func TestACPStartUnsupportedProtocolVersion(t *testing.T) {
	// An agent answering with an unexpected protocol version is rejected
	// before session/new is even attempted.
	var nativeID string
	command := writeScript(t, `while IFS= read -r line; do
  case "$line" in
    *initialize*) printf '%s\n' '{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":99,"agentCapabilities":{}}}' ;;
    *) printf '%s\n' '{"jsonrpc":"2.0","id":2,"result":{}}' ;;
  esac
done`)
	options := acpTestOptions(t, &nativeID)
	value := newACP(command, options)
	err := value.Start("")
	if err == nil || !strings.Contains(err.Error(), "unsupported ACP version") {
		t.Fatalf("err = %v, want protocol version rejection", err)
	}
	_ = value.Close()
}
