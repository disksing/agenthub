package provider

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/disksing/agenthub/internal/config"
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
	if _, err := c.startRequest("initialize", map[string]any{
		"clientInfo":   map[string]any{"name": "agenthub", "title": "AgentHub", "version": "0.1.0"},
		"capabilities": map[string]any{"experimentalApi": true},
	}); err != nil {
		return err
	}
	_ = c.rpc.send("initialized", map[string]any{})
	effort := option(c.options.Agent, "reasoning_effort", "")
	if effort != "" {
		if err := c.validateReasoningEffort(effort); err != nil {
			return err
		}
	}
	method := "thread/start"
	params := map[string]any{
		"cwd": c.options.Cwd, "sandbox": option(c.options.Agent, "sandbox", "danger-full-access"),
		"approvalPolicy": option(c.options.Agent, "approval", "never"), "approvalsReviewer": "user", "threadSource": "api",
	}
	if model := option(c.options.Agent, "model", ""); model != "" {
		params["model"] = model
	}
	if effort != "" {
		params["config"] = map[string]any{"model_reasoning_effort": effort}
	}
	if resumeID != "" {
		method = "thread/resume"
		params["threadId"] = resumeID
	}
	result, err := c.startRequest(method, params)
	if err != nil {
		return err
	}
	if effort != "" {
		if err := checkReasoningEffortEcho(result, effort); err != nil {
			return err
		}
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

// startRequest issues a handshake request bounded by the startup timeout,
// so a stuck app-server fails session creation fast instead of hanging it.
func (c *codexSession) startRequest(method string, params any) (json.RawMessage, error) {
	raw, err := c.rpc.requestWithTimeout(method, params, startupRequestTimeout)
	if err != nil {
		return nil, startRequestError("Codex app-server", method, err)
	}
	return raw, nil
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

// codexModel is the subset of the app-server model/list response AgentHub
// needs to validate the reasoning_effort agent option.
type codexModel struct {
	id        string
	isDefault bool
	efforts   []string
}

// validateReasoningEffort checks the configured effort against the values the
// target model advertises via model/list. The app-server silently accepts
// unknown effort values, so AgentHub validates before starting the thread.
// When the model catalog is unavailable or the target model is not listed,
// the value is passed through untouched.
func (c *codexSession) validateReasoningEffort(effort string) error {
	raw, err := c.startRequest("model/list", map[string]any{})
	if err != nil {
		c.rpc.emit("provider.warning", map[string]any{"message": "model/list unavailable, skipping reasoning_effort validation", "error": err.Error()})
		return nil
	}
	return checkReasoningEffort(effort, option(c.options.Agent, "model", ""), parseCodexModels(raw))
}

func parseCodexModels(raw json.RawMessage) []codexModel {
	var payload struct {
		Data []struct {
			ID        string `json:"id"`
			IsDefault bool   `json:"isDefault"`
			Efforts   []struct {
				Effort string `json:"reasoningEffort"`
			} `json:"supportedReasoningEfforts"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &payload) != nil {
		return nil
	}
	models := make([]codexModel, 0, len(payload.Data))
	for _, entry := range payload.Data {
		model := codexModel{id: entry.ID, isDefault: entry.IsDefault}
		for _, value := range entry.Efforts {
			model.efforts = append(model.efforts, value.Effort)
		}
		models = append(models, model)
	}
	return models
}

// checkReasoningEffort validates effort against the requested model (or the
// server default model when none is requested). Unknown targets pass through.
func checkReasoningEffort(effort, requestedModel string, models []codexModel) error {
	var target *codexModel
	for index := range models {
		if requestedModel != "" && models[index].id == requestedModel {
			target = &models[index]
			break
		}
		if requestedModel == "" && models[index].isDefault {
			target = &models[index]
			break
		}
	}
	if target == nil || len(target.efforts) == 0 {
		return nil
	}
	for _, supported := range target.efforts {
		if supported == effort {
			return nil
		}
	}
	return fmt.Errorf("invalid reasoning_effort %q for model %q (supported: %s)", effort, target.id, strings.Join(target.efforts, ", "))
}

// checkReasoningEffortEcho compares the reasoningEffort echoed by
// thread/start or thread/resume with the requested value.
func checkReasoningEffortEcho(result json.RawMessage, effort string) error {
	echo := lookup(result, "reasoningEffort")
	if echo == "" {
		return nil
	}
	if echo != effort {
		return fmt.Errorf("Codex applied reasoning effort %q instead of requested %q", echo, effort)
	}
	return nil
}

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
