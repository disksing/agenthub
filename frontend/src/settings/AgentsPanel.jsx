import { useState } from "react";
import { CaretDown, CaretRight, Plus, Robot, Trash } from "@phosphor-icons/react";
import {
  agentReferences, normalizeAgentOptions, providerOptionSchema, renameAgentId,
  summarizeOptions, uniqueId,
} from "./configModel";
import { Field, fieldError } from "./fields";

function describeAgentRefs(refs) {
  const parts = [];
  if (refs.isDefault) parts.push("它是默认聊天 Agent");
  if (refs.profiles.length) parts.push(`被 Profile 引用：${refs.profiles.join("、")}`);
  return parts.join("；");
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
      setRenameNote(`已将默认聊天 Agent / Profile 中对“${oldId}”的引用同步为“${newId.trim()}”。`);
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
      setNotice(`无法删除 Agent“${agent.name || agent.id}”：${describeAgentRefs(refs)}。请先在「Profile 路由」或「常规」中调整引用。`);
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
    <section aria-label="Agent 设置">
      <h3 className="settings-section-title">Agent</h3>
      <p className="settings-section-desc">
        Agent 是某个提供方下的具体配置。不同提供方支持不同的选项；切换提供方时会自动清理不适用的选项。
      </p>
      {notice ? <div className="settings-notice" role="alert">{notice}</div> : null}
      {renameNote ? <div className="settings-note" role="status">{renameNote}</div> : null}
      {!draft.agentProviders.length ? (
        <div className="settings-empty">请先在「提供方」中新增提供方，再配置 Agent。</div>
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
                <strong>{agent.name || agent.id || "未命名 Agent"}</strong>
                <span className="settings-pill pill-muted">
                  {provider ? provider.name || provider.id : "未知提供方"}{summary ? ` · ${summary}` : ""}
                </span>
              </button>
              <button
                className="icon-button"
                aria-label={`删除 Agent ${agent.name || agent.id}`}
                title="删除 Agent"
                onClick={() => removeAgent(index)}
              >
                <Trash size={17} />
              </button>
            </div>
            {open ? (
              <div className="settings-grid" id={`${base}-body`}>
                <Field label="名称" htmlFor={`${base}-name`}>
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
                <Field label="提供方" htmlFor={`${base}-provider`} error={showErrors ? fieldError(errors, "agents", index, "providerId") : ""}>
                  <select
                    id={`${base}-provider`}
                    className="settings-select"
                    value={agent.providerId}
                    onChange={(event) => changeProvider(index, event.target.value)}
                  >
                    {!providerById.has(agent.providerId) && agent.providerId ? (
                      <option value={agent.providerId}>{agent.providerId}（不存在）</option>
                    ) : null}
                    {!agent.providerId ? <option value="">请选择提供方</option> : null}
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
        title={draft.agentProviders.length ? "" : "请先新增提供方"}>
        <Plus size={18} />新增 Agent
      </button>
    </section>
  );
}
