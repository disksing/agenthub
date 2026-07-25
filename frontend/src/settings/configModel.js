// 纯数据 helper：不依赖 React，供设置界面与测试共用。
// 配置模型与 internal/config/config.go 保持一致。

export const PROVIDER_TYPES = [
  { value: "codex", label: "Codex" },
  { value: "opencode", label: "OpenCode" },
  { value: "kimi", label: "Kimi Code" },
  { value: "pi", label: "Pi Coding Agent" },
];

export const SANDBOX_OPTIONS = [
  { value: "read-only", label: "只读" },
  { value: "workspace-write", label: "工作区写入" },
  { value: "danger-full-access", label: "完全访问" },
];

export const APPROVAL_OPTIONS = [
  { value: "untrusted", label: "不可信时询问" },
  { value: "on-failure", label: "失败时询问" },
  { value: "on-request", label: "每次询问" },
  { value: "never", label: "从不询问" },
];

export const MODE_OPTIONS = [
  { value: "", label: "默认" },
  { value: "build", label: "构建" },
  { value: "plan", label: "计划" },
];

const MODEL_FIELD = { key: "model", kind: "text", label: "模型", placeholder: "留空使用 Provider 默认模型" };

// providerOptionSchema 返回某 provider 类型支持的 Agent options 表单描述。
export function providerOptionSchema(type) {
  switch (type) {
    case "codex":
      return [
        MODEL_FIELD,
        { key: "sandbox", kind: "enum", label: "沙箱", options: SANDBOX_OPTIONS, fallback: "workspace-write" },
        { key: "approval", kind: "enum", label: "审批策略", options: APPROVAL_OPTIONS, fallback: "on-request" },
      ];
    case "kimi":
    case "opencode":
      return [
        MODEL_FIELD,
        { key: "mode", kind: "enum", label: "模式", options: MODE_OPTIONS, fallback: "" },
      ];
    case "pi":
      return [MODEL_FIELD];
    default:
      return [MODEL_FIELD];
  }
}

function cleanOptions(options) {
  const result = {};
  for (const [key, value] of Object.entries(options || {})) {
    const trimmed = String(value ?? "").trim();
    if (trimmed) result[key] = trimmed;
  }
  return result;
}

// normalizeConfig 把任意来源的配置深拷贝并规范化为固定结构：
// 数组缺省补空、字符串转字符串、剔除空白 options 键与空的可选字段。
export function normalizeConfig(config = {}) {
  const providers = (Array.isArray(config.agentProviders) ? config.agentProviders : []).map((provider) => {
    const result = {
      id: String(provider?.id ?? ""),
      name: String(provider?.name ?? ""),
      type: String(provider?.type ?? ""),
      enabled: Boolean(provider?.enabled),
    };
    const command = String(provider?.command ?? "").trim();
    if (command) result.command = command;
    return result;
  });
  const agents = (Array.isArray(config.agents) ? config.agents : []).map((agent) => {
    const result = {
      id: String(agent?.id ?? ""),
      name: String(agent?.name ?? ""),
      providerId: String(agent?.providerId ?? ""),
    };
    const options = cleanOptions(agent?.options);
    if (Object.keys(options).length) result.options = options;
    return result;
  });
  const profiles = (Array.isArray(config.agentProfiles) ? config.agentProfiles : []).map((profile) => {
    const result = {
      key: String(profile?.key ?? ""),
      agentId: String(profile?.agentId ?? ""),
    };
    const description = String(profile?.description ?? "").trim();
    if (description) result.description = description;
    return result;
  });
  const result = {
    version: Number(config.version) || 1,
    agentProviders: providers,
    agents,
    agentProfiles: profiles,
  };
  const defaultId = String(config.defaultChatAgentId ?? "").trim();
  if (defaultId) result.defaultChatAgentId = defaultId;
  return result;
}

// createDraft 从服务端配置生成编辑用 draft（深拷贝 + 规范化）。
export function createDraft(config) {
  return normalizeConfig(config);
}

// isDirty 比较 draft 与打开时快照（双方都规范化后深比较，避免等价差异误报）。
export function isDirty(draft, snapshot) {
  return JSON.stringify(normalizeConfig(draft)) !== JSON.stringify(normalizeConfig(snapshot));
}

// buildPayload 由 draft 生成 PUT /v1/config 用的 config 对象，
// 保留 version 与所有受支持字段，剔除空 options 键。
export function buildPayload(draft) {
  return normalizeConfig(draft);
}

// normalizeAgentOptions 在切换 provider 时清理不适用字段：
// 只保留新 schema 的键；枚举值非法时回退默认；model 有值保留。
export function normalizeAgentOptions(providerType, oldOptions = {}) {
  const result = {};
  for (const field of providerOptionSchema(providerType)) {
    const raw = String(oldOptions?.[field.key] ?? "").trim();
    if (field.kind === "text") {
      if (raw) result[field.key] = raw;
      continue;
    }
    const allowed = field.options.map((option) => option.value);
    const value = allowed.includes(raw) ? raw : field.fallback;
    if (value) result[field.key] = value;
  }
  return result;
}

function providerMap(draft) {
  return new Map((draft.agentProviders || []).map((provider) => [provider.id, provider]));
}

function agentProviderEnabled(providers, agent) {
  const provider = providers.get(agent.providerId);
  return Boolean(provider && provider.enabled);
}

// agentReferences 返回某 agent 被默认聊天 Agent 与 Profile 引用的情况。
export function agentReferences(draft, agentId) {
  const profiles = (draft.agentProfiles || []).filter((profile) => profile.agentId === agentId).map((profile) => profile.key);
  return { isDefault: (draft.defaultChatAgentId || "") === agentId, profiles };
}

// providerReferences 返回某 provider 被引用的情况：
// 直接使用它的 agent，以及经由这些 agent 产生的默认 Agent / Profile 间接引用。
export function providerReferences(draft, providerId) {
  const agents = (draft.agents || []).filter((agent) => agent.providerId === providerId).map((agent) => agent.id);
  const profiles = [];
  let isDefault = false;
  for (const agentId of agents) {
    const refs = agentReferences(draft, agentId);
    if (refs.isDefault) isDefault = true;
    profiles.push(...refs.profiles);
  }
  return { agents, isDefault, profiles: [...new Set(profiles)] };
}

// renameAgentId 原子地重命名 agent id 并同步 defaultChatAgentId 与 profiles 引用。
export function renameAgentId(draft, oldId, newId) {
  const next = normalizeConfig(draft);
  for (const agent of next.agents) {
    if (agent.id === oldId) agent.id = newId;
  }
  if (next.defaultChatAgentId === oldId) next.defaultChatAgentId = newId;
  for (const profile of next.agentProfiles) {
    if (profile.agentId === oldId) profile.agentId = newId;
  }
  return next;
}

export function normalizeProfileKey(key) {
  return String(key ?? "").trim().toLowerCase();
}

// validateDraft 客户端完整校验，返回结构化错误列表：
// { section, index, field, message }，section ∈ providers/agents/profiles/general。
export function validateDraft(draft) {
  const errors = [];
  const push = (section, index, field, message) => errors.push({ section, index, field, message });

  const providers = draft.agentProviders || [];
  const providerIds = new Set();
  providers.forEach((provider, index) => {
    if (!provider.id.trim()) push("providers", index, "id", "提供方 ID 必填");
    else if (providerIds.has(provider.id)) push("providers", index, "id", `提供方 ID “${provider.id}” 重复`);
    providerIds.add(provider.id);
    if (!provider.type.trim()) push("providers", index, "type", "类型必选");
    else if (!PROVIDER_TYPES.some((type) => type.value === provider.type)) {
      push("providers", index, "type", `不支持的类型 “${provider.type}”`);
    }
  });

  const providerById = providerMap({ agentProviders: providers });
  const agents = draft.agents || [];
  const agentIds = new Set();
  agents.forEach((agent, index) => {
    if (!agent.id.trim()) push("agents", index, "id", "Agent ID 必填");
    else if (agentIds.has(agent.id)) push("agents", index, "id", `Agent ID “${agent.id}” 重复`);
    agentIds.add(agent.id);
    if (!agent.providerId.trim()) push("agents", index, "providerId", "必须选择提供方");
    else if (!providerById.has(agent.providerId)) {
      push("agents", index, "providerId", `引用的提供方 “${agent.providerId}” 不存在`);
    }
  });

  // 可用 agent = provider 存在且已启用；禁用/删除 provider 导致的间接引用破坏在此暴露。
  const availableAgents = new Set(
    agents.filter((agent) => agentProviderEnabled(providerById, agent)).map((agent) => agent.id),
  );
  const explainUnavailable = (agentId) => {
    const agent = agents.find((item) => item.id === agentId);
    if (!agent) return `引用的 Agent “${agentId}” 不存在`;
    const provider = providerById.get(agent.providerId);
    if (!provider) return `Agent “${agentId}” 的提供方 “${agent.providerId}” 已被删除`;
    return `Agent “${agentId}” 的提供方 “${provider.name || provider.id}” 已被禁用`;
  };

  if ((draft.defaultChatAgentId || "").trim()) {
    if (!availableAgents.has(draft.defaultChatAgentId)) {
      push("general", null, "defaultChatAgentId", `默认聊天 Agent 不可用：${explainUnavailable(draft.defaultChatAgentId)}`);
    }
  }

  const profiles = draft.agentProfiles || [];
  const keys = new Set();
  profiles.forEach((profile, index) => {
    const key = normalizeProfileKey(profile.key);
    if (!key) push("profiles", index, "key", "Profile key 必填");
    else if (keys.has(key)) push("profiles", index, "key", `Profile key “${key}” 重复（忽略大小写与首尾空格）`);
    keys.add(key);
    if (!String(profile.agentId ?? "").trim()) push("profiles", index, "agentId", "必须选择 Agent");
    else if (!availableAgents.has(profile.agentId)) {
      push("profiles", index, "agentId", `Profile 路由不可用：${explainUnavailable(profile.agentId)}`);
    }
  });

  return errors;
}

// slugify 从名称生成 id 基础形式。
export function slugify(name) {
  const slug = String(name ?? "")
    .trim()
    .toLowerCase()
    .replace(/[^\p{L}\p{N}]+/gu, "-")
    .replace(/^-+|-+$/g, "");
  return slug || "agent";
}

// uniqueId 在 existing 中冲突时追加 -2 / -3 …
export function uniqueId(base, existing) {
  const taken = new Set(existing);
  if (!taken.has(base)) return base;
  for (let index = 2; ; index += 1) {
    const candidate = `${base}-${index}`;
    if (!taken.has(candidate)) return candidate;
  }
}

// summarizeOptions 生成 agent 摘要 pill 文本用的键值对。
export function summarizeOptions(options = {}) {
  return Object.entries(options)
    .filter(([, value]) => String(value ?? "").trim())
    .map(([key, value]) => `${key}=${value}`);
}
