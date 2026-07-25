import { useState } from "react";
import { Cube, Plus, Trash } from "@phosphor-icons/react";
import { PROVIDER_TYPES, providerReferences, uniqueId } from "./configModel";
import { Field, fieldError } from "./fields";

function typeLabel(type) {
  return PROVIDER_TYPES.find((item) => item.value === type)?.label || type || "Not set";
}

function probePill(provider, probe) {
  if (!provider.enabled) return <span className="settings-pill pill-muted">Disabled · not probed</span>;
  if (!probe) return <span className="settings-pill pill-muted">Not probed</span>;
  if (probe.available) {
    return <span className="settings-pill pill-ok" title={probe.command || ""}>Command available</span>;
  }
  return <span className="settings-pill pill-warn" title={probe.error || "Command not found"}>Command unavailable</span>;
}

function describeProviderRefs(draft, providerId) {
  const refs = providerReferences(draft, providerId);
  return `agents using it: ${refs.agents.join(", ")}`;
}

export function ProvidersPanel({ draft, probes, errors, showErrors, mutate }) {
  const [notice, setNotice] = useState("");
  const probeByProvider = new Map((probes || []).map((probe) => [probe.providerId, probe]));

  const updateProvider = (index, patch) => {
    setNotice("");
    mutate((next) => {
      Object.assign(next.agentProviders[index], patch);
    });
  };

  const removeProvider = (index) => {
    const provider = draft.agentProviders[index];
    const refs = providerReferences(draft, provider.id);
    if (refs.agents.length) {
      setNotice(`Cannot delete provider "${provider.name || provider.id}": ${describeProviderRefs(draft, provider.id)}. Delete or reassign the affected agents first.`);
      return;
    }
    setNotice("");
    mutate((next) => {
      next.agentProviders.splice(index, 1);
    });
  };

  const addProvider = () => {
    setNotice("");
    mutate((next) => {
      const id = uniqueId("provider", next.agentProviders.map((item) => item.id));
      next.agentProviders.push({ id, name: "", type: "codex", enabled: true });
    });
  };

  return (
    <section aria-label="Provider settings">
      <h3 className="settings-section-title">Providers</h3>
      <p className="settings-section-desc">
        Providers connect local agent CLIs to AgentHub. “Enabled” allows agents to use a provider. Command status comes from a background probe; when the command path is empty it is auto-discovered from the provider type.
      </p>
      {notice ? <div className="settings-notice" role="alert">{notice}</div> : null}
      {draft.agentProviders.map((provider, index) => {
        const base = `settings-provider-${index}`;
        return (
          <article className="settings-card" key={index}>
            <div className="settings-card-head">
              <span className="settings-card-icon"><Cube size={20} /></span>
              <div className="settings-card-title">
                <strong>{provider.name || provider.id || "Unnamed provider"}</strong>
                <span className="settings-card-meta">{typeLabel(provider.type)} · {provider.enabled ? "Enabled" : "Disabled"}</span>
              </div>
              {probePill(provider, probeByProvider.get(provider.id))}
              <button
                className="icon-button"
                aria-label={`Delete provider ${provider.name || provider.id}`}
                title="Delete provider"
                onClick={() => removeProvider(index)}
              >
                <Trash size={17} />
              </button>
            </div>
            <div className="settings-grid">
              <Field label="Name" htmlFor={`${base}-name`}>
                <input
                  id={`${base}-name`}
                  className="settings-input"
                  value={provider.name}
                  placeholder={provider.id}
                  onChange={(event) => updateProvider(index, { name: event.target.value })}
                />
              </Field>
              <Field label="ID" htmlFor={`${base}-id`} error={showErrors ? fieldError(errors, "providers", index, "id") : ""}>
                <input
                  id={`${base}-id`}
                  className="settings-input"
                  value={provider.id}
                  onChange={(event) => updateProvider(index, { id: event.target.value.trim() })}
                />
              </Field>
              <Field label="Type" htmlFor={`${base}-type`} error={showErrors ? fieldError(errors, "providers", index, "type") : ""}>
                <select
                  id={`${base}-type`}
                  className="settings-select"
                  value={provider.type}
                  onChange={(event) => updateProvider(index, { type: event.target.value })}
                >
                  {!PROVIDER_TYPES.some((item) => item.value === provider.type) && provider.type ? (
                    <option value={provider.type}>{provider.type} (unsupported)</option>
                  ) : null}
                  {PROVIDER_TYPES.map((item) => <option key={item.value} value={item.value}>{item.label}</option>)}
                </select>
              </Field>
              <Field label="Command path" htmlFor={`${base}-command`}>
                <input
                  id={`${base}-command`}
                  className="settings-input"
                  value={provider.command || ""}
                  placeholder="Leave empty to auto-discover by type"
                  onChange={(event) => updateProvider(index, { command: event.target.value })}
                />
              </Field>
              <label className="settings-switch" htmlFor={`${base}-enabled`}>
                <input
                  id={`${base}-enabled`}
                  type="checkbox"
                  checked={provider.enabled}
                  onChange={(event) => updateProvider(index, { enabled: event.target.checked })}
                />
                <span>Enable this provider</span>
              </label>
            </div>
          </article>
        );
      })}
      <button className="settings-add-card" onClick={addProvider}>
        <Plus size={18} />Add provider
      </button>
    </section>
  );
}
