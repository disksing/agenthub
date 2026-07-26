package provider

import (
	"context"
	"sync"
	"time"

	"github.com/disksing/agenthub/internal/config"
)

// ModelCache caches per-provider model enumeration results and deduplicates
// concurrent lookups, so a UI re-render never spawns several provider
// processes at once. Successful results (including empty lists) are cached
// for the success TTL; failures are cached for a much shorter TTL so a
// retrying client cannot hammer a failing provider, while real recovery is
// still noticed quickly. Cached entries never survive InvalidateAll, which
// the API layer calls on every configuration change.
type ModelCache struct {
	successTTL time.Duration
	errorTTL   time.Duration
	list       func(ctx context.Context, provider config.Provider) ([]Model, error)

	mu       sync.Mutex
	entries  map[string]modelCacheEntry
	inflight map[string]*modelCall
}

type modelCacheEntry struct {
	models  []Model
	err     *ModelError
	expires time.Time
}

type modelCall struct {
	done   chan struct{}
	models []Model
	err    error
}

// NewModelCache returns the production cache backed by ListModels.
func NewModelCache() *ModelCache {
	return newModelCache(5*time.Minute, 15*time.Second, ListModels)
}

func newModelCache(successTTL, errorTTL time.Duration, list func(context.Context, config.Provider) ([]Model, error)) *ModelCache {
	return &ModelCache{
		successTTL: successTTL,
		errorTTL:   errorTTL,
		list:       list,
		entries:    make(map[string]modelCacheEntry),
		inflight:   make(map[string]*modelCall),
	}
}

// cacheKey identifies one enumeration target. The configured command is part
// of the key so a command change naturally misses stale entries.
func (c *ModelCache) cacheKey(provider config.Provider) string {
	return provider.Type + "|" + provider.ID + "|" + provider.Command
}

// Models returns the model list for the provider, from cache when fresh.
func (c *ModelCache) Models(ctx context.Context, provider config.Provider) ([]Model, error) {
	key := c.cacheKey(provider)
	c.mu.Lock()
	if entry, ok := c.entries[key]; ok && time.Now().Before(entry.expires) {
		c.mu.Unlock()
		if entry.err != nil {
			return nil, entry.err
		}
		return cloneModels(entry.models), nil
	}
	if call, ok := c.inflight[key]; ok {
		c.mu.Unlock()
		select {
		case <-call.done:
			return cloneModels(call.models), call.err
		case <-ctx.Done():
			return nil, ctxModelError(ctx, "model enumeration")
		}
	}
	call := &modelCall{done: make(chan struct{})}
	c.inflight[key] = call
	c.mu.Unlock()

	models, err := c.list(ctx, provider)

	c.mu.Lock()
	delete(c.inflight, key)
	call.models, call.err = models, err
	entry := modelCacheEntry{models: cloneModels(models), expires: time.Now().Add(c.successTTL)}
	if err != nil {
		modelErr, ok := err.(*ModelError)
		if !ok {
			modelErr = modelError(ModelErrUpstream, "%s", err.Error())
			call.err = modelErr
		}
		entry.models, entry.err, entry.expires = nil, modelErr, time.Now().Add(c.errorTTL)
	}
	c.entries[key] = entry
	close(call.done)
	c.mu.Unlock()
	return cloneModels(models), call.err
}

// InvalidateAll drops every cached entry. The API layer calls it whenever
// the configuration changes (whole-config PUT, provider enable/disable), so
// cached identities never outlive the state they were enumerated from.
func (c *ModelCache) InvalidateAll() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = make(map[string]modelCacheEntry)
}

func cloneModels(models []Model) []Model {
	if models == nil {
		return nil
	}
	result := make([]Model, len(models))
	copy(result, models)
	return result
}
