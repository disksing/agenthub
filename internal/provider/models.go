package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"syscall"

	"github.com/disksing/agenthub/internal/config"
)

// Model is the canonical, provider-neutral description of one enumerable
// model. ID is the exact identifier the provider's launch path accepts (the
// agent "model" option); Label is a human-friendly display name. Provider
// wire formats never leak into this type.
type Model struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	Default bool   `json:"default,omitempty"`
}

// ModelErrorKind classifies model enumeration failures so the API layer can
// map them to distinct status codes.
type ModelErrorKind string

const (
	// ModelErrUnavailable means the provider CLI is missing or its process
	// could not be started at all.
	ModelErrUnavailable ModelErrorKind = "unavailable"
	// ModelErrTimeout means enumeration did not finish before the caller's
	// deadline.
	ModelErrTimeout ModelErrorKind = "timeout"
	// ModelErrUpstream means the provider ran but reported an error or
	// returned data that could not be understood.
	ModelErrUpstream ModelErrorKind = "upstream"
)

// ModelError carries a classified enumeration failure.
type ModelError struct {
	Kind ModelErrorKind
	Err  error
}

func (e *ModelError) Error() string { return e.Err.Error() }
func (e *ModelError) Unwrap() error { return e.Err }

func modelError(kind ModelErrorKind, format string, args ...any) *ModelError {
	return &ModelError{Kind: kind, Err: fmt.Errorf(format, args...)}
}

// ListModels enumerates the models currently usable by one built-in provider
// through its official interface. It never creates a provider session, never
// writes provider configuration, and always terminates the processes it
// starts. The result is deduplicated by ID and keeps the provider's own
// ordering. An empty list is a valid result, not an error.
func ListModels(ctx context.Context, provider config.Provider) ([]Model, error) {
	command, err := config.ResolveProviderCommand(provider)
	if err != nil {
		return nil, modelError(ModelErrUnavailable, "%s", err.Error())
	}
	switch provider.Type {
	case "codex":
		return listCodexModels(ctx, command)
	case "pi":
		return listPiModels(ctx, command)
	case "kimi":
		return listKimiModels(ctx, command)
	case "opencode":
		return listOpenCodeModels(ctx, command)
	default:
		return nil, modelError(ModelErrUnavailable, "model enumeration is not supported for provider type %q", provider.Type)
	}
}

// dedupeModels removes duplicate IDs, keeping the first occurrence and the
// provider's own ordering.
func dedupeModels(models []Model) []Model {
	seen := make(map[string]bool, len(models))
	result := make([]Model, 0, len(models))
	for _, model := range models {
		if model.ID == "" || seen[model.ID] {
			continue
		}
		seen[model.ID] = true
		if model.Label == "" {
			model.Label = model.ID
		}
		result = append(result, model)
	}
	return result
}

// codex app-server model/list ------------------------------------------------

func listCodexModels(ctx context.Context, command string) ([]Model, error) {
	rpc := newJSONRPC(command, []string{"app-server"}, "", Hooks{})
	defer rpc.close()
	if err := rpc.start(); err != nil {
		return nil, modelError(ModelErrUnavailable, "start Codex app-server: %s", err)
	}
	if _, err := rpcRequestCtx(ctx, rpc, "initialize", map[string]any{
		"clientInfo":   map[string]any{"name": "agenthub", "title": "AgentHub", "version": "0.1.0"},
		"capabilities": map[string]any{"experimentalApi": true},
	}); err != nil {
		return nil, err
	}
	_ = rpc.send("initialized", map[string]any{})
	raw, err := rpcRequestCtx(ctx, rpc, "model/list", map[string]any{})
	if err != nil {
		return nil, err
	}
	var payload struct {
		Data []struct {
			ID          string `json:"id"`
			DisplayName string `json:"displayName"`
			Hidden      bool   `json:"hidden"`
			IsDefault   bool   `json:"isDefault"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, modelError(ModelErrUpstream, "Codex model/list returned unreadable data: %s", err)
	}
	models := make([]Model, 0, len(payload.Data))
	for _, entry := range payload.Data {
		if entry.Hidden {
			continue
		}
		models = append(models, Model{ID: entry.ID, Label: entry.DisplayName, Default: entry.IsDefault})
	}
	return dedupeModels(models), nil
}

// rpcRequestCtx issues a JSON-RPC request bounded by ctx. On deadline it
// closes the connection so the provider process does not linger.
func rpcRequestCtx(ctx context.Context, rpc *jsonRPC, method string, params any) (json.RawMessage, error) {
	type outcome struct {
		data json.RawMessage
		err  error
	}
	ch := make(chan outcome, 1)
	go func() {
		data, err := rpc.request(method, params)
		ch <- outcome{data, err}
	}()
	select {
	case value := <-ch:
		if value.err != nil {
			return nil, modelError(ModelErrUpstream, "Codex %s failed: %s", method, value.err)
		}
		return value.data, nil
	case <-ctx.Done():
		rpc.close()
		return nil, ctxModelError(ctx, "Codex "+method)
	}
}

// pi RPC get_available_models ------------------------------------------------

// listPiModels uses a minimal, session-less Pi RPC exchange: it starts
// `pi --mode rpc --no-session`, so no session file is ever written.
func listPiModels(ctx context.Context, command string) ([]Model, error) {
	cmd := exec.CommandContext(ctx, command, "--mode", "rpc", "--no-session")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, modelError(ModelErrUnavailable, "start Pi RPC: %s", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, modelError(ModelErrUnavailable, "start Pi RPC: %s", err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return nil, modelError(ModelErrUnavailable, "start Pi RPC: %s", err)
	}
	lines := make(chan string, 64)
	go func() {
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
		for scanner.Scan() {
			lines <- scanner.Text()
		}
		close(lines)
	}()
	defer func() {
		_ = stdin.Close()
		if cmd.Process != nil {
			if pgid, err := syscall.Getpgid(cmd.Process.Pid); err == nil {
				_ = syscall.Kill(-pgid, syscall.SIGTERM)
			}
		}
		_ = cmd.Wait()
	}()

	ask := func(id int, command string) (json.RawMessage, error) {
		request, _ := json.Marshal(map[string]any{"id": id, "type": command})
		if _, err := stdin.Write(append(request, '\n')); err != nil {
			return nil, modelError(ModelErrUpstream, "Pi %s: %s", command, err)
		}
		for {
			select {
			case line, ok := <-lines:
				if !ok {
					return nil, modelError(ModelErrUpstream, "Pi exited during %s: %s", command, stderrTail(&stderr))
				}
				var envelope struct {
					Type    string          `json:"type"`
					ID      json.RawMessage `json:"id"`
					Success bool            `json:"success"`
					Data    json.RawMessage `json:"data"`
					Error   string          `json:"error"`
				}
				if json.Unmarshal([]byte(line), &envelope) != nil || envelope.Type != "response" {
					continue
				}
				if strings.Trim(string(envelope.ID), `"`) != fmt.Sprint(id) {
					continue
				}
				if !envelope.Success {
					return nil, modelError(ModelErrUpstream, "Pi %s: %s", command, envelope.Error)
				}
				return envelope.Data, nil
			case <-ctx.Done():
				return nil, ctxModelError(ctx, "Pi "+command)
			}
		}
	}

	// get_state reveals the model Pi would use without an explicit --model,
	// which is the honest meaning of "default" for this provider.
	var current string
	if state, err := ask(1, "get_state"); err == nil {
		var parsed struct {
			Model struct {
				Provider string `json:"provider"`
				ID       string `json:"id"`
			} `json:"model"`
		}
		if json.Unmarshal(state, &parsed) == nil && parsed.Model.Provider != "" && parsed.Model.ID != "" {
			current = parsed.Model.Provider + "/" + parsed.Model.ID
		}
	}

	raw, err := ask(2, "get_available_models")
	if err != nil {
		return nil, err
	}
	var payload struct {
		Models []struct {
			Provider string `json:"provider"`
			ID       string `json:"id"`
			Name     string `json:"name"`
		} `json:"models"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, modelError(ModelErrUpstream, "Pi get_available_models returned unreadable data: %s", err)
	}
	models := make([]Model, 0, len(payload.Models))
	for _, entry := range payload.Models {
		if entry.Provider == "" || entry.ID == "" {
			continue
		}
		id := entry.Provider + "/" + entry.ID
		models = append(models, Model{ID: id, Label: entry.Name, Default: id == current})
	}
	return dedupeModels(models), nil
}

// kimi provider list --json --------------------------------------------------

func listKimiModels(ctx context.Context, command string) ([]Model, error) {
	out, err := runListCommand(ctx, command, "provider", "list", "--json")
	if err != nil {
		return nil, err
	}
	// The models object preserves the configuration order, so decode it with
	// a token stream instead of a map.
	var document struct {
		Models json.RawMessage `json:"models"`
	}
	if err := json.Unmarshal(out, &document); err != nil {
		return nil, modelError(ModelErrUpstream, "kimi provider list --json returned unreadable data: %s", err)
	}
	if len(document.Models) == 0 {
		return nil, modelError(ModelErrUpstream, "kimi provider list --json returned no models object")
	}
	decoder := json.NewDecoder(bytes.NewReader(document.Models))
	token, err := decoder.Token()
	if err != nil {
		return nil, modelError(ModelErrUpstream, "kimi provider list --json returned unreadable models: %s", err)
	}
	if delimiter, ok := token.(json.Delim); !ok || delimiter != '{' {
		return nil, modelError(ModelErrUpstream, "kimi provider list --json models is not an object")
	}
	var models []Model
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return nil, modelError(ModelErrUpstream, "kimi provider list --json returned unreadable models: %s", err)
		}
		var entry struct {
			DisplayName string `json:"displayName"`
		}
		if err := decoder.Decode(&entry); err != nil {
			return nil, modelError(ModelErrUpstream, "kimi provider list --json returned unreadable models: %s", err)
		}
		id, _ := key.(string)
		models = append(models, Model{ID: id, Label: entry.DisplayName})
	}
	return dedupeModels(models), nil
}

// opencode models --verbose ---------------------------------------------------

func listOpenCodeModels(ctx context.Context, command string) ([]Model, error) {
	out, err := runListCommand(ctx, command, "models", "--verbose")
	if err != nil {
		return nil, err
	}
	return parseOpenCodeModels(string(out)), nil
}

// parseOpenCodeModels reads the `opencode models --verbose` layout: one
// unindented `provider/model` line per model, each followed by an indented
// pretty-printed JSON metadata block. A model whose metadata cannot be parsed
// keeps its ID as the label.
func parseOpenCodeModels(output string) []Model {
	var models []Model
	id := ""
	var block []string
	flush := func() {
		if id == "" {
			return
		}
		label := id
		if len(block) > 0 {
			var meta struct {
				Name string `json:"name"`
			}
			if json.Unmarshal([]byte(strings.Join(block, "\n")), &meta) == nil && meta.Name != "" {
				label = meta.Name
			}
		}
		models = append(models, Model{ID: id, Label: label})
	}
	for _, line := range strings.Split(output, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if line[0] != ' ' && line[0] != '\t' && line[0] != '{' && line[0] != '}' {
			flush()
			id = strings.TrimSpace(line)
			block = block[:0]
			continue
		}
		block = append(block, line)
	}
	flush()
	return dedupeModels(models)
}

// shared helpers ---------------------------------------------------------------

// runListCommand executes a short-lived, read-only provider CLI command with
// stdout capture and classified errors.
func runListCommand(ctx context.Context, command string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, command, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return nil, modelError(ModelErrUnavailable, "%s %s: %s", command, strings.Join(args, " "), err)
	}
	err := cmd.Wait()
	if ctx.Err() != nil {
		return nil, ctxModelError(ctx, command+" "+strings.Join(args, " "))
	}
	if err != nil {
		return nil, modelError(ModelErrUpstream, "%s %s failed: %s", command, strings.Join(args, " "), stderrTail(&stderr))
	}
	return stdout.Bytes(), nil
}

func ctxModelError(ctx context.Context, what string) *ModelError {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return modelError(ModelErrTimeout, "%s timed out", what)
	}
	return modelError(ModelErrUpstream, "%s was canceled: %s", what, ctx.Err())
}

// stderrTail returns a short, single-line excerpt of captured stderr for
// error messages. It never includes more than 200 characters.
func stderrTail(stderr *bytes.Buffer) string {
	text := strings.TrimSpace(stderr.String())
	if text == "" {
		return "no diagnostics"
	}
	lines := strings.Split(text, "\n")
	last := strings.TrimSpace(lines[len(lines)-1])
	if len(last) > 200 {
		last = last[:200] + "…"
	}
	return last
}
