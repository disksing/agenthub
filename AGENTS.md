# AgentHub Instructions

The Go daemon, filesystem Session Store, HTTP/SSE API, and CLI are the primary product. The Web UI under `frontend/src/` is currently an auxiliary prototype.

- Keep the daemon as the only writer of Session data.
- Keep `events.jsonl` as the Session source of truth; `session.json` must remain a rebuildable projection.
- Do not add tokens, accounts, or API authentication. Non-loopback listening is allowed only through the explicit `serve --addr` flag, must print the startup security warning, and must keep the Host/Origin guards intact.
- Do not add SQLite or separate Turn/Approval persistence files.
- Keep Provider-specific fields behind adapters rather than exposing them as public Session fields.
- Run `go test -race ./...` for backend changes.

Run the local server yourself and open the preview in the browser available to this environment. Do not give the user server-start instructions when you can run it.

Before making substantial visual changes, use the Product Design plugin's `get-context` skill when the visual source is unclear or no longer matches the current goal. When the user gives durable prototype-specific design feedback, preferences, or decisions, record them in `AGENTS.md`.

The standalone `/beeper` monitor is a full-viewport dark surface without an outer page title or back link. Keep Provider quota in one column in portrait orientation. Active Session labels use large full-width rows, one Session per row, without an elapsed-time/countdown subtitle; retrigger the approved bright highlight on each activity frame and fade it to the resting colors over ten seconds. Render exactly one ECG pulse per Session in each one-second frame regardless of `eventCount`. When a frame has multiple active Sessions, distribute both their ECG pulses and activity beeps at the same evenly spaced offsets within that second; pulses enter from the right and scroll smoothly to the left.

When implementing from a selected generated mock, treat that image as the source of truth for layout, component anatomy, density, spacing, color, typography, visible content, and hierarchy.

Build app UI in `frontend/src/`. Keep `frontend/worker/index.js`, `frontend/scripts/prepare-sites-build.mjs`, and `frontend/tests/sites-worker.test.mjs` intact so the same local prototype can be handed to Sites. Before a Sites handoff, run `npm run build` and `npm run test:sites` in `frontend/`; the build must leave `frontend/dist/client/index.html` and `frontend/dist/server/index.js`.
