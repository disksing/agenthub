import assert from "node:assert/strict";
import test from "node:test";
import {
  agentReferences,
  buildPayload,
  createDraft,
  isDirty,
  normalizeAgentOptions,
  normalizeConfig,
  providerReferences,
  renameAgentId,
  uniqueId,
  validateDraft,
} from "../src/settings/configModel.js";

function sampleConfig() {
  return {
    version: 1,
    defaultChatAgentId: "main",
    agentProviders: [
      { id: "codex", name: "Codex", type: "codex", enabled: true },
      { id: "kimi", name: "Kimi", type: "kimi", enabled: false, command: " /usr/local/bin/kimi " },
    ],
    agents: [
      { id: "main", name: "Main", providerId: "codex", options: { model: " gpt-5 ", sandbox: "workspace-write", empty: "  " } },
      { id: "backup", name: "", providerId: "kimi" },
    ],
    agentProfiles: [
      { key: "fast", agentId: "main", description: " Quick lane " },
      { key: "slow", agentId: "backup" },
    ],
  };
}

test("normalizeConfig normalizes the structure and drops blank fields", () => {
  const normalized = normalizeConfig(sampleConfig());
  assert.deepEqual(normalized, {
    version: 1,
    defaultChatAgentId: "main",
    agentProviders: [
      { id: "codex", name: "Codex", type: "codex", enabled: true },
      { id: "kimi", name: "Kimi", type: "kimi", enabled: false, command: "/usr/local/bin/kimi" },
    ],
    agents: [
      { id: "main", name: "Main", providerId: "codex", options: { model: "gpt-5", sandbox: "workspace-write" } },
      { id: "backup", name: "", providerId: "kimi" },
    ],
    agentProfiles: [
      { key: "fast", agentId: "main", description: "Quick lane" },
      { key: "slow", agentId: "backup" },
    ],
  });
});

test("normalizeConfig tolerates empty config and missing arrays", () => {
  assert.deepEqual(normalizeConfig(undefined), {
    version: 1,
    agentProviders: [],
    agents: [],
    agentProfiles: [],
  });
  assert.deepEqual(normalizeConfig({ agentProviders: "x", agents: 1, agentProfiles: null }), normalizeConfig({}));
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
    agents: [{ id: "main", name: "Main", providerId: "codex", options: { model: "gpt-5 ", sandbox: "workspace-write" } },
      { id: "backup", name: "", providerId: "kimi" }],
  });
  assert.equal(isDirty(same, snapshot), false);
  const changed = createDraft(sampleConfig());
  changed.agents[0].options.model = "gpt-5-mini";
  assert.equal(isDirty(changed, snapshot), true);
  const removed = createDraft(sampleConfig());
  removed.agentProfiles.pop();
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

test("agentReferences/providerReferences reflect direct and indirect references", () => {
  const draft = createDraft(sampleConfig());
  assert.deepEqual(agentReferences(draft, "main"), { isDefault: true, profiles: ["fast"] });
  assert.deepEqual(agentReferences(draft, "backup"), { isDefault: false, profiles: ["slow"] });
  assert.deepEqual(providerReferences(draft, "codex"), { agents: ["main"], isDefault: true, profiles: ["fast"] });
  assert.deepEqual(providerReferences(draft, "kimi"), { agents: ["backup"], isDefault: false, profiles: ["slow"] });
  assert.deepEqual(providerReferences(draft, "missing"), { agents: [], isDefault: false, profiles: [] });
});

test("renameAgentId syncs defaultChatAgentId and profile references", () => {
  const draft = createDraft(sampleConfig());
  const renamed = renameAgentId(draft, "main", "primary");
  assert.equal(renamed.agents[0].id, "primary");
  assert.equal(renamed.defaultChatAgentId, "primary");
  assert.deepEqual(renamed.agentProfiles.map((item) => item.agentId), ["primary", "backup"]);
  // The original draft is not mutated.
  assert.equal(draft.agents[0].id, "main");
  assert.equal(draft.defaultChatAgentId, "main");
});

test("validateDraft returns no errors for a valid config", () => {
  const valid = createDraft({
    version: 1,
    defaultChatAgentId: "main",
    agentProviders: [{ id: "codex", name: "Codex", type: "codex", enabled: true }],
    agents: [{ id: "main", name: "Main", providerId: "codex" }],
    agentProfiles: [{ key: "fast", agentId: "main" }],
  });
  assert.deepEqual(validateDraft(valid), []);
  assert.deepEqual(validateDraft(createDraft({})), []);
});

test("validateDraft reports duplicate ids/keys and missing required fields", () => {
  const draft = createDraft({
    agentProviders: [
      { id: "p", name: "", type: "codex", enabled: true },
      { id: "p", name: "", type: "", enabled: true },
    ],
    agents: [
      { id: "a", name: "", providerId: "p" },
      { id: "a", name: "", providerId: "" },
      { id: "", name: "", providerId: "p" },
    ],
    agentProfiles: [
      { key: "Fast", agentId: "a" },
      { key: " fast ", agentId: "a" },
      { key: "", agentId: "" },
    ],
  });
  const errors = validateDraft(draft);
  const has = (section, index, field, part) => errors.some((item) => (
    item.section === section && item.index === index && item.field === field && item.message.includes(part)
  ));
  assert.ok(has("providers", 1, "id", "already used"));
  assert.ok(has("providers", 1, "type", "required"));
  assert.ok(has("agents", 1, "id", "already used"));
  assert.ok(has("agents", 1, "providerId", "Select a provider"));
  assert.ok(has("agents", 2, "id", "required"));
  assert.ok(has("profiles", 1, "key", "already used"));
  assert.ok(has("profiles", 2, "key", "required"));
  assert.ok(has("profiles", 2, "agentId", "Select an agent"));
});

test("validateDraft reports dangling references and references broken by a disabled provider", () => {
  const dangling = createDraft({
    agentProviders: [{ id: "p", name: "P", type: "codex", enabled: true }],
    agents: [{ id: "a", name: "", providerId: "ghost" }],
    defaultChatAgentId: "nobody",
    agentProfiles: [{ key: "x", agentId: "nobody" }],
  });
  const errors = validateDraft(dangling);
  assert.ok(errors.some((item) => item.section === "agents" && item.field === "providerId" && item.message.includes("does not exist")));
  assert.ok(errors.some((item) => item.section === "general" && item.message.includes("does not exist")));
  assert.ok(errors.some((item) => item.section === "profiles" && item.field === "agentId" && item.message.includes("does not exist")));

  const disabled = createDraft({
    agentProviders: [{ id: "p", name: "P", type: "codex", enabled: false }],
    agents: [{ id: "a", name: "", providerId: "p" }],
    defaultChatAgentId: "a",
    agentProfiles: [{ key: "x", agentId: "a" }],
  });
  const disabledErrors = validateDraft(disabled);
  assert.ok(disabledErrors.some((item) => item.section === "general" && item.message.includes("disabled")));
  assert.ok(disabledErrors.some((item) => item.section === "profiles" && item.message.includes("disabled")));

  const unsupported = createDraft({
    agentProviders: [{ id: "p", name: "", type: "unknown", enabled: true }],
  });
  assert.ok(validateDraft(unsupported).some((item) => item.section === "providers" && item.field === "type" && item.message.includes("Unsupported")));
});

test("uniqueId appends a sequence number on conflict", () => {
  assert.equal(uniqueId("agent", []), "agent");
  assert.equal(uniqueId("agent", ["other"]), "agent");
  assert.equal(uniqueId("agent", ["agent"]), "agent-2");
  assert.equal(uniqueId("agent", ["agent", "agent-2", "agent-3"]), "agent-4");
});
