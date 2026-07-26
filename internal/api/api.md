# AgentHub HTTP API Reference

AgentHub is a local agent launcher and session hub. A single Go daemon manages
the agents installed on the machine (Codex, Kimi, Pi/Grok, OpenCode) and
exposes configuration, session management, approvals and event streams to the
Web UI, the CLI and any other HTTP client through one HTTP JSON + SSE API.

This document is served by the daemon itself at `GET /api.md`
(`text/markdown; charset=utf-8`) and is verified against the registered routes
by automated tests, so it always matches the running implementation.

## Base URL and Security Boundary

The daemon listens on **loopback only** by default:

```text
http://127.0.0.1:4646
```

All examples below use `$BASE` for the base URL and `$SESSION` for a session
id (session ids look like `ses_<timestamp><random hex>`):

```bash
BASE=http://127.0.0.1:4646
SESSION=ses_1753502400000010203040506070809
```

Security model — read this before exposing the daemon anywhere:

- **No authentication.** There are no accounts, tokens or API keys. Any
  process that can reach the listen address has full control of the daemon:
  it can run agents, execute tools through them, and modify sessions and
  configuration.
- **Local-first.** The default loopback binding is only reachable from the
  same machine. `agenthub serve --listen` can explicitly bind a LAN interface
  address, a wildcard address or a hostname that resolves to a local
  interface. Binding a non-loopback address means **every device that can
  reach that address controls the daemon** — only do this on a trusted
  network and never expose the port to the public internet.
- **Host header validation.** Requests whose `Host` header does not name a
  local address of the daemon are rejected with `403 host_rejected`. This
  blocks DNS-rebinding attacks from browsers on the local network.
- **Cross-origin write protection.** Mutating requests (`POST`, `PUT`,
  `PATCH`, `DELETE`) that carry an `Origin` header are rejected with
  `403 origin_rejected` unless the origin matches the daemon's own origin.
  The daemon sends no permissive CORS headers, so browsers on other origins
  cannot issue writes. Non-browser clients (curl, the CLI) simply omit the
  `Origin` header.
- **JSON writes only.** Mutating requests must send
  `Content-Type: application/json` (`415 json_required` otherwise). Bodies
  are limited to 1 MiB and unknown JSON fields are rejected.

## Conventions

### Error responses

Every non-2xx response uses a single error envelope:

```json
{
  "error": {
    "code": "session_not_found",
    "message": "session not found",
    "details": null,
    "requestId": "req_1753502400001a2b3c4d5e6f7a8b9c"
  }
}
```

`code` is a stable machine-readable identifier; `message` is human-readable
and may change; `details` carries optional structured context (for example
the session id after a provider start failure); `requestId` correlates the
response with daemon logs.

### Sessions

A session object looks like:

```json
{
  "id": "ses_1753502400000010203040506070809",
  "title": "Fix the flaky test",
  "cwd": "/path/to/project",
  "agentName": "Codex",
  "launchEnvironment": {"FORGE_SESSION_ID": "session-123"},
  "source": {
    "app": "forge",
    "instanceId": "mac-mini",
    "externalId": "project7.task26"
  },
  "provider": "codex",
  "providerSessionId": "thread_abc123",
  "state": "ready",
  "currentTurnId": "turn_1753502400002b2b3c4d5e6f7a8b9c0d",
  "pendingApprovalIds": ["approval-1"],
  "lastEventId": 42,
  "createdAt": "2026-07-26T12:00:00Z",
  "updatedAt": "2026-07-26T12:05:00Z"
}
```

`state` is one of `starting`, `ready`, `busy`, `waiting_approval`,
`stopped`, `failed` or `archived`. Optional fields (`agentName`, `source`,
`launchEnvironment`, `provider`, `providerSessionId`, `currentTurnId`,
`pendingApprovalIds`) are omitted when absent. `source` is unverified,
caller-supplied correlation metadata; AgentHub does not register source
applications, reserve names, authenticate the values, or require them to be
unique. `launchEnvironment` is durable session data and may be visible to any
client that can read the session or its events. `lastEventId` is the id of the
newest event in the session log and doubles as the resume cursor for the
events endpoint.

### Agents and providers

Agents are referenced **by name only**. An agent has a `name` (required,
unique case-insensitively after trimming, at most 80 characters), a
`providerId` naming one configured provider, and provider-specific `options`.
There are no agent ids, agent profiles or tag-based routing; creating a
session always requires an explicit agent name. The built-in provider ids
are `codex`, `kimi`, `pi` and `opencode`; each can be enabled or disabled,
and a disabled provider makes its agents unavailable for new work without
disturbing sessions that are already running.

### Events

Every state change is appended to the session's durable event log. An event
looks like:

```json
{
  "id": 42,
  "time": "2026-07-26T12:05:00Z",
  "type": "message.assistant.delta",
  "sessionId": "ses_1753502400000010203040506070809",
  "turnId": "turn_1753502400002b2b3c4d5e6f7a8b9c0d",
  "data": {"text": "..."}
}
```

`id` is a durable, per-session, monotonically increasing integer cursor. A
committed id is never reused, including across daemon restarts. `turnId` is
present on events that belong to a turn. Core event types:

| Type | `data` payload | Meaning |
| --- | --- | --- |
| `session.created` | session object | The initial session snapshot, including caller-supplied `source` when present. |
| `session.state` | `{"state": "..."}` | Session state transition (see states above). |
| `session.provider` | `{"agentName", "provider", "providerSessionId"}` | The provider-native session/thread id was established; used for resume. |
| `session.agent` | `{"agentName"}` | The configured agent was renamed; the session now references the new name. |
| `session.archived` | — | The session was archived (moved to the archive store). |
| `message.user` | `{"text"}` | A user message started a new turn. |
| `message.user.steer` | `{"text"}` | A steer message was injected into the active turn. |
| `turn.started` | `{"text"}` | A turn began. |
| `turn.completed` | provider-specific | The active turn finished successfully. |
| `turn.failed` | `{"error"}` or provider-specific | The active turn failed. |
| `turn.cancelled` | `{"reason"}` | The active turn was interrupted. |
| `approval.requested` | `{"approvalId", "method", "params"}` | The provider asks for approval; resolve it through the approvals endpoint. |
| `approval.resolved` | `{"approvalId", "decision"}` | An approval was answered. |
| `message.assistant.delta` | `{"text"}` | Assistant output chunk. |
| `message.reasoning.delta` | `{"text"}` | Reasoning/thinking chunk. |
| `tool.event` | provider-specific | Tool call lifecycle (start, update, end). |
| `provider.event` | `{"method", "raw"}` | Raw provider notification kept for transparency. |
| `provider.error` | `{"message"}` | Provider process or protocol error. |
| `provider.stderr` | `{"text"}` | Provider stderr output. |
| `provider.warning` | `{"message", ...}` | Non-fatal provider problem. |

Providers may emit additional types over time. Consumers must ignore event
types they do not recognize.

## Endpoints

### GET /v1/status

Daemon status, effective data paths and runtime summary.

- **Query parameters:** none.
- **Success `200`:**

```json
{
  "version": "0.1.0",
  "startedAt": "2026-07-26T11:00:00Z",
  "uptimeSeconds": 3600,
  "paths": {
    "config": "/home/user/.agenthub/config.json",
    "sessions": "/home/user/.agenthub/sessions",
    "archive": "/home/user/.agenthub/sessions/Archive",
    "logs": "/home/user/.agenthub/logs"
  },
  "sessionStore": {
    "path": "/home/user/.agenthub/sessions",
    "archivePath": "/home/user/.agenthub/sessions/Archive",
    "sessionCount": 3
  },
  "runtime": {"available": true, "summary": "2 running sessions"}
}
```

  `runtime` is `{"available": false}` when the daemon runs without a
  session runtime.

```bash
curl -s "$BASE/v1/status"
```

### GET /v1/config

Read the effective configuration.

- **Success `200`:** `{"config": {...}}` — the full configuration object
  (`version`, `agentProviders`, `agents`; see `PUT /v1/config` for the
  schema and constraints).
- **Errors:** `503 runtime_unavailable`.

```bash
curl -s "$BASE/v1/config"
```

### PUT /v1/config

Replace the whole configuration. The daemon validates the new
configuration, writes it to the config file atomically (the daemon is the
only writer) and applies it in memory.

- **Request body:**

```json
{
  "config": {
    "version": 1,
    "agentProviders": [
      {"id": "codex", "name": "Codex app-server", "type": "codex", "enabled": true, "command": "codex"}
    ],
    "agents": [
      {"name": "Codex", "providerId": "codex", "options": {"approval": "never", "sandbox": "danger-full-access"}}
    ]
  }
}
```

- **Constraints:** provider `id` unique and `type` one of `codex`, `kimi`,
  `pi`, `opencode`; agent `name` required, at most 80 characters, unique
  case-insensitively after trimming; every agent's `providerId` must
  reference a configured provider. Unknown fields are rejected.
- **Rename semantics:** if an agent disappears while exactly one new agent
  with an identical provider and options appears, the change is treated as a
  rename and active sessions referencing the old name are migrated with a
  `session.agent` event. Ambiguous renames are rejected.
- **Success `200`:** `{"config": {...}}` (the applied configuration).
- **Errors:** `400 invalid_request` (malformed JSON or unknown fields,
  including removed profile/tag fields), `415 json_required`,
  `422 invalid_config` (validation failed), `422 ambiguous_rename`,
  `500 agent_rename_failed`, `503 runtime_unavailable`.

```bash
curl -s -X PUT "$BASE/v1/config" \
  -H "Content-Type: application/json" \
  -d @config.json
```

### PUT /v1/config/providers/{id}

Enable or disable one built-in provider without touching the rest of the
configuration. This is the contract behind the four switches of the Web
settings UI. The provider's other fields survive a disable/enable cycle; a
built-in provider missing from an old configuration is created with its
canonical defaults.

- **Path parameters:** `id` — one of `codex`, `kimi`, `pi`, `opencode`.
- **Request body:** `{"enabled": true}` — `enabled` is required.
- **Success `200`:** `{"provider": {"id", "name", "type", "enabled", "command?"}}`.
- **Errors:** `400 invalid_request` (missing `enabled`), `415 json_required`,
  `404 unknown_provider`, `422 invalid_config`, `503 runtime_unavailable`.
- **Effect:** agents of a disabled provider report `available: false` from
  `GET /v1/agents` and are rejected for new sessions; running sessions keep
  running.

```bash
curl -s -X PUT "$BASE/v1/config/providers/kimi" \
  -H "Content-Type: application/json" \
  -d '{"enabled": false}'
```

### GET /v1/providers/{id}/models

Enumerate the models currently usable by one built-in provider through the
provider's official interface. Read-only: it never creates a provider
session and never changes configuration. Enumeration may start a short-lived
provider CLI process and can take several seconds; the whole request is
bounded by a 45-second timeout. Results are cached until the configuration
changes.

- **Path parameters:** `id` — one of `codex`, `kimi`, `pi`, `opencode`.
- **Success `200`:** `{"provider": {"id", "name", "type"}, "models": [...]}`;
  each model is `{"id", "label", "default?"}`, where `id` is the value the
  agent `model` option accepts. An empty `models` array is a valid result.
- **Errors:** `404 unknown_provider`, `409 provider_disabled`,
  `503 provider_unavailable` (provider CLI missing or failed to start),
  `504 provider_timeout`, `502 provider_error` (provider ran but reported an
  error or returned unreadable data), `503 runtime_unavailable`.

```bash
curl -s "$BASE/v1/providers/codex/models"
```

### GET /v1/agents

List configured providers and agents with their effective availability, plus
CLI availability probes for enabled providers.

- **Success `200`:**

```json
{
  "providers": [{"id": "codex", "name": "Codex app-server", "type": "codex", "enabled": true}],
  "agents": [
    {
      "name": "Codex",
      "providerId": "codex",
      "options": {"approval": "never"},
      "available": true
    }
  ],
  "probes": [{"providerId": "codex", "type": "codex", "command": "/usr/local/bin/codex", "available": true}]
}
```

  An agent whose provider is missing or disabled reports `available: false`
  with an `unavailableReason`.
- **Errors:** `503 runtime_unavailable`.

```bash
curl -s "$BASE/v1/agents"
```

### GET /v1/sessions

List sessions, most recently updated first. Archived sessions are hidden by
default.

- **Query parameters:**
  - `includeArchived=true` — include archived sessions in the list.
  - `archived=true` — list only archived sessions.
  - `state=<state>[,<state>...]` — keep only sessions in one of the given
    states (see [Sessions](#sessions)).
  - `sourceApp=<value>` — exact, case-sensitive `source.app` match.
  - `sourceInstanceId=<value>` — exact, case-sensitive
    `source.instanceId` match.
  - `sourceExternalId=<value>` — exact, case-sensitive
    `source.externalId` match.
    Source filters can be combined with each other and with archive and state
    filters. Sessions created without `source` never match a source filter.
- **Success `200`:** `{"sessions": [...]}` — an array of session objects.

```bash
curl -s "$BASE/v1/sessions"
curl -s "$BASE/v1/sessions?archived=true"
curl -s "$BASE/v1/sessions?state=busy,waiting_approval"
curl -s "$BASE/v1/sessions?sourceApp=forge&sourceInstanceId=mac-mini&state=ready"
```

### POST /v1/sessions

Create a session with an explicit agent and start its provider
synchronously. Optionally send an initial message, which starts the first
turn before the response returns.

- **Request body:**

```json
{
  "title": "Fix the flaky test",
  "cwd": "/path/to/project",
  "agentName": "Codex",
  "launchEnvironment": {"FORGE_SESSION_ID": "session-123"},
  "source": {
    "app": "forge",
    "instanceId": "mac-mini",
    "externalId": "project7.task26"
  },
  "initialMessage": {"text": "Reproduce the failure first."}
}
```

- **Fields:**
  - `agentName` (required) — the unique name of a configured agent; matched
    case-insensitively, and the session records the canonical configured
    spelling. The agent's provider must be enabled.
  - `cwd` (required) — working directory for the agent; must exist and be a
    directory (symlinks are resolved).
  - `title` (optional) — display title.
  - `source` (optional) — caller-supplied correlation metadata containing
    optional string fields `app`, `instanceId`, and `externalId`. The values
    are stored verbatim in `session.created`, survive event replay, and are
    returned by session GET/list responses. They are not authenticated or
    unique: any client may submit any values, and duplicate values are
    allowed.
  - `launchEnvironment` (optional) — string-to-string environment overrides
    for this session's provider process. Session values override daemon
    variables with the same name. Codex also receives every entry as a
    `shell_environment_policy.set.<KEY>` config override on both
    `thread/start` and `thread/resume`; ACP and Pi receive the merged process
    environment. The map is stored in the durable `session.created` event and
    remains in effect after event replay, daemon restart and provider resume.
    **It is persisted in `events.jsonl` and `session.json` and returned by the
    Session API, so never put a secret here unless you intend it to be stored.**
  - `initialMessage.text` (optional) — first user message; when non-empty
    the first turn starts immediately.
  - `agentId` (deprecated, do not use) — agent ids were removed in favor of
    name-only agents. For one compatibility window an id recorded by a
    migrated legacy configuration still resolves through the stored
    id → name mapping; any other id is rejected. Send `agentName` instead,
    and never both.
- **Success `201`:** `{"session": {...}}` with a
  `Location: /v1/sessions/{id}` header.
- **Errors:** `400 invalid_request` (malformed body, or both `agentName`
  and `agentId`), `415 json_required`, `422 agent_required`,
  `422 invalid_agent` (unknown agent or disabled provider),
  `422 agent_id_removed` (unresolvable legacy id), `422 invalid_cwd`,
  `422 invalid_launch_environment` (an environment name is empty or contains
  `=`/NUL, or a value contains NUL),
  `500 session_create_failed`,
  `502 provider_start_failed` (the provider handshake failed; the response
  `details.sessionId` names the session kept for diagnostics in the `failed`
  state), `502 turn_start_failed` (the provider started but the initial
  message could not be sent; `details.sessionId` likewise).

```bash
curl -s -X POST "$BASE/v1/sessions" \
  -H "Content-Type: application/json" \
  -d '{
    "title": "Fix the flaky test",
    "cwd": "/path/to/project",
    "agentName": "Codex",
    "launchEnvironment": {"FORGE_SESSION_ID": "session-123"},
    "source": {
      "app": "forge",
      "instanceId": "mac-mini",
      "externalId": "project7.task26"
    },
    "initialMessage": {"text": "Reproduce the failure first."}
  }'
```

### GET /v1/sessions/{id}

Read one session. Works for active and archived sessions.

- **Path parameters:** `id` — session id.
- **Success `200`:** `{"session": {...}}`.
- **Errors:** `404 session_not_found`, `500 session_store_failed`.

```bash
curl -s "$BASE/v1/sessions/$SESSION"
```

### DELETE /v1/sessions/{id}

Archive a session. Archiving appends a durable `session.archived` event and
then atomically moves the whole session directory into the store's
`Archive/` subdirectory. Nothing is deleted; `GET /v1/sessions/{id}` and the
events endpoint keep working for archived sessions. Archived sessions are
read-only: all write operations below return `409 session_archived`, and
unarchiving is not supported.

- **Preconditions:** the session must have no active work — no starting or
  running provider, no open turn, no pending approval. Stop it first with
  `POST /v1/sessions/{id}/stop`; this endpoint never force-stops a provider.
- **Success `200`:** `{"session": {...}}` (state `archived`). Repeating an
  archive is idempotent.
- **Errors:** `404 session_not_found`, `400 invalid_session_id`,
  `409 session_active` (active work remains),
  `409 session_archive_conflict` (archive target occupied),
  `500 session_archive_failed` (filesystem error; the session's data stays
  intact and a retry or daemon restart completes the move).

```bash
curl -s -X DELETE "$BASE/v1/sessions/$SESSION"
```

### GET /v1/sessions/{id}/events

Read the session event log as JSON, or stream it live over Server-Sent
Events. Works for active and archived sessions.

#### JSON mode (default)

Plain requests return a JSON snapshot of the log after a cursor.

- **Query parameters:**
  - `after=<event-id>` — only events with an id greater than this cursor
    (default `0`, i.e. from the beginning).
  - `limit=<n>` — maximum number of events returned (default `500`,
    values above `1000` are clamped to the page size).
- **Success `200`:** events are in ascending id order. `after` and
  `nextAfter` are exclusive cursors; `latestCursor` is the durable head
  captured for this response.

  ```json
  {
    "events": [],
    "page": {
      "after": 100,
      "limit": 200,
      "nextAfter": 100,
      "hasMore": false
    },
    "latestCursor": 100
  }
  ```

  Page forward with `page.nextAfter` while `page.hasMore` is true. Clients
  that need a stable catch-up target should retain the first response's
  `latestCursor`; events appended later can be consumed by a subsequent
  request or SSE.
- **Errors:** `400 invalid_event_cursor`, `404 session_not_found`,
  `409 event_cursor_ahead` (the supplied cursor is newer than this session's
  durable head; the error details include `latestCursor`).

```bash
curl -s "$BASE/v1/sessions/$SESSION/events"
curl -s "$BASE/v1/sessions/$SESSION/events?after=100&limit=200"
```

#### SSE mode

Send `Accept: text/event-stream` (or append `?stream=true`) to keep the
connection open and receive events as they happen.

- **Headers:** `Accept: text/event-stream`; optional `Last-Event-ID` with
  the id of the last event already processed (the standard SSE resume
  mechanism; `?after=` is an equivalent query form).
- **Connection lifecycle:**
  1. The daemon installs the live subscription and captures a durable
     high-water mark.
  2. It pages through **all** stored events after the exclusive cursor up to
     that high-water mark, with no 1000-event backlog limit.
  3. It then consumes the live subscription, discarding only duplicate
     notifications at or below the high-water mark.
  4. Every 15 seconds without traffic the daemon sends a `: heartbeat`
     comment line to keep proxies and clients alive.
  5. The stream ends when the client disconnects or when the daemon shuts
     down (daemon shutdown closes streams promptly so restarts are fast).
     A subscriber queue overflow also ends the stream immediately; the
     daemon never continues sending a queue known to contain a gap.
- **Recovery:** reconnect with `Last-Event-ID` set to the id of the last
  contiguous event processed. Events are replayed from `events.jsonl`, not
  the in-memory subscriber queue, so overflow and daemon restart are
  recoverable. A client that observes an adjacent id other than
  `last_processed_id + 1` must stop projection and catch up through REST
  before resuming SSE.
- **Frame format:** every event uses the default SSE message channel (no
  per-type `event:` field), so consumers receive every event — including
  types they do not know about yet — instead of silently dropping events
  their subscription list does not name. The payload's `type` field carries
  the event type.

```text
id: 43
data: {"id":43,"time":"2026-07-26T12:05:01Z","type":"message.assistant.delta","sessionId":"ses_...","turnId":"turn_...","data":{"text":"Hello"}}

: heartbeat

id: 44
data: {"id":44,"time":"2026-07-26T12:05:02Z","type":"turn.completed","sessionId":"ses_...","turnId":"turn_...","data":{...}}
```

- **Errors:** `400 invalid_event_cursor`, `404 session_not_found`,
  `409 event_cursor_ahead`, `500 stream_unsupported` (all before the stream
  starts).

```bash
curl -N -H "Accept: text/event-stream" "$BASE/v1/sessions/$SESSION/events"
curl -N -H "Accept: text/event-stream" -H "Last-Event-ID: 100" \
  "$BASE/v1/sessions/$SESSION/events"
```

### POST /v1/sessions/{id}/messages

Send a user message. Without an active turn this starts a new turn; with an
active turn the message is rejected unless `steer` is set.

- **Request body:** `{"text": "...", "steer": false}`
  - `text` (required) — the message; blank text is rejected.
  - `steer` (optional) — inject the message into the currently active turn
    instead of starting a new one. Providers that cannot steer an active
    prompt (the ACP providers: Kimi and OpenCode) reject steer requests.
- **Success `202`:** `{"session": {...}}` — the turn runs asynchronously;
  watch it through the events endpoint.
- **Errors:** `400 invalid_request`, `415 json_required`,
  `404 session_not_found`, `409 session_archived`,
  `409 runtime_operation_failed` (blank text, an active turn without
  `steer=true`, or the provider rejecting the prompt),
  `503 runtime_unavailable`.

```bash
curl -s -X POST "$BASE/v1/sessions/$SESSION/messages" \
  -H "Content-Type: application/json" \
  -d '{"text": "Now fix the root cause."}'

curl -s -X POST "$BASE/v1/sessions/$SESSION/messages" \
  -H "Content-Type: application/json" \
  -d '{"text": "Skip the refactor and patch the test only.", "steer": true}'
```

### POST /v1/sessions/{id}/resume

Start (or restart) the session's provider without sending a message. When
the session recorded a provider-native session/thread id, the provider
resumes that native conversation, so context survives daemon and provider
restarts. Safe to call when the provider is already running.

- **Request body:** empty (`{}`).
- **Success `200`:** `{"session": {...}}`.
- **Errors:** `415 json_required`, `404 session_not_found`,
  `409 session_archived`, `409 runtime_operation_failed` (the provider could
  not start, for example because the recorded agent no longer exists),
  `503 runtime_unavailable`.

```bash
curl -s -X POST "$BASE/v1/sessions/$SESSION/resume" \
  -H "Content-Type: application/json" -d '{}'
```

### POST /v1/sessions/{id}/interrupt

Cancel the session's active turn without stopping the provider. Appends a
`turn.cancelled` event; the provider process stays alive for the next
message.

- **Request body:** empty (`{}`).
- **Success `200`:** `{"session": {...}}`.
- **Errors:** `415 json_required`, `404 session_not_found`,
  `409 session_archived`, `409 runtime_operation_failed` (the provider is
  not running), `503 runtime_unavailable`.

```bash
curl -s -X POST "$BASE/v1/sessions/$SESSION/interrupt" \
  -H "Content-Type: application/json" -d '{}'
```

### POST /v1/sessions/{id}/stop

Stop the session's provider process and mark the session `stopped`. The
session keeps its full history and can be resumed later. Stop is the
required precondition for archiving a session whose provider is running.

- **Request body:** empty (`{}`).
- **Success `200`:** `{"session": {...}}`.
- **Errors:** `415 json_required`, `404 session_not_found`,
  `409 session_archived`, `409 runtime_operation_failed`,
  `503 runtime_unavailable`.

```bash
curl -s -X POST "$BASE/v1/sessions/$SESSION/stop" \
  -H "Content-Type: application/json" -d '{}'
```

### POST /v1/sessions/{id}/approvals/{approvalId}

Resolve a pending approval. When a provider needs confirmation (for example
to run a command outside its sandbox) it emits an `approval.requested`
event and the session enters `waiting_approval` with the id in
`pendingApprovalIds`.

- **Path parameters:** `id` — session id; `approvalId` — the id from the
  `approval.requested` event.
- **Request body:** `{"decision": "accept"}` — one of:
  - `accept` — approve this request once.
  - `acceptForSession` — approve and allow matching requests for the rest of
    the session where the provider supports it.
  - `decline` — reject the request.
  - `cancel` — cancel the operation that asked for approval.
- **Success `200`:** `{"session": {...}}`; an `approval.resolved` event is
  appended.
- **Errors:** `400 invalid_request`, `415 json_required`,
  `404 session_not_found`, `409 session_archived`,
  `409 runtime_operation_failed` (unknown approval id, or the provider is
  not running — pending approvals do not survive a daemon restart),
  `503 runtime_unavailable`.

```bash
curl -s -X POST "$BASE/v1/sessions/$SESSION/approvals/approval-1" \
  -H "Content-Type: application/json" \
  -d '{"decision": "accept"}'
```

## Notes for Client Authors

- The Web UI served by the daemon, the `agenthub` CLI and this document all
  use the same API; anything the UI can do is available through the
  endpoints above.
- `GET /v1/health` exists for process-level health checks only and is not
  part of the public API surface documented here.
- The daemon writes all state; session files under the data root
  (`events.jsonl`, `session.json`) are its private storage — read them for
  diagnostics if needed, but never write them.
