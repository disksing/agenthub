import { useState } from "react";
import { CaretDown, CaretRight, Plus, Robot, Trash } from "@phosphor-icons/react";
import {
  agentReferences, normalizeAgentOptions, providerOptionSchema, renameAgentId,
  summarizeOptions, uniqueId,
} from "./configModel";
import { Field, fieldError } from "./fields";

function describeAgentRefs(refs) {
  const parts = [];
  if (refs.isDefault) parts.push("it is the default chat agent");
  if (refs.profiles.length) parts.push(`referenced by profiles: ${refs.profiles.join(", ")}`);
  return parts.join("; ");
}

export function AgentsPanel({ draft, errors, showErrors, mutate, replace }) {
  const [expanded, setExpanded] = useState(() => new Set());
  const [notice, setNotice] = useState("");
  const [renameNote, setRenameNote] = useState("");
  const providerById = new Map(draft.agentProviders.map((provider) => [provider.id, provider]));

  const toggleCard = (index) => {
    setExpanded((current) => {
      const next = new Set(current);
      if (next.has(index)) next.delete(index);
      else next.add(index);
      return next;
    });
  };

  const updateAgent = (index, patch) => {
    setRenameNote("");
    mutate((next) => {
      Object.assign(next.agents[index], patch);
    });
  };

  const changeId = (index, newId) => {
    const oldId = draft.agents[index].id;
    const refs = agentReferences(draft, oldId);
    replace(renameAgentId(draft, oldId, newId.trim()));
    if (newId.trim() && (refs.isDefault || refs.profiles.length)) {
      setRenameNote(`Default chat agent and profile references to "${oldId}" were updated to "${newId.trim()}".`);
    } else {
      setRenameNote("");
    }
  };

  const changeProvider = (index, providerId) => {
    setRenameNote("");
    mutate((next) => {
      const agent = next.agents[index];
      agent.providerId = providerId;
      const provider = next.agentProviders.find((item) => item.id === providerId);
      const options = normalizeAgentOptions(provider?.type || "", agent.options || {});
      if (Object.keys(options).length) agent.options = options;
      else delete agent.options;
    });
  };

  const changeOption = (index, key, value) => {
    mutate((next) => {
      const agent = next.agents[index];
      const options = { ...(agent.options || {}) };
      if (value.trim()) options[key] = value;
      else delete options[key];
      if (Object.keys(options).length) agent.options = options;
      else delete agent.options;
    });
  };

  const removeAgent = (index) => {
    const agent = draft.agents[index];
    const refs = agentReferences(draft, agent.id);
    if (refs.isDefault || refs.profiles.length) {
      setNotice(`Cannot delete agent "${agent.name || agent.id}": ${describeAgentRefs(refs)}. Update the references in Profiles or General first.`);
      return;
    }
    setNotice("");
    mutate((next) => {
      next.agents.splice(index, 1);
    });
  };

  const addAgent = () => {
    setNotice("");
    setRenameNote("");
    const providerId = draft.agentProviders[0]?.id || "";
    mutate((next) => {
      const id = uniqueId("agent", next.agents.map((item) => item.id));
      next.agents.push({ id, name: "", providerId });
    });
    setExpanded((current) => new Set(current).add(draft.agents.length));
  };

  return (
    <section aria-label="Agent settings">
      <h3 className="settings-section-title">Agents</h3>
      <p className="settings-section-desc">
        An agent is a concrete configuration on top of a provider. Different providers support different options; switching providers removes options that no longer apply.
      </p>
      {notice ? <div className="settings-notice" role="alert">{notice}</div> : null}
      {renameNote ? <div className="settings-note" role="status">{renameNote}</div> : null}
      {!draft.agentProviders.length ? (
        <div className="settings-empty">Add a provider in the Providers section before configuring agents.</div>
      ) : null}
      {draft.agents.map((agent, index) => {
        const provider = providerById.get(agent.providerId);
        const open = expanded.has(index);
        const summary = summarizeOptions(agent.options).join(" · ");
        const base = `settings-agent-${index}`;
        return (
          <article className="settings-card" key={index}>
            <div className="settings-card-head">
              <button
                className="settings-card-toggle"
                aria-expanded={open}
                aria-controls={`${base}-body`}
                onClick={() => toggleCard(index)}
              >
                {open ? <CaretDown size={16} /> : <CaretRight size={16} />}
                <span className="settings-card-icon"><Robot size={19} /></span>
                <strong>{agent.name || agent.id || "Unnamed agent"}</strong>
                <span className="settings-pill pill-muted">
                  {provider ? provider.name || provider.id : "Unknown provider"}{summary ? ` · ${summary}` : ""}
                </span>
              </button>
              <button
                className="icon-button"
                aria-label={`Delete agent ${agent.name || agent.id}`}
                title="Delete agent"
                onClick={() => removeAgent(index)}
              >
                <Trash size={17} />
              </button>
            </div>
            {open ? (
              <div className="settings-grid" id={`${base}-body`}>
                <Field label="Name" htmlFor={`${base}-name`}>
                  <input
                    id={`${base}-name`}
                    className="settings-input"
                    value={agent.name}
                    placeholder={agent.id}
                    onChange={(event) => updateAgent(index, { name: event.target.value })}
                  />
                </Field>
                <Field label="ID" htmlFor={`${base}-id`} error={showErrors ? fieldError(errors, "agents", index, "id") : ""}>
                  <input
                    id={`${base}-id`}
                    className="settings-input"
                    value={agent.id}
                    onChange={(event) => changeId(index, event.target.value)}
                  />
                </Field>
                <Field label="Provider" htmlFor={`${base}-provider`} error={showErrors ? fieldError(errors, "agents", index, "providerId") : ""}>
                  <select
                    id={`${base}-provider`}
                    className="settings-select"
                    value={agent.providerId}
                    onChange={(event) => changeProvider(index, event.target.value)}
                  >
                    {!providerById.has(agent.providerId) && agent.providerId ? (
                      <option value={agent.providerId}>{agent.providerId} (missing)</option>
                    ) : null}
                    {!agent.providerId ? <option value="">Select a provider</option> : null}
                    {draft.agentProviders.map((item) => (
                      <option key={item.id} value={item.id}>{item.name || item.id}</option>
                    ))}
                  </select>
                </Field>
                {providerOptionSchema(provider?.type || "").map((field) => (
                  <Field key={field.key} label={field.label} htmlFor={`${base}-option-${field.key}`}>
                    {field.kind === "enum" ? (
                      <select
                        id={`${base}-option-${field.key}`}
                        className="settings-select"
                        value={agent.options?.[field.key] ?? field.fallback}
                        onChange={(event) => changeOption(index, field.key, event.target.value)}
                      >
                        {field.options.map((option) => (
                          <option key={option.value} value={option.value}>{option.label}</option>
                        ))}
                      </select>
                    ) : (
                      <input
                        id={`${base}-option-${field.key}`}
                        className="settings-input"
                        value={agent.options?.[field.key] || ""}
                        placeholder={field.placeholder}
                        onChange={(event) => changeOption(index, field.key, event.target.value)}
                      />
                    )}
                  </Field>
                ))}
              </div>
            ) : null}
          </article>
        );
      })}
      <button className="settings-add-card" onClick={addAgent} disabled={!draft.agentProviders.length}
        title={draft.agentProviders.length ? "" : "Add a provider first"}>
        <Plus size={18} />Add agent
      </button>
    </section>
  );
}
