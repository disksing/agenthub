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
)

type piSession struct {
	options Options
	command string
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	mu      sync.Mutex
	writeMu sync.Mutex
	nextID  int64
	waiting map[string]chan piResponse
	closed  bool
}

type piResponse struct {
	Type    string          `json:"type"`
	ID      json.RawMessage `json:"id"`
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data"`
	Error   string          `json:"error"`
}

func newPi(command string, options Options) *piSession {
	return &piSession{command: command, options: options, nextID: 1, waiting: make(map[string]chan piResponse)}
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
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	p.cmd, p.stdin = cmd, stdin
	go p.readLoop(stdout)
	go p.stderrLoop(stderr)
	go func() {
		err := cmd.Wait()
		p.finish(err)
	}()
	response, err := p.request("get_state", nil)
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
	command := "prompt"
	if steer {
		command = "steer"
	}
	go func() {
		if _, err := p.request(command, map[string]any{"message": text}); err != nil && p.options.Hooks.Event != nil {
			p.options.Hooks.Event(Event{Type: "provider.error", Data: map[string]any{"message": err.Error()}, TurnDone: true, TurnFailed: true})
		}
	}()
	return nil
}

func (p *piSession) Interrupt() error {
	_, err := p.request("abort", nil)
	return err
}
func (p *piSession) Approve(_, _ string) error { return errors.New("Pi RPC does not expose approvals") }
func (p *piSession) Close() error {
	p.mu.Lock()
	p.closed = true
	cmd, stdin := p.cmd, p.stdin
	p.mu.Unlock()
	if stdin != nil {
		_ = stdin.Close()
	}
	if cmd != nil && cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	}
	return nil
}

func (p *piSession) request(command string, fields map[string]any) (piResponse, error) {
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
	select {
	case value, ok := <-ch:
		if !ok {
			return piResponse{}, errors.New("Pi exited before responding")
		}
		response = value
	case <-time.After(15 * time.Minute):
		p.mu.Lock()
		delete(p.waiting, key)
		p.mu.Unlock()
		return piResponse{}, fmt.Errorf("Pi %s timed out", command)
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
	wasClosed := p.closed
	p.closed = true
	for key, ch := range p.waiting {
		delete(p.waiting, key)
		close(ch)
	}
	p.mu.Unlock()
	if !wasClosed && p.options.Hooks.ProcessEnd != nil {
		p.options.Hooks.ProcessEnd(err)
	}
}
