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
	"time"

	"github.com/disksing/agenthub/internal/config"
	"github.com/disksing/agenthub/internal/runtime"
	"github.com/disksing/agenthub/internal/session"
)

type Server struct {
	store     *session.Store
	startedAt time.Time
	version   string
	runtime   *runtime.Manager
	config    string
	webDir    string
	listen    *ListenAddress
}

type Dependencies struct {
	Runtime    *runtime.Manager
	ConfigPath string
	WebDir     string
	// Listen, when set, enables the Host header guard derived from the
	// validated listen address.
	Listen *ListenAddress
}

func New(store *session.Store, version string, startedAt time.Time, dependencies ...Dependencies) *Server {
	server := &Server{store: store, version: version, startedAt: startedAt}
	if len(dependencies) > 0 {
		server.runtime = dependencies[0].Runtime
		server.config = dependencies[0].ConfigPath
		server.webDir = dependencies[0].WebDir
		server.listen = dependencies[0].Listen
	}
	return server
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/health", s.health)
	mux.HandleFunc("GET /v1/status", s.status)
	mux.HandleFunc("GET /v1/config", s.getConfig)
	mux.HandleFunc("PUT /v1/config", s.putConfig)
	mux.HandleFunc("GET /v1/agents", s.agents)
	mux.HandleFunc("GET /v1/sessions", s.listSessions)
	mux.HandleFunc("POST /v1/sessions", s.createSession)
	mux.HandleFunc("/v1/sessions/", s.sessionRoute)
	if s.webDir != "" {
		mux.Handle("/", spaHandler(s.webDir))
	}
	var handler http.Handler = requestMiddleware(mux)
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
		"version":       s.version,
		"startedAt":     s.startedAt,
		"uptimeSeconds": int64(time.Since(s.startedAt).Seconds()),
		"sessionStore": map[string]any{
			"path":         s.store.Root(),
			"archivePath":  s.store.ArchiveRoot(),
			"sessionCount": len(s.store.List(true)),
		},
		"runtime": s.runtimeStatus(),
	})
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
	if err := config.Save(s.config, body.Config); err != nil {
		writeAPIError(w, http.StatusUnprocessableEntity, "invalid_config", err.Error(), nil)
		return
	}
	_ = s.runtime.SetConfig(body.Config)
	writeJSON(w, http.StatusOK, map[string]any{"config": body.Config})
}

func (s *Server) agents(w http.ResponseWriter, _ *http.Request) {
	if s.runtime == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "runtime_unavailable", "runtime is unavailable", nil)
		return
	}
	cfg := s.runtime.Config()
	writeJSON(w, http.StatusOK, map[string]any{
		"providers": cfg.AgentProviders,
		"agents":    cfg.Agents,
		"probes":    cfg.Probes(),
	})
}

func (s *Server) listSessions(w http.ResponseWriter, r *http.Request) {
	archivedOnly := r.URL.Query().Get("archived") == "true"
	includeArchived := archivedOnly || r.URL.Query().Get("includeArchived") == "true"
	values := s.store.List(includeArchived)
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
		Title          string `json:"title"`
		Cwd            string `json:"cwd"`
		AgentID        string `json:"agentId"`
		InitialMessage *struct {
			Text string `json:"text"`
		} `json:"initialMessage"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error(), nil)
		return
	}
	agentID := strings.TrimSpace(body.AgentID)
	if agentID == "" {
		writeAPIError(w, http.StatusUnprocessableEntity, "agent_required", "agentId is required: sessions are always created with an explicit agent", nil)
		return
	}
	if s.runtime != nil {
		if _, _, err := s.runtime.Config().Agent(agentID); err != nil {
			writeAPIError(w, http.StatusUnprocessableEntity, "invalid_agent", err.Error(), nil)
			return
		}
	}
	cwd, err := canonicalDirectory(body.Cwd)
	if err != nil {
		writeAPIError(w, http.StatusUnprocessableEntity, "invalid_cwd", err.Error(), nil)
		return
	}
	value, err := s.store.Create(session.CreateInput{
		Title:   strings.TrimSpace(body.Title),
		Cwd:     cwd,
		AgentID: agentID,
	})
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "session_create_failed", err.Error(), nil)
		return
	}
	if s.runtime != nil {
		value, err = s.runtime.Start(value.ID)
		if err != nil {
			writeAPIError(w, http.StatusBadGateway, "provider_start_failed", err.Error(), map[string]any{"sessionId": value.ID})
			return
		}
		if body.InitialMessage != nil && strings.TrimSpace(body.InitialMessage.Text) != "" {
			value, err = s.runtime.Send(value.ID, body.InitialMessage.Text, false)
			if err != nil {
				writeAPIError(w, http.StatusBadGateway, "turn_start_failed", err.Error(), map[string]any{"sessionId": value.ID})
				return
			}
		}
	}
	w.Header().Set("Location", "/v1/sessions/"+value.ID)
	writeJSON(w, http.StatusCreated, map[string]any{"session": value})
}

func (s *Server) sessionRoute(w http.ResponseWriter, r *http.Request) {
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/v1/sessions/"), "/")
	parts := strings.Split(path, "/")
	if len(parts) == 0 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	id := parts[0]
	if len(parts) == 1 {
		switch r.Method {
		case http.MethodGet:
			s.getSession(w, id)
		case http.MethodDelete:
			s.archiveSession(w, id)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
		return
	}
	if len(parts) == 2 && parts[1] == "events" && r.Method == http.MethodGet {
		s.events(w, r, id)
		return
	}
	if len(parts) == 2 && r.Method == http.MethodPost {
		switch parts[1] {
		case "messages":
			s.sendMessage(w, r, id)
		case "resume":
			s.resumeSession(w, id)
		case "interrupt":
			s.interruptSession(w, id)
		case "stop":
			s.stopSession(w, id)
		default:
			http.NotFound(w, r)
		}
		return
	}
	if len(parts) == 3 && parts[1] == "approvals" && r.Method == http.MethodPost {
		s.resolveApproval(w, r, id, parts[2])
		return
	}
	http.NotFound(w, r)
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
	value, err := s.runtime.Send(id, body.Text, body.Steer)
	if err != nil {
		s.writeRuntimeError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"session": value})
}

func (s *Server) resumeSession(w http.ResponseWriter, id string) {
	if s.rejectArchivedSession(w, id) {
		return
	}
	if s.runtime == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "runtime_unavailable", "runtime is unavailable", nil)
		return
	}
	value, err := s.runtime.Start(id)
	if err != nil {
		s.writeRuntimeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"session": value})
}

func (s *Server) interruptSession(w http.ResponseWriter, id string) {
	if s.rejectArchivedSession(w, id) {
		return
	}
	if s.runtime == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "runtime_unavailable", "runtime is unavailable", nil)
		return
	}
	if err := s.runtime.Interrupt(id); err != nil {
		s.writeRuntimeError(w, err)
		return
	}
	value, _ := s.store.Get(id)
	writeJSON(w, http.StatusOK, map[string]any{"session": value})
}

func (s *Server) stopSession(w http.ResponseWriter, id string) {
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

func (s *Server) resolveApproval(w http.ResponseWriter, r *http.Request, id, approvalID string) {
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
	if err := s.runtime.Approve(id, approvalID, body.Decision); err != nil {
		s.writeRuntimeError(w, err)
		return
	}
	value, _ := s.store.Get(id)
	writeJSON(w, http.StatusOK, map[string]any{"session": value})
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

func (s *Server) getSession(w http.ResponseWriter, id string) {
	value, err := s.store.Get(id)
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"session": value})
}

func (s *Server) archiveSession(w http.ResponseWriter, id string) {
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
	after := parseEventCursor(r)
	if !strings.Contains(r.Header.Get("Accept"), "text/event-stream") && r.URL.Query().Get("stream") != "true" {
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		events, err := s.store.EventsAfter(id, after, limit)
		if err != nil {
			s.writeStoreError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"events": events})
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeAPIError(w, http.StatusInternalServerError, "stream_unsupported", "response writer does not support streaming", nil)
		return
	}
	live, cancel, err := s.store.Subscribe(id)
	if err != nil {
		s.writeStoreError(w, err)
		return
	}
	defer cancel()
	replay, err := s.store.EventsAfter(id, after, 1000)
	if err != nil {
		s.writeStoreError(w, err)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	lastSent := after
	for _, event := range replay {
		if err := writeSSE(w, event); err != nil {
			return
		}
		lastSent = event.ID
	}
	flusher.Flush()

	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case event, ok := <-live:
			if !ok {
				return
			}
			if event.ID <= lastSent {
				continue
			}
			if err := writeSSE(w, event); err != nil {
				return
			}
			lastSent = event.ID
			flusher.Flush()
		case <-heartbeat.C:
			if _, err := fmt.Fprint(w, ": heartbeat\n\n"); err != nil {
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

func parseEventCursor(r *http.Request) int64 {
	value := strings.TrimSpace(r.Header.Get("Last-Event-ID"))
	if value == "" {
		value = strings.TrimSpace(r.URL.Query().Get("after"))
	}
	cursor, _ := strconv.ParseInt(value, 10, 64)
	return cursor
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
	_, err = fmt.Fprintf(w, "id: %d\ndata: %s\n\n", event.ID, data)
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
			"details":   details,
			"requestId": requestID,
		},
	})
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
