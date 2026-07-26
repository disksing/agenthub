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
	if resolved.Isolated {
		t.Error("default layout must not be marked isolated")
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
	if !resolved.Isolated {
		t.Error("AGENTHUB_HOME layout must be marked isolated")
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

func TestLegacyDefaultsMatchPlatform(t *testing.T) {
	home := string(filepath.Separator) + "home/user"
	empty := func(string) string { return "" }

	darwin := LegacyFor(home, "darwin", empty)
	if darwin.DataDir != filepath.Join(home, "Library", "Application Support", "agenthub") {
		t.Errorf("darwin data = %q", darwin.DataDir)
	}
	if darwin.LogsDir != filepath.Join(home, "Library", "Logs", "AgentHub") {
		t.Errorf("darwin logs = %q", darwin.LogsDir)
	}

	windows := LegacyFor(home, "windows", func(key string) string {
		if key == "LOCALAPPDATA" {
			return `C:\Users\user\AppData\Local`
		}
		return ""
	})
	if windows.DataDir != filepath.Join(`C:\Users\user\AppData\Local`, "agenthub") {
		t.Errorf("windows data = %q", windows.DataDir)
	}
	if windows.LogsDir != "" {
		t.Errorf("windows logs = %q, want empty", windows.LogsDir)
	}
	if missing := LegacyFor(home, "windows", empty); missing.DataDir != "" {
		t.Errorf("windows without LOCALAPPDATA = %q, want empty", missing.DataDir)
	}

	linux := LegacyFor(home, "linux", empty)
	if linux.DataDir != filepath.Join(home, ".local", "share", "agenthub") {
		t.Errorf("linux data = %q", linux.DataDir)
	}
	xdg := LegacyFor(home, "linux", func(key string) string {
		if key == "XDG_DATA_HOME" {
			return "/xdg/data"
		}
		return ""
	})
	if xdg.DataDir != filepath.Join("/xdg/data", "agenthub") {
		t.Errorf("linux XDG data = %q", xdg.DataDir)
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
