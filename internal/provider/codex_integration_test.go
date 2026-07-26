package provider

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/disksing/agenthub/internal/config"
)

// TestCodexReasoningEffortIntegration exercises the reasoning_effort option
// against a real codex app-server. It is opt-in: set
// AGENTHUB_CODEX_INTEGRATION=1 and have the codex CLI on PATH. CODEX_HOME is
// redirected to a temporary directory so the test never touches the real
// Codex configuration or threads, and no turn is started (no model calls).
func TestCodexReasoningEffortIntegration(t *testing.T) {
	if os.Getenv("AGENTHUB_CODEX_INTEGRATION") == "" {
		t.Skip("set AGENTHUB_CODEX_INTEGRATION=1 to run the Codex app-server integration test")
	}
	if _, err := exec.LookPath("codex"); err != nil {
		t.Skip("codex CLI not found on PATH")
	}
	t.Setenv("CODEX_HOME", t.TempDir())

	valid := newCodex("codex", Options{
		Cwd:   t.TempDir(),
		Agent: config.Agent{Options: map[string]string{"reasoning_effort": "high"}},
	})
	if err := valid.Start(""); err != nil {
		t.Fatalf("start with reasoning_effort=high: %v", err)
	}
	if valid.thread == "" {
		t.Fatal("thread/start returned no thread id")
	}
	valid.Close()

	invalid := newCodex("codex", Options{
		Cwd:   t.TempDir(),
		Agent: config.Agent{Options: map[string]string{"reasoning_effort": "totally-bogus"}},
	})
	err := invalid.Start("")
	if err == nil || !strings.Contains(err.Error(), "invalid reasoning_effort") {
		t.Fatalf("expected invalid reasoning_effort error, got %v", err)
	}
	invalid.Close()
}
