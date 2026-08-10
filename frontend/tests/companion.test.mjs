import assert from "node:assert/strict";
import { readFile, stat } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";
import { COMPLETION_SOUNDS, completionSoundURL, TonePlayer } from "../src/companion/audio.js";
import {
	activityPulsesForFrame,
	activitySessions,
	companionPlacement,
	companionPositionFromPixels,
	companionPositionPixels,
	formatDuration,
	noteForSession,
	pruneActivityPulses,
	quotaCycleItems,
	waveformPoints,
} from "../src/companion/model.js";

const frontendRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");

test("quota cycle keeps provider order and skips empty providers", () => {
	const items = quotaCycleItems({ providers: [
		{ provider: "codex", label: "Codex", status: "healthy", quotas: [{ kind: "7d", remainingPercent: 83, status: "healthy" }] },
		{ provider: "empty", label: "Empty", quotas: [] },
		{ provider: "grok", label: "Grok", quotas: [{ kind: "credits", remainingPercent: 22, status: "warning" }] },
	] });
	assert.deepEqual(items.map((item) => item.provider), ["Codex", "Grok"]);
	assert.equal(items[1].value, 22);
	assert.equal(items[1].label, "credits");
});

test("session notes are stable and activity expires after five minutes", () => {
	assert.deepEqual(noteForSession("ses_abc"), noteForSession("ses_abc"));
	const first = activitySessions(new Map(), { sessions: [{ sessionId: "ses_abc", eventCount: 3 }] }, 1000);
	assert.equal(first.size, 1);
	assert.equal(activitySessions(first, { sessions: [] }, 300999).size, 1);
	assert.equal(activitySessions(first, { sessions: [] }, 301000).size, 0);
});

test("quota labels and activity waveform helpers are deterministic", () => {
	assert.equal(formatDuration(5 * 86400 + 2 * 3600), "5d 2h");
	assert.equal(formatDuration(3 * 3600 + 12 * 60), "3h 12m");
	const now = Date.parse("2026-08-11T10:00:01.000Z");
	const frame = { sessions: [{
		sessionId: "ses_abc",
		eventCount: 3,
		lastEventAt: "2026-08-11T10:00:00.900Z",
		completed: false,
	}] };
	const pulses = activityPulsesForFrame(frame, now);
	assert.equal(pulses.length, 3);
	assert.deepEqual(pulses, activityPulsesForFrame(frame, now));
	assert.notEqual(waveformPoints(pulses, now), waveformPoints([], now));
	assert.notEqual(waveformPoints(pulses, now), waveformPoints(pulses, now + 1000));
	assert.equal(pruneActivityPulses(pulses, now + 10000).length, 0);
});

test("companion position round-trips and card expands away from viewport edges", () => {
	const viewport = { width: 1200, height: 800 };
	const pill = { width: 236, height: 42 };
	for (const position of [{ x: 0, y: 0 }, { x: 1, y: 0 }, { x: 0, y: 1 }, { x: 1, y: 1 }, { x: 0.37, y: 0.62 }]) {
		const pixels = companionPositionPixels(position, viewport, pill);
		const restored = companionPositionFromPixels(pixels, viewport, pill);
		assert.ok(Math.abs(restored.x - position.x) < 0.0001);
		assert.ok(Math.abs(restored.y - position.y) < 0.0001);
	}
	const topLeft = companionPlacement(companionPositionPixels({ x: 0, y: 0 }, viewport, pill), viewport, pill);
	const topRight = companionPlacement(companionPositionPixels({ x: 1, y: 0 }, viewport, pill), viewport, pill);
	const bottomLeft = companionPlacement(companionPositionPixels({ x: 0, y: 1 }, viewport, pill), viewport, pill);
	const bottomRight = companionPlacement(companionPositionPixels({ x: 1, y: 1 }, viewport, pill), viewport, pill);
	assert.deepEqual([topLeft.vertical, topLeft.horizontal], ["down", "right"]);
	assert.deepEqual([topRight.vertical, topRight.horizontal], ["down", "left"]);
	assert.deepEqual([bottomLeft.vertical, bottomLeft.horizontal], ["up", "right"]);
	assert.deepEqual([bottomRight.vertical, bottomRight.horizontal], ["up", "left"]);
	for (const placement of [topLeft, topRight, bottomLeft, bottomRight]) {
		assert.ok(placement.left >= 12);
		assert.ok(placement.left + 380 <= viewport.width - 12);
		assert.ok(placement.maxHeight <= viewport.height - 12);
	}
});

test("TonePlayer uses a suspended Web Audio context only after resume", async () => {
	const scheduled = [];
	class FakeAudioContext {
		constructor() { this.state = "suspended"; this.currentTime = 10; this.destination = {}; }
		async resume() { this.state = "running"; }
		createOscillator() { return { type: "", frequency: { setValueAtTime: (...args) => scheduled.push(["frequency", ...args]) }, connect() {}, start: (...args) => scheduled.push(["start", ...args]), stop() {} }; }
		createGain() { return { gain: { setValueAtTime() {}, exponentialRampToValueAtTime() {} }, connect() {} }; }
	}
	const player = new TonePlayer(FakeAudioContext);
	assert.equal(player.pulse("ses_abc"), false);
	assert.equal(await player.resume(), true);
	assert.equal(player.pulse("ses_abc", 0.2, 0.08), true);
	assert.ok(scheduled.some(([kind, time]) => kind === "start" && time === 10.08));
});

test("TonePlayer plays each bundled Codex Beeper completion sound", async () => {
	const played = [];
	class FakeAudio {
		constructor(src) { this.src = src; this.volume = 1; this.currentTime = 0; }
		addEventListener() {}
		async play() { played.push(this.src); }
		pause() {}
	}
	const player = new TonePlayer(undefined, FakeAudio);
	assert.equal(await player.resume(), true);
	for (const option of COMPLETION_SOUNDS) {
		player.completion(option.value, 0.42);
		assert.equal(completionSoundURL(option.value), `/completion-sounds/${option.file}`);
		const metadata = await stat(path.join(frontendRoot, "public", "completion-sounds", option.file));
		assert.ok(metadata.size > 1000, `${option.file} must be a non-empty bundled audio file`);
	}
	assert.equal(played.length, COMPLETION_SOUNDS.length + 1);
});

test("companion uses one global EventSource and never scans provider sessions", async () => {
	const source = await readFile(path.join(frontendRoot, "src", "companion", "Companion.jsx"), "utf8");
	const model = await readFile(path.join(frontendRoot, "src", "companion", "model.js"), "utf8");
	assert.equal((source.match(/new EventSource/g) || []).length, 1);
	assert.ok(source.includes('new EventSource("/v1/activity/events")'));
	assert.ok(source.includes("activityPulsesForFrame(frame, receivedAt)"));
	assert.ok(!model.includes("Math.random"));
	for (const forbidden of [".codex/sessions", "fsnotify", "/v1/sessions/${"]) {
		assert.ok(!source.includes(forbidden), `companion must not contain ${forbidden}`);
	}
});
