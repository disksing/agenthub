import assert from "node:assert/strict";
import { readFile, stat } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";
import { activityPlaybackPlan, COMPLETION_SOUNDS, completionSoundURL, TonePlayer } from "../src/companion/audio.js";
import {
	BEEP_CHORDS,
	BEEP_MAX_FRAMES_PER_CHORD,
	BEEP_MIN_FRAMES_PER_CHORD,
	BEEP_OCTAVE_ORDER,
	BEEP_PROGRESSIONS,
	chordTonePool,
	nextProgressionFrame,
	noteForToneSlot,
	progressionChordValues,
	randomProgressionDuration,
} from "../src/companion/chords.js";
import {
	ACTIVITY_SESSION_RETENTION_MS,
	activityPulsesForFrame,
	activitySessionHoldsTone,
	activitySessionNeedsTone,
	activitySessions,
	activitySessionTerminal,
	companionPlacement,
	companionPositionFromPixels,
	companionPositionPixels,
	filterQuotaSnapshot,
	formatDuration,
	normalizeCompanionSize,
	pruneActivityPulses,
	quotaCycleItems,
	quotaVisibilityKey,
	resizeCompanionSize,
	SessionToneAllocator,
	TERMINAL_TONE_HOLD_MS,
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

test("quota visibility filters individual rows before card rendering and rotation", () => {
	const snapshot = { connected: true, providers: [
		{ provider: "kimi", label: "Kimi", quotas: [
			{ kind: "5h", label: "5-hour", remainingPercent: 80 },
			{ kind: "7d", label: "Weekly", remainingPercent: 60 },
		] },
		{ provider: "codex", label: "Codex", quotas: [{ kind: "5h", label: "5-hour", remainingPercent: 40 }] },
	] };
	const hiddenKey = quotaVisibilityKey(snapshot.providers[0], snapshot.providers[0].quotas[0]);
	assert.equal(hiddenKey, '["kimi","5h"]');
	const filtered = filterQuotaSnapshot(snapshot, [hiddenKey]);
	assert.deepEqual(filtered.providers.map((provider) => (
		[provider.provider, provider.quotas.map((quota) => quota.kind)]
	)), [["kimi", ["7d"]], ["codex", ["5h"]]]);
	assert.deepEqual(quotaCycleItems(filtered).map((item) => [item.provider, item.label]), [["Kimi", "7d"], ["Codex", "5h"]]);
	assert.equal(snapshot.providers[0].quotas.length, 2, "filtering must not mutate the source snapshot");
	assert.deepEqual(filterQuotaSnapshot(snapshot, [
		hiddenKey,
		quotaVisibilityKey(snapshot.providers[0], snapshot.providers[0].quotas[1]),
	]).providers.map((provider) => provider.provider), ["codex"]);
});

test("session tone slots fill in octave order and reuse released slots", () => {
	assert.deepEqual(BEEP_OCTAVE_ORDER, [5, 4, 6, 3, 7]);
	assert.equal(BEEP_CHORDS.length, 24);
	assert.deepEqual(chordTonePool("c-major").map((note) => note.name), [
		"C5", "E5", "G5", "C4", "E4", "G4", "C6", "E6", "G6", "C3", "E3", "G3", "C7", "E7", "G7",
	]);
	assert.deepEqual(chordTonePool("d-major").map((note) => note.name).slice(0, 3), ["D5", "F#5", "A5"]);
	assert.deepEqual(chordTonePool("c-minor").map((note) => note.name).slice(0, 3), ["C5", "Eb5", "G5"]);
	assert.ok(Math.abs(noteForToneSlot(0).frequency - 523.2511306) < 0.0001);

	const allocator = new SessionToneAllocator();
	assert.deepEqual(Array.from({ length: 15 }, (_, index) => allocator.assign(`ses_${index}`)), [...Array(15).keys()]);
	assert.equal(allocator.assign("ses_0"), 0);
	assert.equal(allocator.assign("ses_15"), 0);
	allocator.release("ses_1");
	assert.equal(allocator.assign("ses_new"), 1);
	allocator.retain(["ses_5"]);
	assert.equal(allocator.assign("ses_after_retain"), 0);
});

test("canon progression uses immediate random one-to-six-frame chord changes", () => {
	assert.equal(BEEP_MIN_FRAMES_PER_CHORD, 1);
	assert.equal(BEEP_MAX_FRAMES_PER_CHORD, 6);
	assert.deepEqual(BEEP_PROGRESSIONS.map((option) => option.value), ["single", "canon-in-c"]);
	assert.deepEqual(progressionChordValues("canon-in-c"), [
		"c-major", "g-major", "a-minor", "e-minor", "f-major", "c-major", "f-major", "g-major",
	]);
	assert.equal(randomProgressionDuration(() => 0), 1);
	assert.equal(randomProgressionDuration(() => 0.999999), 6);

	const samples = [0, 0.999999, 0.2];
	const random = () => samples.shift();
	let frame = null;
	const observed = [];
	for (let sequence = 1; sequence <= 9; sequence += 1) {
		frame = nextProgressionFrame(frame, "canon-in-c", "c-major", sequence, random);
		observed.push([frame.chord, frame.frameInChord, frame.duration]);
	}
	assert.deepEqual(observed, [
		["c-major", 1, 1],
		["g-major", 1, 6],
		["g-major", 2, 6],
		["g-major", 3, 6],
		["g-major", 4, 6],
		["g-major", 5, 6],
		["g-major", 6, 6],
		["a-minor", 1, 2],
		["a-minor", 2, 2],
	]);
	const reset = nextProgressionFrame(frame, "canon-in-c", "c-major", 20, () => 0.5);
	assert.deepEqual([reset.chord, reset.frameInChord, reset.duration], ["c-major", 1, 4]);
	const single = nextProgressionFrame(null, "single", "d-major", 1, () => { throw new Error("single chord must not sample"); });
	assert.deepEqual([single.chord, single.duration], ["d-major", null]);
});

test("activity expires after five minutes", () => {
	const first = activitySessions(new Map(), { sessions: [{ sessionId: "ses_abc", eventCount: 3 }] }, 1000);
	assert.equal(first.size, 1);
	assert.equal(first.get("ses_abc").lastActiveAt, 1000);
	const refreshed = activitySessions(first, { sessions: [{ sessionId: "ses_abc", eventCount: 9 }] }, 2000);
	assert.equal(refreshed.get("ses_abc").lastActiveAt, 2000);
	assert.equal(activitySessions(first, { sessions: [] }, 300999).size, 1);
	assert.equal(activitySessions(first, { sessions: [] }, 301000).size, 0);
});

test("terminal activity keeps status for five minutes and its tone for ten seconds", () => {
	const active = activitySessions(new Map(), { sessions: [{
		sessionId: "ses_abc", turnId: "turn-1", toneSlot: 2, completed: false,
	}] }, 1000);
	const terminalEvent = {
		sessionId: "ses_abc",
		turnId: "turn-1",
		toneSlot: 2,
		completed: true,
		turnTerminal: { turnId: "turn-1", status: "completed", endedAt: "2026-08-12T10:00:00Z" },
	};
	const terminal = activitySessions(active, { sessions: [terminalEvent] }, 2000);
	assert.equal(terminal.get("ses_abc").expiresAt, 2000 + ACTIVITY_SESSION_RETENTION_MS);
	assert.equal(terminal.get("ses_abc").toneReleaseAt, 2000 + TERMINAL_TONE_HOLD_MS);
	assert.equal(activitySessionTerminal(terminal.get("ses_abc")).status, "completed");
	assert.equal(activitySessionHoldsTone(terminal.get("ses_abc"), 11999), true);
	assert.equal(activitySessionHoldsTone(terminal.get("ses_abc"), 12000), false);

	const sameTurnCleanup = activitySessions(terminal, { sessions: [{
		sessionId: "ses_abc", turnId: "turn-1", toneSlot: 2, completed: false,
	}] }, 5000);
	assert.equal(activitySessionTerminal(sameTurnCleanup.get("ses_abc")).status, "completed");
	assert.equal(sameTurnCleanup.get("ses_abc").expiresAt, 2000 + ACTIVITY_SESSION_RETENTION_MS);
	assert.equal(activitySessionNeedsTone(sameTurnCleanup.get("ses_abc"), { turnId: "turn-1" }), false);
	assert.equal(activitySessions(sameTurnCleanup, { sessions: [] }, 301999).size, 1);
	assert.equal(activitySessions(sameTurnCleanup, { sessions: [] }, 302000).size, 0);

	const newTurn = { sessionId: "ses_abc", turnId: "turn-2", toneSlot: 4, completed: false };
	assert.equal(activitySessionNeedsTone(sameTurnCleanup.get("ses_abc"), newTurn), true);
	const resumed = activitySessions(sameTurnCleanup, { sessions: [newTurn] }, 6000);
	assert.equal(activitySessionTerminal(resumed.get("ses_abc")), null);
	assert.equal(resumed.get("ses_abc").toneReleaseAt, null);
	assert.equal(resumed.get("ses_abc").toneSlot, 4);
});

test("each active session creates one quantized waveform pulse per frame", () => {
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
	const concurrent = activityPulsesForFrame({ sequence: 1, sessions: [
		{ sessionId: "ses_c", eventCount: 7, completed: false },
		{ sessionId: "ses_a", eventCount: 1, completed: false },
		{ sessionId: "ses_b", eventCount: 4, completed: false },
	] }, now);
	assert.deepEqual(concurrent.map((pulse) => pulse.sessionId), ["ses_a", "ses_b", "ses_c"]);
	assert.deepEqual(concurrent.map((pulse) => pulse.at), [now, now + 250, now + 500]);
	const rotated = activityPulsesForFrame({ sequence: 2, sessions: [
		{ sessionId: "ses_a" }, { sessionId: "ses_b" }, { sessionId: "ses_c" },
	] }, now);
	assert.deepEqual(rotated.map((pulse) => [pulse.sessionId, pulse.at]), [
		["ses_b", now], ["ses_c", now + 250], ["ses_a", now + 750],
	]);
	assert.notEqual(waveformPoints(pulses, now), waveformPoints([], now));
	assert.notEqual(waveformPoints(pulses, now), waveformPoints(pulses, now + 1000));
	const peak = (points) => points.split(" ").map((point) => point.split(",").map(Number)).reduce((best, point) => point[1] < best[1] ? point : best);
	const enteringPeak = peak(waveformPoints([pulses[0]], now));
	const scrolledPeak = peak(waveformPoints([pulses[0]], now + 500));
	assert.ok(scrolledPeak[0] < enteringPeak[0]);
	assert.equal(scrolledPeak[1], enteringPeak[1]);
	assert.equal(pruneActivityPulses(pulses, now + 10000).length, 0);
});

test("activity playback uses deterministic quarter-second patterns and rotation", () => {
	const plan = (count, sequence) => activityPlaybackPlan(
		Array.from({ length: count }, (_, index) => String.fromCharCode(65 + index)),
		sequence,
	).map(({ item, slot, delay, gain }) => ({ item, slot, delay, gain }));
	assert.deepEqual(plan(0, 1), []);
	assert.deepEqual(plan(1, 1).map(({ item, slot, delay }) => [item, slot, delay]), [["A", 0, 0]]);
	assert.deepEqual(plan(2, 1).map(({ item, slot }) => [item, slot]), [["A", 0], ["B", 2]]);
	assert.deepEqual(plan(2, 2).map(({ item, slot }) => [item, slot]), [["B", 0], ["A", 2]]);
	assert.deepEqual(plan(3, 1).map(({ item, slot }) => [item, slot]), [["A", 0], ["B", 1], ["C", 2]]);
	assert.deepEqual(plan(3, 2).map(({ item, slot }) => [item, slot]), [["B", 0], ["C", 1], ["A", 3]]);
	assert.deepEqual(plan(3, 3).map(({ item, slot }) => [item, slot]), [["C", 0], ["A", 2], ["B", 3]]);
	assert.deepEqual(plan(4, 2).map(({ item, slot }) => [item, slot]), [["B", 0], ["C", 1], ["D", 2], ["A", 3]]);
	assert.deepEqual(plan(5, 1).map(({ item, slot }) => [item, slot]), [["A", 0], ["B", 1], ["C", 2], ["D", 3], ["E", 0]]);
	assert.deepEqual(plan(5, 2).map(({ item, slot }) => [item, slot]), [["A", 1], ["B", 2], ["C", 3], ["D", 0], ["E", 1]]);
	for (const count of [1, 2, 3, 4, 5, 12]) {
		for (const value of plan(count, 3)) {
			assert.ok([0, 0.25, 0.5, 0.75].includes(value.delay));
			assert.ok(value.gain > 0 && value.gain <= 1);
		}
	}
	assert.equal(plan(1, 1)[0].gain, 1);
	assert.equal(plan(4, 1)[1].gain, 0.85);
	assert.equal(plan(4, 1)[2].gain, 0.92);
	assert.equal(plan(5, 1)[0].gain, 1 / Math.sqrt(2));
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
	assert.equal(player.pulse(0), false);
	assert.equal(await player.resume(), true);
	assert.equal(player.pulse(0, "c-major", 0.2, 0.08), true);
	assert.ok(scheduled.some(([kind, time]) => kind === "start" && time === 10.08));
	assert.ok(scheduled.some(([kind, frequency]) => kind === "frequency" && Math.abs(frequency - 523.2511306) < 0.0001));
	assert.deepEqual(player.previewChord("c-major", 0.2), [true, true, true]);
	assert.ok(scheduled.some(([kind, time]) => kind === "start" && time === 10.24));
	assert.equal(player.previewProgression("canon-in-c", "c-major", 0.2).length, 24);
	assert.equal(scheduled.some(([kind]) => kind === "ramp"), false);
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
	assert.ok(source.includes("activityPulsesForFrame(assignedFrame, receivedAt)"));
	assert.ok(source.includes("activityPlaybackPlan(sessions, frame.sequence)"));
	assert.ok(source.includes("nextProgressionFrame("));
	assert.ok(source.includes("tonePlayer.current.pulse(session.toneSlot, frameChord"));
	assert.ok(!source.includes("chordForProgressionToneSlot"));
	assert.ok(source.includes("toneAllocator.current.assign(session.sessionId)"));
	assert.ok(source.includes("session.toneReleaseAt - now"));
	assert.ok(source.includes("activityTerminalTone(session)"));
	assert.ok(source.includes('className="companion-thread-title"'));
	assert.ok(!source.includes("companion-thread-meta"));
	assert.ok(styles.includes("companion-thread-flash 10s"));
	assert.ok(styles.includes(".companion-thread-row.terminal-completed"));
	assert.ok(styles.includes(".companion-thread-row.terminal-error"));
	assert.ok(source.includes('className="companion-resize-handle"'));
	assert.ok(source.includes("agenthub.companion.size.v1"));
	assert.ok(source.includes("saveCompanionPreferences"));
	assert.ok(!source.includes('api("/v1/config", { method: "PUT"'));
	assert.ok(styles.includes("@container companion-card (min-width: 560px)"));
	assert.ok(styles.includes("@container companion-card (max-height: 390px)"));
	assert.ok(!model.includes("Math.random"));
	for (const forbidden of [".codex/sessions", "fsnotify", "/v1/sessions/${"]) {
		assert.ok(!source.includes(forbidden), `companion must not contain ${forbidden}`);
	}
});
