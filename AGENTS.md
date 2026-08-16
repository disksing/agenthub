# AgentHub Instructions

The Go daemon, filesystem Session Store, HTTP/SSE API, and CLI are the primary product. The Web UI under `frontend/src/` is currently an auxiliary prototype.

- Keep the daemon as the only writer of Session data.
- Keep `events.jsonl` as the Session source of truth; `session.json` must remain a rebuildable projection.
- Do not add tokens, accounts, or API authentication. Non-loopback listening is allowed only through the explicit `serve --addr` flag, must print the startup security warning, and must keep the Host/Origin guards intact.
- Do not add SQLite or separate Turn/Approval persistence files.
- Keep Provider-specific fields behind adapters rather than exposing them as public Session fields.
- Run `go test -race ./...` for backend changes. Run `go test -race -count=1 -tags=integration ./integration` when changing process lifecycle, durable replay, SSE recovery, or the PUA-facing contract.

Run the local server yourself and open the preview in the browser available to this environment. Do not give the user server-start instructions when you can run it.

Before making substantial visual changes, use the Product Design plugin's `get-context` skill when the visual source is unclear or no longer matches the current goal. When the user gives durable prototype-specific design feedback, preferences, or decisions, record them in `AGENTS.md`.

The standalone `/beeper` monitor is a full-viewport dark surface without an outer page title or back link. Keep Provider quota in one column in portrait orientation. Active Session labels use compact full-width rows, one Session per row, without an elapsed-time/countdown subtitle; retrigger the approved bright highlight on each activity frame and fade it to the resting colors over ten seconds. Keep a terminal row visible for five minutes without extending that deadline for late events from the same Turn: use yellow for completed Turns and red for failed or cancelled Turns, and clear the terminal state when a different Turn starts. Render exactly one ECG pulse per Session in each one-second frame regardless of `eventCount`. Quantize both ECG pulses and activity beeps to the same four 250ms subdivisions: use `[X---]` for one Session, `[X-X-]` for two, deterministically rotate `[XXX-]`, `[XX-X]`, and `[X-XX]` for three, and `[XXXX]` for four; stack chord tones on rotating subdivisions above four rather than shortening the grid. Activity beeps use the selected major or minor triad, or the optional built-in Canon in C progression (C, G, Am, Em, F, C, F, G), independently holding each chord for a random one to six one-second frames. At every boundary, switch all Sessions immediately to discrete tones from the new chord; do not glide, bend, or stagger pitches. Allocate stable Session tone slots through octave bands 5, 4, 6, 3, then 7, prioritizing the lower bands before the highest tone, releasing a terminal Session's slot after ten seconds or when it leaves the active window. Keep the active Session title font at 13px (matching the per-row note) rather than a size larger than surrounding labels, and keep rows compact: no minimum height, only tight vertical padding (3px) around the text.

When implementing from a selected generated mock, treat that image as the source of truth for layout, component anatomy, density, spacing, color, typography, visible content, and hierarchy.

Build app UI in `frontend/src/`. Keep `frontend/worker/index.js`, `frontend/scripts/prepare-sites-build.mjs`, and `frontend/tests/sites-worker.test.mjs` intact so the same local prototype can be handed to Sites. Before a Sites handoff, run `npm run build` and `npm run test:sites` in `frontend/`; the build must leave `frontend/dist/client/index.html` and `frontend/dist/server/index.js`.
