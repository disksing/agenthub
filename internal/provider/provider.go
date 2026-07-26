package provider

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	goruntime "runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/disksing/agenthub/internal/config"
)

type Event struct {
	Type       string
	Data       any
	TurnDone   bool
	TurnFailed bool
}

type Hooks struct {
	Event      func(Event)
	NativeID   func(string)
	Approval   func(id, method string, params json.RawMessage)
	ProcessEnd func(error)
}

type Session interface {
	Start(resumeID string) error
	Prompt(text string, steer bool) error
	Interrupt() error
	Approve(id, decision string) error
	Close() error
}

type Options struct {
	ID          string
	Cwd         string
	Title       string
	Agent       config.Agent
	Provider    config.Provider
	Environment map[string]string
	Hooks       Hooks
}

func New(options Options) (Session, error) {
	command, err := config.ResolveProviderCommand(options.Provider)
	if err != nil {
		return nil, err
	}
	switch options.Provider.Type {
	case "codex":
		return newCodex(command, options), nil
	case "opencode", "kimi":
		return newACP(command, options), nil
	case "pi":
		return newPi(command, options), nil
	default:
		return nil, fmt.Errorf("unsupported provider type %q", options.Provider.Type)
	}
}

type rpcResult struct {
	data json.RawMessage
	err  error
}

// defaultRequestTimeout bounds long-running provider requests such as
// session/prompt, which may legitimately run for many minutes.
const defaultRequestTimeout = 15 * time.Minute

// startupRequestTimeout bounds the handshake requests that must complete
// before a session becomes ready (initialize, session/new, thread/start,
// and friends). A provider that cannot answer these is stuck — for example
// blocked on an operating-system permission prompt while reading the
// session working directory — and the session must fail fast with an
// actionable error instead of hanging the create request.
var startupRequestTimeout = 2 * time.Minute

// RequestTimeoutError reports a provider that did not answer a JSON-RPC
// request within the allowed time.
type RequestTimeoutError struct {
	Method  string
	Timeout time.Duration
}

func (e *RequestTimeoutError) Error() string {
	return fmt.Sprintf("%s timed out after %s waiting for the provider to respond", e.Method, e.Timeout)
}

// startRequestError wraps a handshake failure with the provider name and,
// for timeouts, an actionable hint about the known stuck-provider causes.
func startRequestError(providerName, method string, err error) error {
	var timeoutErr *RequestTimeoutError
	if errors.As(err, &timeoutErr) {
		hint := "the provider process is running but did not respond; it may be stuck reading the session working directory"
		if goruntime.GOOS == "darwin" {
			hint += " — on macOS this happens when a privacy permission prompt (System Settings > Privacy & Security, e.g. the Downloads folder or Full Disk Access) is waiting for user approval"
		}
		return fmt.Errorf("start %s: %w: %s", providerName, err, hint)
	}
	return fmt.Errorf("start %s: %s failed: %w", providerName, method, err)
}

type jsonRPC struct {
	command     string
	args        []string
	cwd         string
	environment map[string]string
	hooks       Hooks
	inbound     func(id json.RawMessage, method string, params json.RawMessage)
	notify      func(method string, params json.RawMessage)

	mu      sync.Mutex
	writeMu sync.Mutex
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	nextID  int64
	waiting map[string]chan rpcResult
	pending map[string]pendingRequest
	closed  bool
}

type pendingRequest struct {
	id     json.RawMessage
	method string
	params json.RawMessage
}

func newJSONRPC(command string, args []string, cwd string, environment map[string]string, hooks Hooks) *jsonRPC {
	return &jsonRPC{command: command, args: args, cwd: cwd, environment: environment, hooks: hooks, nextID: 1, waiting: make(map[string]chan rpcResult), pending: make(map[string]pendingRequest)}
}

func (r *jsonRPC) start() error {
	r.mu.Lock()
	if r.cmd != nil {
		r.mu.Unlock()
		return nil
	}
	cmd := exec.Command(r.command, r.args...)
	cmd.Dir = r.cwd
	cmd.Env = processEnvironment(r.environment)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		r.mu.Unlock()
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		r.mu.Unlock()
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		r.mu.Unlock()
		return err
	}
	if err := cmd.Start(); err != nil {
		r.mu.Unlock()
		return err
	}
	r.cmd, r.stdin = cmd, stdin
	r.mu.Unlock()
	go r.readLoop(stdout)
	go r.stderrLoop(stderr)
	go func() {
		err := cmd.Wait()
		r.finish(err)
	}()
	return nil
}

// processEnvironment merges per-session overrides onto the daemon's
// environment. exec.Cmd does not merge Env itself, so the complete inherited
// environment must be supplied while ensuring a session value wins when the
// daemon defines the same key.
func processEnvironment(overrides map[string]string) []string {
	base := os.Environ()
	if len(overrides) == 0 {
		return base
	}
	result := append([]string(nil), base...)
	index := make(map[string]int, len(base))
	for position, entry := range result {
		if key, _, ok := strings.Cut(entry, "="); ok {
			index[key] = position
		}
	}
	keys := make([]string, 0, len(overrides))
	for key := range overrides {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		entry := key + "=" + overrides[key]
		if position, ok := index[key]; ok {
			result[position] = entry
			continue
		}
		index[key] = len(result)
		result = append(result, entry)
	}
	return result
}

func (r *jsonRPC) request(method string, params any) (json.RawMessage, error) {
	return r.requestWithTimeout(method, params, defaultRequestTimeout)
}

func (r *jsonRPC) requestWithTimeout(method string, params any, timeout time.Duration) (json.RawMessage, error) {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil, errors.New("provider process is closed")
	}
	id := r.nextID
	r.nextID++
	key := strconv.FormatInt(id, 10)
	ch := make(chan rpcResult, 1)
	r.waiting[key] = ch
	r.mu.Unlock()
	if err := r.write(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
		r.mu.Lock()
		delete(r.waiting, key)
		r.mu.Unlock()
		return nil, err
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case result, ok := <-ch:
		if !ok {
			return nil, errors.New("provider exited before responding")
		}
		return result.data, result.err
	case <-timer.C:
		r.mu.Lock()
		delete(r.waiting, key)
		r.mu.Unlock()
		return nil, &RequestTimeoutError{Method: method, Timeout: timeout}
	}
}

func (r *jsonRPC) send(method string, params any) error {
	return r.write(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
}

func (r *jsonRPC) respond(id json.RawMessage, result any) error {
	return r.writeRaw(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

func (r *jsonRPC) respondError(id json.RawMessage, code int, message string) error {
	return r.writeRaw(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": code, "message": message}})
}

func (r *jsonRPC) write(value any) error { return r.writeRaw(value) }
func (r *jsonRPC) writeRaw(value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	r.writeMu.Lock()
	defer r.writeMu.Unlock()
	if r.stdin == nil {
		return errors.New("provider stdin is unavailable")
	}
	_, err = r.stdin.Write(append(data, '\n'))
	return err
}

func (r *jsonRPC) readLoop(reader io.Reader) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		var envelope map[string]json.RawMessage
		if err := json.Unmarshal(scanner.Bytes(), &envelope); err != nil {
			r.emit("provider.error", map[string]any{"message": "invalid JSON-RPC output", "error": err.Error()})
			continue
		}
		method := rawString(envelope["method"])
		if id, ok := envelope["id"]; ok {
			if method != "" {
				if r.inbound != nil {
					r.inbound(id, method, envelope["params"])
				} else {
					_ = r.respondError(id, -32601, "unsupported request")
				}
				continue
			}
			key := strings.Trim(string(id), `"`)
			r.mu.Lock()
			ch := r.waiting[key]
			delete(r.waiting, key)
			r.mu.Unlock()
			if ch == nil {
				continue
			}
			if raw, ok := envelope["error"]; ok && len(raw) > 0 && string(raw) != "null" {
				ch <- rpcResult{err: fmt.Errorf("%s", compact(raw))}
			} else {
				ch <- rpcResult{data: envelope["result"]}
			}
			close(ch)
			continue
		}
		if method != "" && r.notify != nil {
			r.notify(method, envelope["params"])
		}
	}
}

func (r *jsonRPC) stderrLoop(reader io.Reader) {
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		if text := strings.TrimSpace(scanner.Text()); text != "" {
			r.emit("provider.stderr", map[string]any{"text": text})
		}
	}
}

func (r *jsonRPC) emit(kind string, data any) {
	if r.hooks.Event != nil {
		r.hooks.Event(Event{Type: kind, Data: data})
	}
}

func (r *jsonRPC) finish(processErr error) {
	r.mu.Lock()
	wasClosed := r.closed
	r.closed = true
	for key, ch := range r.waiting {
		delete(r.waiting, key)
		close(ch)
	}
	r.mu.Unlock()
	if !wasClosed && r.hooks.ProcessEnd != nil {
		r.hooks.ProcessEnd(processErr)
	}
}

func (r *jsonRPC) close() error {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil
	}
	r.closed = true
	cmd, stdin := r.cmd, r.stdin
	r.mu.Unlock()
	if stdin != nil {
		_ = stdin.Close()
	}
	if cmd != nil && cmd.Process != nil {
		if pgid, err := syscall.Getpgid(cmd.Process.Pid); err == nil {
			_ = syscall.Kill(-pgid, syscall.SIGTERM)
		} else {
			_ = cmd.Process.Kill()
		}
	}
	return nil
}

func rawString(raw json.RawMessage) string {
	var value string
	_ = json.Unmarshal(raw, &value)
	return value
}

func lookup(raw json.RawMessage, keys ...string) string {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return ""
	}
	for _, key := range keys {
		object, ok := value.(map[string]any)
		if !ok {
			return ""
		}
		value = object[key]
	}
	text, _ := value.(string)
	return text
}

func compact(raw json.RawMessage) string {
	var out bytes.Buffer
	if json.Compact(&out, raw) == nil {
		return out.String()
	}
	return string(raw)
}
