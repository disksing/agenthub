# AgentHub

[简体中文](README.zh-CN.md)

AgentHub is a local agent launcher and session hub. A single Go daemon manages Codex, Kimi, Pi/Grok, and OpenCode on your machine, and both the Web UI and the CLI work through the same HTTP API and SSE event stream.

## Capabilities

- Listens on loopback only by default; LAN/wildcard/IPv6 addresses can be configured explicitly. No accounts, tokens, or API authentication.
- Independent provider and agent configuration.
- Sessions are always created with an explicitly selected agent; there is no implicit routing or fallback.
- Real provider adapters:
  - Codex app-server
  - Kimi / OpenCode ACP v1
  - Pi JSONL RPC, including models such as Kimi K3 and Grok
- Session creation, chat, steer, interrupt, stop, resume, archive, and approvals.
- On-demand recovery of provider-native sessions/threads after a daemon restart.
- Same-origin Web UI: session list, real-time chat, status, approvals, stop, and a structured settings panel for providers and agents.
- CLI: one-shot runs, interactive chat, attach, event queries, and session management.
- Each session stores only `session.json` and an append-only `events.jsonl`; turns and approvals are events, with no separate files.

## Build and Run

Requires Go 1.24+ and Node.js. The Web UI lives in `frontend/` and is served same-origin by the daemon after building:

```bash
cd frontend
npm install
npm run build
cd ..
go build -o agenthub ./cmd/agenthub
./agenthub serve
```

Open <http://127.0.0.1:4646>.

### Listen Address

By default the daemon listens on loopback only; `agenthub serve` is equivalent to `agenthub serve --addr 127.0.0.1:4646`. To make the daemon reachable from other devices on the LAN, explicitly choose a local address with `--addr host:port`:

```bash
agenthub serve --addr 192.168.2.150:4646   # a specific LAN IPv4 address
agenthub serve --addr 0.0.0.0:4646         # all IPv4 interfaces (wildcard)
agenthub serve --addr '[::]:4646'          # all IPv6 interfaces (wildcard)
agenthub serve --addr '[::1]:4646'         # IPv6 loopback only
agenthub serve --addr myhost.local:4646    # a hostname/domain that resolves to a local interface
```

The hostname must resolve to a local network interface or loopback. Unresolvable hostnames, addresses of non-local interfaces, malformed values, and invalid ports all fail at startup with an error; there is no silent fallback to another address. IPv6 addresses must be enclosed in square brackets.

> **Security warning**: AgentHub has no accounts, tokens, or API authentication. When listening on a non-loopback or wildcard address, any device that can reach that address gets full control of the daemon (running agents, modifying sessions and configuration). Only use this on trusted networks, and never expose it directly to the public internet. The startup log prints the same warning.

When listening on a non-loopback address, the local CLI still discovers the daemon automatically through the loopback endpoint in `server.json`. Browser write requests still require the `Origin` to match the request `Host`, and the `Host` must be a local interface address or the local hostname (to prevent DNS rebinding); arbitrary origins are not accepted.

During development you can run the two parts separately:

```bash
go run ./cmd/agenthub serve
cd frontend && npm run dev
```

Vite proxies `/v1` to the default daemon port.

## CLI

The CLI ships layered help: `agenthub help` prints an overview plus a concept guide (providers, agents, sessions, turns, approvals, events), `agenthub help <command>` (optionally `agenthub help session <subcommand>`) prints per-command usage, options, defaults and examples, and `agenthub <command> --help` does the same inline. Unknown commands and invalid arguments exit non-zero with a pointer to the matching help topic.

```bash
agenthub help
agenthub help session approve
agenthub serve --help

agenthub status
agenthub agents

agenthub run --agent pi-kimi --cwd . "Investigate why the tests fail"
agenthub run --agent codex-default --cwd . "Implement this feature and run the tests"

agenthub chat --agent gpt-5-6-sol --cwd .
agenthub session create --agent pi-kimi --title "bug hunt"
agenthub session attach <session-id>
agenthub session list
agenthub session list --archived
agenthub session show <session-id>
agenthub session events <session-id>
agenthub session resume <session-id>
agenthub session interrupt <session-id>
agenthub session stop <session-id>
agenthub session approve --decision accept <session-id> <approval-id>
agenthub session archive <session-id>
```

Interactive chat supports `/interrupt`, `/stop`, and `/quit`. The CLI automatically discovers the endpoint from the daemon's `server.json`; you can also set `AGENTHUB_ENDPOINT`.

## Configuration

The default config file is:

```text
$HOME/.agenthub/config.json
```

On first startup, if the config does not exist, AgentHub generates its own minimal default configuration; it does not read or migrate any other program's config. Config structure:

```json
{
  "version": 1,
  "agentProviders": [
    { "id": "codex", "name": "Codex app-server", "type": "codex", "enabled": true },
    { "id": "kimi", "name": "Kimi Code", "type": "kimi", "enabled": true },
    { "id": "pi", "name": "Pi Coding Agent", "type": "pi", "enabled": true }
  ],
  "agents": [
    {
      "id": "pi-kimi",
      "name": "pi-kimi",
      "providerId": "pi",
      "options": { "mode": "build", "model": "kimi-coding/k3" }
    }
  ]
}
```

A provider wraps a local agent runtime or protocol; an agent references one provider and holds its concrete launch options. Every session is created with an explicit agent ID (`POST /v1/sessions` requires `agentId`, and the CLI requires `--agent`); an unknown, missing, or disabled-provider agent fails with a clear error instead of being routed elsewhere.

Codex agents accept the options `model`, `sandbox`, `approval`, and `reasoning_effort`. `reasoning_effort` controls the Codex reasoning ("thinking") effort: it is sent as the `model_reasoning_effort` config override on `thread/start` and `thread/resume`, and the daemon validates the value against the efforts the selected model advertises via `model/list` (for example `low`, `medium`, `high`, `xhigh`; some models add `max` and `ultra`). An unsupported value fails session creation with the list of valid values; an empty value inherits the Codex default.

The Web UI's **Settings** panel is the recommended way to edit this configuration. It provides structured, validated forms for providers and agents, along with provider command availability probes. All changes go through the daemon API (`PUT /v1/config`), which remains the only writer of the config file — no manual JSON editing is required.

### Migrating Older Configs

Earlier versions supported agent profiles, tag-based routing, and a `defaultChatAgentId` fallback. These were removed: sessions now always name an explicit agent. Config files that still contain the legacy `agentProfiles` or `defaultChatAgentId` keys keep working — the keys are ignored on read, and the daemon rewrites the file once without them on startup. Providers and agents are preserved untouched. Sessions recorded before this change remain readable and resumable as long as they have a determined agent; a legacy session that never started (and therefore has no agent) fails with a clear error instead of being guessed onto a configured agent.

Command discovery order: the provider's `command`, `AGENTHUB_*_CLI`, then `PATH`. Supported:

- `AGENTHUB_CODEX_CLI`
- `AGENTHUB_OPENCODE_CLI`
- `AGENTHUB_KIMI_CLI`
- `AGENTHUB_PI_CLI`

`AGENTHUB_HOME=/path` isolates all config, data, and runtime state into a single directory; the config file then lives at `/path/config/config.json`, which is useful for testing.

## API

Main endpoints:

```text
GET    /v1/health
GET    /v1/status
GET    /v1/config
PUT    /v1/config
GET    /v1/agents

POST   /v1/sessions
GET    /v1/sessions
GET    /v1/sessions/{id}
DELETE /v1/sessions/{id}
POST   /v1/sessions/{id}/messages
POST   /v1/sessions/{id}/resume
POST   /v1/sessions/{id}/interrupt
POST   /v1/sessions/{id}/stop
POST   /v1/sessions/{id}/approvals/{approvalId}
GET    /v1/sessions/{id}/events
```

The events endpoint returns JSON for plain requests; with `Accept: text/event-stream` or `?stream=true` it returns SSE and supports `Last-Event-ID`. SSE frames use the default message channel (no per-type `event:` field); the JSON payload's `type` field carries the event type, so consumers receive every event — including types they do not know about yet.

### Archiving Sessions

`DELETE /v1/sessions/{id}` archives a session: the daemon appends a durable `session.archived` event and then moves the whole session directory into the store's `Archive/` subdirectory (`sessions/Archive/<session-id>/`). Nothing is deleted — `session.json`, `events.jsonl` and all other files move along.

- Only inactive sessions can be archived: no starting/running provider, no open turn, no pending approval. The endpoint never force-stops a session; stop it first with `POST /v1/sessions/{id}/stop`.
- Status codes: `200` archived (repeating an archive is idempotent), `404` unknown session, `409 session_active` the session still has active work, `409 session_archive_conflict` the archive target is occupied, `500 session_archive_failed` a filesystem error (the session's data stays intact and a retry or a daemon restart completes the move).
- `GET /v1/sessions` hides archived sessions by default; use `?includeArchived=true` to include them or `?archived=true` to list only archived sessions. `GET /v1/sessions/{id}` and the events endpoint keep working for archived sessions.
- Archived sessions are read-only: `messages`, `resume`, `interrupt`, `stop` and approval writes return `409 session_archived`. Unarchiving is not supported.

The CLI equivalents are `agenthub session archive <id>`, `agenthub session list --all` and `agenthub session list --archived`; the Web UI offers an Archive action with an in-app confirmation and an "Archived Sessions" view.

## Data and Security

The config lives at `$HOME/.agenthub/config.json` by default; session data and runtime state follow the operating system's user data directory. For each session:

```text
sessions/<session-id>/
  session.json
  events.jsonl
sessions/Archive/<session-id>/   (archived sessions, same files)
```

`events.jsonl` is the single source of truth, and `session.json` is a rebuildable projection. Writes use append + fsync; snapshots use a temporary file + fsync + rename. Truncated trailing log lines are repaired at startup. The archive is a plain directory move inside the same store: if the daemon stops between the archived event and the move, startup completes the move, so the physical location always matches the event log.

The no-authentication mode is only suitable for the local machine and trusted networks: it listens on loopback only by default, sends no permissive CORS headers, rejects cross-origin browser write requests, and verifies that the request Host points to a local address.

## Verification

```bash
go test -race ./...
go vet ./...
cd frontend
npm run build
npm run test:sites
```

The implementation has also been integration-tested locally against real providers: Codex app-server, Kimi ACP, Pi/Kimi K3, Pi/Grok, Codex native thread recovery across restarts, and Kimi creating and writing files in the workspace.

## License

This project is released under the [BSD 3-Clause License](LICENSE) (New BSD License / Revised BSD License).
