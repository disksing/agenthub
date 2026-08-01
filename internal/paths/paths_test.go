package paths

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultLayoutLivesUnderDotAgentHub(t *testing.T) {
	t.Setenv("AGENTHUB_HOME", "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	resolved, err := Resolve()
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(home, ".agenthub")
	want := map[string]string{
		"ConfigDir":   root,
		"ConfigFile":  filepath.Join(root, "config.json"),
		"DataDir":     root,
		"StateDir":    root,
		"SessionsDir": filepath.Join(root, "sessions"),
		"LogsDir":     filepath.Join(root, "logs"),
		"ServerFile":  filepath.Join(root, "server.json"),
		"LockFile":    filepath.Join(root, "server.lock"),
	}
	got := map[string]string{
		"ConfigDir":   resolved.ConfigDir,
		"ConfigFile":  resolved.ConfigFile,
		"DataDir":     resolved.DataDir,
		"StateDir":    resolved.StateDir,
		"SessionsDir": resolved.SessionsDir,
		"LogsDir":     resolved.LogsDir,
		"ServerFile":  resolved.ServerFile,
		"LockFile":    resolved.LockFile,
	}
	for field, wantPath := range want {
		if got[field] != wantPath {
			t.Errorf("%s = %q, want %q", field, got[field], wantPath)
		}
	}
	// The session store must sit directly at ~/.agenthub/sessions, never at
	// a duplicated ~/.agenthub/agenthub/sessions.
	if filepath.Base(filepath.Dir(resolved.SessionsDir)) != ".agenthub" {
		t.Errorf("sessions dir %q is not directly under .agenthub", resolved.SessionsDir)
	}
}

func TestAgentHubHomeKeepsExplicitIsolatedLayout(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AGENTHUB_HOME", root)
	resolved, err := Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if resolved.ConfigFile != filepath.Join(root, "config", "config.json") {
		t.Errorf("config file = %q", resolved.ConfigFile)
	}
	if resolved.SessionsDir != filepath.Join(root, "data", "sessions") {
		t.Errorf("sessions dir = %q", resolved.SessionsDir)
	}
	if resolved.ServerFile != filepath.Join(root, "state", "server.json") {
		t.Errorf("server file = %q", resolved.ServerFile)
	}
	if resolved.LogsDir != filepath.Join(root, "logs") {
		t.Errorf("logs dir = %q", resolved.LogsDir)
	}
}

func TestEnsureCreatesPrivateDirectories(t *testing.T) {
	home := t.TempDir()
	resolved := Default(home)
	if err := resolved.Ensure(); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{resolved.ConfigDir, resolved.SessionsDir, resolved.LogsDir} {
		info, err := os.Stat(dir)
		if err != nil {
			t.Errorf("%s: %v", dir, err)
			continue
		}
		if info.Mode().Perm() != 0o700 {
			t.Errorf("%s mode = %o, want 700", dir, info.Mode().Perm())
		}
	}
}
