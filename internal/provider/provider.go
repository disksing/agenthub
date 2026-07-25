package provider

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
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
	ID       string
	Cwd      string
	Title    string
	Agent    config.Agent
	Provider config.Provider
	Hooks    Hooks
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

type jsonRPC struct {
	command string
	args    []string
	cwd     string
	hooks   Hooks
	inbound func(id json.RawMessage, method string, params json.RawMessage)
	notify  func(method string, params json.RawMessage)

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

func newJSONRPC(command string, args []string, cwd string, hooks Hooks) *jsonRPC {
	return &jsonRPC{command: command, args: args, cwd: cwd, hooks: hooks, nextID: 1, waiting: make(map[string]chan rpcResult), pending: make(map[string]pendingRequest)}
}

func (r *jsonRPC) start() error {
	r.mu.Lock()
	if r.cmd != nil {
		r.mu.Unlock()
		return nil
	}
	cmd := exec.Command(r.command, r.args...)
	cmd.Dir = r.cwd
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

func (r *jsonRPC) request(method string, params any) (json.RawMessage, error) {
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
	select {
	case result, ok := <-ch:
		if !ok {
			return nil, errors.New("provider exited before responding")
		}
		return result.data, result.err
	case <-time.After(15 * time.Minute):
		return nil, fmt.Errorf("%s timed out", method)
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
