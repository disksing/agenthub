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
      { key: "fast", agentId: "main", description: " 快速 " },
      { key: "slow", agentId: "backup" },
    ],
  };
}

test("normalizeConfig 规范结构并剔除空白字段", () => {
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
      { key: "fast", agentId: "main", description: "快速" },
      { key: "slow", agentId: "backup" },
    ],
  });
});

test("normalizeConfig 容忍空配置与缺省数组", () => {
  assert.deepEqual(normalizeConfig(undefined), {
    version: 1,
    agentProviders: [],
    agents: [],
    agentProfiles: [],
  });
  assert.deepEqual(normalizeConfig({ agentProviders: "x", agents: 1, agentProfiles: null }), normalizeConfig({}));
});

test("createDraft/buildPayload 与源配置无损往返", () => {
  const source = normalizeConfig(sampleConfig());
  const draft = createDraft(source);
  assert.deepEqual(draft, source);
  // draft 是深拷贝，修改不影响源。
  draft.agents[0].name = "改";
  assert.equal(source.agents[0].name, "Main");
  const payload = buildPayload(createDraft(source));
  assert.deepEqual(payload, source);
  assert.deepEqual(normalizeConfig(payload), source);
});

test("isDirty 对等价差异不敏感、对真实修改敏感", () => {
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

test("normalizeAgentOptions 切换 provider 时清理不适用字段", () => {
  // codex → kimi：sandbox/approval 被丢弃，model 保留，mode 回退默认（空则省略）。
  assert.deepEqual(
    normalizeAgentOptions("kimi", { model: "gpt-5", sandbox: "read-only", approval: "never" }),
    { model: "gpt-5" },
  );
  // kimi → codex：mode 被丢弃，枚举缺省回退 fallback。
  assert.deepEqual(
    normalizeAgentOptions("codex", { model: "k2", mode: "plan" }),
    { model: "k2", sandbox: "workspace-write", approval: "on-request" },
  );
  // 非法枚举值回退默认；合法值保留。
  assert.deepEqual(
    normalizeAgentOptions("codex", { sandbox: "bogus", approval: "never" }),
    { sandbox: "workspace-write", approval: "never" },
  );
  // 空旧 options 也得到完整默认值。
  assert.deepEqual(normalizeAgentOptions("codex", {}), { sandbox: "workspace-write", approval: "on-request" });
  // kimi 的 mode 默认值为空串则不写入。
  assert.deepEqual(normalizeAgentOptions("kimi", {}), {});
});

test("agentReferences/providerReferences 反映直接与间接引用", () => {
  const draft = createDraft(sampleConfig());
  assert.deepEqual(agentReferences(draft, "main"), { isDefault: true, profiles: ["fast"] });
  assert.deepEqual(agentReferences(draft, "backup"), { isDefault: false, profiles: ["slow"] });
  assert.deepEqual(providerReferences(draft, "codex"), { agents: ["main"], isDefault: true, profiles: ["fast"] });
  assert.deepEqual(providerReferences(draft, "kimi"), { agents: ["backup"], isDefault: false, profiles: ["slow"] });
  assert.deepEqual(providerReferences(draft, "missing"), { agents: [], isDefault: false, profiles: [] });
});

test("renameAgentId 同步 defaultChatAgentId 与 Profile 引用", () => {
  const draft = createDraft(sampleConfig());
  const renamed = renameAgentId(draft, "main", "primary");
  assert.equal(renamed.agents[0].id, "primary");
  assert.equal(renamed.defaultChatAgentId, "primary");
  assert.deepEqual(renamed.agentProfiles.map((item) => item.agentId), ["primary", "backup"]);
  // 原 draft 不被修改。
  assert.equal(draft.agents[0].id, "main");
  assert.equal(draft.defaultChatAgentId, "main");
});

test("validateDraft 对合法配置返回空", () => {
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

test("validateDraft 报告重复 id/key 与空必填", () => {
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
  assert.ok(has("providers", 1, "id", "重复"));
  assert.ok(has("providers", 1, "type", "必选"));
  assert.ok(has("agents", 1, "id", "重复"));
  assert.ok(has("agents", 1, "providerId", "必须选择提供方"));
  assert.ok(has("agents", 2, "id", "必填"));
  assert.ok(has("profiles", 1, "key", "重复"));
  assert.ok(has("profiles", 2, "key", "必填"));
  assert.ok(has("profiles", 2, "agentId", "必须选择 Agent"));
});

test("validateDraft 报告悬空引用与禁用提供方导致的不可用引用", () => {
  const dangling = createDraft({
    agentProviders: [{ id: "p", name: "P", type: "codex", enabled: true }],
    agents: [{ id: "a", name: "", providerId: "ghost" }],
    defaultChatAgentId: "nobody",
    agentProfiles: [{ key: "x", agentId: "nobody" }],
  });
  const errors = validateDraft(dangling);
  assert.ok(errors.some((item) => item.section === "agents" && item.field === "providerId" && item.message.includes("不存在")));
  assert.ok(errors.some((item) => item.section === "general" && item.message.includes("不存在")));
  assert.ok(errors.some((item) => item.section === "profiles" && item.field === "agentId" && item.message.includes("不存在")));

  const disabled = createDraft({
    agentProviders: [{ id: "p", name: "P", type: "codex", enabled: false }],
    agents: [{ id: "a", name: "", providerId: "p" }],
    defaultChatAgentId: "a",
    agentProfiles: [{ key: "x", agentId: "a" }],
  });
  const disabledErrors = validateDraft(disabled);
  assert.ok(disabledErrors.some((item) => item.section === "general" && item.message.includes("已被禁用")));
  assert.ok(disabledErrors.some((item) => item.section === "profiles" && item.message.includes("已被禁用")));

  const unsupported = createDraft({
    agentProviders: [{ id: "p", name: "", type: "unknown", enabled: true }],
  });
  assert.ok(validateDraft(unsupported).some((item) => item.section === "providers" && item.field === "type" && item.message.includes("不支持")));
});

test("uniqueId 冲突时追加序号", () => {
  assert.equal(uniqueId("agent", []), "agent");
  assert.equal(uniqueId("agent", ["other"]), "agent");
  assert.equal(uniqueId("agent", ["agent"]), "agent-2");
  assert.equal(uniqueId("agent", ["agent", "agent-2", "agent-3"]), "agent-4");
});
