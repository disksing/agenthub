package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

type Provider struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Type    string `json:"type"`
	Enabled bool   `json:"enabled"`
	Command string `json:"command,omitempty"`
}

type Agent struct {
	Name       string            `json:"name"`
	ProviderID string            `json:"providerId"`
	Options    map[string]string `json:"options,omitempty"`
}

type OnWatch struct {
	Enabled                bool   `json:"enabled"`
	ServerURL              string `json:"serverUrl"`
	AuthMode               string `json:"authMode"`
	Username               string `json:"username,omitempty"`
	Password               string `json:"password"`
	RefreshIntervalSeconds int    `json:"refreshIntervalSeconds"`
}

type Companion struct {
	ShowActivity    bool    `json:"showActivity"`
	EnableBeeping   bool    `json:"enableBeeping"`
	BeepVolume      float64 `json:"beepVolume"`
	CompletionSound string  `json:"completionSound"`
}

const DefaultCompletionSound = "completed-voice"

var completionSounds = map[string]struct{}{
	"completed-voice":       {},
	"done-for-you-girl":     {},
	"light-hearted-message": {},
	"not-bad":               {},
	"slow-spring-board":     {},
	"smile":                 {},
}

var legacyCompletionSounds = map[string]struct{}{
	"chime": {}, "bell": {}, "ding": {}, "marimba": {}, "pop": {},
}

// AgentNameMaxLength bounds the length of an agent name (counted in runes
// after trimming).
const AgentNameMaxLength = 80

// NormalizeAgentName returns the comparison key of an agent name: leading
// and trailing whitespace removed and the rest Unicode lower-cased. Agent
// names are unique under this normalization, so "Codex" and " codex " refer
// to the same agent. The user-provided display form is always preserved; the
// normalized form is only used for uniqueness checks and lookups.
func NormalizeAgentName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

type Config struct {
	Version        int        `json:"version"`
	AgentProviders []Provider `json:"agentProviders"`
	Agents         []Agent    `json:"agents"`
	OnWatch        OnWatch    `json:"onWatch"`
	Companion      Companion  `json:"companion"`
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
	cfg := Config{OnWatch: defaultOnWatch(), Companion: defaultCompanion()}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("decode config: %w", err)
	}
	if cfg.Version != 1 {
		return Config{}, fmt.Errorf("unsupported config version %d; expected 1", cfg.Version)
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg.WithDefaults(), nil
}

func Save(path string, cfg Config) error {
	cfg = cfg.WithDefaults()
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
	effective := c.WithDefaults()
	if err := effective.OnWatch.validate(); err != nil {
		return err
	}
	if err := effective.Companion.validate(); err != nil {
		return err
	}
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
	agents := make(map[string]int)
	for index, value := range c.Agents {
		name := strings.TrimSpace(value.Name)
		if name == "" {
			return fmt.Errorf("agents[%d]: agent name is required", index)
		}
		if utf8.RuneCountInString(name) > AgentNameMaxLength {
			return fmt.Errorf("agents[%d]: agent name %q exceeds %d characters", index, name, AgentNameMaxLength)
		}
		if value.ProviderID == "" {
			return fmt.Errorf("agents[%d] (%q): providerId is required", index, name)
		}
		key := NormalizeAgentName(name)
		if previous, exists := agents[key]; exists {
			return fmt.Errorf("agents[%d] (%q): duplicate agent name, already used by agents[%d] (%q); names must be unique case-insensitively", index, name, previous, strings.TrimSpace(c.Agents[previous].Name))
		}
		agents[key] = index
		if _, exists := providers[value.ProviderID]; !exists {
			return fmt.Errorf("agent %q references unknown provider %q", name, value.ProviderID)
		}
	}
	return nil
}

// BuiltinProviders returns the canonical definitions of the four built-in
// providers in display order. The Web settings UI exposes exactly these as
// enable/disable switches; other providers remain manageable only through
// the config file.
func BuiltinProviders() []Provider {
	return []Provider{
		{ID: "codex", Name: "Codex app-server", Type: "codex"},
		{ID: "kimi", Name: "Kimi Code", Type: "kimi"},
		{ID: "pi", Name: "Pi Coding Agent", Type: "pi"},
		{ID: "opencode", Name: "OpenCode", Type: "opencode"},
	}
}

// BuiltinProvider returns the canonical definition of a built-in provider.
func BuiltinProvider(id string) (Provider, bool) {
	for _, provider := range BuiltinProviders() {
		if provider.ID == id {
			return provider, true
		}
	}
	return Provider{}, false
}

// SetProviderEnabled returns a copy of the config with the enabled flag of a
// built-in provider flipped. Only the flag changes: an existing provider keeps
// its name, type, command and position, so disabling and re-enabling never
// loses the underlying configuration. When the provider is absent from an old
// config, the canonical built-in default is appended. Unknown provider IDs are
// rejected; the input config is never mutated.
func (c Config) SetProviderEnabled(id string, enabled bool) (Config, Provider, error) {
	if _, ok := BuiltinProvider(id); !ok {
		return Config{}, Provider{}, fmt.Errorf("unknown built-in provider %q", id)
	}
	next := Config{
		Version:        c.Version,
		AgentProviders: make([]Provider, len(c.AgentProviders)),
		Agents:         make([]Agent, len(c.Agents)),
		OnWatch:        c.OnWatch,
		Companion:      c.Companion,
	}
	copy(next.AgentProviders, c.AgentProviders)
	for i, agent := range c.Agents {
		if agent.Options != nil {
			options := make(map[string]string, len(agent.Options))
			for key, value := range agent.Options {
				options[key] = value
			}
			agent.Options = options
		}
		next.Agents[i] = agent
	}
	for i := range next.AgentProviders {
		if next.AgentProviders[i].ID == id {
			next.AgentProviders[i].Enabled = enabled
			return next, next.AgentProviders[i], nil
		}
	}
	provider, _ := BuiltinProvider(id)
	provider.Enabled = enabled
	next.AgentProviders = append(next.AgentProviders, provider)
	return next, provider, nil
}

func Defaults() Config {
	providers := BuiltinProviders()
	for i := range providers {
		providers[i].Enabled = true
	}
	return Config{
		Version:        1,
		AgentProviders: providers,
		Agents:         []Agent{{Name: "Codex", ProviderID: "codex", Options: map[string]string{"approval": "never", "sandbox": "danger-full-access"}}},
		OnWatch:        defaultOnWatch(),
		Companion:      defaultCompanion(),
	}
}

func defaultOnWatch() OnWatch {
	return OnWatch{
		ServerURL:              "http://127.0.0.1:9211",
		AuthMode:               "trusted_proxy",
		Username:               "admin",
		RefreshIntervalSeconds: 60,
	}
}

func defaultCompanion() Companion {
	return Companion{
		ShowActivity:    true,
		EnableBeeping:   true,
		BeepVolume:      0.28,
		CompletionSound: DefaultCompletionSound,
	}
}

// WithDefaults fills companion fields absent from configurations written by
// older AgentHub versions. It intentionally treats only an entirely empty
// nested object as absent, so explicit false and zero values survive when the
// remaining fields identify a current configuration.
func (c Config) WithDefaults() Config {
	if c.OnWatch.ServerURL == "" && c.OnWatch.AuthMode == "" && c.OnWatch.RefreshIntervalSeconds == 0 {
		c.OnWatch = defaultOnWatch()
	}
	if c.Companion.CompletionSound == "" {
		c.Companion = defaultCompanion()
	} else if _, legacy := legacyCompletionSounds[c.Companion.CompletionSound]; legacy {
		c.Companion.CompletionSound = DefaultCompletionSound
	}
	return c
}

func (value OnWatch) validate() error {
	parsed, err := url.Parse(strings.TrimSpace(value.ServerURL))
	if err != nil || parsed == nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return errors.New("onWatch.serverUrl must be an absolute HTTP or HTTPS URL")
	}
	if parsed.User != nil {
		return errors.New("onWatch.serverUrl must not contain credentials")
	}
	switch value.AuthMode {
	case "trusted_proxy", "basic", "none":
	default:
		return fmt.Errorf("onWatch.authMode %q is unsupported", value.AuthMode)
	}
	if value.RefreshIntervalSeconds != 30 && value.RefreshIntervalSeconds != 60 && value.RefreshIntervalSeconds != 300 {
		return errors.New("onWatch.refreshIntervalSeconds must be 30, 60, or 300")
	}
	if value.AuthMode == "basic" && strings.TrimSpace(value.Username) == "" {
		return errors.New("onWatch.username is required for basic authentication")
	}
	if value.Enabled && value.AuthMode == "basic" && value.Password == "" {
		return errors.New("onWatch.password is required for basic authentication")
	}
	return nil
}

func (value Companion) validate() error {
	if value.BeepVolume < 0 || value.BeepVolume > 1 {
		return errors.New("companion.beepVolume must be between 0 and 1")
	}
	if _, ok := completionSounds[value.CompletionSound]; !ok {
		return fmt.Errorf("companion.completionSound %q is unsupported", value.CompletionSound)
	}
	return nil
}

// Redacted returns a copy safe to expose through the public configuration API.
// The stored Basic Auth password remains available only inside the daemon.
func (c Config) Redacted() Config {
	c.OnWatch.Password = ""
	return c
}

// PreserveSecrets carries forward an omitted Basic Auth password from the
// current configuration. This lets settings clients edit unrelated fields
// without receiving or resubmitting the stored secret.
func (c Config) PreserveSecrets(previous Config) Config {
	if c.OnWatch.Password == "" && c.OnWatch.AuthMode == "basic" {
		c.OnWatch.Password = previous.OnWatch.Password
	}
	return c
}

// Agent resolves an agent by name. Matching is case-insensitive and ignores
// surrounding whitespace, consistent with the uniqueness rule; the returned
// agent always carries the canonical configured display name.
func (c Config) Agent(name string) (Agent, Provider, error) {
	key := NormalizeAgentName(name)
	for _, agent := range c.Agents {
		if NormalizeAgentName(agent.Name) != key {
			continue
		}
		for _, provider := range c.AgentProviders {
			if provider.ID == agent.ProviderID && provider.Enabled {
				return agent, provider, nil
			}
		}
		return Agent{}, Provider{}, fmt.Errorf("provider for agent %q is disabled", agent.Name)
	}
	return Agent{}, Provider{}, fmt.Errorf("unknown agent %q", strings.TrimSpace(name))
}

// DetectRenames compares the agents of two configurations and reports
// renames as old name → new name pairs. A rename is a name that disappeared
// while exactly one new agent appeared with an identical configuration
// (provider and options) and no other removed agent claims it. A removed
// name with several identical candidates is ambiguous and reported as an
// error so the caller can reject the change instead of guessing; a removed
// name without candidates is a deletion, not a rename.
func DetectRenames(oldConfig, newConfig Config) (map[string]string, error) {
	removed := map[string]Agent{}
	for _, agent := range oldConfig.Agents {
		removed[NormalizeAgentName(agent.Name)] = agent
	}
	added := map[string]Agent{}
	for _, agent := range newConfig.Agents {
		key := NormalizeAgentName(agent.Name)
		added[key] = agent
		delete(removed, key)
	}
	for key := range added {
		for _, agent := range oldConfig.Agents {
			if NormalizeAgentName(agent.Name) == key {
				delete(added, key)
				break
			}
		}
	}
	if len(removed) == 0 || len(added) == 0 {
		return nil, nil
	}
	renames := map[string]string{}
	claimed := map[string]string{}
	for _, oldAgent := range removed {
		var candidates []Agent
		for _, newAgent := range added {
			if agentConfigEqual(oldAgent, newAgent) {
				candidates = append(candidates, newAgent)
			}
		}
		if len(candidates) > 1 {
			names := make([]string, 0, len(candidates))
			for _, candidate := range candidates {
				names = append(names, strconv.Quote(candidate.Name))
			}
			sort.Strings(names)
			return nil, fmt.Errorf("renaming agent %q is ambiguous: new agents %s have identical configurations; give the renamed agent a distinct provider or options, or apply the changes in two saves", oldAgent.Name, strings.Join(names, ", "))
		}
		if len(candidates) == 1 {
			newKey := NormalizeAgentName(candidates[0].Name)
			if other, taken := claimed[newKey]; taken {
				return nil, fmt.Errorf("renaming agents %q and %q both match the new agent %q; apply the changes in two saves", other, oldAgent.Name, candidates[0].Name)
			}
			claimed[newKey] = oldAgent.Name
			renames[oldAgent.Name] = candidates[0].Name
		}
	}
	return renames, nil
}

func agentConfigEqual(a, b Agent) bool {
	if a.ProviderID != b.ProviderID || len(a.Options) != len(b.Options) {
		return false
	}
	for key, value := range a.Options {
		if b.Options[key] != value {
			return false
		}
	}
	return true
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
