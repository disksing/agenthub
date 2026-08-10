package companion

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/disksing/agenthub/internal/config"
)

const upstreamTimeout = 8 * time.Second

type Quota struct {
	Kind                  string   `json:"kind"`
	Label                 string   `json:"label"`
	RemainingPercent      float64  `json:"remainingPercent"`
	UsedPercent           float64  `json:"usedPercent"`
	ResetInSeconds        *int64   `json:"resetInSeconds,omitempty"`
	ResetsAt              string   `json:"resetsAt,omitempty"`
	WindowPositionPercent *float64 `json:"windowPositionPercent,omitempty"`
	Status                string   `json:"status"`
	Used                  *float64 `json:"used,omitempty"`
	Limit                 *float64 `json:"limit,omitempty"`
	CurrentRate           *float64 `json:"currentRate,omitempty"`
	ProjectedUtil         *float64 `json:"projectedUtil,omitempty"`
	Stale                 bool     `json:"stale,omitempty"`
}

type ProviderQuota struct {
	Provider   string  `json:"provider"`
	Label      string  `json:"label"`
	PlanLabel  string  `json:"planLabel,omitempty"`
	CapturedAt string  `json:"capturedAt,omitempty"`
	Status     string  `json:"status"`
	Stale      bool    `json:"stale,omitempty"`
	Error      string  `json:"error,omitempty"`
	Quotas     []Quota `json:"quotas"`
}

type QuotaSnapshot struct {
	Configured        bool            `json:"configured"`
	Connected         bool            `json:"connected"`
	Stale             bool            `json:"stale,omitempty"`
	Error             string          `json:"error,omitempty"`
	UpdatedAt         time.Time       `json:"updatedAt"`
	StaleAfterSeconds int             `json:"staleAfterSeconds"`
	Providers         []ProviderQuota `json:"providers"`
}

type ProviderCatalog struct {
	Providers []string          `json:"providers"`
	Labels    map[string]string `json:"providerLabels"`
}

type Service struct {
	client *http.Client

	refreshMu sync.Mutex
	mu        sync.Mutex
	signature string
	expiresAt time.Time
	cached    *QuotaSnapshot
}

func NewService(client *http.Client) *Service {
	if client == nil {
		client = &http.Client{Timeout: upstreamTimeout}
	}
	return &Service{client: client}
}

func (s *Service) Invalidate() {
	s.mu.Lock()
	s.signature = ""
	s.expiresAt = time.Time{}
	s.cached = nil
	s.mu.Unlock()
}

func (s *Service) Snapshot(ctx context.Context, settings config.OnWatch) QuotaSnapshot {
	now := time.Now().UTC()
	if !settings.Enabled {
		return QuotaSnapshot{
			Configured: false, Connected: false, UpdatedAt: now,
			StaleAfterSeconds: settings.RefreshIntervalSeconds * 2,
			Providers:         []ProviderQuota{},
		}
	}
	signature := settingsSignature(settings)
	s.mu.Lock()
	if s.signature == signature && s.cached != nil && now.Before(s.expiresAt) {
		result := cloneSnapshot(*s.cached)
		s.mu.Unlock()
		return result
	}
	s.mu.Unlock()

	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()
	s.mu.Lock()
	if s.signature == signature && s.cached != nil && now.Before(s.expiresAt) {
		result := cloneSnapshot(*s.cached)
		s.mu.Unlock()
		return result
	}
	s.mu.Unlock()

	result, err := s.fetch(ctx, settings, now)
	if err != nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.signature == signature && s.cached != nil {
			stale := cloneSnapshot(*s.cached)
			stale.Connected = false
			stale.Stale = true
			stale.Error = err.Error()
			for index := range stale.Providers {
				stale.Providers[index].Stale = true
				for quotaIndex := range stale.Providers[index].Quotas {
					stale.Providers[index].Quotas[quotaIndex].Stale = true
				}
			}
			return stale
		}
		return QuotaSnapshot{
			Configured: true, Connected: false, Error: err.Error(), UpdatedAt: now,
			StaleAfterSeconds: settings.RefreshIntervalSeconds * 2,
			Providers:         []ProviderQuota{},
		}
	}
	s.mu.Lock()
	if s.signature == signature && s.cached != nil {
		result = mergePartialSnapshot(result, *s.cached)
	}
	s.signature = signature
	s.expiresAt = now.Add(time.Duration(settings.RefreshIntervalSeconds) * time.Second)
	copy := cloneSnapshot(result)
	s.cached = &copy
	s.mu.Unlock()
	return result
}

func (s *Service) TestConnection(ctx context.Context, settings config.OnWatch) (ProviderCatalog, error) {
	return s.fetchCatalog(ctx, settings)
}

func (s *Service) fetch(ctx context.Context, settings config.OnWatch, now time.Time) (QuotaSnapshot, error) {
	catalog, err := s.fetchCatalog(ctx, settings)
	if err != nil {
		return QuotaSnapshot{}, err
	}
	result := QuotaSnapshot{
		Configured: true, Connected: true, UpdatedAt: now,
		StaleAfterSeconds: settings.RefreshIntervalSeconds * 2,
		Providers:         make([]ProviderQuota, 0, len(catalog.Providers)),
	}
	type providerResult struct {
		id       string
		label    string
		provider ProviderQuota
		err      error
	}
	results := make([]providerResult, 0, len(catalog.Providers))
	for _, id := range catalog.Providers {
		if id == "" || id == "both" {
			continue
		}
		results = append(results, providerResult{id: id, label: catalog.Labels[id]})
	}
	var wait sync.WaitGroup
	for index := range results {
		wait.Add(1)
		go func(target *providerResult) {
			defer wait.Done()
			target.provider, target.err = s.fetchProvider(ctx, settings, target.id, target.label, now)
		}(&results[index])
	}
	wait.Wait()
	for _, fetched := range results {
		if fetched.err != nil {
			result.Providers = append(result.Providers, ProviderQuota{
				Provider: fetched.id, Label: providerLabel(fetched.id, fetched.label), Status: "unavailable",
				Error: fetched.err.Error(), Quotas: []Quota{},
			})
			continue
		}
		result.Providers = append(result.Providers, fetched.provider)
	}
	return result, nil
}

func (s *Service) fetchCatalog(ctx context.Context, settings config.OnWatch) (ProviderCatalog, error) {
	var upstream struct {
		Providers []string          `json:"providers"`
		Labels    map[string]string `json:"provider_labels"`
	}
	if err := s.getJSON(ctx, settings, "/api/providers", nil, &upstream); err != nil {
		return ProviderCatalog{}, err
	}
	providers := make([]string, 0, len(upstream.Providers))
	for _, provider := range upstream.Providers {
		if provider != "both" && provider != "" {
			providers = append(providers, provider)
		}
	}
	if len(providers) == 0 {
		return ProviderCatalog{}, errors.New("OnWatch returned no providers")
	}
	return ProviderCatalog{Providers: providers, Labels: upstream.Labels}, nil
}

type upstreamQuota struct {
	Name                  string   `json:"name"`
	DisplayName           string   `json:"displayName"`
	Label                 string   `json:"label"`
	RemainingPercent      *float64 `json:"remainingPercent"`
	Headroom              *float64 `json:"headroom"`
	CardPercent           *float64 `json:"cardPercent"`
	Utilization           *float64 `json:"utilization"`
	TimeUntilResetSeconds *int64   `json:"timeUntilResetSeconds"`
	ResetsAt              string   `json:"resetsAt"`
	Status                string   `json:"status"`
	Used                  *float64 `json:"used"`
	Limit                 *float64 `json:"limit"`
	CurrentRate           *float64 `json:"currentRate"`
	ProjectedUtil         *float64 `json:"projectedUtil"`
	IsStale               bool     `json:"isStale"`
	AgeSeconds            *int64   `json:"ageSeconds"`
}

func (s *Service) fetchProvider(ctx context.Context, settings config.OnWatch, id, label string, now time.Time) (ProviderQuota, error) {
	var upstream struct {
		CapturedAt  string          `json:"capturedAt"`
		PlanType    string          `json:"planType"`
		PlanName    string          `json:"planName"`
		Membership  string          `json:"membership"`
		LoginMethod string          `json:"login_method"`
		Quotas      []upstreamQuota `json:"quotas"`
	}
	if err := s.getJSON(ctx, settings, "/api/current", url.Values{"provider": []string{id}}, &upstream); err != nil {
		return ProviderQuota{}, err
	}
	provider := ProviderQuota{
		Provider: id, Label: providerLabel(id, label), PlanLabel: firstNonEmpty(upstream.PlanName, upstream.PlanType, upstream.Membership, upstream.LoginMethod),
		CapturedAt: upstream.CapturedAt, Status: "healthy", Quotas: make([]Quota, 0, len(upstream.Quotas)),
	}
	for _, value := range upstream.Quotas {
		quota := normalizeQuota(value, now, settings.RefreshIntervalSeconds)
		provider.Quotas = append(provider.Quotas, quota)
		if statusRank(quota.Status) > statusRank(provider.Status) {
			provider.Status = quota.Status
		}
		provider.Stale = provider.Stale || quota.Stale
	}
	if capturedAt, err := time.Parse(time.RFC3339, upstream.CapturedAt); err == nil && now.Sub(capturedAt) > time.Duration(settings.RefreshIntervalSeconds*2)*time.Second {
		provider.Stale = true
		for index := range provider.Quotas {
			provider.Quotas[index].Stale = true
		}
	}
	return provider, nil
}

func normalizeQuota(value upstreamQuota, now time.Time, refreshInterval int) Quota {
	remaining := firstNumber(value.RemainingPercent, value.Headroom, value.CardPercent)
	if remaining == nil && value.Utilization != nil {
		computed := 100 - *value.Utilization
		remaining = &computed
	}
	remainingValue := clamp(percentValue(remaining), 0, 100)
	usedValue := 100 - remainingValue
	if value.Utilization != nil {
		usedValue = clamp(*value.Utilization, 0, 100)
	}
	resetSeconds := value.TimeUntilResetSeconds
	if resetSeconds == nil && value.ResetsAt != "" {
		if resetAt, err := time.Parse(time.RFC3339, value.ResetsAt); err == nil {
			seconds := int64(math.Max(0, resetAt.Sub(now).Seconds()))
			resetSeconds = &seconds
		}
	}
	kind := quotaKind(value.Name, value.DisplayName)
	windowPosition := windowPosition(kind, resetSeconds)
	stale := value.IsStale || (value.AgeSeconds != nil && *value.AgeSeconds > int64(refreshInterval*2))
	status := strings.ToLower(strings.TrimSpace(value.Status))
	if status == "" {
		status = derivedStatus(remainingValue)
	}
	return Quota{
		Kind: kind, Label: firstNonEmpty(value.DisplayName, value.Label, value.Name),
		RemainingPercent: remainingValue, UsedPercent: usedValue,
		ResetInSeconds: resetSeconds, ResetsAt: value.ResetsAt, WindowPositionPercent: windowPosition,
		Status: status, Used: value.Used, Limit: value.Limit, CurrentRate: value.CurrentRate,
		ProjectedUtil: value.ProjectedUtil, Stale: stale,
	}
}

func (s *Service) getJSON(ctx context.Context, settings config.OnWatch, path string, query url.Values, target any) error {
	endpoint, err := endpointURL(settings.ServerURL, path, query)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/json")
	switch settings.AuthMode {
	case "trusted_proxy":
		user := strings.TrimSpace(settings.Username)
		if user == "" {
			user = "admin"
		}
		request.Header.Set("X-Forwarded-User", user)
	case "basic":
		request.SetBasicAuth(settings.Username, settings.Password)
	}
	response, err := s.client.Do(request)
	if err != nil {
		return fmt.Errorf("OnWatch request failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("OnWatch returned %s", response.Status)
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 2<<20))
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode OnWatch response: %w", err)
	}
	return nil
}

func endpointURL(baseURL, path string, query url.Values) (string, error) {
	base, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return "", err
	}
	base.Path = strings.TrimRight(base.Path, "/") + path
	base.RawQuery = query.Encode()
	base.Fragment = ""
	return base.String(), nil
}

func settingsSignature(settings config.OnWatch) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%s\x00%s\x00%d", settings.ServerURL, settings.AuthMode, settings.Username, settings.Password, settings.RefreshIntervalSeconds)))
	return hex.EncodeToString(sum[:])
}

func cloneSnapshot(value QuotaSnapshot) QuotaSnapshot {
	data, _ := json.Marshal(value)
	var result QuotaSnapshot
	_ = json.Unmarshal(data, &result)
	return result
}

func mergePartialSnapshot(current, previous QuotaSnapshot) QuotaSnapshot {
	previousByID := make(map[string]ProviderQuota, len(previous.Providers))
	for _, provider := range previous.Providers {
		previousByID[provider.Provider] = provider
	}
	for index := range current.Providers {
		provider := &current.Providers[index]
		if provider.Error == "" {
			continue
		}
		cached, ok := previousByID[provider.Provider]
		if !ok || len(cached.Quotas) == 0 {
			continue
		}
		errorMessage := provider.Error
		*provider = cached
		provider.Error = errorMessage
		provider.Stale = true
		for quotaIndex := range provider.Quotas {
			provider.Quotas[quotaIndex].Stale = true
		}
		current.Stale = true
	}
	return current
}

func providerLabel(id, upstream string) string {
	if strings.TrimSpace(upstream) != "" {
		return upstream
	}
	return map[string]string{"codex": "Codex", "kimi": "Kimi Code", "grok": "Grok", "opencode": "OpenCode"}[id]
}

func quotaKind(name, label string) string {
	value := strings.ToLower(name + " " + label)
	switch {
	case strings.Contains(value, "five") || strings.Contains(value, "5h") || strings.Contains(value, "5-hour"):
		return "5h"
	case strings.Contains(value, "seven") || strings.Contains(value, "weekly") || strings.Contains(value, "7-day") || strings.Contains(value, "7d"):
		return "7d"
	case strings.Contains(value, "month"):
		return "monthly"
	case strings.Contains(value, "credit"):
		return "credits"
	default:
		return strings.TrimSpace(name)
	}
}

func windowPosition(kind string, resetSeconds *int64) *float64 {
	if resetSeconds == nil {
		return nil
	}
	durations := map[string]float64{"5h": 5 * 60 * 60, "7d": 7 * 24 * 60 * 60, "monthly": 30 * 24 * 60 * 60}
	duration, ok := durations[kind]
	if !ok {
		return nil
	}
	// The track is a countdown: a full reset window starts at the right edge
	// and moves toward the left edge as the reset approaches.
	value := clamp(100*float64(*resetSeconds)/duration, 0, 100)
	return &value
}

func firstNumber(values ...*float64) *float64 {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func percentValue(value *float64) float64 {
	if value == nil {
		return 0
	}
	return *value
}

func clamp(value, low, high float64) float64 {
	return math.Min(high, math.Max(low, value))
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func derivedStatus(remaining float64) string {
	switch {
	case remaining <= 10:
		return "critical"
	case remaining <= 25:
		return "danger"
	case remaining <= 40:
		return "warning"
	default:
		return "healthy"
	}
}

func statusRank(status string) int {
	return map[string]int{"healthy": 0, "warning": 1, "danger": 2, "critical": 3, "unavailable": 4}[status]
}
