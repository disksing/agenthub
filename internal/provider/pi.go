package provider

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/disksing/agenthub/internal/session"
)

type piSession struct {
	options Options
	command string
	cmd     *exec.Cmd
	pgid    int
	stdin   io.WriteCloser
	mu      sync.Mutex
	writeMu sync.Mutex
	nextID  int64
	waiting map[string]chan piResponse
	closed  bool
	done    chan struct{}
	prompts asyncOperations
}

type piResponse struct {
	Type    string          `json:"type"`
	ID      json.RawMessage `json:"id"`
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data"`
	Error   string          `json:"error"`
}

func newPi(command string, options Options) *piSession {
	value := &piSession{command: command, options: options, nextID: 1, waiting: make(map[string]chan piResponse), done: make(chan struct{})}
	processEnd := value.options.Hooks.ProcessEnd
	value.options.Hooks.ProcessEnd = func(err error) {
		value.prompts.stopAndWait()
		if processEnd != nil {
			processEnd(err)
		}
	}
	return value
}

func (p *piSession) Start(resumeID string) error {
	args := []string{"--mode", "rpc"}
	if model := strings.TrimSpace(p.options.Agent.Options["model"]); model != "" {
		args = append(args, "--model", model)
	}
	if resumeID != "" {
		args = append(args, "--session", resumeID)
	}
	if p.options.Title != "" {
		args = append(args, "--name", p.options.Title)
	}
	cmd := exec.Command(p.command, args...)
	cmd.Dir = p.options.Cwd
	cmd.Env = processEnvironment(p.options.Environment)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = processTerminateGrace
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, stdoutWriter := io.Pipe()
	stderr, stderrWriter := io.Pipe()
	cmd.Stdout = stdoutWriter
	cmd.Stderr = stderrWriter
	if err := cmd.Start(); err != nil {
		_ = stdout.Close()
		_ = stdoutWriter.Close()
		_ = stderr.Close()
		_ = stderrWriter.Close()
		return err
	}
	pgid, err := syscall.Getpgid(cmd.Process.Pid)
	if err != nil {
		_ = stdout.Close()
		_ = stderr.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		_ = stdoutWriter.Close()
		_ = stderrWriter.Close()
		return fmt.Errorf("get provider process group: %w", err)
	}
	p.cmd, p.pgid, p.stdin = cmd, pgid, stdin
	stdoutDone := make(chan struct{})
	stderrDone := make(chan struct{})
	go func() {
		defer close(stdoutDone)
		defer stdout.Close()
		p.readLoop(stdout)
	}()
	go func() {
		defer close(stderrDone)
		defer stderr.Close()
		p.stderrLoop(stderr)
	}()
	go func() {
		// Wait owns the copy into these writers and therefore cannot publish
		// process completion before the final protocol output is delivered.
		err := cmd.Wait()
		_ = stdoutWriter.Close()
		_ = stderrWriter.Close()
		<-stdoutDone
		<-stderrDone
		p.finish(err)
	}()
	if p.options.Hooks.ProcessStart != nil {
		if err := p.options.Hooks.ProcessStart(ProcessInfo{PID: cmd.Process.Pid, ProcessGroupID: pgid}); err != nil {
			_ = p.Close()
			return fmt.Errorf("persist provider process: %w", err)
		}
	}
	response, err := p.startRequest("get_state", nil)
	if err != nil {
		return err
	}
	native := lookup(response.Data, "sessionId")
	if native == "" {
		return errors.New("Pi RPC returned no session id")
	}
	if p.options.Hooks.NativeID != nil {
		p.options.Hooks.NativeID(native)
	}
	return nil
}

func (p *piSession) Prompt(text string, steer bool) error {
	return p.PromptMessage(session.MessageInput{Text: text, Role: session.MessageRoleUser, Steer: steer})
}

func (p *piSession) PromptMessage(value session.MessageInput) error {
	text, err := PromptText(value)
	if err != nil {
		return err
	}
	return p.prompt(text, value.Steer)
}

func (p *piSession) prompt(text string, steer bool) error {
	command := "prompt"
	if steer {
		command = "steer"
	}
	if !p.prompts.begin() {
		return errors.New("Pi provider process is stopping")
	}
	go func() {
		defer p.prompts.done()
		if _, err := p.requestLongRunning(command, map[string]any{"message": text}); err != nil && p.options.Hooks.Event != nil {
			p.options.Hooks.Event(Event{Type: "provider.error", Data: map[string]any{"message": err.Error()}, TurnDone: true, TurnFailed: true})
		}
	}()
	return nil
}

func (p *piSession) Interrupt() error {
	_, err := p.request("abort", nil)
	return err
}
func (p *piSession) Approve(string, ApprovalResolution) error {
	return errors.New("Pi RPC does not expose approvals")
}
func (p *piSession) Close() error {
	p.mu.Lock()
	if p.closed {
		cmd, pgid, stdin, done := p.cmd, p.pgid, p.stdin, p.done
		p.mu.Unlock()
		if cmd != nil {
			return terminateChildProcess(cmd, pgid, stdin, done)
		}
		return nil
	}
	p.closed = true
	cmd, pgid, stdin := p.cmd, p.pgid, p.stdin
	p.mu.Unlock()
	return terminateChildProcess(cmd, pgid, stdin, p.done)
}

// startRequest issues a handshake request bounded by the startup timeout,
// so a stuck Pi process fails session creation fast instead of hanging it.
func (p *piSession) startRequest(command string, fields map[string]any) (piResponse, error) {
	response, err := p.requestWithTimeout(command, fields, startupRequestTimeout)
	if err != nil {
		return response, startRequestError("Pi Coding Agent", command, err)
	}
	return response, nil
}

func (p *piSession) request(command string, fields map[string]any) (piResponse, error) {
	return p.requestWithTimeout(command, fields, controlRequestTimeout)
}

// requestLongRunning waits for a prompt/steer response until the provider
// settles the turn or session shutdown closes the waiter.
func (p *piSession) requestLongRunning(command string, fields map[string]any) (piResponse, error) {
	return p.requestWithTimeout(command, fields, 0)
}

func (p *piSession) requestWithTimeout(command string, fields map[string]any, timeout time.Duration) (piResponse, error) {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return piResponse{}, errors.New("Pi process is closed")
	}
	id := p.nextID
	p.nextID++
	key := fmt.Sprint(id)
	ch := make(chan piResponse, 1)
	p.waiting[key] = ch
	p.mu.Unlock()
	value := map[string]any{"id": id, "type": command}
	for key, field := range fields {
		value[key] = field
	}
	if err := p.write(value); err != nil {
		p.mu.Lock()
		delete(p.waiting, key)
		p.mu.Unlock()
		return piResponse{}, err
	}
	var response piResponse
	if timeout <= 0 {
		value, ok := <-ch
		if !ok {
			return piResponse{}, errors.New("Pi exited before responding")
		}
		response = value
		if !response.Success {
			return response, fmt.Errorf("Pi %s: %s", command, response.Error)
		}
		return response, nil
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case value, ok := <-ch:
		if !ok {
			return piResponse{}, errors.New("Pi exited before responding")
		}
		response = value
	case <-timer.C:
		p.mu.Lock()
		delete(p.waiting, key)
		p.mu.Unlock()
		return piResponse{}, &RequestTimeoutError{Method: command, Timeout: timeout}
	}
	if !response.Success {
		return response, fmt.Errorf("Pi %s: %s", command, response.Error)
	}
	return response, nil
}

func (p *piSession) write(value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	_, err = p.stdin.Write(append(data, '\n'))
	return err
}

func (p *piSession) readLoop(reader io.Reader) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		raw := append(json.RawMessage(nil), scanner.Bytes()...)
		var envelope struct {
			Type string          `json:"type"`
			ID   json.RawMessage `json:"id"`
		}
		if json.Unmarshal(raw, &envelope) != nil {
			continue
		}
		if envelope.Type == "response" {
			var response piResponse
			_ = json.Unmarshal(raw, &response)
			key := strings.Trim(string(response.ID), `"`)
			p.mu.Lock()
			ch := p.waiting[key]
			delete(p.waiting, key)
			p.mu.Unlock()
			if ch != nil {
				ch <- response
				close(ch)
			}
			continue
		}
		p.event(envelope.Type, raw)
	}
}

func (p *piSession) event(kind string, raw json.RawMessage) {
	event := Event{Type: "provider.event", Data: map[string]any{"method": kind, "raw": raw}}
	var value struct {
		AssistantMessageEvent struct {
			Type  string `json:"type"`
			Delta string `json:"delta"`
		} `json:"assistantMessageEvent"`
		ToolName string `json:"toolName"`
	}
	_ = json.Unmarshal(raw, &value)
	switch kind {
	case "message_update":
		switch value.AssistantMessageEvent.Type {
		case "text_delta":
			event.Type, event.Data = "message.assistant.delta", map[string]any{"text": value.AssistantMessageEvent.Delta, "method": kind}
		case "thinking_delta":
			event.Type, event.Data = "message.reasoning.delta", map[string]any{"text": value.AssistantMessageEvent.Delta, "method": kind}
		}
	case "tool_execution_start", "tool_execution_end":
		event.Type = "tool.event"
	case "agent_settled":
		event.Type, event.TurnDone = "provider.turn.completed", true
	case "extension_error":
		event.Type, event.TurnDone, event.TurnFailed = "provider.error", true, true
	}
	if p.options.Hooks.Event != nil {
		p.options.Hooks.Event(event)
	}
}

func (p *piSession) stderrLoop(reader io.Reader) {
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		if text := strings.TrimSpace(scanner.Text()); text != "" && p.options.Hooks.Event != nil {
			p.options.Hooks.Event(Event{Type: "provider.stderr", Data: map[string]any{"text": text}})
		}
	}
}

func (p *piSession) finish(err error) {
	p.mu.Lock()
	p.closed = true
	for key, ch := range p.waiting {
		delete(p.waiting, key)
		close(ch)
	}
	p.mu.Unlock()
	close(p.done)
	if p.options.Hooks.ProcessEnd != nil {
		p.options.Hooks.ProcessEnd(err)
	}
}
