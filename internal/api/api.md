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
    "retryable": false,
    "details": null,
    "requestId": "req_1753502400001a2b3c4d5e6f7a8b9c"
  }
}
```

`code` is a stable machine-readable identifier; `message` is human-readable
and may change; `retryable` says whether retrying the same operation later
can be useful without first changing the request; `details` carries optional
structured context (for example the session id after a provider start
failure); `requestId` correlates the response with daemon logs. Clients must
branch on `code`, not on `message`. A false `retryable` does not mean the
condition is permanent: the caller may need to change state or construct a
new request first. In particular, create and message failures are never
marked retryable because the daemon may already have persisted a session or
turn.

Unknown API paths and unsupported methods use this envelope too
(`route_not_found` and `method_not_allowed`); no public API error is plain
text or an empty body.

### Compatibility and capability negotiation

Call `GET /v1/status` before creating a session. `apiVersion` is the major
public contract version; a client must reject a value it does not support.
`capabilities` is a set of independently testable behaviors. Require every
behavior your workflow needs and reject a daemon that omits one. Older
daemons that return neither field are therefore explicitly incompatible,
not silently assumed to support the current contract. Ignore unknown
capabilities, response fields and event types so a compatible daemon can add
information without breaking the client.

Current runtime-backed daemon instances advertise:

| Capability | Guaranteed behavior |
| --- | --- |
| `session.source` | Durable caller source metadata and exact list filters. |
| `session.launch-environment` | Durable per-session provider environment, including provider resume. |
| `session.launch-environment-update` | Resume accepts a `launchEnvironment` overlay, persisted before provider start. |
| `session.strict-stopped` | `stopped` is published only after provider exit is confirmed. |
| `events.lossless-replay` | Durable exclusive cursors, paginated REST catch-up and gap-free SSE replay. |
| `events.delta-merge` | Consecutive same-message text deltas are folded into one durable event; SSE delivers live folds as append patches (`data.append: true`) under the folded event's id and re-sends the full cursor event on reconnect. |
| `events.backward-pagination` | JSON event pages can also read the log backwards with `latest=true` or an exclusive `before` cursor. |
| `activity.global-sse` | Best-effort one-second activity frames across all Sessions, with no historical beep replay. |
| `events.canonical-turn-terminals` | Provider-independent `turn.completed`, `turn.failed` and `turn.cancelled`. |
| `recovery.closed-turns` | Daemon recovery closes open approvals and turns before publishing `stopped`. |

Store-only test/diagnostic server instances omit runtime-backed capabilities;
the daemon never advertises them merely because their names are compiled
into the binary.

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
`stopping`, `stopped` or `archived`. A stopped session may include `stopReason`:
`requested`, `completed`, `provider_error`, `startup_error` or
`daemon_recovery`. Optional fields (`agentName`, `source`,
`launchEnvironment`, `provider`, `stopReason`, `providerSessionId`,
`currentTurnId`, `pendingApprovalIds`) are omitted when absent. `source` is
unverified, caller-supplied correlation metadata; AgentHub does not register
source applications, reserve names, authenticate the values, or require them
to be unique. `launchEnvironment` is durable session data and may be visible
to any client that can read the session or its events. `lastEventId` is the id
of the newest event in the session log and doubles as the resume cursor for
the events endpoint.

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
present on events that belong to a turn.

Consecutive `message.assistant.delta` or `message.reasoning.delta` fragments
of one provider message (same type, turn, and provider method) are folded
into a single durable event instead of one event per fragment: the event
keeps its original id, its `text` accumulates, its `time` tracks the newest
fragment, and its optional `startTime` records the first fragment time.
`startTime` is present once a delta has been folded and is omitted from
events that contain only one fragment. Folding stops at a 32 KiB accumulated
payload, so one long message may span several events. REST pages and stream
replays always serve the full accumulated event; live streams deliver only
the new fragment as an append patch under the folded event's id. See SSE
mode below. This holds for backward pages too, so a client paging up that
already saw a newer copy of an id (for example a live append patch) treats
the repeated id as a full replacement, never as an append. Core event types:

| Type | `data` payload | Meaning |
| --- | --- | --- |
| `session.created` | session object | The initial session snapshot, including caller-supplied `source` when present. |
| `session.state` | `{"state": "...", "reason": "..."}` | Session state transition. `reason` is present on new `stopped` events; consumers must also accept old events without it. |
| `session.provider` | `{"agentName", "provider", "providerSessionId"}` | The provider-native session/thread id was established; used for resume. |
| `provider.process.started` | `{"pid", "processGroupId"}` | Internal process-group evidence used to clean up after daemon crashes. |
| `session.agent` | `{"agentName"}` | The configured agent was renamed; the session now references the new name. |
| `session.launch-environment` | `{"environment": {...}}` | The session's launch environment was overlaid at resume; the payload is the full merged map, replacing the projected environment. |
| `session.archived` | — | The session was archived (moved to the archive store). |
| `message.input` | `{"text", "role", "sender?", "steer", "messageId?", "replyTo?", "correlationId?"}` | The canonical inbound message and its provenance metadata. `role` is `user`, `system`, or `agent`; `sender` is caller-provided identity metadata, not an authorization boundary. |
| `message.user` | `{"text"}` | Legacy user input event. Readers must project it as `message.input` with `role: "user"`; new writes never use this type. |
| `message.user.steer` | `{"text"}` | Legacy steer input event. Readers must project it as `message.input` with `role: "user", "steer": true`; new writes never use this type. |
| `turn.started` | `{}` | A turn began. This is lifecycle-only; message text and provenance come from `message.input`. |
| `turn.completed` | `{}` | The active turn finished successfully. |
| `turn.failed` | `{"error"}` | The active turn failed. |
| `turn.cancelled` | `{"reason"}` | The active turn was interrupted. |
| `approval.requested` | `{"approvalId", "method", "params"}` | The provider asks for approval; resolve it through the approvals endpoint. ACP elicitation requests carry the question in `params.toolCall` and the selectable answers in `params.options`. |
| `approval.resolved` | `{"approvalId", "decision", "optionId?", "text?"}` | An approval was answered. `optionId` records an explicit option selection; decision `text` records a custom free-text reply. |
| `message.assistant.delta` | `{"text"}` | Assistant output chunk. |
| `message.reasoning.delta` | `{"text"}` | Reasoning/thinking chunk. |
| `tool.event` | provider-specific | Tool call lifecycle (start, update, end). |
| `provider.event` | `{"method", "raw"}` | Raw provider notification kept for transparency. |
| `provider.error` | `{"message", "details"?, "willRetry"?}` | Provider process or protocol error. A Codex event with `willRetry: true` is recoverable and does not close the active turn; clients must wait for a canonical `turn.*` terminal event. |
| `provider.stderr` | `{"text"}` | Provider stderr output. |
| `provider.warning` | `{"message", ...}` | Non-fatal provider problem. |

Providers may emit additional types over time. Consumers must ignore event
types they do not recognize. The three `turn.*` terminal events above are
the only canonical turn terminal signals. A preceding provider-native event
such as `provider.turn.completed` is diagnostic only: clients must not use it
to close a turn, and its private payload is never copied into the canonical
terminal event.

## Endpoints

### GET /v1/status

Daemon status, effective data paths and runtime summary.

- **Query parameters:** none.
- **Success `200`:**

```json
{
  "apiVersion": "1",
  "capabilities": [
    "events.lossless-replay",
    "events.backward-pagination",
    "session.source",
    "events.canonical-turn-terminals",
    "recovery.closed-turns",
    "session.launch-environment",
    "session.launch-environment-update",
    "session.strict-stopped"
  ],
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
  session runtime. Such an instance also omits the runtime-backed
  capabilities described under
  [Compatibility and capability negotiation](#compatibility-and-capability-negotiation).

```bash
curl -s "$BASE/v1/status"
```

### GET /v1/config

Read the effective configuration.

- **Success `200`:** `{"config": {...}}` — the configuration object
  (`version`, `agentProviders`, `agents`, `onWatch`, `companion`; see
  `PUT /v1/config` for the schema and constraints). `onWatch.password` is
  always returned as an empty string; the stored secret never leaves the
  daemon.
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
}
```

- **Constraints:** provider `id` unique and `type` one of `codex`, `kimi`,
  `pi`, `opencode`; agent `name` required, at most 80 characters, unique
  case-insensitively after trimming; every agent's `providerId` must
  reference a configured provider. Unknown fields are rejected.
- **OnWatch constraints:** `serverUrl` is an absolute HTTP(S) URL without
  embedded credentials; `authMode` is `trusted_proxy`, `basic` or `none`;
  refresh is 30, 60 or 300 seconds. Basic Auth requires a username and, when
  enabled, a password. Because GET responses redact the password, an empty
  Basic password in PUT preserves the currently stored value.
- **Companion constraints:** `beepVolume` is between 0 and 1 and
  `completionSound` is `chime`, `bell`, `ding`, `marimba` or `pop`.
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

### POST /v1/onwatch/test

Test an OnWatch connection without saving the supplied settings. The request
body is `{"onWatch": {...}}` using the same object as `PUT /v1/config`.
An empty Basic Auth password reuses the currently stored secret. The daemon
requests OnWatch `/api/providers`; it never returns credentials.

- **Success `200`:** `{"connected": true, "providers": ["codex", ...]}`.
- **Errors:** `400 invalid_request`, `415 json_required`,
  `422 invalid_config`, `502 onwatch_unavailable`, `503 runtime_unavailable`.

```bash
curl -s -X POST "$BASE/v1/onwatch/test" \
  -H "Content-Type: application/json" \
  -d '{"onWatch":{"enabled":true,"serverUrl":"http://127.0.0.1:9211","authMode":"trusted_proxy","username":"admin","refreshIntervalSeconds":60}}'
```

### GET /v1/quota

Read the companion's normalized Provider quota snapshot. The browser never
contacts OnWatch directly. The daemon discovers Provider order from
`/api/providers`, reads each `/api/current?provider=...` response, normalizes
remaining/used percentages, reset data and status, and caches the result for
the configured refresh interval.

- **Success `200`:** `{"quota": {"configured", "connected", "stale?",
  "error?", "updatedAt", "staleAfterSeconds", "providers": [...]}}`.
  Each Provider carries `provider`, `label`, optional `planLabel`, `status`,
  `capturedAt`, optional stale/error state, and `quotas`. Each quota carries
  `kind`, `label`, `remainingPercent`, `usedPercent`, reset/window fields when
  available, `status`, and optional `used`, `limit`, rate and projection data.
  `windowPositionPercent` is the percentage of the known reset window still
  remaining: `100` is the right edge at the start of a window and it moves
  toward the left edge (`0`) as reset approaches.
- When an upstream refresh fails after a successful read, the last cached
  snapshot is returned with `connected:false`, `stale:true` and an error.
- **Errors:** `503 runtime_unavailable`. Upstream availability is represented
  inside the successful snapshot so the UI can retain stale data.

```bash
curl -s "$BASE/v1/quota"
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
  "initialMessage": {"text": "Reproduce the failure first.", "role": "user"}
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
  - `initialMessage` (optional) — first inbound message; it accepts the same
    `text`, `role`, `sender`, `steer`, and reserved correlation fields as the
    messages endpoint. An omitted role means `user`; when non-empty the first
    turn starts immediately. `assistant` is rejected.
- **Success `201`:** `{"session": {...}}` with a
  `Location: /v1/sessions/{id}` header.
- **Errors:** `400 invalid_request` (malformed body or removed/unknown fields), `415 json_required`, `422 agent_required`,
  `422 invalid_agent` (unknown agent or disabled provider),
  `422 invalid_cwd`,
  `422 invalid_launch_environment` (an environment name is empty or contains
  `=`/NUL, or a value contains NUL),
  `500 session_create_failed`,
  `502 provider_start_failed` (the provider handshake failed; the response
  `details.sessionId` names the session kept for diagnostics in the
  `stopped` state with `stopReason: "startup_error"`),
  `502 turn_start_failed` (the provider started but the initial
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
    "initialMessage": {"text": "Reproduce the failure first.", "role": "user"}
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

- **Preconditions:** the session must be `stopped`, with no open turn or
  pending approval. Stop it first with
  `POST /v1/sessions/{id}/stop`; this endpoint never force-stops a provider.
- **Success `200`:** `{"session": {...}}` (state `archived`). Repeating an
  archive is idempotent.
- **Errors:** `404 session_not_found`, `400 invalid_session_id`,
  `409 session_active` (active work remains),
  `409 session_archive_conflict` (archive target occupied),
  `500 session_archive_failed` (filesystem error; the session's data stays
  intact and a retry or daemon restart completes the move).

```bash
curl -s -X DELETE "$BASE/v1/sessions/$SESSION" \
  -H "Content-Type: application/json" -d '{}'
```

### GET /v1/sessions/{id}/events

Read the session event log as JSON, or stream it live over Server-Sent
Events. Works for active and archived sessions.

#### JSON mode (default)

Plain requests return a JSON snapshot of the log after a cursor.

- **Query parameters:**
  - `after=<event-id>` — only events with an id greater than this cursor
    (default `0`, i.e. from the beginning).
  - `before=<event-id>` — backward pagination: the last `limit` events with
    an id smaller than this exclusive cursor. A `before` value past the
    durable head is clamped to `latestCursor+1` instead of rejected, so a
    tail read can never silently skip future events. Mutually exclusive
    with `after` and `latest`.
  - `latest=true` — equivalent to `before=<latestCursor+1>`: returns the
    last `limit` events of the log. Mutually exclusive with `after` and
    `before`.
  - `limit=<n>` — maximum number of events returned (default `500`,
    values above `1000` are clamped to the page size).
- **Success `200`:** events are in ascending id order in both directions.
  `after` and `nextAfter` are exclusive cursors; `latestCursor` is the
  durable head captured for this response.

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

  Backward pages (requested with `before` or `latest`) additionally carry
  `before` (the clamped exclusive cursor used), `nextBefore` and
  `hasMoreBefore` in `page`; forward pages never include these fields.

  ```json
  {
    "events": [],
    "page": {
      "after": 0,
      "limit": 100,
      "nextAfter": 1030,
      "hasMore": false,
      "before": 1031,
      "nextBefore": 931,
      "hasMoreBefore": true
    },
    "latestCursor": 1030
  }
  ```

  Page backward with `page.nextBefore` while `page.hasMoreBefore` is true.
  `nextAfter`, `hasMore` and `latestCursor` stay populated, so a tail page
  can hand `page.nextAfter` to a subsequent `after` request or SSE stream
  directly.
- **Errors:** `400 invalid_event_cursor` (malformed cursor, or `before` /
  `latest` combined with each other or with an explicit `after`),
  `404 session_not_found`, `409 event_cursor_ahead` (the supplied `after`
  cursor is newer than this session's durable head; the error details
  include `latestCursor`).

```bash
curl -s "$BASE/v1/sessions/$SESSION/events"
curl -s "$BASE/v1/sessions/$SESSION/events?after=100&limit=200"
curl -s "$BASE/v1/sessions/$SESSION/events?latest=true&limit=100"
curl -s "$BASE/v1/sessions/$SESSION/events?before=931&limit=100"
```

#### SSE mode

Send `Accept: text/event-stream` (or append `?stream=true`) to keep the
connection open and receive events as they happen. Backward pagination is
a JSON-mode feature: streams reject `before` and `latest` with
`400 invalid_event_cursor`.

- **Headers:** `Accept: text/event-stream`; optional `Last-Event-ID` with
  the id of the last event already processed (the standard SSE resume
  mechanism; `?after=` is an equivalent query form).
- **Connection lifecycle:**
  1. The daemon installs the live subscription and captures a durable
     high-water mark.
  2. It pages through **all** stored events after the exclusive cursor up to
     that high-water mark, with no 1000-event backlog limit.
  3. It then consumes the live subscription. When a text delta folds into
     the tail event, the live frame is an append patch: it reuses the folded
     event's id and its `data` carries only the new fragment flagged with
     `"append": true`. Consumers extend their stored copy of that event;
     only a new id advances the stream cursor.
  4. Every 15 seconds without traffic the daemon sends a `: heartbeat`
     comment line to keep proxies and clients alive.
  5. The stream ends when the client disconnects or when the daemon shuts
     down (daemon shutdown closes streams promptly so restarts are fast).
     A subscriber queue overflow also ends the stream immediately; the
     daemon never continues sending a queue known to contain a gap. Event
     and heartbeat writes have a five-second deadline, so a client that
     stops reading cannot pin the handler and prevent that terminal close.
- **Recovery:** reconnect with `Last-Event-ID` set to the id of the last
  contiguous event processed. The replay first re-sends that cursor event
  with its current durable content — append patches never move the cursor,
  so this heals fragments that folded into the tail event while the client
  was disconnected — and then continues with events after it, replayed from
  `events.jsonl` rather than the in-memory subscriber queue, so overflow
  and daemon restart are recoverable. A frame whose id is at or below the
  last processed id is either an append patch (`data.append: true`; extend
  the stored event's `text` with the fragment) or a full replacement (swap
  in the complete event); neither moves the cursor. Only an id greater
  than `last_processed_id + 1` is a gap; a client that observes one must
  stop projection and catch up through REST before resuming SSE.
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

### GET /v1/activity/events

Subscribe to a process-local, best-effort view of new activity across every
AgentHub Session. This stream is derived only from Events published after they
are durably appended to AgentHub's own `events.jsonl`; it does not scan or
read Provider-native Session files.

- Events are grouped into one-second windows and then by `sessionId`. A
  Session appears at most once per frame with `eventCount`, `provider`,
  `title`, `lastEventAt`, and `completed`. `completed` is true when the window
  contains canonical `turn.completed`, `turn.failed` or `turn.cancelled`.
- Frames carry a daemon-wide monotonic `sequence`, `windowStartedAt`,
  `windowEndedAt`, and `sessions`. They use the default SSE message channel.
- There is no historical replay and `Last-Event-ID` is ignored: reconnecting
  starts with current activity so stale work never produces delayed beeps.
  Clients use `sequence` only to detect a dropped frame and reset transient
  visualization state.
- The stream flushes immediately, sends a heartbeat every 15 seconds, uses
  bounded writes, and terminates on shutdown, client cancellation, or bounded
  subscriber overflow. EventSource can then reconnect automatically.

```text
id: 1842
data: {"sequence":1842,"windowStartedAt":"2026-08-10T15:06:23Z","windowEndedAt":"2026-08-10T15:06:24Z","sessions":[{"sessionId":"ses_abc123","provider":"codex","title":"Fix quota polling race","eventCount":18,"completed":false,"lastEventAt":"2026-08-10T15:06:23.921Z"}]}
```

```bash
curl -N -H "Accept: text/event-stream" "$BASE/v1/activity/events"
```

### POST /v1/sessions/{id}/messages

Send an inbound message. Without an active turn this starts a new turn; with
an active turn the message is rejected unless `steer` is set. The role and
sender describe message provenance only. They are not authentication,
authorization, trust, or instruction-priority signals. Every accepted input is
still submitted to the selected Provider as ordinary user-level prompt text.

- **Request body:** `{"text": "...", "role": "user", "sender": {...}, "steer": false}`
  - `text` (required) — the original message text; blank text is rejected.
  - `role` (optional) — `user`, `system`, or `agent`; omitted means `user`.
    `assistant` is reserved for output events produced by the current Session
    Provider and is rejected on inbound requests.
  - `sender` (optional) — provenance identity object with optional `id`,
    `name`, and `sessionId`. It is stored and displayed, but never
    authenticated. Agent messages can omit it for compatibility.
  - `steer` (optional) — inject the message into the currently active turn
    instead of starting a new one. Providers that cannot steer an active
    prompt (the ACP providers: Kimi and OpenCode) reject steer requests.
  - `messageId`, `replyTo`, `correlationId` (optional) — reserved string
    fields persisted for future message correlation; AgentHub does not yet
    implement Agent-to-Agent routing.
- **Success `202`:** `{"session": {...}}` — the turn runs asynchronously;
  watch it through the events endpoint.
- **Errors:** `400 invalid_request`, `415 json_required`,
  `400 invalid_message_role`, `400 assistant_message_forbidden`,
  `400 invalid_message_sender`, `400 invalid_message_reference`,
  `404 session_not_found`, `409 session_archived`,
  `409 session_stopping`, `409 turn_active` (an active turn exists without
  `steer=true`), `409 runtime_operation_failed` (the provider rejected the
  prompt),
  `503 runtime_unavailable`.

```bash
curl -s -X POST "$BASE/v1/sessions/$SESSION/messages" \
  -H "Content-Type: application/json" \
  -d '{"text": "Now fix the root cause."}'

curl -s -X POST "$BASE/v1/sessions/$SESSION/messages" \
  -H "Content-Type: application/json" \
  -d '{"text": "Skip the refactor and patch the test only.", "steer": true}'

curl -s -X POST "$BASE/v1/sessions/$SESSION/messages" \
  -H "Content-Type: application/json" \
  -d '{"text":"Resume the queued work.","role":"system","sender":{"name":"Forge Scheduler"}}'

curl -s -X POST "$BASE/v1/sessions/$SESSION/messages" \
  -H "Content-Type: application/json" \
  -d '{"text":"The worker finished its scan.","role":"agent","sender":{"name":"Review Agent","sessionId":"ses_worker"}}'
```

### POST /v1/sessions/{id}/resume

Start (or restart) a stopped session's provider without sending a message. When
the session recorded a provider-native session/thread id, the provider
resumes that native conversation, so context survives daemon and provider
restarts. Safe to call when the provider is already running.

- **Request body:** optional. An empty body (or `{}`) resumes with the
  session's recorded launch environment. `launchEnvironment` (optional) is a
  string-to-string overlay onto that durable environment: supplied entries
  replace same-named values, keys the overlay omits are kept, and nothing is
  deleted. The merged map is validated and persisted as a
  `session.launch-environment` event (carrying the full merged
  `environment` map) **before** the provider starts, so the provider resume
  picks it up; the historical `session.created` snapshot is never rewritten.
  The update stays durable even if the provider then fails to start, and it
  is visible to any client that can read the session — never put secrets
  here. When the provider is already running, the overlay is recorded and
  takes effect on the next provider start.
- **Success `200`:** `{"session": {...}}`.
- **Errors:** `415 json_required`, `400 invalid_request` (malformed JSON or
  unknown fields), `404 session_not_found`,
  `422 invalid_launch_environment` (an empty variable name, `=` or NUL in a
  name, or NUL in a value), `409 session_archived`, `409 session_stopping`,
  `409 runtime_operation_failed` (the provider could not start, for example
  because the recorded agent no longer exists),
  `503 runtime_unavailable`.

```bash
curl -s -X POST "$BASE/v1/sessions/$SESSION/resume" \
  -H "Content-Type: application/json" -d '{}'

curl -s -X POST "$BASE/v1/sessions/$SESSION/resume" \
  -H "Content-Type: application/json" \
  -d '{"launchEnvironment": {"FORGE_SESSION_ID": "session-456"}}'
```

### POST /v1/sessions/{id}/interrupt

Cancel the session's active turn without stopping the provider. Appends a
`turn.cancelled` event; the provider process stays alive for the next
message.

- **Request body:** empty (`{}`).
- **Success `200`:** `{"session": {...}}`.
- **Errors:** `415 json_required`, `404 session_not_found`,
  `409 session_archived`, `409 turn_not_active`,
  `409 runtime_operation_failed` (the provider rejected interruption),
  `503 runtime_unavailable`.

```bash
curl -s -X POST "$BASE/v1/sessions/$SESSION/interrupt" \
  -H "Content-Type: application/json" -d '{}'
```

### POST /v1/sessions/{id}/stop

Stop the session's provider process. The durable transition is `stopping`
followed by `stopped` with `reason: "requested"` only after the adapter Wait
path and process-group probe confirm that the provider can no longer write
to the working directory. The request does not return early. The session
keeps its full history and can be resumed later. Stop is the
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
- **Request body:** exactly one of the following reply modes:
  - `{"decision": "..."}` — a coarse decision, one of:
    - `accept` — approve this request once.
    - `acceptForSession` — approve and allow matching requests for the rest
      of the session where the provider supports it.
    - `decline` — reject the request.
    - `cancel` — cancel the operation that asked for approval.
  - `{"optionId": "..."}` — select one specific option offered by the
    request (the `optionId` values come from
    `approval.requested` → `params.options`). Unknown option ids are
    rejected and the approval stays pending.
  - `{"text": "..."}` — answer a question with custom free text. Provider
    protocols cannot carry free text inside an approval response, so the
    question is dismissed and the text is delivered as a regular user
    message once the current turn closes; the `approval.resolved` event
    records decision `text` with the reply.
- **Success `200`:** `{"session": {...}}`; an `approval.resolved` event is
  appended.
- **Errors:** `400 invalid_request`, `415 json_required`,
  `404 session_not_found`, `409 session_archived`,
  `400 invalid_approval_decision`, `409 approval_not_pending`,
  `409 runtime_operation_failed` (the provider is not running or rejected
  the response). Pending approvals do not survive a daemon restart: recovery
  appends `approval.resolved` with decision `cancel`, closes the open turn,
  and then publishes `stopped` with reason `daemon_recovery`,
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
- JSON request objects are strict: unknown fields, malformed JSON, multiple
  top-level JSON values and bodies larger than 1 MiB are rejected with
  `invalid_request`. JSON response objects are extensible: clients must
  ignore fields they do not recognize.

### Idempotency and conflicts

AgentHub does not currently interpret an `Idempotency-Key` header. Callers
must use the following operation semantics:

| Operation | Idempotency contract | Important conflict |
| --- | --- | --- |
| Create | Non-idempotent. Every accepted request creates a new session. A `provider_start_failed` response includes the created `details.sessionId`; inspect that session instead of blindly retrying. | Validation errors are 400/422; provider startup is 502. |
| Message | Non-idempotent. A user message and `turn.started` may already be durable even if provider submission fails. | `turn_active`, `session_stopping`, `session_archived`. |
| Steer (`messages` with `steer=true`) | Non-idempotent. Repeating it injects the text again. With no active turn it starts a normal new turn. | `session_stopping`, `session_archived`; provider rejection uses `runtime_operation_failed`. |
| Approval | Non-idempotent. Once resolved, the same id is no longer pending. | `approval_not_pending`, `session_archived`. |
| Interrupt | Not retry-idempotent: a successful call creates one `turn.cancelled`; a repeat sees no active turn. | `turn_not_active`, `session_archived`. |
| Stop | Idempotent for an already stopped session. It blocks through confirmed provider exit. | `session_archived`; process cleanup failure uses `runtime_operation_failed`. |
| Resume | Idempotent while the provider is already running; one provider instance remains associated with the session. | `session_stopping`, `session_archived`. |
| Archive | Idempotent for an already archived session. | `session_active`, `session_archive_conflict`. |

### Complete session flow

The following sequence negotiates the contract, creates a Forge-owned
session, catches up through the durable cursor, sends a message, resolves an
approval if one appears, waits for a canonical terminal event, then stops
and resumes the session. Shell snippets use `jq` for brevity:

```bash
STATUS=$(curl -fsS "$BASE/v1/status")
test "$(jq -r .apiVersion <<<"$STATUS")" = "1"
for capability in session.source session.launch-environment \
  session.strict-stopped events.lossless-replay \
  events.canonical-turn-terminals recovery.closed-turns; do
  jq -e --arg c "$capability" '.capabilities | index($c) != null' \
    <<<"$STATUS" >/dev/null
done

CREATED=$(curl -fsS -X POST "$BASE/v1/sessions" \
  -H "Content-Type: application/json" \
  -d "{
    \"title\":\"Forge task\",
    \"cwd\":\"$PWD\",
    \"agentName\":\"Codex\",
    \"source\":{\"app\":\"forge\",\"instanceId\":\"mac-mini\",\"externalId\":\"project7.task30\"},
    \"launchEnvironment\":{\"FORGE_SESSION_ID\":\"session-123\"}
  }")
SESSION=$(jq -r .session.id <<<"$CREATED")

PAGE=$(curl -fsS "$BASE/v1/sessions/$SESSION/events?after=0&limit=500")
CURSOR=$(jq -r .page.nextAfter <<<"$PAGE")

curl -fsS -X POST "$BASE/v1/sessions/$SESSION/messages" \
  -H "Content-Type: application/json" \
  -d '{"text":"Implement the requested change."}'

# Reconnect with Last-Event-ID=$CURSOR. Process each adjacent id, extend
# events on append patches, swap in full replacements for repeated ids, and
# ignore unknown types. If approval.requested arrives, resolve its
# approvalId:
curl -fsS -X POST "$BASE/v1/sessions/$SESSION/approvals/approval-1" \
  -H "Content-Type: application/json" \
  -d '{"decision":"accept"}'

# End the turn only on turn.completed, turn.failed or turn.cancelled, never
# on provider.turn.completed. Then release and later restore the provider:
curl -fsS -X POST "$BASE/v1/sessions/$SESSION/stop" \
  -H "Content-Type: application/json" -d '{}'
curl -fsS -X POST "$BASE/v1/sessions/$SESSION/resume" \
  -H "Content-Type: application/json" -d '{}'
```
