# Forge Integration Gate

AgentHub includes a black-box contract gate for clients such as Forge. The
gate starts a race-enabled `agenthub serve` binary and a deterministic fake
ACP provider as separate operating-system processes. It uses a new
`AGENTHUB_HOME`, working directory, loopback port, daemon process group, and
provider process group for every case. It never discovers or reads the user's
default AgentHub data root.

Run the complete gate with:

```bash
go test -race ./integration
```

The normal backend command, `go test -race ./...`, includes the same gate and
is the required CI check.

## Covered contract

The process-level scenarios cover:

- source metadata persistence, exact combined filters, replay, and daemon
  restart;
- per-session launch environment propagation, parallel isolation, native
  provider resume, and restart;
- the strict stopped boundary for startup failure, clean provider exit,
  provider crash, concurrent stop/exit, concurrent resume/stop, and archive;
- graceful daemon `SIGTERM`, ungraceful `SIGKILL`, orphan provider process
  cleanup, and deterministic closure of an open approval and turn;
- a 5,000-plus durable event backlog, interrupted SSE, subscriber overflow,
  REST catch-up, cursor-ahead errors, and restart while a turn is open;
- the API version, advertised capabilities, canonical turn terminals, and
  structured non-2xx error envelope.

The fake provider accepts fault controls only through the isolated session
launch environment. It can crash during startup or a turn, hold a prompt,
request an approval, exit normally, and emit a large event burst. These are
test fixtures, not public AgentHub configuration options.

SSE event and heartbeat writes are bounded to five seconds. A client that
stops reading therefore cannot pin a handler after its subscriber queue
overflows; it is disconnected and must resume from its last contiguous
durable cursor.

Every test registers cleanup before starting a process. Cleanup terminates
the daemon, and the runtime's strict stopped contract terminates and probes
the provider process group. Temporary roots are removed by the Go test
harness. Tests may run repeatedly or in parallel without sharing ports,
processes, or session directories.

## Forge responsibilities

AgentHub provides durable sessions, process lifecycle, replay cursors,
capability negotiation, and structured errors. Forge still owns:

- project, task, and resource-lock semantics;
- scheduler and AutoRun generation state;
- validating required capabilities before creating a session;
- generating a unique source identity and per-session launch environment;
- persisting its last contiguous event cursor and reconnecting after any SSE
  interruption or unknown event;
- deduplicating client retries according to each endpoint's documented
  idempotency, and reconciling ambiguous message delivery through events;
- deciding when a stopped session should be resumed or archived.

Source metadata is self-asserted correlation data, not authentication or
tenant isolation. Launch environment values are durable and visible through
the Session API, so Forge must not place secrets there unless persistence is
intended.
