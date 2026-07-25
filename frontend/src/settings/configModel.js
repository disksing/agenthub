// Pure data helpers with no React dependency, shared by the settings UI and tests.
// The config model mirrors internal/config/config.go.

export const PROVIDER_TYPES = [
  { value: "codex", label: "Codex" },
  { value: "opencode", label: "OpenCode" },
  { value: "kimi", label: "Kimi Code" },
  { value: "pi", label: "Pi Coding Agent" },
];

export const SANDBOX_OPTIONS = [
  { value: "read-only", label: "Read only" },
  { value: "workspace-write", label: "Workspace write" },
  { value: "danger-full-access", label: "Danger full access" },
];

export const APPROVAL_OPTIONS = [
  { value: "untrusted", label: "Ask when untrusted" },
  { value: "on-failure", label: "Ask on failure" },
  { value: "on-request", label: "Ask on request" },
  { value: "never", label: "Never ask" },
];

export const MODE_OPTIONS = [
  { value: "", label: "Default" },
  { value: "build", label: "Build" },
  { value: "plan", label: "Plan" },
];

const MODEL_FIELD = { key: "model", kind: "text", label: "Model", placeholder: "Leave empty to use the provider default model" };

// providerOptionSchema returns the form description of the Agent options a
// provider type supports.
export function providerOptionSchema(type) {
  switch (type) {
    case "codex":
      return [
        MODEL_FIELD,
        { key: "sandbox", kind: "enum", label: "Sandbox", options: SANDBOX_OPTIONS, fallback: "workspace-write" },
        { key: "approval", kind: "enum", label: "Approval policy", options: APPROVAL_OPTIONS, fallback: "on-request" },
      ];
    case "kimi":
    case "opencode":
      return [
        MODEL_FIELD,
        { key: "mode", kind: "enum", label: "Mode", options: MODE_OPTIONS, fallback: "" },
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

// normalizeConfig deep-copies config from any source and normalizes it into a
// fixed shape: missing arrays become empty, values become strings, blank
// option keys and empty optional fields are dropped.
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

// createDraft builds an editing draft (deep copy + normalization) from the
// server-side config.
export function createDraft(config) {
  return normalizeConfig(config);
}

// isDirty compares a draft against the snapshot taken when it was opened. Both
// sides are normalized before the deep comparison so equivalent differences do
// not report false positives.
export function isDirty(draft, snapshot) {
  return JSON.stringify(normalizeConfig(draft)) !== JSON.stringify(normalizeConfig(snapshot));
}

// buildPayload produces the config object for PUT /v1/config from a draft,
// keeping the version and all supported fields and dropping empty option keys.
export function buildPayload(draft) {
  return normalizeConfig(draft);
}

// normalizeAgentOptions drops fields that do not apply after a provider
// switch: only keys from the new schema are kept, invalid enum values fall
// back to the default, and a non-empty model is preserved.
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

// agentReferences reports how an agent is referenced by the default chat agent
// and by profiles.
export function agentReferences(draft, agentId) {
  const profiles = (draft.agentProfiles || []).filter((profile) => profile.agentId === agentId).map((profile) => profile.key);
  return { isDefault: (draft.defaultChatAgentId || "") === agentId, profiles };
}

// providerReferences reports how a provider is referenced: the agents that use
// it directly, plus the default-agent and profile references that go through
// those agents.
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

// renameAgentId atomically renames an agent id and syncs the
// defaultChatAgentId and profile references.
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

// validateDraft performs full client-side validation and returns structured
// errors: { section, index, field, message },
// section ∈ providers/agents/profiles/general.
export function validateDraft(draft) {
  const errors = [];
  const push = (section, index, field, message) => errors.push({ section, index, field, message });

  const providers = draft.agentProviders || [];
  const providerIds = new Set();
  providers.forEach((provider, index) => {
    if (!provider.id.trim()) push("providers", index, "id", "Provider ID is required");
    else if (providerIds.has(provider.id)) push("providers", index, "id", `Provider ID "${provider.id}" is already used`);
    providerIds.add(provider.id);
    if (!provider.type.trim()) push("providers", index, "type", "Provider type is required");
    else if (!PROVIDER_TYPES.some((type) => type.value === provider.type)) {
      push("providers", index, "type", `Unsupported provider type "${provider.type}"`);
    }
  });

  const providerById = providerMap({ agentProviders: providers });
  const agents = draft.agents || [];
  const agentIds = new Set();
  agents.forEach((agent, index) => {
    if (!agent.id.trim()) push("agents", index, "id", "Agent ID is required");
    else if (agentIds.has(agent.id)) push("agents", index, "id", `Agent ID "${agent.id}" is already used`);
    agentIds.add(agent.id);
    if (!agent.providerId.trim()) push("agents", index, "providerId", "Select a provider");
    else if (!providerById.has(agent.providerId)) {
      push("agents", index, "providerId", `Referenced provider "${agent.providerId}" does not exist`);
    }
  });

  // An agent is usable when its provider exists and is enabled; indirect
  // references broken by disabling or deleting a provider surface here.
  const availableAgents = new Set(
    agents.filter((agent) => agentProviderEnabled(providerById, agent)).map((agent) => agent.id),
  );
  const explainUnavailable = (agentId) => {
    const agent = agents.find((item) => item.id === agentId);
    if (!agent) return `agent "${agentId}" does not exist`;
    const provider = providerById.get(agent.providerId);
    if (!provider) return `the provider "${agent.providerId}" of agent "${agentId}" has been deleted`;
    return `the provider "${provider.name || provider.id}" of agent "${agentId}" is disabled`;
  };

  if ((draft.defaultChatAgentId || "").trim()) {
    if (!availableAgents.has(draft.defaultChatAgentId)) {
      push("general", null, "defaultChatAgentId", `Default chat agent is unavailable: ${explainUnavailable(draft.defaultChatAgentId)}`);
    }
  }

  const profiles = draft.agentProfiles || [];
  const keys = new Set();
  profiles.forEach((profile, index) => {
    const key = normalizeProfileKey(profile.key);
    if (!key) push("profiles", index, "key", "Profile key is required");
    else if (keys.has(key)) push("profiles", index, "key", `Profile key "${key}" is already used (case and surrounding whitespace are ignored)`);
    keys.add(key);
    if (!String(profile.agentId ?? "").trim()) push("profiles", index, "agentId", "Select an agent");
    else if (!availableAgents.has(profile.agentId)) {
      push("profiles", index, "agentId", `Profile route is unavailable: ${explainUnavailable(profile.agentId)}`);
    }
  });

  return errors;
}

// slugify derives the base form of an id from a name.
export function slugify(name) {
  const slug = String(name ?? "")
    .trim()
    .toLowerCase()
    .replace(/[^\p{L}\p{N}]+/gu, "-")
    .replace(/^-+|-+$/g, "");
  return slug || "agent";
}

// uniqueId appends -2 / -3 … when the base id is already taken.
export function uniqueId(base, existing) {
  const taken = new Set(existing);
  if (!taken.has(base)) return base;
  for (let index = 2; ; index += 1) {
    const candidate = `${base}-${index}`;
    if (!taken.has(candidate)) return candidate;
  }
}

// summarizeOptions builds the key=value pairs shown in an agent summary pill.
export function summarizeOptions(options = {}) {
  return Object.entries(options)
    .filter(([, value]) => String(value ?? "").trim())
    .map(([key, value]) => `${key}=${value}`);
}
