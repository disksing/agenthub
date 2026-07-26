package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/disksing/agenthub/internal/config"
	"github.com/disksing/agenthub/internal/provider"
	"github.com/disksing/agenthub/internal/runtime"
	"github.com/disksing/agenthub/internal/session"
)

const APIVersion = "1"

const (
	CapabilitySessionSource            = "session.source"
	CapabilitySessionLaunchEnvironment = "session.launch-environment"
	CapabilitySessionStrictStopped     = "session.strict-stopped"
	CapabilityEventsLosslessReplay     = "events.lossless-replay"
	CapabilityEventsCanonicalTerminal  = "events.canonical-turn-terminals"
	CapabilityRecoveryClosedTurns      = "recovery.closed-turns"
)

// ModelLister enumerates the models of a built-in provider and can drop its
// cached results. *provider.ModelCache implements it; tests substitute fakes.
type ModelLister interface {
	Models(ctx context.Context, provider config.Provider) ([]provider.Model, error)
	InvalidateAll()
}

type Server struct {
	store     *session.Store
	startedAt time.Time
	version   string
	runtime   *runtime.Manager
	config    string
	logsDir   string
	webDir    string
	listen    *ListenAddress
	models    ModelLister
	// closing, when set, is closed once the HTTP server begins shutting
	// down so long-lived handlers (SSE streams) can finish promptly.
	closing <-chan struct{}
	// configMu serializes config mutations so a whole-config PUT and a
	// single-provider toggle cannot interleave and lose each other's changes.
	configMu sync.Mutex
}

type Dependencies struct {
	Runtime    *runtime.Manager
	ConfigPath string
	WebDir     string
	// Listen, when set, enables the Host header guard derived from the
	// validated listen address.
	Listen *ListenAddress
	// Models, when set, enables the provider model enumeration endpoint.
	Models ModelLister
	// LogsDir is the directory service logs are written to, reported by
	// the status endpoint.
	LogsDir string
	// Closing, when set, is closed when the HTTP server starts shutting
	// down; streaming handlers must return so Shutdown can complete.
	Closing <-chan struct{}
}

func New(store *session.Store, version string, startedAt time.Time, dependencies ...Dependencies) *Server {
	server := &Server{store: store, version: version, startedAt: startedAt}
	if len(dependencies) > 0 {
		server.runtime = dependencies[0].Runtime
		server.config = dependencies[0].ConfigPath
		server.logsDir = dependencies[0].LogsDir
		server.webDir = dependencies[0].WebDir
		server.listen = dependencies[0].Listen
		server.models = dependencies[0].Models
		server.closing = dependencies[0].Closing
	}
	return server
}

// apiRoute binds a mux pattern to a handler. doc is the canonical
// "METHOD /path" label of a documented public API route; routes with an
// empty doc are internal (health probe, the session sub-route dispatcher,
// the docs page itself) and must not be listed in api.md. The table
// returned by routes() is the canonical route inventory, so the coverage
// test in docs_test.go can prove api.md documents exactly the public API.
type apiRoute struct {
	pattern string
	handler http.HandlerFunc
	doc     string
}

func (s *Server) routes() []apiRoute {
	return []apiRoute{
		{"GET /v1/health", s.health, ""},
		{"GET /v1/status", s.status, "GET /v1/status"},
		{"GET /v1/config", s.getConfig, "GET /v1/config"},
		{"PUT /v1/config", s.putConfig, "PUT /v1/config"},
		{"PUT /v1/config/providers/{id}", s.putProviderEnabled, "PUT /v1/config/providers/{id}"},
		{"GET /v1/providers/{id}/models", s.providerModels, "GET /v1/providers/{id}/models"},
		{"GET /v1/agents", s.agents, "GET /v1/agents"},
		{"GET /v1/sessions", s.listSessions, "GET /v1/sessions"},
		{"POST /v1/sessions", s.createSession, "POST /v1/sessions"},
		{"/v1/sessions/", s.sessionRoute, ""}, // sub-routes dispatch through sessionOps()
		{"GET /api.md", s.apiDocs, ""},
		{"/api.md", s.apiDocsRoute, ""},
		{"/v1", s.apiNotFound, ""},
		{"/v1/", s.apiNotFound, ""},
	}
}

// publicAPILabels returns the canonical "METHOD /path" labels of every
// documented public API route, top-level and under /v1/sessions/{id}.
func (s *Server) publicAPILabels() []string {
	labels := make([]string, 0, len(s.routes())+len(s.sessionOps()))
	for _, route := range s.routes() {
		if route.doc != "" {
			labels = append(labels, route.doc)
		}
	}
	for _, op := range s.sessionOps() {
		labels = append(labels, op.doc)
	}
	return labels
}

func (s *Server) mux() *http.ServeMux {
	mux := http.NewServeMux()
	for _, route := range s.routes() {
		mux.HandleFunc(route.pattern, route.handler)
	}
	if s.webDir != "" {
		mux.Handle("/", spaHandler(s.webDir))
	}
	return mux
}

func (s *Server) Handler() http.Handler {
	var handler http.Handler = requestMiddleware(s.mux())
	if s.listen != nil {
		handler = hostGuardMiddleware(s.listen, handler)
	}
	return handler
}

// hostGuardMiddleware rejects requests whose Host header does not name an
// address of this daemon, blocking DNS-rebinding attacks against browsers on
// the local network.
func hostGuardMiddleware(listen *ListenAddress, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !listen.AllowsHost(r.Host) {
			writeAPIError(w, http.StatusForbidden, "host_rejected", "request host does not match an address of this daemon", nil)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

func (s *Server) status(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"apiVersion":    APIVersion,
		"capabilities":  s.capabilities(),
		"version":       s.version,
		"startedAt":     s.startedAt,
		"uptimeSeconds": int64(time.Since(s.startedAt).Seconds()),
		"paths": map[string]any{
			"config":   s.config,
			"sessions": s.store.Root(),
			"archive":  s.store.ArchiveRoot(),
			"logs":     s.logsDir,
		},
		"sessionStore": map[string]any{
			"path":         s.store.Root(),
			"archivePath":  s.store.ArchiveRoot(),
			"sessionCount": len(s.store.List(true)),
		},
		"runtime": s.runtimeStatus(),
	})
}

// capabilities reports only behavior available from this server instance.
// Store/API capabilities are always present. Runtime lifecycle capabilities
// are omitted from store-only instances instead of claiming behavior that
// cannot be exercised.
func (s *Server) capabilities() []string {
	capabilities := []string{
		CapabilityEventsLosslessReplay,
		CapabilitySessionSource,
	}
	if s.runtime != nil {
		capabilities = append(capabilities,
			CapabilityEventsCanonicalTerminal,
			CapabilityRecoveryClosedTurns,
			CapabilitySessionLaunchEnvironment,
			CapabilitySessionStrictStopped,
		)
	}
	return capabilities
}

func (s *Server) runtimeStatus() any {
	if s.runtime == nil {
		return map[string]any{"available": false}
	}
	return map[string]any{"available": true, "summary": s.runtime.String()}
}

func (s *Server) getConfig(w http.ResponseWriter, _ *http.Request) {
	if s.runtime == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "runtime_unavailable", "runtime is unavailable", nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"config": s.runtime.Config()})
}

func (s *Server) putConfig(w http.ResponseWriter, r *http.Request) {
	if s.runtime == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "runtime_unavailable", "runtime is unavailable", nil)
		return
	}
	var body struct {
		Config config.Config `json:"config"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error(), nil)
		return
	}
	s.configMu.Lock()
	defer s.configMu.Unlock()
	previous := s.runtime.Config()
	renames, err := config.DetectRenames(previous, body.Config)
	if err != nil {
		writeAPIError(w, http.StatusUnprocessableEntity, "ambiguous_rename", err.Error(), nil)
		return
	}
	if err := config.Save(s.config, body.Config); err != nil {
		writeAPIError(w, http.StatusUnprocessableEntity, "invalid_config", err.Error(), nil)
		return
	}
	_ = s.runtime.SetConfig(body.Config)
	s.invalidateModels()
	if err := s.migrateSessionAgentReferences(renames); err != nil {
		writeAPIError(w, http.StatusInternalServerError, "agent_rename_failed", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"config": body.Config})
}

// migrateSessionAgentReferences re-points sessions at renamed agents by
// appending a session.agent event. Only active sessions are migrated:
// archived sessions are read-only, and their stored name stays a historical
// record that remains readable. Config validation guarantees names are
// unique case-insensitively, so matching the old name case-insensitively
// cannot hit the wrong session.
func (s *Server) migrateSessionAgentReferences(renames map[string]string) error {
	if len(renames) == 0 {
		return nil
	}
	lookup := make(map[string]string, len(renames))
	for oldName, newName := range renames {
		lookup[config.NormalizeAgentName(oldName)] = newName
	}
	for _, value := range s.store.List(false) {
		newName, ok := lookup[config.NormalizeAgentName(value.AgentName)]
		if !ok {
			continue
		}
		data, err := json.Marshal(session.AgentRenameEventData{AgentName: newName})
		if err != nil {
			return err
		}
		if _, err := s.store.Append(value.ID, "session.agent", "", data); err != nil {
			return fmt.Errorf("migrate session %s to renamed agent %q: %w", value.ID, newName, err)
		}
	}
	return nil
}

// modelEnumerationTimeout bounds a whole model enumeration, including the
// provider process startup. It is generous because app-server and RPC CLIs
// can take seconds to boot, and short enough that a hung provider cannot pin
// a request handler.
const modelEnumerationTimeout = 45 * time.Second

// providerModels enumerates the models of one built-in provider through its
// official interface. It is read-only: it never creates a provider session
// and never changes the configuration. Status codes distinguish the failure
// modes a UI must render differently: 404 unknown provider, 409 disabled,
// 503 CLI unavailable, 504 enumeration timeout, 502 upstream error; an empty
// list is a 200 with an empty models array.
func (s *Server) providerModels(w http.ResponseWriter, r *http.Request) {
	if s.runtime == nil || s.models == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "runtime_unavailable", "runtime is unavailable", nil)
		return
	}
	id := r.PathValue("id")
	target, ok := s.providerByID(id)
	if !ok {
		writeAPIError(w, http.StatusNotFound, "unknown_provider", fmt.Sprintf("unknown built-in provider %q", id), nil)
		return
	}
	if !target.Enabled {
		writeAPIError(w, http.StatusConflict, "provider_disabled", fmt.Sprintf("provider %q is disabled", id), nil)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), modelEnumerationTimeout)
	defer cancel()
	models, err := s.models.Models(ctx, target)
	if err != nil {
		var modelErr *provider.ModelError
		if errors.As(err, &modelErr) {
			switch modelErr.Kind {
			case provider.ModelErrTimeout:
				writeAPIError(w, http.StatusGatewayTimeout, "provider_timeout", modelErr.Error(), nil)
				return
			case provider.ModelErrUnavailable:
				writeAPIError(w, http.StatusServiceUnavailable, "provider_unavailable", modelErr.Error(), nil)
				return
			}
		}
		writeAPIError(w, http.StatusBadGateway, "provider_error", err.Error(), nil)
		return
	}
	if models == nil {
		models = []provider.Model{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"provider": map[string]any{"id": target.ID, "name": target.Name, "type": target.Type},
		"models":   models,
	})
}

// providerByID resolves a built-in provider id against the live
// configuration: a configured provider keeps its name, type, command and
// enabled flag; a built-in provider missing from the configuration is
// reported with its canonical definition and enabled=false.
func (s *Server) providerByID(id string) (config.Provider, bool) {
	canonical, ok := config.BuiltinProvider(id)
	if !ok {
		return config.Provider{}, false
	}
	for _, configured := range s.runtime.Config().AgentProviders {
		if configured.ID == id {
			return configured, true
		}
	}
	return canonical, true
}

func (s *Server) invalidateModels() {
	if s.models != nil {
		s.models.InvalidateAll()
	}
}

// putProviderEnabled flips the enabled flag of one built-in provider without
// touching the rest of the configuration. It is the minimal contract behind
// the four switches of the Web settings UI: clients never have to rebuild or
// resubmit the whole provider structure, and the provider's command and other
// fields survive a disable/enable cycle. A built-in provider missing from an
// old config is created with its canonical defaults.
func (s *Server) putProviderEnabled(w http.ResponseWriter, r *http.Request) {
	if s.runtime == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "runtime_unavailable", "runtime is unavailable", nil)
		return
	}
	var body struct {
		Enabled *bool `json:"enabled"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error(), nil)
		return
	}
	if body.Enabled == nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "enabled is required", nil)
		return
	}
	s.configMu.Lock()
	defer s.configMu.Unlock()
	next, provider, err := s.runtime.Config().SetProviderEnabled(r.PathValue("id"), *body.Enabled)
	if err != nil {
		writeAPIError(w, http.StatusNotFound, "unknown_provider", err.Error(), nil)
		return
	}
	if err := config.Save(s.config, next); err != nil {
		writeAPIError(w, http.StatusUnprocessableEntity, "invalid_config", err.Error(), nil)
		return
	}
	_ = s.runtime.SetConfig(next)
	s.invalidateModels()
	writeJSON(w, http.StatusOK, map[string]any{"provider": provider})
}

// agentStatus extends an agent with its effective availability. An agent is
// unavailable when its provider is disabled or missing; the Web UI hides such
// agents from the new-session choices and the daemon rejects attempts to use
// them anyway.
type agentStatus struct {
	config.Agent
	Available         bool   `json:"available"`
	UnavailableReason string `json:"unavailableReason,omitempty"`
}

func (s *Server) agents(w http.ResponseWriter, _ *http.Request) {
	if s.runtime == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "runtime_unavailable", "runtime is unavailable", nil)
		return
	}
	cfg := s.runtime.Config()
	providers := make(map[string]config.Provider, len(cfg.AgentProviders))
	for _, provider := range cfg.AgentProviders {
		providers[provider.ID] = provider
	}
	agents := make([]agentStatus, 0, len(cfg.Agents))
	for _, agent := range cfg.Agents {
		status := agentStatus{Agent: agent, Available: true}
		provider, ok := providers[agent.ProviderID]
		switch {
		case !ok:
			status.Available = false
			status.UnavailableReason = fmt.Sprintf("provider %q is not configured", agent.ProviderID)
		case !provider.Enabled:
			status.Available = false
			status.UnavailableReason = fmt.Sprintf("provider %q is disabled", agent.ProviderID)
		}
		agents = append(agents, status)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"providers": cfg.AgentProviders,
		"agents":    agents,
		"probes":    cfg.Probes(),
	})
}

func (s *Server) listSessions(w http.ResponseWriter, r *http.Request) {
	archivedOnly := r.URL.Query().Get("archived") == "true"
	includeArchived := archivedOnly || r.URL.Query().Get("includeArchived") == "true"
	query := r.URL.Query()
	filter := session.ListFilter{IncludeArchived: includeArchived}
	if query.Has("sourceApp") {
		filter.SourceApp = stringPointer(query.Get("sourceApp"))
	}
	if query.Has("sourceInstanceId") {
		filter.SourceInstanceID = stringPointer(query.Get("sourceInstanceId"))
	}
	if query.Has("sourceExternalId") {
		filter.SourceExternalID = stringPointer(query.Get("sourceExternalId"))
	}
	values := s.store.Filter(filter)
	if archivedOnly {
		filtered := values[:0]
		for _, value := range values {
			if value.State == session.StateArchived {
				filtered = append(filtered, value)
			}
		}
		values = filtered
	}
	if stateFilter := strings.TrimSpace(r.URL.Query().Get("state")); stateFilter != "" {
		allowed := make(map[string]bool)
		for _, state := range strings.Split(stateFilter, ",") {
			allowed[strings.TrimSpace(state)] = true
		}
		filtered := values[:0]
		for _, value := range values {
			if allowed[value.State] {
				filtered = append(filtered, value)
			}
		}
		values = filtered
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessions": values})
}

func (s *Server) createSession(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Title     string          `json:"title"`
		Cwd       string          `json:"cwd"`
		AgentName string          `json:"agentName"`
		Source    *session.Source `json:"source"`
		// AgentID is the removed reference form. It is still accepted for one
		// compatibility window: the daemon resolves it through the id → name
		// mapping recorded when the legacy configuration was migrated, and
		// rejects ids it cannot map. New clients must use agentName.
		AgentID           string            `json:"agentId"`
		LaunchEnvironment map[string]string `json:"launchEnvironment"`
		InitialMessage    *struct {
			Text string `json:"text"`
		} `json:"initialMessage"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error(), nil)
		return
	}
	agentName := strings.TrimSpace(body.AgentName)
	if id := strings.TrimSpace(body.AgentID); id != "" {
		if agentName != "" {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "send agentName or the deprecated agentId, not both", nil)
			return
		}
		if s.runtime == nil {
			writeAPIError(w, http.StatusUnprocessableEntity, "agent_id_removed", "agentId is no longer supported: agents are referenced by their unique name; send agentName instead", nil)
			return
		}
		mapped, ok := s.runtime.ResolveLegacyAgentName(id)
		if !ok {
			writeAPIError(w, http.StatusUnprocessableEntity, "agent_id_removed", fmt.Sprintf("agentId %q cannot be resolved: agents are now referenced by their unique name; send agentName instead (list names with GET /v1/agents or 'agenthub agents')", id), nil)
			return
		}
		agentName = mapped
	}
	if agentName == "" {
		writeAPIError(w, http.StatusUnprocessableEntity, "agent_required", "agentName is required: sessions are always created with an explicit agent", nil)
		return
	}
	var agent config.Agent
	if s.runtime != nil {
		resolved, _, err := s.runtime.Config().Agent(agentName)
		if err != nil {
			writeAPIError(w, http.StatusUnprocessableEntity, "invalid_agent", err.Error(), nil)
			return
		}
		agent = resolved
	}
	cwd, err := canonicalDirectory(body.Cwd)
	if err != nil {
		writeAPIError(w, http.StatusUnprocessableEntity, "invalid_cwd", err.Error(), nil)
		return
	}
	if err := session.ValidateLaunchEnvironment(body.LaunchEnvironment); err != nil {
		writeAPIError(w, http.StatusUnprocessableEntity, "invalid_launch_environment", err.Error(), nil)
		return
	}
	// Persist the canonical configured name, not the spelling the client
	// sent, so the session always records the user's display form.
	canonicalName := agentName
	if agent.Name != "" {
		canonicalName = agent.Name
	}
	value, err := s.store.Create(session.CreateInput{
		Title:             strings.TrimSpace(body.Title),
		Cwd:               cwd,
		AgentName:         canonicalName,
		Source:            body.Source,
		LaunchEnvironment: body.LaunchEnvironment,
	})
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "session_create_failed", err.Error(), nil)
		return
	}
	if s.runtime != nil {
		started, err := s.runtime.Start(value.ID)
		if err != nil {
			writeAPIError(w, http.StatusBadGateway, "provider_start_failed", err.Error(), map[string]any{"sessionId": value.ID})
			return
		}
		value = started
		if body.InitialMessage != nil && strings.TrimSpace(body.InitialMessage.Text) != "" {
			sent, err := s.runtime.Send(value.ID, body.InitialMessage.Text, false)
			if err != nil {
				writeAPIError(w, http.StatusBadGateway, "turn_start_failed", err.Error(), map[string]any{"sessionId": value.ID})
				return
			}
			value = sent
		}
	}
	w.Header().Set("Location", "/v1/sessions/"+value.ID)
	writeJSON(w, http.StatusCreated, map[string]any{"session": value})
}

func stringPointer(value string) *string {
	return &value
}

// sessionOp is one operation under /v1/sessions/{id}. suffix is the fixed
// path after the session id ("" for the bare session, "approvals/{approvalId}"
// for the one nested operation); doc is the canonical label listed in
// api.md. sessionRoute dispatches through this table, so adding, renaming
// or dropping a session operation here is what the documentation coverage
// test compares api.md against.
type sessionOp struct {
	method  string
	suffix  string
	handler func(http.ResponseWriter, *http.Request, string)
	doc     string
}

func (s *Server) sessionOps() []sessionOp {
	return []sessionOp{
		{http.MethodGet, "", s.getSession, "GET /v1/sessions/{id}"},
		{http.MethodDelete, "", s.archiveSession, "DELETE /v1/sessions/{id}"},
		{http.MethodGet, "events", s.events, "GET /v1/sessions/{id}/events"},
		{http.MethodPost, "messages", s.sendMessage, "POST /v1/sessions/{id}/messages"},
		{http.MethodPost, "resume", s.resumeSession, "POST /v1/sessions/{id}/resume"},
		{http.MethodPost, "interrupt", s.interruptSession, "POST /v1/sessions/{id}/interrupt"},
		{http.MethodPost, "stop", s.stopSession, "POST /v1/sessions/{id}/stop"},
		{http.MethodPost, "approvals/{approvalId}", s.resolveApproval, "POST /v1/sessions/{id}/approvals/{approvalId}"},
	}
}

func (s *Server) sessionRoute(w http.ResponseWriter, r *http.Request) {
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/v1/sessions/"), "/")
	parts := strings.Split(path, "/")
	if parts[0] == "" {
		writeAPIError(w, http.StatusNotFound, "route_not_found", "API route not found", nil)
		return
	}
	id, tail := parts[0], parts[1:]
	for _, op := range s.sessionOps() {
		if r.Method != op.method {
			continue
		}
		var suffix []string
		if op.suffix != "" {
			suffix = strings.Split(op.suffix, "/")
		}
		if len(suffix) != len(tail) {
			continue
		}
		match := true
		for i, segment := range suffix {
			if strings.HasPrefix(segment, "{") {
				r.SetPathValue(strings.Trim(segment, "{}"), tail[i])
				continue
			}
			if segment != tail[i] {
				match = false
			}
		}
		if match {
			op.handler(w, r, id)
			return
		}
	}
	if len(tail) == 0 {
		// The session exists as an addressable resource; the method is not.
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method is not allowed for this API route", nil)
		return
	}
	writeAPIError(w, http.StatusNotFound, "route_not_found", "API route not found", nil)
}

func (s *Server) sendMessage(w http.ResponseWriter, r *http.Request, id string) {
	if s.rejectArchivedSession(w, id) {
		return
	}
	if s.runtime == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "runtime_unavailable", "runtime is unavailable", nil)
		return
	}
	var body struct {
		Text  string `json:"text"`
		Steer bool   `json:"steer"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error(), nil)
		return
	}
	if strings.TrimSpace(body.Text) == "" {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "message text is required", nil)
		return
	}
	current, err := s.store.Get(id)
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	if current.State == session.StateStopping {
		writeAPIError(w, http.StatusConflict, "session_stopping", "session provider is stopping", nil)
		return
	}
	if current.CurrentTurnID != "" && !body.Steer {
		writeAPIError(w, http.StatusConflict, "turn_active", "session already has an active turn; set steer=true or wait", map[string]any{"turnId": current.CurrentTurnID})
		return
	}
	value, err := s.runtime.Send(id, body.Text, body.Steer)
	if err != nil {
		s.writeRuntimeError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"session": value})
}

func (s *Server) resumeSession(w http.ResponseWriter, _ *http.Request, id string) {
	if s.rejectArchivedSession(w, id) {
		return
	}
	if s.runtime == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "runtime_unavailable", "runtime is unavailable", nil)
		return
	}
	current, err := s.store.Get(id)
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	if current.State == session.StateStopping {
		writeAPIError(w, http.StatusConflict, "session_stopping", "session provider is stopping", nil)
		return
	}
	value, err := s.runtime.Start(id)
	if err != nil {
		s.writeRuntimeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"session": value})
}

func (s *Server) interruptSession(w http.ResponseWriter, _ *http.Request, id string) {
	if s.rejectArchivedSession(w, id) {
		return
	}
	if s.runtime == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "runtime_unavailable", "runtime is unavailable", nil)
		return
	}
	current, err := s.store.Get(id)
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	if current.CurrentTurnID == "" {
		writeAPIError(w, http.StatusConflict, "turn_not_active", "session has no active turn to interrupt", nil)
		return
	}
	if err := s.runtime.Interrupt(id); err != nil {
		s.writeRuntimeError(w, err)
		return
	}
	value, _ := s.store.Get(id)
	writeJSON(w, http.StatusOK, map[string]any{"session": value})
}

func (s *Server) stopSession(w http.ResponseWriter, _ *http.Request, id string) {
	if s.rejectArchivedSession(w, id) {
		return
	}
	if s.runtime == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "runtime_unavailable", "runtime is unavailable", nil)
		return
	}
	if err := s.runtime.Stop(id); err != nil {
		s.writeRuntimeError(w, err)
		return
	}
	value, _ := s.store.Get(id)
	writeJSON(w, http.StatusOK, map[string]any{"session": value})
}

func (s *Server) resolveApproval(w http.ResponseWriter, r *http.Request, id string) {
	approvalID := r.PathValue("approvalId")
	if s.rejectArchivedSession(w, id) {
		return
	}
	if s.runtime == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "runtime_unavailable", "runtime is unavailable", nil)
		return
	}
	var body struct {
		Decision string `json:"decision"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error(), nil)
		return
	}
	switch body.Decision {
	case "accept", "acceptForSession", "decline", "cancel":
	default:
		writeAPIError(w, http.StatusBadRequest, "invalid_approval_decision", "decision must be accept, acceptForSession, decline, or cancel", nil)
		return
	}
	current, err := s.store.Get(id)
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	if !containsString(current.PendingApprovalIDs, approvalID) {
		writeAPIError(w, http.StatusConflict, "approval_not_pending", "approval is not pending", map[string]any{"approvalId": approvalID})
		return
	}
	if err := s.runtime.Approve(id, approvalID, body.Decision); err != nil {
		s.writeRuntimeError(w, err)
		return
	}
	value, _ := s.store.Get(id)
	writeJSON(w, http.StatusOK, map[string]any{"session": value})
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func (s *Server) writeRuntimeError(w http.ResponseWriter, err error) {
	if errors.Is(err, session.ErrNotFound) {
		s.writeStoreError(w, err)
		return
	}
	if errors.Is(err, session.ErrArchived) {
		writeAPIError(w, http.StatusConflict, "session_archived", "session is archived and read-only", nil)
		return
	}
	writeAPIError(w, http.StatusConflict, "runtime_operation_failed", err.Error(), nil)
}

func (s *Server) getSession(w http.ResponseWriter, _ *http.Request, id string) {
	value, err := s.store.Get(id)
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"session": value})
}

func (s *Server) archiveSession(w http.ResponseWriter, _ *http.Request, id string) {
	if _, err := s.store.Get(id); err != nil {
		s.writeStoreError(w, err)
		return
	}
	if s.runtime != nil && s.runtime.IsRunning(id) {
		writeAPIError(w, http.StatusConflict, "session_active", "the session provider is still running; stop the session before archiving it", nil)
		return
	}
	value, err := s.store.Archive(id)
	if err != nil {
		switch {
		case errors.Is(err, session.ErrNotFound):
			s.writeStoreError(w, err)
		case errors.Is(err, session.ErrInvalidID):
			writeAPIError(w, http.StatusBadRequest, "invalid_session_id", err.Error(), nil)
		case errors.Is(err, session.ErrSessionActive):
			writeAPIError(w, http.StatusConflict, "session_active", err.Error(), nil)
		case errors.Is(err, session.ErrArchiveConflict):
			writeAPIError(w, http.StatusConflict, "session_archive_conflict", err.Error(), nil)
		default:
			writeAPIError(w, http.StatusInternalServerError, "session_archive_failed", err.Error(), nil)
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"session": value})
}

// rejectArchivedSession writes 409 and reports true when the session is
// archived. Archived sessions are read-only: turns, steer, resume,
// interrupt and approval writes are rejected before reaching the runtime.
func (s *Server) rejectArchivedSession(w http.ResponseWriter, id string) bool {
	value, err := s.store.Get(id)
	if err != nil {
		s.writeStoreError(w, err)
		return true
	}
	if value.State == session.StateArchived {
		writeAPIError(w, http.StatusConflict, "session_archived", "session is archived and read-only", nil)
		return true
	}
	return false
}

func (s *Server) events(w http.ResponseWriter, r *http.Request, id string) {
	after, err := parseEventCursor(r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_event_cursor", err.Error(), nil)
		return
	}
	if !strings.Contains(r.Header.Get("Accept"), "text/event-stream") && r.URL.Query().Get("stream") != "true" {
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		page, err := s.store.EventsPage(id, after, limit)
		if err != nil {
			if errors.Is(err, session.ErrEventCursorAhead) {
				writeAPIError(w, http.StatusConflict, "event_cursor_ahead", err.Error(), map[string]any{
					"latestCursor": page.LatestCursor,
				})
				return
			}
			s.writeStoreError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"events": page.Events,
			"page": map[string]any{
				"after":     page.After,
				"limit":     page.Limit,
				"nextAfter": page.NextAfter,
				"hasMore":   page.HasMore,
			},
			"latestCursor": page.LatestCursor,
		})
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeAPIError(w, http.StatusInternalServerError, "stream_unsupported", "response writer does not support streaming", nil)
		return
	}
	live, highWater, err := s.store.Subscribe(id)
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	defer live.Cancel()
	if after > highWater {
		writeAPIError(w, http.StatusConflict, "event_cursor_ahead",
			fmt.Sprintf("%s: %d > %d", session.ErrEventCursorAhead, after, highWater),
			map[string]any{"latestCursor": highWater})
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	lastSent := after
	for lastSent < highWater {
		if live.Overflowed() {
			return
		}
		replay, err := s.store.EventsThrough(id, lastSent, highWater, session.MaxEventPageSize)
		if err != nil || len(replay) == 0 {
			return
		}
		for _, event := range replay {
			if live.Overflowed() || event.ID != lastSent+1 {
				return
			}
			if err := writeSSE(w, event); err != nil {
				return
			}
			lastSent = event.ID
		}
		flusher.Flush()
	}
	if lastSent == after {
		flusher.Flush()
	}

	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-s.closing:
			// The daemon is shutting down; end the stream so
			// http.Server.Shutdown is not held open by SSE clients.
			return
		case <-live.Overflow():
			return
		case event := <-live.Events():
			if live.Overflowed() {
				return
			}
			if event.ID <= lastSent {
				continue
			}
			if event.ID != lastSent+1 {
				return
			}
			if err := writeSSE(w, event); err != nil {
				return
			}
			lastSent = event.ID
			flusher.Flush()
		case <-heartbeat.C:
			if err := writeSSEHeartbeat(w); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func (s *Server) writeStoreError(w http.ResponseWriter, err error) {
	if errors.Is(err, session.ErrNotFound) {
		writeAPIError(w, http.StatusNotFound, "session_not_found", err.Error(), nil)
		return
	}
	writeAPIError(w, http.StatusInternalServerError, "session_store_failed", err.Error(), nil)
}

func requestMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if mutatingMethod(r.Method) {
			contentType := r.Header.Get("Content-Type")
			if !strings.HasPrefix(strings.ToLower(contentType), "application/json") {
				writeAPIError(w, http.StatusUnsupportedMediaType, "json_required", "Content-Type must be application/json", nil)
				return
			}
			if origin := strings.TrimSpace(r.Header.Get("Origin")); origin != "" && !sameOrigin(origin, r.Host) {
				writeAPIError(w, http.StatusForbidden, "origin_rejected", "browser origin does not match the daemon origin", nil)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func decodeJSON(r *http.Request, target any) error {
	decoder := json.NewDecoder(io.LimitReader(r.Body, 1024*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("request body must contain one JSON value")
		}
		return err
	}
	return nil
}

func canonicalDirectory(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", errors.New("cwd is required")
	}
	absolute, err := filepath.Abs(value)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", errors.New("cwd is not a directory")
	}
	return resolved, nil
}

func parseEventCursor(r *http.Request) (int64, error) {
	value := strings.TrimSpace(r.Header.Get("Last-Event-ID"))
	if value == "" {
		value = strings.TrimSpace(r.URL.Query().Get("after"))
	}
	if value == "" {
		return 0, nil
	}
	cursor, err := strconv.ParseInt(value, 10, 64)
	if err != nil || cursor < 0 {
		return 0, fmt.Errorf("event cursor must be a non-negative integer")
	}
	return cursor, nil
}

// writeSSE frames an event using the default SSE message channel instead of
// a per-type `event:` field. The payload already carries the type, and a
// single channel guarantees that consumers receive every event — including
// event types they do not know about yet — instead of silently dropping
// events their subscription list does not name.
func writeSSE(w http.ResponseWriter, event session.Event) error {
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	return writeSSEBounded(w, func() error {
		_, err := fmt.Fprintf(w, "id: %d\ndata: %s\n\n", event.ID, data)
		return err
	})
}

const sseWriteTimeout = 5 * time.Second

func writeSSEHeartbeat(w http.ResponseWriter) error {
	return writeSSEBounded(w, func() error {
		_, err := fmt.Fprint(w, ": heartbeat\n\n")
		return err
	})
}

// writeSSEBounded prevents a client that stopped reading from pinning an SSE
// handler forever. This is also what makes subscriber overflow terminal at
// the real socket boundary: a blocked write is released, the handler returns,
// and the client can reconnect from its last contiguous durable cursor.
func writeSSEBounded(w http.ResponseWriter, write func() error) error {
	controller := http.NewResponseController(w)
	deadlineSet := controller.SetWriteDeadline(time.Now().Add(sseWriteTimeout)) == nil
	err := write()
	if err == nil && deadlineSet {
		// Clear successful writes so the 15-second idle heartbeat interval
		// does not inherit the previous event's deadline. On failure the
		// expired deadline intentionally remains in force while net/http
		// flushes and closes the response; clearing it there can block the
		// handler a second time on the same non-reading socket.
		_ = controller.SetWriteDeadline(time.Time{})
	}
	if err != nil && deadlineSet {
		// A timed-out HTTP/1 write can leave a partially buffered SSE frame
		// attached to a connection even after the handler returns. AgentHub
		// serves plaintext HTTP, so explicitly take ownership and close the
		// connection when possible; the client then observes a prompt EOF and
		// can reconnect instead of waiting on a permanently truncated frame.
		if connection, _, hijackErr := controller.Hijack(); hijackErr == nil {
			_ = connection.Close()
		}
	}
	return err
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeAPIError(w http.ResponseWriter, status int, code, message string, details any) {
	requestID, _ := session.NewID("req")
	writeJSON(w, status, map[string]any{
		"error": map[string]any{
			"code":      code,
			"message":   message,
			"retryable": retryableAPIError(code),
			"details":   details,
			"requestId": requestID,
		},
	})
}

func retryableAPIError(code string) bool {
	switch code {
	case "runtime_unavailable",
		"provider_unavailable",
		"provider_timeout",
		"provider_error",
		"session_store_failed",
		"session_archive_failed":
		return true
	default:
		return false
	}
}

func (s *Server) apiNotFound(w http.ResponseWriter, _ *http.Request) {
	writeAPIError(w, http.StatusNotFound, "route_not_found", "API route not found", nil)
}

func (s *Server) apiDocsRoute(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method is not allowed for this API route", nil)
		return
	}
	s.apiDocs(w, r)
}

func mutatingMethod(method string) bool {
	return method == http.MethodPost || method == http.MethodPut || method == http.MethodPatch || method == http.MethodDelete
}

func sameOrigin(origin, host string) bool {
	parsed, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return strings.EqualFold(parsed.Scheme, "http") && strings.EqualFold(parsed.Host, host)
}

func spaHandler(root string) http.Handler {
	files := http.FileServer(http.Dir(root))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := filepath.Join(root, filepath.Clean(r.URL.Path))
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			files.ServeHTTP(w, r)
			return
		}
		http.ServeFile(w, r, filepath.Join(root, "index.html"))
	})
}

func Shutdown(ctx context.Context, server *http.Server) error {
	return server.Shutdown(ctx)
}
