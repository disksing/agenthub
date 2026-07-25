package provider

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/disksing/project-incubator/agenthub/internal/config"
)

type codexSession struct {
	options Options
	rpc     *jsonRPC
	thread  string
	turn    string
}

func newCodex(command string, options Options) *codexSession {
	value := &codexSession{options: options}
	value.rpc = newJSONRPC(command, []string{"app-server"}, options.Cwd, options.Hooks)
	value.rpc.inbound = value.inbound
	value.rpc.notify = value.notification
	return value
}

func (c *codexSession) Start(resumeID string) error {
	if err := c.rpc.start(); err != nil {
		return fmt.Errorf("start Codex app-server: %w", err)
	}
	if _, err := c.rpc.request("initialize", map[string]any{
		"clientInfo":   map[string]any{"name": "agenthub", "title": "AgentHub", "version": "0.1.0"},
		"capabilities": map[string]any{"experimentalApi": true},
	}); err != nil {
		return err
	}
	_ = c.rpc.send("initialized", map[string]any{})
	method := "thread/start"
	params := map[string]any{
		"cwd": c.options.Cwd, "sandbox": option(c.options.Agent, "sandbox", "danger-full-access"),
		"approvalPolicy": option(c.options.Agent, "approval", "never"), "approvalsReviewer": "user", "threadSource": "api",
	}
	if model := option(c.options.Agent, "model", ""); model != "" {
		params["model"] = model
	}
	if resumeID != "" {
		method = "thread/resume"
		params["threadId"] = resumeID
	}
	result, err := c.rpc.request(method, params)
	if err != nil {
		return err
	}
	c.thread = lookup(result, "thread", "id")
	if c.thread == "" {
		c.thread = lookup(result, "threadId")
	}
	if c.thread == "" {
		c.thread = lookup(result, "id")
	}
	if c.thread == "" {
		return errors.New(method + " returned no thread id")
	}
	if c.options.Hooks.NativeID != nil {
		c.options.Hooks.NativeID(c.thread)
	}
	return nil
}

func (c *codexSession) Prompt(text string, steer bool) error {
	if c.thread == "" {
		return errors.New("Codex thread is not ready")
	}
	input := []map[string]string{{"type": "text", "text": text}}
	if steer && c.turn != "" {
		_, err := c.rpc.request("turn/steer", map[string]any{"threadId": c.thread, "expectedTurnId": c.turn, "input": input})
		return err
	}
	params := map[string]any{"threadId": c.thread, "cwd": c.options.Cwd, "approvalPolicy": option(c.options.Agent, "approval", "never"), "input": input}
	if model := option(c.options.Agent, "model", ""); model != "" {
		params["model"] = model
	}
	_, err := c.rpc.request("turn/start", params)
	return err
}

func (c *codexSession) Interrupt() error {
	if c.thread == "" || c.turn == "" {
		return nil
	}
	_, err := c.rpc.request("turn/interrupt", map[string]any{"threadId": c.thread, "turnId": c.turn})
	return err
}

func (c *codexSession) Approve(id, decision string) error {
	c.rpc.mu.Lock()
	pending, ok := c.rpc.pending[id]
	if ok {
		delete(c.rpc.pending, id)
	}
	c.rpc.mu.Unlock()
	if !ok {
		return fmt.Errorf("unknown approval %q", id)
	}
	if pending.method == "item/permissions/requestApproval" {
		return c.rpc.respond(pending.id, map[string]any{"permissions": map[string]any{}, "scope": "turn"})
	}
	if decision != "accept" && decision != "acceptForSession" && decision != "cancel" {
		decision = "decline"
	}
	return c.rpc.respond(pending.id, map[string]any{"decision": decision})
}

func (c *codexSession) Close() error { return c.rpc.close() }

func (c *codexSession) inbound(id json.RawMessage, method string, params json.RawMessage) {
	switch method {
	case "item/commandExecution/requestApproval", "item/fileChange/requestApproval", "item/permissions/requestApproval":
		key := strings.Trim(string(id), `"`)
		c.rpc.mu.Lock()
		c.rpc.pending[key] = pendingRequest{id: append(json.RawMessage(nil), id...), method: method, params: append(json.RawMessage(nil), params...)}
		c.rpc.mu.Unlock()
		if c.options.Hooks.Approval != nil {
			c.options.Hooks.Approval(key, method, params)
		}
	default:
		_ = c.rpc.respondError(id, -32601, "unsupported by AgentHub")
	}
}

func (c *codexSession) notification(method string, params json.RawMessage) {
	event := Event{Type: "provider.event", Data: map[string]any{"method": method, "raw": json.RawMessage(params)}}
	switch method {
	case "turn/started":
		c.turn = lookup(params, "turn", "id")
		event.Type = "provider.turn.started"
	case "turn/completed":
		c.turn = ""
		event.Type, event.TurnDone = "provider.turn.completed", true
	case "turn/failed", "error":
		c.turn = ""
		event.Type, event.TurnDone, event.TurnFailed = "provider.error", true, true
	case "item/agentMessage/delta":
		text := lookup(params, "delta")
		if text == "" {
			text = lookup(params, "text")
		}
		event.Type = "message.assistant.delta"
		event.Data = map[string]any{"text": text, "method": method}
	case "item/reasoning/summaryTextDelta", "item/reasoning/textDelta":
		event.Type = "message.reasoning.delta"
		event.Data = map[string]any{"text": lookup(params, "delta"), "method": method}
	case "item/started", "item/completed", "item/updated", "item/commandExecution/outputDelta", "command/exec/outputDelta":
		event.Type = "tool.event"
	}
	if c.options.Hooks.Event != nil {
		c.options.Hooks.Event(event)
	}
}

func option(agent config.Agent, key, fallback string) string {
	if value := strings.TrimSpace(agent.Options[key]); value != "" {
		return value
	}
	return fallback
}
