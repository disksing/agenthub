package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgentResolvesProvider(t *testing.T) {
	cfg := Config{
		Version:        1,
		AgentProviders: []Provider{{ID: "p", Type: "pi", Enabled: true}},
		Agents:         []Agent{{ID: "a", ProviderID: "p"}, {ID: "b", ProviderID: "p"}},
	}
	agent, provider, err := cfg.Agent("b")
	if err != nil || agent.ID != "b" || provider.ID != "p" {
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
		Agents:         []Agent{{ID: "a", ProviderID: "p"}},
	}
	if _, _, err := cfg.Agent("a"); err == nil {
		t.Fatal("expected an error for an agent whose provider is disabled")
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
	if len(reloaded.Agents) != len(loaded.Agents) || reloaded.Agents[0].ID != loaded.Agents[0].ID {
		t.Fatalf("config was not persisted: %+v", reloaded)
	}
}

// Legacy configs written by the profile-routing era keep their providers and
// agents; the removed fields are ignored on read and dropped from the file by
// a one-time rewrite during Load.
func TestLoadMigratesLegacyProfileFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	legacy := `{
		"version": 1,
		"defaultChatAgentId": "agent-a",
		"agentProviders": [
			{"id": "p", "name": "Pi", "type": "pi", "enabled": true, "command": "missing-test-command"}
		],
		"agents": [
			{"id": "agent-a", "name": "A", "providerId": "p", "options": {"model": "m"}},
			{"id": "agent-b", "name": "B", "providerId": "p"}
		],
		"agentProfiles": [
			{"key": "fast", "description": "fast lane", "agentId": "agent-a"}
		]
	}`
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.AgentProviders) != 1 || len(loaded.Agents) != 2 || loaded.Agents[0].Options["model"] != "m" {
		t.Fatalf("providers/agents were not preserved: %+v", loaded)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "agentProfiles") || strings.Contains(string(data), "defaultChatAgentId") {
		t.Fatalf("legacy fields were not dropped from the file: %s", data)
	}
	var rewritten map[string]any
	if err := json.Unmarshal(data, &rewritten); err != nil {
		t.Fatal(err)
	}
	if len(rewritten["agents"].([]any)) != 2 {
		t.Fatalf("rewritten config lost agents: %s", data)
	}

	// The second load is stable and does not rewrite again.
	if _, err := Load(path); err != nil {
		t.Fatal(err)
	}
}
