import assert from "node:assert/strict";
import test from "node:test";
import { buildModelChoices, modelListView, PROVIDER_DEFAULT_VALUE } from "../src/settings/modelOptions.js";

test("buildModelChoices always starts with the provider default option", () => {
  assert.deepEqual(buildModelChoices([], ""), [
    { value: PROVIDER_DEFAULT_VALUE, label: "Provider default", unavailable: false },
  ]);
  assert.deepEqual(buildModelChoices(null, ""), [
    { value: PROVIDER_DEFAULT_VALUE, label: "Provider default", unavailable: false },
  ]);
});

test("buildModelChoices maps ids and labels and marks the provider default model", () => {
  const choices = buildModelChoices(
    [
      { id: "gpt-5.6-sol", label: "GPT-5.6-Sol", default: true },
      { id: "gpt-5.5", label: "GPT-5.5" },
    ],
    "",
  );
  assert.deepEqual(choices[1], { value: "gpt-5.6-sol", label: "GPT-5.6-Sol (default)", unavailable: false });
  assert.deepEqual(choices[2], { value: "gpt-5.5", label: "GPT-5.5", unavailable: false });
});

test("buildModelChoices falls back to the id as label and dedupes", () => {
  const choices = buildModelChoices(
    [
      { id: "kimi-code/k3" },
      { id: "kimi-code/k3", label: "duplicate" },
      { id: "  " },
      {},
    ],
    "",
  );
  assert.equal(choices.length, 2);
  assert.deepEqual(choices[1], { value: "kimi-code/k3", label: "kimi-code/k3", unavailable: false });
});

test("buildModelChoices prefixes the upstream provider from the model id", () => {
  const choices = buildModelChoices(
    [
      { id: "xai/grok-9", label: "Grok 9" },
      { id: "openrouter/grok-9", label: "Grok 9", default: true },
      { id: "gpt-5.5", label: "GPT-5.5" },
    ],
    "",
  );
  // Same model name from two upstreams stays distinguishable.
  assert.deepEqual(choices[1], { value: "xai/grok-9", label: "xai / Grok 9", unavailable: false });
  assert.deepEqual(choices[2], { value: "openrouter/grok-9", label: "openrouter / Grok 9 (default)", unavailable: false });
  // IDs without an upstream prefix (codex) keep the bare label.
  assert.deepEqual(choices[3], { value: "gpt-5.5", label: "GPT-5.5", unavailable: false });
});

test("buildModelChoices does not repeat a provider the label already mentions", () => {
  const choices = buildModelChoices(
    [
      // Fallback labels are the full id, which already carries the provider.
      { id: "kimi-code/k3-256k" },
      // Some upstream display names embed the provider themselves.
      { id: "kimi-for-coding/k3", label: "Kimi For Coding/Kimi K3" },
    ],
    "",
  );
  assert.deepEqual(choices[1], { value: "kimi-code/k3-256k", label: "kimi-code/k3-256k", unavailable: false });
  assert.deepEqual(choices[2], { value: "kimi-for-coding/k3", label: "Kimi For Coding/Kimi K3", unavailable: false });
});

test("buildModelChoices preserves a saved value missing from the list", () => {
  const choices = buildModelChoices([{ id: "m1", label: "M1" }], "legacy-model");
  assert.equal(choices.length, 3);
  assert.deepEqual(choices[2], {
    value: "legacy-model",
    label: "legacy-model (saved, not currently listed)",
    unavailable: true,
  });
});

test("buildModelChoices does not duplicate a saved value that is listed", () => {
  const choices = buildModelChoices([{ id: "m1", label: "M1" }], "m1");
  assert.equal(choices.length, 2);
});

test("modelListView ready state exposes choices and is interactive", () => {
  const view = modelListView({ status: "ready", models: [{ id: "m1", label: "M1" }] }, "m1");
  assert.equal(view.disabled, false);
  assert.equal(view.choices.length, 2);
  assert.equal(view.message, "");
});

test("modelListView ready state with an empty list explains the default", () => {
  const view = modelListView({ status: "ready", models: [] }, "");
  assert.equal(view.disabled, false);
  assert.match(view.message, /did not report any models/);
});

test("modelListView loading keeps the saved value and disables the select", () => {
  const view = modelListView({ status: "loading" }, "m1");
  assert.equal(view.disabled, true);
  assert.deepEqual(view.choices, [{ value: "m1", label: "Loading models…", unavailable: false }]);
});

test("modelListView error offers a retry and keeps the saved value", () => {
  const view = modelListView({ status: "error", error: "boom" }, "m1");
  assert.equal(view.disabled, true);
  assert.equal(view.retry, true);
  assert.equal(view.tone, "error");
  assert.equal(view.choices[0].value, "m1");
});

test("modelListView disabled provider keeps the saved value", () => {
  const view = modelListView({ status: "disabled" }, "kimi-code/k3");
  assert.equal(view.disabled, true);
  assert.equal(view.choices[0].value, "kimi-code/k3");
  assert.match(view.message, /disabled/);
});

test("modelListView without a provider asks for a selection first", () => {
  const view = modelListView({ status: "none" }, "");
  assert.equal(view.disabled, true);
  assert.match(view.message, /Select a provider/);
});
