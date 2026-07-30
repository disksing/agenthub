# AgentHub Event Timeline

`@agenthub/event-timeline` is the single, dependency-free projection used by
AgentHub Web and intended for fixed-version vendoring by other AgentHub API
v1 clients. It accepts the complete ordered canonical event sequence:

```js
{ id, time, type, sessionId, turnId?, data? }
```

and returns plain timeline items with these `kind` values:

```text
message, thinking, tools, approval, lifecycle, error, unknown
```

The projection filters provider activity, merges assistant and reasoning
deltas within a turn, interprets Codex/ACP/Pi tool payloads, correlates tool
updates across visible events, groups adjacent tools, resolves approvals, and
settles open tools from turn or session terminal events. Hosts remain
responsible for time formatting, icons, Markdown, styles, and interactive
expanded/collapsed state.

Events must be in durable `id` order. The function does not fetch, sort, or
retain hidden state: after adding REST or SSE events, call it again with the
available contiguous sequence. A sequence may be a truncated recent tail;
pure tool output deltas whose call started before that tail stay hidden because
an update is not a standalone tool call. It returns new mutable objects and
never mutates the input events.

All items include `kind`, `key` (the originating event id), and the raw
canonical `time` string. Kind-specific fields are:

| Kind | Fields |
| --- | --- |
| `message` | `role` (`user` or `assistant`), `text`, optional `turnId` and `steer` |
| `thinking` | `text`, `turnId`, semantic `active` flag |
| `tools` | `calls[]`; each call has identity, label, status, output/error, method, time, and a bounded raw preview |
| `approval` | `approvalId`, summary, `pending`/`accepted`/`declined` status, decision |
| `lifecycle` | stable English summary plus semantic `muted`/`info`/`ok`/`danger` tone |
| `error` | provider error text |
| `unknown` | original event type and a bounded payload preview |

Tool status is `running`, `completed`, or `failed`. The library does not emit
an expanded/collapsed property; hosts choose their own initial and persistent
interaction state.

## ESM

```js
import {
  API_EVENT_CONTRACT_VERSION,
  VERSION,
  buildTimeline,
} from "@agenthub/event-timeline";

const items = buildTimeline(events);
```

Only `buildTimeline`, `VERSION`, and `API_EVENT_CONTRACT_VERSION` are public.
The current package version is `1.0.0`; its input contract is
`agenthub.api.v1`.

## Browser IIFE

Load `dist/event-timeline.iife.js` without React or a bundler:

```html
<script src="./event-timeline.iife.js"></script>
<script>
  const items = AgentHubEventTimeline.buildTimeline(events);
</script>
```

The stable global is `AgentHubEventTimeline`.

## Build and verify

```sh
npm run build
npm test
```

The zero-dependency build is deterministic. `dist/manifest.json` records the
package and contract versions, source input hashes, artifact SHA-256 hashes,
license, build command, and source revision policy. Shared sanitized canonical
events live in `fixtures/`; exact expected projections live in `snapshots/`.

Consumers should vendor all files from a single AgentHub Git revision and
verify the selected artifact against `dist/manifest.json`. No npm registry
publication or token is required.
