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

type Config struct {
	Version        int        `json:"version"`
	AgentProviders []Provider `json:"agentProviders"`
	Agents         []Agent    `json:"agents"`
}

// legacyFields mirrors the config keys removed with the Agent Profile and tag
// routing model. They are tolerated on read and dropped for good on the next
// save; Load rewrites the file once to complete the migration.
type legacyFields struct {
	DefaultChatAgentID string          `json:"defaultChatAgentId"`
	AgentProfiles      json.RawMessage `json:"agentProfiles"`
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
	var legacy legacyFields
	if err := json.Unmarshal(data, &legacy); err == nil && legacy.hasLegacy() {
		// One-time migration: rewrite the file without the removed profile
		// routing fields. Providers and agents are kept untouched.
		if err := Save(path, cfg); err != nil {
			return Config{}, fmt.Errorf("migrate legacy profile fields: %w", err)
		}
	}
	return cfg, nil
}

func (l legacyFields) hasLegacy() bool {
	return l.DefaultChatAgentID != "" || len(l.AgentProfiles) > 0
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
	return nil
}

func Defaults() Config {
	return Config{
		Version: 1,
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
