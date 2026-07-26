import assert from "node:assert/strict";
import test from "node:test";
import {
  buildPayload,
  createDraft,
  isDirty,
  normalizeAgentName,
  normalizeAgentOptions,
  normalizeConfig,
  uniqueAgentName,
  validateDraft,
} from "../src/settings/configModel.js";

function sampleConfig() {
  return {
    version: 1,
    agentProviders: [
      { id: "codex", name: "Codex", type: "codex", enabled: true },
      { id: "kimi", name: "Kimi", type: "kimi", enabled: false, command: " /usr/local/bin/kimi " },
    ],
    agents: [
      { name: "Main", providerId: "codex", options: { model: " gpt-5 ", sandbox: "workspace-write", empty: "  " } },
      { name: "Backup", providerId: "kimi" },
    ],
  };
}

test("normalizeConfig normalizes the structure and drops blank fields", () => {
  const normalized = normalizeConfig(sampleConfig());
  assert.deepEqual(normalized, {
    version: 1,
    agentProviders: [
      { id: "codex", name: "Codex", type: "codex", enabled: true },
      { id: "kimi", name: "Kimi", type: "kimi", enabled: false, command: "/usr/local/bin/kimi" },
    ],
    agents: [
      { name: "Main", providerId: "codex", options: { model: "gpt-5", sandbox: "workspace-write" } },
      { name: "Backup", providerId: "kimi" },
    ],
  });
});

test("normalizeConfig drops removed agent ids", () => {
  const normalized = normalizeConfig({
    agentProviders: [{ id: "codex", name: "Codex", type: "codex", enabled: true }],
    agents: [{ id: "main", name: "Main", providerId: "codex" }],
  });
  assert.equal("id" in normalized.agents[0], false);
  assert.equal(normalized.agents[0].name, "Main");
});

test("normalizeConfig tolerates empty config and missing arrays", () => {
  assert.deepEqual(normalizeConfig(undefined), {
    version: 1,
    agentProviders: [],
    agents: [],
  });
  assert.deepEqual(normalizeConfig({ agentProviders: "x", agents: 1 }), normalizeConfig({}));
});

test("normalizeConfig drops removed legacy fields", () => {
  const legacy = {
    ...sampleConfig(),
    defaultChatAgentId: "main",
    agentProfiles: [{ key: "fast", agentId: "main", description: "Quick lane" }],
  };
  const normalized = normalizeConfig(legacy);
  assert.equal("defaultChatAgentId" in normalized, false);
  assert.equal("agentProfiles" in normalized, false);
});

test("createDraft/buildPayload round-trip the source config losslessly", () => {
  const source = normalizeConfig(sampleConfig());
  const draft = createDraft(source);
  assert.deepEqual(draft, source);
  // The draft is a deep copy; mutating it does not affect the source.
  draft.agents[0].name = "Changed";
  assert.equal(source.agents[0].name, "Main");
  const payload = buildPayload(createDraft(source));
  assert.deepEqual(payload, source);
  assert.deepEqual(normalizeConfig(payload), source);
});

test("isDirty ignores equivalent differences and detects real changes", () => {
  const snapshot = createDraft(sampleConfig());
  const same = createDraft({
    ...sampleConfig(),
    agents: [{ name: "Main", providerId: "codex", options: { model: "gpt-5 ", sandbox: "workspace-write" } },
      { name: "Backup", providerId: "kimi" }],
  });
  assert.equal(isDirty(same, snapshot), false);
  const changed = createDraft(sampleConfig());
  changed.agents[0].options.model = "gpt-5-mini";
  assert.equal(isDirty(changed, snapshot), true);
  const removed = createDraft(sampleConfig());
  removed.agents.pop();
  assert.equal(isDirty(removed, snapshot), true);
});

test("normalizeAgentOptions drops inapplicable fields on provider switch", () => {
  // codex → kimi: sandbox/approval are dropped, model is kept, and mode falls
  // back to the default (omitted when empty).
  assert.deepEqual(
    normalizeAgentOptions("kimi", { model: "gpt-5", sandbox: "read-only", approval: "never" }),
    { model: "gpt-5" },
  );
  // kimi → codex: mode is dropped and missing enums fall back to defaults.
  assert.deepEqual(
    normalizeAgentOptions("codex", { model: "k2", mode: "plan" }),
    { model: "k2", sandbox: "workspace-write", approval: "on-request" },
  );
  // Invalid enum values fall back to the default; valid values are kept.
  assert.deepEqual(
    normalizeAgentOptions("codex", { sandbox: "bogus", approval: "never" }),
    { sandbox: "workspace-write", approval: "never" },
  );
  // Empty old options still produce the full defaults.
  assert.deepEqual(normalizeAgentOptions("codex", {}), { sandbox: "workspace-write", approval: "on-request" });
  // The kimi mode default is an empty string and therefore not written.
  assert.deepEqual(normalizeAgentOptions("kimi", {}), {});
});

test("validateDraft returns no errors for a valid config", () => {
  const valid = createDraft({
    version: 1,
    agentProviders: [{ id: "codex", name: "Codex", type: "codex", enabled: true }],
    agents: [{ name: "Main", providerId: "codex" }],
  });
  assert.deepEqual(validateDraft(valid), []);
  assert.deepEqual(validateDraft(createDraft({})), []);
});

test("validateDraft reports duplicate provider ids and missing required fields", () => {
  const draft = createDraft({
    agentProviders: [
      { id: "p", name: "", type: "codex", enabled: true },
      { id: "p", name: "", type: "", enabled: true },
    ],
    agents: [
      { name: "", providerId: "p" },
      { name: "", providerId: "" },
    ],
  });
  const errors = validateDraft(draft);
  const has = (section, index, field, part) => errors.some((item) => (
    item.section === section && item.index === index && item.field === field && item.message.includes(part)
  ));
  assert.ok(has("providers", 1, "id", "already used"));
  assert.ok(has("providers", 1, "type", "required"));
  assert.ok(has("agents", 0, "name", "required"));
  assert.ok(has("agents", 1, "name", "required"));
  assert.ok(has("agents", 1, "providerId", "Select a provider"));
});

test("validateDraft rejects duplicate agent names case-insensitively", () => {
  const draft = createDraft({
    agentProviders: [{ id: "p", name: "P", type: "codex", enabled: true }],
    agents: [
      { name: "Codex", providerId: "p" },
      { name: " codex ", providerId: "p" },
    ],
  });
  const errors = validateDraft(draft);
  const duplicate = errors.find((item) => item.section === "agents" && item.field === "name");
  assert.ok(duplicate);
  assert.equal(duplicate.index, 1);
  assert.match(duplicate.message, /already used/);
  assert.match(duplicate.message, /codex/);
});

test("validateDraft enforces the agent name length limit", () => {
  const draft = createDraft({
    agentProviders: [{ id: "p", name: "P", type: "codex", enabled: true }],
    agents: [{ name: "x".repeat(81), providerId: "p" }],
  });
  assert.ok(validateDraft(draft).some((item) => item.field === "name" && item.message.includes("80 characters")));
  const ok = createDraft({
    agentProviders: [{ id: "p", name: "P", type: "codex", enabled: true }],
    agents: [{ name: "x".repeat(80), providerId: "p" }],
  });
  assert.deepEqual(validateDraft(ok), []);
});

test("validateDraft reports dangling provider references and unsupported types", () => {
  const dangling = createDraft({
    agentProviders: [{ id: "p", name: "P", type: "codex", enabled: true }],
    agents: [{ name: "A", providerId: "ghost" }],
  });
  const errors = validateDraft(dangling);
  assert.ok(errors.some((item) => item.section === "agents" && item.field === "providerId" && item.message.includes("does not exist")));

  const unsupported = createDraft({
    agentProviders: [{ id: "p", name: "", type: "unknown", enabled: true }],
  });
  assert.ok(validateDraft(unsupported).some((item) => item.section === "providers" && item.field === "type" && item.message.includes("Unsupported")));
});

test("uniqueAgentName appends a sequence number on conflict", () => {
  assert.equal(uniqueAgentName("Agent", []), "Agent");
  assert.equal(uniqueAgentName("Agent", ["other"]), "Agent");
  assert.equal(uniqueAgentName("Agent", ["Agent"]), "Agent 2");
  // Conflicts compare case-insensitively and ignore surrounding whitespace.
  assert.equal(uniqueAgentName("Agent", [" agent "]), "Agent 2");
  assert.equal(uniqueAgentName("Agent", ["Agent", "AGENT 2", "Agent 3"]), "Agent 4");
});

test("normalizeAgentName trims and lower-cases", () => {
  assert.equal(normalizeAgentName("  Kimi K3 "), "kimi k3");
  assert.equal(normalizeAgentName(""), "");
  assert.equal(normalizeAgentName(undefined), "");
});
