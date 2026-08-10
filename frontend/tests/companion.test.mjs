import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";
import { TonePlayer } from "../src/companion/audio.js";
import { activitySessions, formatDuration, noteForSession, quotaCycleItems, waveformPoints } from "../src/companion/model.js";

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

test("quota labels and waveform helpers are deterministic", () => {
	assert.equal(formatDuration(5 * 86400 + 2 * 3600), "5d 2h");
	assert.equal(formatDuration(3 * 3600 + 12 * 60), "3h 12m");
	assert.equal(waveformPoints(4), waveformPoints(4));
	assert.notEqual(waveformPoints(4), waveformPoints(5));
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

test("companion uses one global EventSource and never scans provider sessions", async () => {
	const source = await readFile(path.join(frontendRoot, "src", "companion", "Companion.jsx"), "utf8");
	assert.equal((source.match(/new EventSource/g) || []).length, 1);
	assert.ok(source.includes('new EventSource("/v1/activity/events")'));
	for (const forbidden of [".codex/sessions", "fsnotify", "/v1/sessions/${"]) {
		assert.ok(!source.includes(forbidden), `companion must not contain ${forbidden}`);
	}
});
