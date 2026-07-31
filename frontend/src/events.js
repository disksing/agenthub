import { api } from "./api.js";

export class EventCursorGapError extends Error {
  constructor(expected, got) {
    super(`Event cursor gap: expected ${expected}, got ${got}.`);
    this.name = "EventCursorGapError";
    this.expected = expected;
    this.got = got;
  }
}

// catchUpEvents pages only to the durable head reported by the first request.
// Events appended during the catch-up are left for SSE, which removes an
// otherwise unbounded chase of a busy session.
export async function catchUpEvents(sessionId, after = 0, request = api) {
  let cursor = after;
  let target = null;
  const events = [];
  do {
    const body = await request(`/v1/sessions/${sessionId}/events?after=${cursor}&limit=1000`);
    if (target === null) target = body.latestCursor;
    const page = body.events || [];
    for (const event of page) {
      if (event.id > target) break;
      if (event.id !== cursor + 1) throw new EventCursorGapError(cursor + 1, event.id);
      events.push(event);
      cursor = event.id;
    }
    if (cursor < target && page.length === 0) {
      throw new EventCursorGapError(cursor + 1, 0);
    }
  } while (cursor < target);
  return { events, cursor, latestCursor: target };
}

// A live gap is never projected directly. The caller pauses the live source,
// catches up from the last contiguous cursor through REST, and only then
// resumes projection.
//
// The store folds consecutive text deltas into the tail event and republishes
// frames under the id the client already has: append patches carrying only the
// new fragment live, and the full accumulated event when a stream reconnect
// replays the cursor. Both are projected again so the host converges on the
// merged content; the cursor only advances on new ids.
export async function projectLiveEvent({
  sessionId,
  cursor,
  event,
  request = api,
  project,
}) {
  if (event.id <= cursor) {
    project([event]);
    return cursor;
  }
  if (event.id === cursor + 1) {
    project([event]);
    return event.id;
  }
  const caughtUp = await catchUpEvents(sessionId, cursor, request);
  project(caughtUp.events);
  return caughtUp.cursor;
}

// mergeIncomingEvents folds incoming frames into the stored contiguous event
// list. New ids append in arrival order. A repeated id is either a full
// replacement (history replays and reconnect cursor re-sends) or an append
// patch (data.append === true) whose text fragment extends the stored event;
// patches also move the stored event time to the newest fragment.
export function mergeIncomingEvents(current, incoming) {
  const next = [...current];
  const indexById = new Map(next.map((event, index) => [event.id, index]));
  for (const event of incoming) {
    const index = indexById.get(event.id);
    if (index === undefined) {
      indexById.set(event.id, next.length);
      next.push(event);
    } else if (event.data?.append === true) {
      next[index] = appendEventFragment(next[index], event);
    } else {
      next[index] = event;
    }
  }
  return next;
}

function appendEventFragment(existing, patch) {
  const current = typeof existing.data?.text === "string" ? existing.data.text : "";
  const fragment = typeof patch.data?.text === "string" ? patch.data.text : "";
  return {
    ...existing,
    time: patch.time || existing.time,
    data: { ...existing.data, text: current + fragment },
  };
}
