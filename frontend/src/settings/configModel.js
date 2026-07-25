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
// option keys and empty optional fields are dropped. Removed legacy fields
// (agentProfiles, defaultChatAgentId) are tolerated in the input and dropped.
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
  return {
    version: Number(config.version) || 1,
    agentProviders: providers,
    agents,
  };
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

// providerReferences reports which agents use a provider directly.
export function providerReferences(draft, providerId) {
  return { agents: (draft.agents || []).filter((agent) => agent.providerId === providerId).map((agent) => agent.id) };
}

// validateDraft performs full client-side validation and returns structured
// errors: { section, index, field, message },
// section ∈ providers/agents.
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
