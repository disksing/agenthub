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
export async function projectLiveEvent({
  sessionId,
  cursor,
  event,
  request = api,
  project,
}) {
  if (event.id <= cursor) return cursor;
  if (event.id === cursor + 1) {
    project([event]);
    return event.id;
  }
  const caughtUp = await catchUpEvents(sessionId, cursor, request);
  project(caughtUp.events);
  return caughtUp.cursor;
}
