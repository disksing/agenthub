// Pure logic behind the agent model dropdown, shared by the React component
// and node --test unit tests. The dropdown never accepts free text: every
// choice comes from the provider's live model list (GET
// /v1/providers/{id}/models), except a previously saved value that is no
// longer listed, which is preserved explicitly instead of being dropped.

export const PROVIDER_DEFAULT_VALUE = "";

// buildModelChoices turns the daemon's model list into select options. The
// first option is always the empty "Provider default" choice (the agent
// simply omits the model option). A saved value that is missing from the
// list is appended as an explicit unavailable option so editing an old agent
// never silently rewrites or clears its model.
export function buildModelChoices(models, current) {
  const choices = [{ value: PROVIDER_DEFAULT_VALUE, label: "Provider default", unavailable: false }];
  const seen = new Set();
  for (const model of Array.isArray(models) ? models : []) {
    const value = String(model?.id ?? "").trim();
    if (!value || seen.has(value)) continue;
    seen.add(value);
    const label = String(model?.label ?? "").trim() || value;
    choices.push({
      value,
      label: model?.default ? `${label} (default)` : label,
      unavailable: false,
    });
  }
  const saved = String(current ?? "").trim();
  if (saved && !seen.has(saved)) {
    choices.push({
      value: saved,
      label: `${saved} (saved, not currently listed)`,
      unavailable: true,
    });
  }
  return choices;
}

// modelListView reduces the fetch state and the saved value into everything
// the component needs to render: the select options, whether the select is
// interactive, and an optional status message. States: "loading", "ready",
// "error", "disabled" (provider toggled off), "none" (no provider selected).
export function modelListView(state, current) {
  const saved = String(current ?? "").trim();
  switch (state.status) {
    case "ready":
      return {
        disabled: false,
        choices: buildModelChoices(state.models, saved),
        message: state.models?.length ? "" : "This provider did not report any models; the provider default will be used.",
        tone: state.models?.length ? "" : "muted",
      };
    case "loading":
      return {
        disabled: true,
        choices: [{ value: saved, label: "Loading models…", unavailable: false }],
        message: "Loading the provider's model list…",
        tone: "muted",
      };
    case "error":
      return {
        disabled: true,
        choices: [{ value: saved, label: saved || "Provider default", unavailable: false }],
        message: state.error || "Failed to load the model list.",
        tone: "error",
        retry: true,
      };
    case "disabled":
      return {
        disabled: true,
        choices: [{ value: saved, label: saved || "Provider default", unavailable: false }],
        message: "This provider is disabled; its model list is unavailable.",
        tone: "muted",
      };
    default:
      return {
        disabled: true,
        choices: [{ value: saved, label: saved || "Provider default", unavailable: false }],
        message: "Select a provider to load its models.",
        tone: "muted",
      };
  }
}
