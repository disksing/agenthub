import assert from "node:assert/strict";
import { readFile, stat } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";
import { COMPLETION_SOUNDS, completionSoundURL, pulsePlaybackOffsets, TonePlayer } from "../src/companion/audio.js";
import {
	activityPulsesForFrame,
	activitySessions,
	companionPlacement,
	companionPositionFromPixels,
	companionPositionPixels,
	formatDuration,
	normalizeCompanionSize,
	noteForSession,
	pruneActivityPulses,
	quotaCycleItems,
	resizeCompanionSize,
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
	assert.equal(first.get("ses_abc").lastActiveAt, 1000);
	const refreshed = activitySessions(first, { sessions: [{ sessionId: "ses_abc", eventCount: 9 }] }, 2000);
	assert.equal(refreshed.get("ses_abc").lastActiveAt, 2000);
	assert.equal(activitySessions(first, { sessions: [] }, 300999).size, 1);
	assert.equal(activitySessions(first, { sessions: [] }, 301000).size, 0);
});

test("each active session creates one evenly spaced waveform pulse per frame", () => {
	assert.equal(formatDuration(5 * 86400 + 2 * 3600), "5d 2h");
	assert.equal(formatDuration(3 * 3600 + 12 * 60), "3h 12m");
	const now = Date.parse("2026-08-11T10:00:01.000Z");
	const frame = { sessions: [{
		sessionId: "ses_abc",
		eventCount: 18,
		lastEventAt: "2026-08-11T10:00:00.900Z",
		completed: false,
	}] };
	const pulses = activityPulsesForFrame(frame, now);
	assert.equal(pulses.length, 1);
	assert.deepEqual(pulses, activityPulsesForFrame(frame, now));
	assert.deepEqual(pulses.map((pulse) => pulse.at), [now]);
	const concurrent = activityPulsesForFrame({ sessions: [
		{ sessionId: "ses_c", eventCount: 7, completed: false },
		{ sessionId: "ses_a", eventCount: 1, completed: false },
		{ sessionId: "ses_b", eventCount: 4, completed: false },
	] }, now);
	assert.deepEqual(concurrent.map((pulse) => pulse.sessionId), ["ses_a", "ses_b", "ses_c"]);
	assert.deepEqual(concurrent.map((pulse) => pulse.at), [now, now + 1000 / 3, now + 2000 / 3]);
	assert.notEqual(waveformPoints(pulses, now), waveformPoints([], now));
	assert.notEqual(waveformPoints(pulses, now), waveformPoints(pulses, now + 1000));
	const peak = (points) => points.split(" ").map((point) => point.split(",").map(Number)).reduce((best, point) => point[1] < best[1] ? point : best);
	const enteringPeak = peak(waveformPoints([pulses[0]], now));
	const scrolledPeak = peak(waveformPoints([pulses[0]], now + 500));
	assert.ok(scrolledPeak[0] < enteringPeak[0]);
	assert.equal(scrolledPeak[1], enteringPeak[1]);
	assert.equal(pruneActivityPulses(pulses, now + 10000).length, 0);
});

test("activity beeps are evenly spaced across each one-second frame", () => {
	assert.deepEqual(pulsePlaybackOffsets(0), []);
	assert.deepEqual(pulsePlaybackOffsets(1), [0]);
	assert.deepEqual(pulsePlaybackOffsets(2), [0, 0.5]);
	assert.deepEqual(pulsePlaybackOffsets(3), [0, 1 / 3, 2 / 3]);
	assert.deepEqual(pulsePlaybackOffsets(4), [0, 0.25, 0.5, 0.75]);
});

test("companion position round-trips and card expands away from viewport edges", () => {
	const viewport = { width: 1200, height: 800 };
	const pill = { width: 236, height: 42 };
	const size = { width: 380, height: 520 };
	for (const position of [{ x: 0, y: 0 }, { x: 1, y: 0 }, { x: 0, y: 1 }, { x: 1, y: 1 }, { x: 0.37, y: 0.62 }]) {
		const pixels = companionPositionPixels(position, viewport, pill);
		const restored = companionPositionFromPixels(pixels, viewport, pill);
		assert.ok(Math.abs(restored.x - position.x) < 0.0001);
		assert.ok(Math.abs(restored.y - position.y) < 0.0001);
	}
	const topLeft = companionPlacement(companionPositionPixels({ x: 0, y: 0 }, viewport, pill), viewport, pill, size);
	const topRight = companionPlacement(companionPositionPixels({ x: 1, y: 0 }, viewport, pill), viewport, pill, size);
	const bottomLeft = companionPlacement(companionPositionPixels({ x: 0, y: 1 }, viewport, pill), viewport, pill, size);
	const bottomRight = companionPlacement(companionPositionPixels({ x: 1, y: 1 }, viewport, pill), viewport, pill, size);
	assert.deepEqual([topLeft.vertical, topLeft.horizontal], ["down", "right"]);
	assert.deepEqual([topRight.vertical, topRight.horizontal], ["down", "left"]);
	assert.deepEqual([bottomLeft.vertical, bottomLeft.horizontal], ["up", "right"]);
	assert.deepEqual([bottomRight.vertical, bottomRight.horizontal], ["up", "left"]);
	for (const placement of [topLeft, topRight, bottomLeft, bottomRight]) {
		assert.ok(placement.left >= 12);
		assert.ok(placement.left + placement.width <= viewport.width - 12);
		assert.equal(placement.width, 380);
		assert.equal(placement.height, 520);
		assert.ok(placement.maxHeight <= viewport.height - 12);
	}
});

test("companion resizing follows its expansion corner and clamps to available space", () => {
	const viewport = { width: 1200, height: 800 };
	const pill = { width: 236, height: 42 };
	const cases = [
		[{ x: 0, y: 0 }, { x: 100, y: 80 }],
		[{ x: 1, y: 0 }, { x: -100, y: 80 }],
		[{ x: 0, y: 1 }, { x: 100, y: -80 }],
		[{ x: 1, y: 1 }, { x: -100, y: -80 }],
	];
	for (const [position, delta] of cases) {
		const placement = companionPlacement(
			companionPositionPixels(position, viewport, pill),
			viewport,
			pill,
			{ width: 380, height: 520 },
		);
		assert.deepEqual(resizeCompanionSize({ width: 380, height: 520 }, delta, placement), { width: 480, height: 600 });
		const maximum = resizeCompanionSize({ width: 380, height: 520 }, { x: delta.x * 20, y: delta.y * 20 }, placement);
		assert.equal(maximum.width, placement.maxWidth);
		assert.equal(maximum.height, placement.maxHeight);
	}
	assert.deepEqual(normalizeCompanionSize(null), { width: 380, height: 520 });
	assert.deepEqual(normalizeCompanionSize({ width: 10, height: 20 }), { width: 280, height: 260 });
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
	const styles = await readFile(path.join(frontendRoot, "src", "styles.css"), "utf8");
	assert.equal((source.match(/new EventSource/g) || []).length, 1);
	assert.ok(source.includes('new EventSource("/v1/activity/events")'));
	assert.ok(source.includes("activityPulsesForFrame(frame, receivedAt)"));
	assert.ok(source.includes("pulsePlaybackOffsets(sessions.length)"));
	assert.ok(source.includes('className="companion-thread-row"'));
	assert.ok(source.includes('className="companion-thread-title"'));
	assert.ok(!source.includes("companion-thread-meta"));
	assert.ok(styles.includes("companion-thread-flash 10s"));
	assert.ok(source.includes('className="companion-resize-handle"'));
	assert.ok(source.includes("agenthub.companion.size.v1"));
	assert.ok(styles.includes("@container companion-card (min-width: 560px)"));
	assert.ok(styles.includes("@container companion-card (max-height: 390px)"));
	assert.ok(!model.includes("Math.random"));
	for (const forbidden of [".codex/sessions", "fsnotify", "/v1/sessions/${"]) {
		assert.ok(!source.includes(forbidden), `companion must not contain ${forbidden}`);
	}
});
