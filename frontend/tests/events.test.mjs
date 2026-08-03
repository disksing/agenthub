import assert from "node:assert/strict";
import test from "node:test";
import {
  EventCursorGapError,
  catchUpEvents,
  mergeIncomingEvents,
  projectLiveEvent,
} from "../src/events.js";

const event = (id, type = "provider.unknown") => ({ id, type, data: { id } });

test("catchUpEvents pages through the first durable high-water mark", async () => {
  const calls = [];
  const request = async (path) => {
    calls.push(path);
    const after = Number(new URL(path, "http://agenthub.test").searchParams.get("after"));
    const latestCursor = calls.length === 1 ? 2500 : 2600;
    const end = Math.min(after + 1000, 2600);
    return {
      events: Array.from({ length: end - after }, (_, index) => event(after + index + 1)),
      latestCursor,
    };
  };
  const result = await catchUpEvents("ses_test", 0, request);
  assert.equal(result.cursor, 2500);
  assert.equal(result.events.length, 2500);
  assert.deepEqual(result.events.slice(-2).map(({ id }) => id), [2499, 2500]);
  assert.equal(calls.length, 3);
});

test("a live cursor gap stops projection and catches up through REST", async () => {
  const actions = [];
  const projected = [];
  const request = async () => {
    actions.push("catch-up");
    return { events: [event(2), event(3), event(4)], latestCursor: 4 };
  };
  const cursor = await projectLiveEvent({
    sessionId: "ses_test",
    cursor: 1,
    event: event(4),
    request,
    project(events) {
      actions.push("project");
      projected.push(...events);
    },
  });
  assert.equal(cursor, 4);
  assert.deepEqual(actions, ["catch-up", "project"]);
  assert.deepEqual(projected.map(({ id }) => id), [2, 3, 4]);
});

test("catch-up refuses a non-contiguous REST page", async () => {
  await assert.rejects(
    catchUpEvents("ses_test", 1, async () => ({
      events: [event(2), event(4)],
      latestCursor: 4,
    })),
    (error) => error instanceof EventCursorGapError && error.expected === 3 && error.got === 4,
  );
});

test("delta-merge replacements are projected again under the same id", async () => {
  const projected = [];
  const replacementCursor = await projectLiveEvent({
    sessionId: "ses_test",
    cursor: 5,
    event: event(5, "message.assistant.delta"),
    project: (events) => projected.push(...events),
  });
  assert.equal(replacementCursor, 5);
  const nextCursor = await projectLiveEvent({
    sessionId: "ses_test",
    cursor: replacementCursor,
    event: event(6, "future.event.type"),
    project: (events) => projected.push(...events),
  });
  assert.equal(nextCursor, 6);
  assert.deepEqual(projected, [event(5, "message.assistant.delta"), event(6, "future.event.type")]);
});

test("mergeIncomingEvents appends, replaces, and extends events", () => {
  const stored = [
    { id: 1, time: "t1", type: "turn.started" },
    { id: 2, time: "t2", type: "message.assistant.delta", data: { text: "Hello", method: "m" } },
  ];

  // Append patches extend the stored event text, move its time, and retain
  // the first-fragment time used by the folded reasoning presentation.
  const patched = mergeIncomingEvents(stored, [
    { id: 2, time: "t3", startTime: "t2", type: "message.assistant.delta", data: { text: ", ", method: "m", append: true } },
    { id: 2, time: "t4", startTime: "t2", type: "message.assistant.delta", data: { text: "world", method: "m", append: true } },
  ]);
  assert.equal(patched.length, 2);
  assert.equal(patched[1].data.text, "Hello, world");
  assert.equal(patched[1].data.method, "m");
  assert.equal(patched[1].time, "t4");
  assert.equal(patched[1].startTime, "t2");
  assert.equal(patched[1].data.append, undefined);
  // The input list is never mutated.
  assert.equal(stored[1].data.text, "Hello");

  // Full replacements (history replays, reconnect cursor re-sends) swap the
  // whole event and heal any missed patches.
  const healed = mergeIncomingEvents(patched, [
    { id: 2, time: "t5", startTime: "t2", type: "message.assistant.delta", data: { text: "Hello, world!", method: "m" } },
  ]);
  assert.equal(healed[1].data.text, "Hello, world!");
  assert.equal(healed[1].startTime, "t2");

  // New ids append in arrival order.
  const appended = mergeIncomingEvents(healed, [
    { id: 3, time: "t6", type: "turn.completed" },
  ]);
  assert.equal(appended.length, 3);
  assert.equal(appended[2].type, "turn.completed");
});
