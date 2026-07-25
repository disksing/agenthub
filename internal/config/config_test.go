package config

import "testing"

func TestRouteMatchesProfileThenDefault(t *testing.T) {
	cfg := Config{
		Version: 1, DefaultChatAgentID: "a",
		AgentProviders: []Provider{{ID: "p", Type: "pi", Enabled: true}},
		Agents:         []Agent{{ID: "a", ProviderID: "p"}, {ID: "b", ProviderID: "p"}},
		AgentProfiles:  []Profile{{Key: "fast", AgentID: "b"}},
	}
	agent, _, reason, err := cfg.Route("", []string{"unknown", "FAST"})
	if err != nil || agent.ID != "b" || reason != "matched profile fast" {
		t.Fatalf("unexpected route: %+v %q %v", agent, reason, err)
	}
	agent, _, _, err = cfg.Route("", nil)
	if err != nil || agent.ID != "a" {
		t.Fatalf("unexpected default: %+v %v", agent, err)
	}
}

func TestValidateRejectsBrokenProfile(t *testing.T) {
	cfg := Defaults()
	cfg.AgentProfiles = []Profile{{Key: "bad", AgentID: "missing"}}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error")
	}
}

func TestLoadCreatesIndependentDefaults(t *testing.T) {
	path := t.TempDir() + "/config.json"
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Version != 1 || loaded.DefaultChatAgentID != "codex-default" {
		t.Fatalf("unexpected defaults: %+v", loaded)
	}
	reloaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.DefaultChatAgentID != loaded.DefaultChatAgentID {
		t.Fatalf("config was not persisted: %+v", reloaded)
	}
}
