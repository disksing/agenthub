package paths

import (
	"path/filepath"
	"testing"
)

func TestDefaultConfigLivesInDotAgentHub(t *testing.T) {
	t.Setenv("AGENTHUB_HOME", "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	resolved, err := Resolve()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, ".agenthub", "config.json")
	if resolved.ConfigFile != want {
		t.Fatalf("config file = %q, want %q", resolved.ConfigFile, want)
	}
}
