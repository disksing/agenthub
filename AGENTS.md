# AgentHub Instructions

The Go daemon, filesystem Session Store, HTTP/SSE API, and CLI are the primary product. The Web UI under `src/` is currently an auxiliary prototype.

- Keep the daemon as the only writer of Session data.
- Keep `events.jsonl` as the Session source of truth; `session.json` must remain a rebuildable projection.
- Do not add tokens, accounts, or API authentication. Non-loopback listening is allowed only through the explicit `serve --addr` flag, must print the startup security warning, and must keep the Host/Origin guards intact.
- Do not add SQLite or separate Turn/Approval persistence files.
- Keep Provider-specific fields behind adapters rather than exposing them as public Session fields.
- Run `go test -race ./...` for backend changes.

Run the local server yourself and open the preview in the browser available to this environment. Do not give the user server-start instructions when you can run it.

Before making substantial visual changes, use the Product Design plugin's `get-context` skill when the visual source is unclear or no longer matches the current goal. When the user gives durable prototype-specific design feedback, preferences, or decisions, record them in `AGENTS.md`.

When implementing from a selected generated mock, treat that image as the source of truth for layout, component anatomy, density, spacing, color, typography, visible content, and hierarchy.

Build app UI in `src/`. Keep `.openai/hosting.json`, `worker/index.js`, `scripts/prepare-sites-build.mjs`, and `tests/sites-worker.test.mjs` intact so the same local prototype can be handed to Sites. Before a Sites handoff, run `npm run build` and `npm run test:sites`; the build must leave `dist/client/index.html`, `dist/server/index.js`, and `dist/.openai/hosting.json`.
