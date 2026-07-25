package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

type Provider struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Type    string `json:"type"`
	Enabled bool   `json:"enabled"`
	Command string `json:"command,omitempty"`
}

type Agent struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	ProviderID string            `json:"providerId"`
	Options    map[string]string `json:"options,omitempty"`
}

type Profile struct {
	Key         string `json:"key"`
	Description string `json:"description,omitempty"`
	AgentID     string `json:"agentId"`
}

type Config struct {
	Version            int        `json:"version"`
	DefaultChatAgentID string     `json:"defaultChatAgentId,omitempty"`
	AgentProviders     []Provider `json:"agentProviders"`
	Agents             []Agent    `json:"agents"`
	AgentProfiles      []Profile  `json:"agentProfiles,omitempty"`
}

type Probe struct {
	ProviderID string `json:"providerId"`
	Type       string `json:"type"`
	Command    string `json:"command,omitempty"`
	Available  bool   `json:"available"`
	Error      string `json:"error,omitempty"`
}

func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		cfg := Defaults()
		if err := Save(path, cfg); err != nil {
			return Config{}, err
		}
		return cfg, nil
	}
	if err != nil {
		return Config{}, err
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("decode config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func Save(path string, cfg Config) error {
	if cfg.Version == 0 {
		cfg.Version = 1
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".config-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

func (c Config) Validate() error {
	providers := make(map[string]Provider)
	for _, value := range c.AgentProviders {
		if value.ID == "" || value.Type == "" {
			return errors.New("provider id and type are required")
		}
		if _, exists := providers[value.ID]; exists {
			return fmt.Errorf("duplicate provider %q", value.ID)
		}
		switch value.Type {
		case "codex", "opencode", "kimi", "pi":
		default:
			return fmt.Errorf("provider %q has unsupported type %q", value.ID, value.Type)
		}
		providers[value.ID] = value
	}
	agents := make(map[string]Agent)
	for _, value := range c.Agents {
		if value.ID == "" || value.ProviderID == "" {
			return errors.New("agent id and providerId are required")
		}
		if _, exists := agents[value.ID]; exists {
			return fmt.Errorf("duplicate agent %q", value.ID)
		}
		provider, exists := providers[value.ProviderID]
		if !exists {
			return fmt.Errorf("agent %q references unknown provider %q", value.ID, value.ProviderID)
		}
		if !provider.Enabled {
			continue
		}
		agents[value.ID] = value
	}
	if c.DefaultChatAgentID != "" {
		if _, ok := agents[c.DefaultChatAgentID]; !ok {
			return fmt.Errorf("defaultChatAgentId references unavailable agent %q", c.DefaultChatAgentID)
		}
	}
	keys := make(map[string]bool)
	for _, value := range c.AgentProfiles {
		key := strings.ToLower(strings.TrimSpace(value.Key))
		if key == "" || value.AgentID == "" {
			return errors.New("profile key and agentId are required")
		}
		if keys[key] {
			return fmt.Errorf("duplicate profile %q", value.Key)
		}
		keys[key] = true
		if _, ok := agents[value.AgentID]; !ok {
			return fmt.Errorf("profile %q references unavailable agent %q", value.Key, value.AgentID)
		}
	}
	return nil
}

func Defaults() Config {
	return Config{
		Version:            1,
		DefaultChatAgentID: "codex-default",
		AgentProviders: []Provider{
			{ID: "codex", Name: "Codex app-server", Type: "codex", Enabled: true},
			{ID: "opencode", Name: "OpenCode", Type: "opencode", Enabled: true},
			{ID: "kimi", Name: "Kimi Code", Type: "kimi", Enabled: true},
			{ID: "pi", Name: "Pi Coding Agent", Type: "pi", Enabled: true},
		},
		Agents: []Agent{{ID: "codex-default", Name: "Codex", ProviderID: "codex", Options: map[string]string{"approval": "never", "sandbox": "danger-full-access"}}},
	}
}

func (c Config) Agent(id string) (Agent, Provider, error) {
	for _, agent := range c.Agents {
		if agent.ID != id {
			continue
		}
		for _, provider := range c.AgentProviders {
			if provider.ID == agent.ProviderID && provider.Enabled {
				return agent, provider, nil
			}
		}
		return Agent{}, Provider{}, fmt.Errorf("provider for agent %q is disabled", id)
	}
	return Agent{}, Provider{}, fmt.Errorf("unknown agent %q", id)
}

func (c Config) Route(explicit string, tags []string) (Agent, Provider, string, error) {
	if explicit != "" {
		agent, provider, err := c.Agent(explicit)
		return agent, provider, "explicit agent " + explicit, err
	}
	for _, requested := range tags {
		for _, profile := range c.AgentProfiles {
			if strings.EqualFold(strings.TrimSpace(requested), strings.TrimSpace(profile.Key)) {
				agent, provider, err := c.Agent(profile.AgentID)
				return agent, provider, "matched profile " + profile.Key, err
			}
		}
	}
	if c.DefaultChatAgentID == "" {
		return Agent{}, Provider{}, "", errors.New("no matching profile and defaultChatAgentId is empty")
	}
	agent, provider, err := c.Agent(c.DefaultChatAgentID)
	return agent, provider, "defaultChatAgentId", err
}

func (c Config) Probes() []Probe {
	result := make([]Probe, 0, len(c.AgentProviders))
	for _, provider := range c.AgentProviders {
		if !provider.Enabled {
			continue
		}
		command := provider.Command
		if command == "" {
			command = providerCommand(provider.Type)
		}
		resolved, err := resolveCommand(command, provider.Type)
		probe := Probe{ProviderID: provider.ID, Type: provider.Type, Command: resolved, Available: err == nil}
		if err != nil {
			probe.Error = err.Error()
		}
		result = append(result, probe)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ProviderID < result[j].ProviderID })
	return result
}

func ResolveProviderCommand(provider Provider) (string, error) {
	command := provider.Command
	if command == "" {
		command = providerCommand(provider.Type)
	}
	return resolveCommand(command, provider.Type)
}

func providerCommand(providerType string) string {
	env := map[string]string{"codex": "AGENTHUB_CODEX_CLI", "opencode": "AGENTHUB_OPENCODE_CLI", "kimi": "AGENTHUB_KIMI_CLI", "pi": "AGENTHUB_PI_CLI"}[providerType]
	if value := strings.TrimSpace(os.Getenv(env)); value != "" {
		return value
	}
	return map[string]string{"codex": "codex", "opencode": "opencode", "kimi": "kimi", "pi": "pi"}[providerType]
}

func resolveCommand(command, providerType string) (string, error) {
	if path, err := exec.LookPath(command); err == nil {
		return path, nil
	}
	if providerType == "kimi" && runtime.GOOS != "windows" {
		home, _ := os.UserHomeDir()
		fallback := filepath.Join(home, ".kimi-code", "bin", "kimi")
		if info, err := os.Stat(fallback); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			return fallback, nil
		}
	}
	return "", fmt.Errorf("%s executable %q not found", providerType, command)
}
