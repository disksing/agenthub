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
- Same-origin Web UI: session list, real-time chat, status, approvals, stop, structured settings, and a floating activity/quota companion with optional Web Audio beeps.
- Provider model enumeration: each built-in provider reports its currently usable models through its official interface, normalized into one read-only API.
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

agenthub run --agent "Kimi K3" --cwd . "Investigate why the tests fail"
agenthub run --agent "Codex" --cwd . "Implement this feature and run the tests"

agenthub chat --agent "Codex GPT" --cwd .
agenthub session create --agent "Kimi K3" --title "bug hunt"
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
      "name": "Kimi K3",
      "providerId": "pi",
      "options": { "mode": "build", "model": "kimi-coding/k3" }
    }
  ],
  "onWatch": {
    "enabled": false,
    "serverUrl": "http://127.0.0.1:9211",
    "authMode": "trusted_proxy",
    "username": "admin",
    "password": "",
    "refreshIntervalSeconds": 60
  },
  "companion": {
    "showActivity": true,
    "enableBeeping": true,
    "beepVolume": 0.28,
    "completionSound": "chime"
  }
}
```

A provider wraps a local agent runtime or protocol; an agent references one provider and holds its concrete launch options. An agent has no separate id: its `name` is required (up to 80 characters), is unique case-insensitively after trimming surrounding whitespace, and is the only reference key — the config, the API, the CLI and session records all use it. Every session is created with an explicit agent name (`POST /v1/sessions` requires `agentName`, and the CLI requires `--agent`); name matching is case-insensitive and sessions record the canonical configured spelling. An unknown, missing, or disabled-provider agent fails with a clear error instead of being routed elsewhere.

Renaming an agent is safe: when a config save replaces a name with exactly one otherwise identical agent, the daemon appends a `session.agent` event to every active session that referenced the old name, so those sessions follow the rename. An ambiguous rename (several identical candidates) is rejected with an actionable error, and deleting or renaming so that no unique target exists leaves old sessions failing with a clear "unknown agent" error rather than guessing.

Codex agents accept the options `model`, `sandbox`, `approval`, and `reasoning_effort`. `reasoning_effort` controls the Codex reasoning ("thinking") effort: it is sent as the `model_reasoning_effort` config override on `thread/start` and `thread/resume`, and the daemon validates the value against the efforts the selected model advertises via `model/list` (for example `low`, `medium`, `high`, `xhigh`; some models add `max` and `ultra`). An unsupported value fails session creation with the list of valid values; an empty value inherits the Codex default.

### Per-session launch environment

`POST /v1/sessions` accepts an optional string map named `launchEnvironment`. It is merged over the daemon environment for that session's Provider process, so a session value wins when both define the same variable. Codex receives the merged process environment and each session entry as `shell_environment_policy.set.<KEY>` on both `thread/start` and `thread/resume`; ACP and Pi receive the merged process environment. The value is part of the durable `session.created` event, so it survives event replay, daemon restarts, and Provider resume without leaking into another concurrent session. Older sessions without the field continue to inherit the daemon environment unchanged.

The map is deliberately persisted in the Session's `events.jsonl` and rebuildable `session.json`, and is returned by Session API responses. Do not put credentials or any other value in `launchEnvironment` that you do not want stored on disk. Session files remain private (`0600`), but that is not a substitute for secret storage.

### Model Enumeration

Each built-in provider can report the models currently usable on this machine through its official interface — no provider session is created and nothing is written to provider configuration:

- Codex: the app-server `model/list` request (account-scoped, with display names, a default flag, and hidden-model filtering).
- Kimi: `kimi provider list --json` (the configured model registry, with display names).
- Pi: the RPC `get_available_models` command in `--no-session` mode (every configured upstream; the model Pi would use by default is flagged).
- OpenCode: `opencode models --verbose` (configured providers plus OpenCode Zen free models, with display names).

`GET /v1/providers/{id}/models` normalizes all four into `{ "provider": {...}, "models": [{ "id", "label", "default" }] }`, where `id` is exactly the value to put into an agent's `model` option. Results are deduplicated, kept in provider order, cached briefly (5 minutes for successes, 15 seconds for failures) with concurrent lookups deduplicated, and the cache is dropped on every configuration change (whole-config save or provider toggle). Failures are classified so clients can render them differently: `404 unknown_provider`, `409 provider_disabled`, `503 provider_unavailable` (CLI missing or not startable), `504 provider_timeout`, and `502 provider_error` (upstream or parse failure); an empty list is a successful `200` with `"models": []`. The endpoint is read-only: it never creates a provider session and never changes configuration.

In the Web settings, the agent **Model** field is a dropdown fed by this endpoint instead of a free-text input: pick the provider first, then choose a model. The empty "Provider default" choice simply omits the `model` option. A previously saved model that is not in the current list is kept as an explicit "saved, not currently listed" option until you pick a replacement, and loading, retry, empty, and disabled-provider states are shown inline.

The Web UI's **Settings** panel is the recommended way to manage this configuration. The **Providers** section is intentionally minimal: exactly four switches enable or disable the built-in providers (Codex, Kimi, Grok/Pi, OpenCode). There is no provider add/delete and no editing of commands, arguments, environment variables or other advanced fields. A toggle flips only the `enabled` flag through `PUT /v1/config/providers/{id}`, so the underlying configuration survives a disable/enable cycle; a built-in provider missing from an old config is created with canonical defaults when it is first enabled. The **Agents** section keeps structured, validated forms, and provider command availability probes distinguish *enabled* from *CLI available*. All changes go through the daemon API, which remains the only writer of the config file — no manual JSON editing is required.

The **General** and **Activity** sections configure the floating companion. Provider quota is fetched only by the daemon from OnWatch, normalized, cached for the configured interval, and exposed through `GET /v1/quota`; Basic Auth passwords are stored in the local `0600` config but redacted from every API response. Session activity comes only from AgentHub's own durably appended Events and is aggregated per Session into one-second frames at `GET /v1/activity/events`. The browser keeps one global EventSource, assigns a stable pitch to each active Session, and synthesizes optional beeps and completion sounds with Web Audio. AgentHub never scans Codex or another Provider's native Session files for this feature.

A disabled provider's agents are reported as unavailable (`available: false` with a reason in `GET /v1/agents`), are hidden from the new-session choices, and are rejected by the daemon on session creation and resume even when a client bypasses the Web UI. Disabling never interrupts an already running session, and existing session history stays readable.

### Removed Older Formats

Sessions now use the explicit Agent name as their only identity. Agent Profiles, tag routing, `defaultChatAgentId`, Agent `id` fields, and `POST /v1/sessions`'s `agentId` field are no longer accepted. The daemon does not rewrite old config files, create an id-mapping sidecar, or replay event payloads that use the removed identity fields. Convert or back up older config and session data before starting this version.

Command discovery order: the provider's `command`, `AGENTHUB_*_CLI`, then `PATH`. Supported:

- `AGENTHUB_CODEX_CLI`
- `AGENTHUB_OPENCODE_CLI`
- `AGENTHUB_KIMI_CLI`
- `AGENTHUB_PI_CLI`

`AGENTHUB_HOME=/path` isolates all config, data, and runtime state into a single directory; the config file then lives at `/path/config/config.json`, which is useful for testing. The layout is explicit and is read as-is.

## API

The daemon serves a complete Markdown API reference at **`GET /api.md`**
(`text/markdown; charset=utf-8`): every public endpoint with parameters,
request and response bodies, error codes, curl examples and the SSE event
contract. It is embedded in the binary, needs no frontend build, and is kept
in sync with the registered routes by automated tests. Fetch it with:

```bash
curl -s http://127.0.0.1:4646/api.md
```

`GET /v1/status` is the compatibility handshake. It returns
`"apiVersion": "1"` and only the capabilities this daemon instance can
actually exercise: `session.source`, `session.launch-environment`,
`session.launch-environment-update`,
`session.strict-stopped`, `events.lossless-replay`, `events.delta-merge`,
`activity.global-sse`,
`events.canonical-turn-terminals`, and `recovery.closed-turns`. A client
must reject an unsupported API version or a missing required capability
before creating a session; older daemons with neither field are explicitly
incompatible. Unknown additional capabilities, response fields and event
types must be ignored.

Every non-2xx public API response uses the same JSON error envelope with a
stable `code`, human-readable `message`, boolean `retryable`, optional
`details`, and `requestId`. Unknown routes and unsupported methods are JSON
errors too. The three provider-independent turn terminal events are
`turn.completed`, `turn.failed`, and `turn.cancelled`; provider-native
completion events are diagnostic and must not close a client turn.

Main endpoints:

```text
GET    /v1/health
GET    /v1/status
GET    /v1/config
PUT    /v1/config
POST   /v1/onwatch/test
GET    /v1/quota
GET    /v1/activity/events
GET    /v1/agents
GET    /v1/providers/{id}/models

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

The events endpoint returns paginated JSON with an exclusive `after` cursor, `nextAfter`, `hasMore`, and the latest durable cursor. With `Accept: text/event-stream` or `?stream=true` it returns SSE and uses the same exclusive cursor through `Last-Event-ID`. The daemon subscribes before capturing a high-water mark, replays the entire durable backlog in pages, and then switches to live delivery; overflow closes the stream so reconnecting from the last contiguous id can recover from `events.jsonl`, including after a daemon restart. SSE frames use the default message channel (no per-type `event:` field), so unknown event envelopes are preserved.

Clients may attach optional caller-defined correlation metadata when creating a session:

```json
{
  "agentName": "Codex",
  "cwd": "/path/to/project",
  "source": {
    "app": "forge",
    "instanceId": "mac-mini",
    "externalId": "project7.task26"
  }
}
```

The `source` object is persisted in `session.created`, rebuilt into `session.json` on replay, and returned by session GET/list responses. It is deliberately self-asserted metadata: AgentHub does not register applications, reserve names, authenticate values, enforce uniqueness, or isolate tenants. Any client may submit any values and duplicates are valid. `GET /v1/sessions` accepts exact, case-sensitive `sourceApp`, `sourceInstanceId`, and `sourceExternalId` filters in any combination; they also compose with `includeArchived`, `archived`, and `state`. Sessions created without source metadata remain compatible and do not match source filters. See the complete [HTTP API reference](http://127.0.0.1:4646/api.md) served by the daemon.

### Message provenance

`POST /v1/sessions/{id}/messages` accepts `role: "user"`, `"system"`, or
`"agent"`; an omitted role remains `user` for old clients. An optional
`sender` object carries descriptive `id`, `name`, and `sessionId` values. These
fields describe provenance only: they are self-asserted, unauthenticated, and
never change permissions, trust, or instruction priority. `assistant` is
reserved for output events produced by the current Provider and cannot be
submitted by an inbound client.

New inputs are persisted as one `message.input` event containing the original
text, role, sender, and `steer` flag. Historical `message.user` and
`message.user.steer` events replay as user messages without rewriting the
session log. System and agent inputs are delivered to Codex, Kimi, OpenCode,
and Pi as ordinary user-level text with a JSON provenance envelope; the
envelope is not stored in the event timeline or shown in the Web UI.

Examples:

```json
{"text":"Please inspect the failing test."}
{"text":"Resume the queued work.","role":"system","sender":{"name":"Forge Scheduler"}}
{"text":"The worker finished its scan.","role":"agent","sender":{"name":"Review Agent","sessionId":"ses_worker"}}
```

### Reusable Event Timeline

[`packages/event-timeline`](packages/event-timeline/README.md) is the
dependency-free reference projection for API v1 canonical events. AgentHub
Web imports its ESM artifact directly; non-bundled browser clients can vendor
the IIFE and call `AgentHubEventTimeline.buildTimeline(events)`. The package
contains sanitized conformance fixtures and exact snapshots for provider
noise, cross-turn deltas, reasoning, Codex/ACP/Pi tools, approvals, terminal
fallbacks, failures, cancellation, unknown events, and paginated replay.

Run `npm run build` and `npm test` in the package directory to reproduce and
verify both artifacts. `dist/manifest.json` records version `1.0.0`, contract
`agenthub.api.v1`, BSD-3-Clause licensing, the deterministic build command,
and SHA-256 hashes for source inputs and generated artifacts. Consumers
should pin the AgentHub Git commit containing the manifest.

### Archiving Sessions

`DELETE /v1/sessions/{id}` archives a session: the daemon appends a durable `session.archived` event and then moves the whole session directory into the store's `Archive/` subdirectory (`sessions/Archive/<session-id>/`). Nothing is deleted — `session.json`, `events.jsonl` and all other files move along.

- Only strictly stopped sessions can be archived, with no open turn or pending approval. The endpoint never force-stops a session; stop it first with `POST /v1/sessions/{id}/stop`.
- Status codes: `200` archived (repeating an archive is idempotent), `404` unknown session, `409 session_active` the session still has active work, `409 session_archive_conflict` the archive target is occupied, `500 session_archive_failed` a filesystem error (the session's data stays intact and a retry or a daemon restart completes the move).
- `GET /v1/sessions` hides archived sessions by default; use `?includeArchived=true` to include them or `?archived=true` to list only archived sessions. `GET /v1/sessions/{id}` and the events endpoint keep working for archived sessions.
- Archived sessions are read-only: `messages`, `resume`, `interrupt`, `stop` and approval writes return `409 session_archived`. Unarchiving is not supported.

The CLI equivalents are `agenthub session archive <id>`, `agenthub session list --all` and `agenthub session list --archived`; the Web UI offers an Archive action with an in-app confirmation and an "Archived Sessions" view.

### Provider Startup Failures

Session creation starts the provider synchronously: the handshake requests (`initialize`, `session/new` / `thread/start`, and their resume/load variants) must answer within a 2-minute startup timeout. A provider that cannot answer — for example a process stuck reading the session working directory because the operating system is holding a privacy permission prompt — fails the request instead of hanging it:

- The API returns `502 provider_start_failed` with the provider's real error and, on timeout, an actionable hint (on macOS this points at System Settings > Privacy & Security prompts, e.g. the Downloads folder or Full Disk Access). The Web New Session dialog shows this message.
- The session is kept for diagnostics with `provider.error`, any open turn is failed, and the session converges to `stopped` with `stopReason: "startup_error"` only after process exit is confirmed. It can then be inspected, resumed, archived, or left alone.

### Strict stopped lifecycle and crash recovery

`stopped` is the single trustworthy provider-release boundary. A stop request
first appends `stopping`; the stop call returns and the final
`session.state {"state":"stopped","reason":"requested"}` event is appended
only after the adapter Wait path and process-group probe confirm that the
provider and its descendants cannot write to the working directory.

All exit paths use the same terminal sequence. Clean provider exit uses
`completed`; a crash records `provider.error`, closes approvals and the open
turn, then uses `provider_error`; startup failure uses `startup_error`;
explicit stop and graceful daemon shutdown use `requested`. If the daemon is
killed, the next daemon uses durable `provider.process.started` evidence to
terminate any surviving process group, deterministically cancels pending
approvals and the open turn, and finishes with `daemon_recovery`.

Active provider turns have no fixed wall-clock deadline. ACP `session/prompt`
and Pi `prompt`/`steer` requests wait for the provider's real terminal result,
even when reasoning or tool work runs longer than 15 minutes. Users can still
end work explicitly with interrupt or stop, and provider exit or daemon
shutdown releases the pending request. Startup handshakes keep their 2-minute
bound, while ordinary control requests keep a separate bounded timeout.

## Data and Security

All persistent user data lives under a single root, `$HOME/.agenthub`:

```text
~/.agenthub/
├── config.json                 (providers and agents)
├── sessions/<session-id>/
│     session.json
│     events.jsonl
├── sessions/Archive/<session-id>/   (archived sessions, same files)
├── logs/                       (service stdout/stderr when installed as a service)
├── server.json                 (transient daemon endpoint discovery)
└── server.lock                 (transient single-daemon lock)
```

`events.jsonl` is the single source of truth, and `session.json` is a rebuildable projection. Writes use append + fsync; snapshots use a temporary file + fsync + rename. A partial final line caused by an interrupted current write is repaired at startup. The archive is a plain directory move inside the same store: if the daemon stops between the archived event and the move, startup completes the move, so the physical location always matches the event log. Directories are created `0700` and sensitive files `0600`.

`agenthub status` (and `GET /v1/status`) reports the effective config, session store, archive and logs paths, so you can confirm the layout after an upgrade.

### Data Layout

The daemon reads only the unified `~/.agenthub` layout. Older releases may have stored sessions under an operating-system data directory (for example `~/Library/Application Support/agenthub` on macOS) and logs under `~/Library/Logs/AgentHub`; those paths are no longer read or migrated automatically. Before upgrading, perform a one-time, verified copy or export into the current layout and keep a backup. The daemon never merges two roots or chooses a winner.

The no-authentication mode is only suitable for the local machine and trusted networks: it listens on loopback only by default, sends no permissive CORS headers, rejects cross-origin browser write requests, and verifies that the request Host points to a local address.

## Verification

```bash
go test -race ./...
go vet ./...
cd frontend
npm ci
npm run build
npm test
npm run test:sites
```

The Go suite includes a real-process
[Forge integration gate](docs/forge-integration-gate.md): it launches an
isolated daemon and fake ACP provider subprocesses, injects lifecycle and
streaming failures, and verifies cleanup, recovery, replay, capabilities,
and structured errors across the process boundary.

The implementation has also been integration-tested locally against real providers: Codex app-server, Kimi ACP, Pi/Kimi K3, Pi/Grok, Codex native thread recovery across restarts, and Kimi creating and writing files in the workspace.

## License

This project is released under the [BSD 3-Clause License](LICENSE) (New BSD License / Revised BSD License).
