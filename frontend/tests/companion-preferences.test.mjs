import assert from "node:assert/strict";
import test from "node:test";
import {
  COMPANION_PREFERENCES_STORAGE_KEY,
  DEFAULT_COMPANION_PREFERENCES,
  companionPreferencesEqual,
  loadCompanionPreferences,
  normalizeCompanionPreferences,
  saveCompanionPreferences,
  validateCompanionPreferences,
} from "../src/companion/preferences.js";

function memoryStorage(initial = {}) {
  const values = new Map(Object.entries(initial));
  return {
    getItem(key) { return values.get(key) ?? null; },
    setItem(key, value) { values.set(key, value); },
  };
}

test("companion preferences normalize invalid browser-local values", () => {
  assert.deepEqual(normalizeCompanionPreferences(), DEFAULT_COMPANION_PREFERENCES);
  assert.deepEqual(normalizeCompanionPreferences({
    showActivity: false,
    enableBeeping: false,
    beepVolume: 0,
    beepProgression: "canon-in-c",
    completionSound: "smile",
    hiddenQuotaKeys: ["b", "a", "b", ""],
  }), {
    showActivity: false,
    enableBeeping: false,
    beepVolume: 0,
    beepProgression: "canon-in-c",
    completionSound: "smile",
    hiddenQuotaKeys: ["a", "b"],
  });
  assert.deepEqual(normalizeCompanionPreferences({
    beepVolume: 2,
    beepProgression: "noise",
    completionSound: "noise",
  }), DEFAULT_COMPANION_PREFERENCES);
  // Legacy single-chord preferences normalize to the default progression.
  assert.deepEqual(normalizeCompanionPreferences({ beepChord: "a-minor", beepProgression: "single" }), DEFAULT_COMPANION_PREFERENCES);
});

test("companion preferences persist only through the supplied browser storage", () => {
  const storage = memoryStorage();
  const saved = saveCompanionPreferences({
    ...DEFAULT_COMPANION_PREFERENCES,
    enableBeeping: false,
    beepVolume: 0.51,
    hiddenQuotaKeys: ['["kimi","5h"]'],
  }, storage);
  assert.deepEqual(loadCompanionPreferences(storage), saved);
  assert.equal(JSON.parse(storage.getItem(COMPANION_PREFERENCES_STORAGE_KEY)).beepProgression, "canon-in-c");
  assert.deepEqual(loadCompanionPreferences(storage).hiddenQuotaKeys, ['["kimi","5h"]']);
  assert.equal(companionPreferencesEqual(saved, loadCompanionPreferences(storage)), true);

  const broken = memoryStorage({ [COMPANION_PREFERENCES_STORAGE_KEY]: "{" });
  assert.deepEqual(loadCompanionPreferences(broken), DEFAULT_COMPANION_PREFERENCES);
});

test("companion preference validation reports unsupported draft values", () => {
  assert.deepEqual(validateCompanionPreferences(DEFAULT_COMPANION_PREFERENCES), []);
  const errors = validateCompanionPreferences({
    beepVolume: 2,
    beepProgression: "noise",
    completionSound: "noise",
  });
  assert.deepEqual(errors.map((item) => item.field), [
    "beepVolume",
    "beepProgression",
    "completionSound",
  ]);
  assert.ok(errors.every((item) => item.section === "activity"));
});
