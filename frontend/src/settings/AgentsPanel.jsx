import { useRef, useState } from "react";
import { CaretDown, CaretRight, Plus, Robot, Trash } from "@phosphor-icons/react";
import {
  normalizeAgentOptions, providerOptionSchema, summarizeOptions, uniqueAgentName,
} from "./configModel";
import { Field, fieldError } from "./fields";
import { ModelSelect } from "./ModelSelect";

// AgentsPanel edits the agents of the settings draft. Agents are identified
// by their unique name only; there is no separate id field. React list keys
// come from a per-mount counter aligned with the draft rows (adds appends,
// removes splice, edits never reorder), so rows never key on the array index
// and edits cannot shift onto the wrong card.
export function AgentsPanel({ draft, errors, showErrors, mutate }) {
  const [expanded, setExpanded] = useState(() => new Set());
  const keyCounter = useRef(0);
  const rowKeys = useRef(draft.agents.map(() => keyCounter.current++));
  // Re-synchronize if the draft was replaced externally (e.g. a reload after
  // a save conflict): regenerate keys so lengths stay aligned.
  if (rowKeys.current.length !== draft.agents.length) {
    rowKeys.current = draft.agents.map(() => keyCounter.current++);
  }
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
    mutate((next) => {
      Object.assign(next.agents[index], patch);
    });
  };

  const changeProvider = (index, providerId) => {
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
    rowKeys.current.splice(index, 1);
    mutate((next) => {
      next.agents.splice(index, 1);
    });
  };

  const addAgent = () => {
    const providerId = draft.agentProviders[0]?.id || "";
    rowKeys.current.push(keyCounter.current++);
    mutate((next) => {
      next.agents.push({ name: uniqueAgentName("Agent", next.agents.map((item) => item.name)), providerId });
    });
    setExpanded((current) => new Set(current).add(draft.agents.length));
  };

  return (
    <section aria-label="Agent settings">
      <h3 className="settings-section-title">Agents</h3>
      <p className="settings-section-desc">
        An agent is a concrete configuration on top of a provider, referenced everywhere by its unique name
        (names must differ even when case and surrounding whitespace are ignored). Different providers support
        different options; switching providers removes options that no longer apply. Sessions are always created
        with an explicit agent.
      </p>
      {!draft.agentProviders.length ? (
        <div className="settings-empty">Add a provider in the Providers section before configuring agents.</div>
      ) : null}
      {draft.agents.map((agent, index) => {
        const provider = providerById.get(agent.providerId);
        const open = expanded.has(index);
        const summary = summarizeOptions(agent.options).join(" · ");
        const base = `settings-agent-${index}`;
        return (
          <article className="settings-card" key={rowKeys.current[index]}>
            <div className="settings-card-head">
              <button
                className="settings-card-toggle"
                aria-expanded={open}
                aria-controls={`${base}-body`}
                onClick={() => toggleCard(index)}
              >
                {open ? <CaretDown size={16} /> : <CaretRight size={16} />}
                <span className="settings-card-icon"><Robot size={19} /></span>
                <strong>{agent.name || "Unnamed agent"}</strong>
                <span className="settings-pill pill-muted">
                  {provider ? provider.name || provider.id : "Unknown provider"}{summary ? ` · ${summary}` : ""}
                </span>
              </button>
              <button
                className="icon-button"
                aria-label={`Delete agent ${agent.name || "Unnamed agent"}`}
                title="Delete agent"
                onClick={() => removeAgent(index)}
              >
                <Trash size={17} />
              </button>
            </div>
            {open ? (
              <div className="settings-grid" id={`${base}-body`}>
                <Field label="Name" htmlFor={`${base}-name`} error={showErrors ? fieldError(errors, "agents", index, "name") : ""}>
                  <input
                    id={`${base}-name`}
                    className="settings-input"
                    value={agent.name}
                    placeholder="Agent name"
                    onChange={(event) => updateAgent(index, { name: event.target.value })}
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
                    {field.kind === "model" ? (
                      <ModelSelect
                        id={`${base}-option-${field.key}`}
                        providerId={agent.providerId}
                        providerEnabled={Boolean(provider?.enabled)}
                        value={agent.options?.[field.key] || ""}
                        onChange={(next) => changeOption(index, field.key, next)}
                      />
                    ) : field.kind === "enum" ? (
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
