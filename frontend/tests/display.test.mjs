import assert from "node:assert/strict";
import test from "node:test";
import { displayDuration } from "../src/display.js";

test("displayDuration renders sub-minute spans as seconds", () => {
  assert.equal(displayDuration("2026-07-25T10:00:00Z", "2026-07-25T10:00:00Z"), "0 seconds");
  assert.equal(displayDuration("2026-07-25T10:00:00Z", "2026-07-25T10:00:01Z"), "1 second");
  assert.equal(displayDuration("2026-07-25T10:00:00Z", "2026-07-25T10:00:42Z"), "42 seconds");
});

test("displayDuration renders minute spans as XmYs", () => {
  assert.equal(displayDuration("2026-07-25T10:00:00Z", "2026-07-25T10:01:02Z"), "1m2s");
  assert.equal(displayDuration("2026-07-25T10:00:00Z", "2026-07-25T10:10:00Z"), "10m0s");
});

test("displayDuration returns an empty label for missing or invalid times", () => {
  assert.equal(displayDuration("", "2026-07-25T10:00:00Z"), "");
  assert.equal(displayDuration("2026-07-25T10:00:00Z", "not-a-date"), "");
  assert.equal(displayDuration("2026-07-25T10:00:05Z", "2026-07-25T10:00:00Z"), "");
});
