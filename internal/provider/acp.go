package provider

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

type acpSession struct {
	options      Options
	rpc          *jsonRPC
	sessionID    string
	capabilities struct {
		LoadSession         bool                       `json:"loadSession"`
		SessionCapabilities map[string]json.RawMessage `json:"sessionCapabilities"`
	}
}

type acpSessionResult struct {
	SessionID     string `json:"sessionId"`
	ConfigOptions []struct {
		ID           string `json:"id"`
		Category     string `json:"category"`
		CurrentValue string `json:"currentValue"`
		Options      []struct {
			Value string `json:"value"`
		} `json:"options"`
	} `json:"configOptions"`
}

func newACP(command string, options Options) *acpSession {
	args := []string{"acp"}
	value := &acpSession{options: options}
	value.rpc = newJSONRPC(command, args, options.Cwd, options.Environment, options.Hooks)
	value.rpc.inbound = value.inbound
	value.rpc.notify = value.notification
	return value
}

func (a *acpSession) Start(resumeID string) error {
	if err := a.rpc.start(); err != nil {
		return fmt.Errorf("start %s ACP: %w", a.options.Provider.Name, err)
	}
	result, err := a.startRequest("initialize", map[string]any{
		"protocolVersion": 1,
		"clientCapabilities": map[string]any{
			"fs":       map[string]any{"readTextFile": false, "writeTextFile": false},
			"terminal": false,
		},
		"clientInfo": map[string]any{"name": "agenthub", "title": "AgentHub", "version": "0.1.0"},
	})
	if err != nil {
		return err
	}
	var initialized struct {
		ProtocolVersion   int             `json:"protocolVersion"`
		AgentCapabilities json.RawMessage `json:"agentCapabilities"`
	}
	if err := json.Unmarshal(result, &initialized); err != nil {
		return err
	}
	if initialized.ProtocolVersion != 1 {
		return fmt.Errorf("unsupported ACP version %d", initialized.ProtocolVersion)
	}
	_ = json.Unmarshal(initialized.AgentCapabilities, &a.capabilities)

	method := "session/new"
	params := map[string]any{"cwd": a.options.Cwd, "mcpServers": []any{}}
	if resumeID != "" {
		method = "session/resume"
		if !a.supports("resume") {
			if !a.capabilities.LoadSession {
				return errors.New("ACP provider does not support session resume/load")
			}
			method = "session/load"
		}
		params = map[string]any{"sessionId": resumeID, "cwd": a.options.Cwd}
		if method == "session/load" {
			params["mcpServers"] = []any{}
		}
	}
	raw, err := a.startRequest(method, params)
	if err != nil {
		return err
	}
	var session acpSessionResult
	if len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &session); err != nil {
			return err
		}
	}
	if session.SessionID == "" {
		session.SessionID = resumeID
	}
	if session.SessionID == "" {
		return errors.New(method + " returned no session id")
	}
	a.sessionID = session.SessionID
	if err := a.configure(session); err != nil {
		return err
	}
	if a.options.Hooks.NativeID != nil {
		a.options.Hooks.NativeID(a.sessionID)
	}
	return nil
}

func (a *acpSession) configure(session acpSessionResult) error {
	wanted := map[string]string{
		"model": strings.TrimSpace(a.options.Agent.Options["model"]),
		"mode":  strings.TrimSpace(a.options.Agent.Options["mode"]),
	}
	if a.options.Provider.Type == "kimi" && wanted["mode"] == "build" {
		wanted["mode"] = "yolo"
	}
	for _, option := range session.ConfigOptions {
		value := wanted[option.Category]
		if value == "" {
			continue
		}
		available := option.CurrentValue == value
		for _, choice := range option.Options {
			available = available || choice.Value == value
		}
		if !available {
			return fmt.Errorf("%s %s %q is unavailable", a.options.Provider.Name, option.Category, value)
		}
		if _, err := a.startRequest("session/set_config_option", map[string]any{"sessionId": session.SessionID, "configId": option.ID, "value": value}); err != nil {
			return err
		}
	}
	return nil
}

// startRequest issues a handshake request that must complete before the
// session can become ready. These requests use the bounded startup timeout:
// a provider that never answers (observed with Kimi Code when the operating
// system blocks its read of the session working directory on a pending
// privacy permission prompt) must fail the session creation quickly with an
// actionable error instead of hanging the API call.
func (a *acpSession) startRequest(method string, params any) (json.RawMessage, error) {
	raw, err := a.rpc.requestWithTimeout(method, params, startupRequestTimeout)
	if err != nil {
		return nil, startRequestError(a.options.Provider.Name+" ACP", method, err)
	}
	return raw, nil
}

func (a *acpSession) Prompt(text string, steer bool) error {
	if steer {
		return errors.New("ACP does not support steering an active prompt")
	}
	if a.sessionID == "" {
		return errors.New("ACP session is not ready")
	}
	go func() {
		result, err := a.rpc.request("session/prompt", map[string]any{
			"sessionId": a.sessionID,
			"prompt":    []map[string]any{{"type": "text", "text": text}},
		})
		event := Event{Type: "provider.turn.completed", Data: map[string]any{"raw": result}, TurnDone: true}
		if err != nil {
			event.Type, event.TurnFailed, event.Data = "provider.error", true, map[string]any{"message": err.Error()}
		}
		if a.options.Hooks.Event != nil {
			a.options.Hooks.Event(event)
		}
	}()
	return nil
}

func (a *acpSession) Interrupt() error {
	if a.sessionID == "" {
		return nil
	}
	return a.rpc.send("session/cancel", map[string]any{"sessionId": a.sessionID})
}

func (a *acpSession) Approve(id, decision string) error {
	a.rpc.mu.Lock()
	pending, ok := a.rpc.pending[id]
	if ok {
		delete(a.rpc.pending, id)
	}
	a.rpc.mu.Unlock()
	if !ok {
		return fmt.Errorf("unknown approval %q", id)
	}
	var params struct {
		Options []struct {
			OptionID string `json:"optionId"`
			Kind     string `json:"kind"`
		} `json:"options"`
	}
	_ = json.Unmarshal(pending.params, &params)
	if decision == "cancel" || decision == "decline" {
		return a.rpc.respond(pending.id, map[string]any{"outcome": map[string]any{"outcome": "cancelled"}})
	}
	optionID := ""
	preferred := "allow_once"
	if decision == "acceptForSession" {
		preferred = "allow_always"
	}
	for _, choice := range params.Options {
		if choice.Kind == preferred || optionID == "" {
			optionID = choice.OptionID
		}
	}
	if optionID == "" {
		return errors.New("approval request exposes no permission options")
	}
	return a.rpc.respond(pending.id, map[string]any{"outcome": map[string]any{"outcome": "selected", "optionId": optionID}})
}

func (a *acpSession) Close() error {
	if a.sessionID != "" && a.supports("close") {
		_, _ = a.rpc.request("session/close", map[string]any{"sessionId": a.sessionID})
	}
	return a.rpc.close()
}

func (a *acpSession) supports(name string) bool {
	raw := a.capabilities.SessionCapabilities[name]
	return len(raw) > 0 && string(raw) != "null"
}

func (a *acpSession) inbound(id json.RawMessage, method string, params json.RawMessage) {
	if method == "session/request_permission" {
		key := strings.Trim(string(id), `"`)
		a.rpc.mu.Lock()
		a.rpc.pending[key] = pendingRequest{id: append(json.RawMessage(nil), id...), method: method, params: append(json.RawMessage(nil), params...)}
		a.rpc.mu.Unlock()
		if a.options.Hooks.Approval != nil {
			a.options.Hooks.Approval(key, method, params)
		}
		return
	}
	_ = a.rpc.respondError(id, -32601, "unsupported by AgentHub client capabilities")
}

func (a *acpSession) notification(method string, params json.RawMessage) {
	event := Event{Type: "provider.event", Data: map[string]any{"method": method, "raw": json.RawMessage(params)}}
	if method == "session/update" {
		var update struct {
			Update json.RawMessage `json:"update"`
		}
		_ = json.Unmarshal(params, &update)
		kind := lookup(update.Update, "sessionUpdate")
		switch kind {
		case "agent_message_chunk":
			event.Type = "message.assistant.delta"
			event.Data = map[string]any{"text": lookup(update.Update, "content", "text"), "method": method}
		case "agent_thought_chunk":
			event.Type = "message.reasoning.delta"
			event.Data = map[string]any{"text": lookup(update.Update, "content", "text"), "method": method}
		case "tool_call", "tool_call_update":
			event.Type = "tool.event"
		case "plan", "plan_update", "plan_removed":
			event.Type = "plan.event"
		default:
			event.Type = "provider.metadata"
		}
	}
	if a.options.Hooks.Event != nil {
		a.options.Hooks.Event(event)
	}
}
