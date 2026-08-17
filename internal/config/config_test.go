package config

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgentResolvesProvider(t *testing.T) {
	cfg := Config{
		Version:        1,
		AgentProviders: []Provider{{ID: "p", Type: "pi", Enabled: true}},
		Agents:         []Agent{{Name: "Agent A", ProviderID: "p"}, {Name: "Agent B", ProviderID: "p"}},
	}
	agent, provider, err := cfg.Agent("Agent B")
	if err != nil || agent.Name != "Agent B" || provider.ID != "p" {
		t.Fatalf("unexpected agent: %+v %+v %v", agent, provider, err)
	}
	if _, _, err := cfg.Agent("missing"); err == nil {
		t.Fatal("expected an error for an unknown agent")
	}
}

func TestAgentRejectsDisabledProvider(t *testing.T) {
	cfg := Config{
		Version:        1,
		AgentProviders: []Provider{{ID: "p", Type: "pi", Enabled: false}},
		Agents:         []Agent{{Name: "Agent A", ProviderID: "p"}},
	}
	if _, _, err := cfg.Agent("Agent A"); err == nil {
		t.Fatal("expected an error for an agent whose provider is disabled")
	}
}

// Lookup uses the same normalization as the uniqueness rule: surrounding
// whitespace and case differences resolve to the canonical configured name.
func TestAgentLookupIsCaseInsensitiveAndTrims(t *testing.T) {
	cfg := Config{
		Version:        1,
		AgentProviders: []Provider{{ID: "p", Type: "pi", Enabled: true}},
		Agents:         []Agent{{Name: "Kimi K3", ProviderID: "p"}},
	}
	for _, reference := range []string{"Kimi K3", "kimi k3", "  KIMI K3  "} {
		agent, _, err := cfg.Agent(reference)
		if err != nil || agent.Name != "Kimi K3" {
			t.Fatalf("lookup %q did not resolve to the canonical name: %+v %v", reference, agent, err)
		}
	}
}

func TestValidateAgentNames(t *testing.T) {
	provider := Provider{ID: "p", Type: "pi", Enabled: true}
	cases := []struct {
		name   string
		agents []Agent
		want   string
	}{
		{"blank name", []Agent{{Name: "   ", ProviderID: "p"}}, "name is required"},
		{"missing name", []Agent{{ProviderID: "p"}}, "name is required"},
		{"too long", []Agent{{Name: strings.Repeat("x", AgentNameMaxLength+1), ProviderID: "p"}}, "exceeds"},
		{"exact duplicates", []Agent{{Name: "Codex", ProviderID: "p"}, {Name: "Codex", ProviderID: "p"}}, "duplicate agent name"},
		{"case-insensitive duplicates", []Agent{{Name: "Codex", ProviderID: "p"}, {Name: "codex", ProviderID: "p"}}, "duplicate agent name"},
		{"whitespace duplicates", []Agent{{Name: "Codex", ProviderID: "p"}, {Name: " Codex ", ProviderID: "p"}}, "duplicate agent name"},
		{"missing provider", []Agent{{Name: "Codex"}}, "providerId is required"},
		{"unknown provider", []Agent{{Name: "Codex", ProviderID: "ghost"}}, "unknown provider"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Config{Version: 1, AgentProviders: []Provider{provider}, Agents: tc.agents}
			err := cfg.Validate()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected an error containing %q, got %v", tc.want, err)
			}
		})
	}
	// The duplicate error names both conflicting entries.
	cfg := Config{Version: 1, AgentProviders: []Provider{provider}, Agents: []Agent{
		{Name: "Codex", ProviderID: "p"}, {Name: "Other", ProviderID: "p"}, {Name: "codex", ProviderID: "p"},
	}}
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "agents[2]") || !strings.Contains(err.Error(), "agents[0]") {
		t.Fatalf("duplicate error does not point at both entries: %v", err)
	}
	// A name at the exact length limit and distinct names pass.
	valid := Config{Version: 1, AgentProviders: []Provider{provider}, Agents: []Agent{
		{Name: strings.Repeat("x", AgentNameMaxLength), ProviderID: "p"}, {Name: "Codex", ProviderID: "p"},
	}}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
}

func TestValidateAgentEnvironment(t *testing.T) {
	provider := Provider{ID: "p", Type: "pi", Enabled: true}
	cases := []struct {
		name string
		env  map[string]string
		want string
	}{
		{"empty name", map[string]string{"": "v"}, "name cannot be empty"},
		{"equals in name", map[string]string{"A=B": "v"}, "invalid environment variable name"},
		{"nul in name", map[string]string{"A\x00": "v"}, "invalid environment variable name"},
		{"nul in value", map[string]string{"A": "v\x00"}, "contains NUL"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Config{Version: 1, AgentProviders: []Provider{provider}, Agents: []Agent{{Name: "Codex", ProviderID: "p", Environment: tc.env}}}
			err := cfg.Validate()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected an error containing %q, got %v", tc.want, err)
			}
		})
	}
	valid := Config{Version: 1, AgentProviders: []Provider{provider}, Agents: []Agent{{Name: "Codex", ProviderID: "p", Environment: map[string]string{"FOO": "bar", "BAZ": "qux"}}}}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid environment rejected: %v", err)
	}
}

func TestLoadCreatesIndependentDefaults(t *testing.T) {
	path := t.TempDir() + "/config.json"
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Version != 1 || len(loaded.AgentProviders) == 0 || len(loaded.Agents) == 0 {
		t.Fatalf("unexpected defaults: %+v", loaded)
	}
	reloaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.Agents) != len(loaded.Agents) || reloaded.Agents[0].Name != loaded.Agents[0].Name {
		t.Fatalf("config was not persisted: %+v", reloaded)
	}
	if reloaded.Agents[0].Name != "Codex" {
		t.Fatalf("default agent lost its name: %+v", reloaded.Agents[0])
	}
}

func TestLoadAcceptsAndDropsLegacyCompanionConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	legacy := `{"version":1,"agentProviders":[{"id":"codex","name":"Codex","type":"codex","enabled":true}],"agents":[{"name":"Codex","providerId":"codex"}],"companion":{"showActivity":false,"enableBeeping":false,"beepVolume":0,"beepChord":"a-minor","beepProgression":"canon-in-c","completionSound":"smile"}}`
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.OnWatch.ServerURL != "http://127.0.0.1:9211" || loaded.OnWatch.RefreshIntervalSeconds != 60 {
		t.Fatalf("OnWatch defaults = %+v", loaded.OnWatch)
	}
	if loaded.LegacyCompanion != nil {
		t.Fatalf("legacy companion config survived normalization: %s", loaded.LegacyCompanion)
	}
	if err := Save(path, loaded); err != nil {
		t.Fatal(err)
	}
	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(written, []byte(`"companion"`)) {
		t.Fatalf("legacy companion config was written back: %s", written)
	}
}

func TestConfigRedactsAndPreservesOnWatchPassword(t *testing.T) {
	previous := Defaults()
	previous.OnWatch.Enabled = true
	previous.OnWatch.AuthMode = "basic"
	previous.OnWatch.Username = "alice"
	previous.OnWatch.Password = "secret"
	public := previous.Redacted()
	if public.OnWatch.Password != "" || previous.OnWatch.Password != "secret" {
		t.Fatalf("redaction mutated or exposed password: public=%+v stored=%+v", public.OnWatch, previous.OnWatch)
	}
	next := public.PreserveSecrets(previous)
	if next.OnWatch.Password != "secret" {
		t.Fatalf("preserved config = %+v", next)
	}
	if err := next.Validate(); err != nil {
		t.Fatalf("preserved config is invalid: %v", err)
	}
}

func TestOnWatchConfigValidation(t *testing.T) {
	cfg := Defaults()
	cfg.OnWatch.ServerURL = "file:///tmp/quota"
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "serverUrl") {
		t.Fatalf("unsafe URL validation = %v", err)
	}
	cfg = Defaults()
	cfg.OnWatch.Enabled = true
	cfg.OnWatch.AuthMode = "basic"
	cfg.OnWatch.Password = ""
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "password") {
		t.Fatalf("missing password validation = %v", err)
	}
}

func TestLoadRejectsRemovedConfigFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	legacy := `{"version":1,"agentProviders":[],"agents":[{"id":"agent-a","name":"Agent A","providerId":"p"}]}`
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "id") {
		t.Fatalf("expected removed agent id field to be rejected, got %v", err)
	}
}

func TestSetProviderEnabledFlipsOnlyTheFlag(t *testing.T) {
	cfg := Config{
		Version: 1,
		AgentProviders: []Provider{
			{ID: "codex", Name: "Codex app-server", Type: "codex", Enabled: true},
			{ID: "kimi", Name: "Kimi Code", Type: "kimi", Enabled: true, Command: "/opt/kimi/bin/kimi"},
		},
		Agents: []Agent{{Name: "Kimi K3", ProviderID: "kimi", Options: map[string]string{"model": "k3"}}},
	}
	next, provider, err := cfg.SetProviderEnabled("kimi", false)
	if err != nil {
		t.Fatal(err)
	}
	if provider.Enabled || provider.Command != "/opt/kimi/bin/kimi" || provider.Name != "Kimi Code" {
		t.Fatalf("toggle did not preserve the provider: %+v", provider)
	}
	// Re-enabling restores the same underlying configuration.
	restored, provider, err := next.SetProviderEnabled("kimi", true)
	if err != nil {
		t.Fatal(err)
	}
	if !provider.Enabled || provider.Command != "/opt/kimi/bin/kimi" {
		t.Fatalf("re-enable lost the underlying configuration: %+v", provider)
	}
	if len(restored.AgentProviders) != 2 || restored.AgentProviders[0].ID != "codex" {
		t.Fatalf("provider order changed: %+v", restored.AgentProviders)
	}
	if restored.Agents[0].Options["model"] != "k3" {
		t.Fatalf("agents were altered: %+v", restored.Agents)
	}
	// The input config is never mutated.
	if !cfg.AgentProviders[1].Enabled {
		t.Fatal("SetProviderEnabled mutated its input")
	}
}

func TestSetProviderEnabledCopiesEnvironment(t *testing.T) {
	cfg := Config{
		Version:        1,
		AgentProviders: []Provider{{ID: "codex", Name: "Codex app-server", Type: "codex", Enabled: true}, {ID: "kimi", Name: "Kimi Code", Type: "kimi", Enabled: true}},
		Agents:         []Agent{{Name: "Kimi K3", ProviderID: "kimi", Options: map[string]string{"model": "k3"}, Environment: map[string]string{"FOO": "bar"}}},
	}
	next, _, err := cfg.SetProviderEnabled("kimi", false)
	if err != nil {
		t.Fatal(err)
	}
	if next.Agents[0].Environment["FOO"] != "bar" {
		t.Fatalf("toggle lost agent environment: %+v", next.Agents[0])
	}
	next.Agents[0].Environment["FOO"] = "mutated"
	if cfg.Agents[0].Environment["FOO"] != "bar" {
		t.Fatal("SetProviderEnabled mutated the input agent environment")
	}
}

func TestSetProviderEnabledAppendsBuiltinDefault(t *testing.T) {
	cfg := Config{
		Version:        1,
		AgentProviders: []Provider{{ID: "codex", Name: "Codex app-server", Type: "codex", Enabled: true}},
	}
	next, provider, err := cfg.SetProviderEnabled("pi", true)
	if err != nil {
		t.Fatal(err)
	}
	if provider != (Provider{ID: "pi", Name: "Pi Coding Agent", Type: "pi", Enabled: true}) {
		t.Fatalf("unexpected default provider: %+v", provider)
	}
	if err := next.Validate(); err != nil {
		t.Fatalf("config with appended default is invalid: %v", err)
	}
	// A missing provider can also be recorded as disabled.
	_, provider, err = cfg.SetProviderEnabled("opencode", false)
	if err != nil || provider.Enabled {
		t.Fatalf("unexpected disabled default: %+v %v", provider, err)
	}
}

func TestSetProviderEnabledRejectsUnknownProvider(t *testing.T) {
	cfg := Defaults()
	if _, _, err := cfg.SetProviderEnabled("ghost", true); err == nil {
		t.Fatal("expected an error for an unknown provider")
	}
}

func TestDefaultsCoverTheFourBuiltinProviders(t *testing.T) {
	defaults := Defaults()
	builtin := BuiltinProviders()
	if len(defaults.AgentProviders) != len(builtin) {
		t.Fatalf("defaults = %+v, want the four built-in providers", defaults.AgentProviders)
	}
	for i, provider := range builtin {
		if defaults.AgentProviders[i].ID != provider.ID || !defaults.AgentProviders[i].Enabled {
			t.Fatalf("unexpected default at %d: %+v", i, defaults.AgentProviders[i])
		}
	}
}

// Duplicate names (case-insensitive, whitespace-normalized) are rejected.
func TestLoadRejectsDuplicateAgentNames(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	legacy := `{
		"version": 1,
		"agentProviders": [{"id": "p", "name": "Pi", "type": "pi", "enabled": true}],
		"agents": [
			{"name": "Codex", "providerId": "p"},
			{"name": " codex ", "providerId": "p"}
		]
	}`
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "duplicate agent name") {
		t.Fatalf("expected a duplicate name error, got %v", err)
	}
	data, readErr := os.ReadFile(path)
	if readErr != nil || string(data) != legacy {
		t.Fatalf("failed migration must leave the original file untouched: %v", readErr)
	}
}

func TestDetectRenames(t *testing.T) {
	providers := []Provider{{ID: "p", Type: "pi", Enabled: true}}
	oldConfig := Config{Version: 1, AgentProviders: providers, Agents: []Agent{
		{Name: "Codex", ProviderID: "p", Options: map[string]string{"model": "m"}},
		{Name: "Kimi", ProviderID: "p"},
	}}
	// A single identical-except-name agent is an unambiguous rename.
	renamed := Config{Version: 1, AgentProviders: providers, Agents: []Agent{
		{Name: "Codex X", ProviderID: "p", Options: map[string]string{"model": "m"}},
		{Name: "Kimi", ProviderID: "p"},
	}}
	renames, err := DetectRenames(oldConfig, renamed)
	if err != nil || len(renames) != 1 || renames["Codex"] != "Codex X" {
		t.Fatalf("unexpected renames: %+v %v", renames, err)
	}

	// Deletion (no identical new agent) is not a rename.
	deleted := Config{Version: 1, AgentProviders: providers, Agents: []Agent{{Name: "Kimi", ProviderID: "p"}}}
	renames, err = DetectRenames(oldConfig, deleted)
	if err != nil || len(renames) != 0 {
		t.Fatalf("deletion must not be a rename: %+v %v", renames, err)
	}

	// Several identical candidates make the rename ambiguous.
	ambiguous := Config{Version: 1, AgentProviders: providers, Agents: []Agent{
		{Name: "Codex X", ProviderID: "p", Options: map[string]string{"model": "m"}},
		{Name: "Codex Y", ProviderID: "p", Options: map[string]string{"model": "m"}},
		{Name: "Kimi", ProviderID: "p"},
	}}
	if _, err := DetectRenames(oldConfig, ambiguous); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("expected an ambiguity error, got %v", err)
	}

	// A case-only change keeps the same normalized name and is not a rename.
	recased := Config{Version: 1, AgentProviders: providers, Agents: []Agent{
		{Name: "codex", ProviderID: "p", Options: map[string]string{"model": "m"}},
		{Name: "Kimi", ProviderID: "p"},
	}}
	renames, err = DetectRenames(oldConfig, recased)
	if err != nil || len(renames) != 0 {
		t.Fatalf("recasing must not be a rename: %+v %v", renames, err)
	}

	// An environment change with otherwise identical options is a config
	// change, not a rename.
	envChanged := Config{Version: 1, AgentProviders: providers, Agents: []Agent{
		{Name: "Codex X", ProviderID: "p", Options: map[string]string{"model": "m"}, Environment: map[string]string{"FOO": "bar"}},
		{Name: "Kimi", ProviderID: "p"},
	}}
	renames, err = DetectRenames(oldConfig, envChanged)
	if err != nil || len(renames) != 0 {
		t.Fatalf("environment-only change must not be a rename: %+v %v", renames, err)
	}
}
