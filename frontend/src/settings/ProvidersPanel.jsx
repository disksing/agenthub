import { useState } from "react";
import { Cube, Plus, Trash } from "@phosphor-icons/react";
import { PROVIDER_TYPES, providerReferences, uniqueId } from "./configModel";
import { Field, fieldError } from "./fields";

function typeLabel(type) {
  return PROVIDER_TYPES.find((item) => item.value === type)?.label || type || "未设置";
}

function probePill(provider, probe) {
  if (!provider.enabled) return <span className="settings-pill pill-muted">未启用 · 不探测</span>;
  if (!probe) return <span className="settings-pill pill-muted">未探测</span>;
  if (probe.available) {
    return <span className="settings-pill pill-ok" title={probe.command || ""}>命令可用</span>;
  }
  return <span className="settings-pill pill-warn" title={probe.error || "命令不可发现"}>命令不可用</span>;
}

function describeProviderRefs(draft, providerId) {
  const refs = providerReferences(draft, providerId);
  const parts = [];
  if (refs.agents.length) parts.push(`使用它的 Agent：${refs.agents.join("、")}`);
  if (refs.isDefault) parts.push("其中包含默认聊天 Agent");
  if (refs.profiles.length) parts.push(`Profile 间接引用：${refs.profiles.join("、")}`);
  return parts.join("；");
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

  const toggleEnabled = (index, enabled) => {
    const provider = draft.agentProviders[index];
    if (!enabled) {
      const refs = providerReferences(draft, provider.id);
      if (refs.isDefault || refs.profiles.length) {
        setNotice(`无法禁用提供方“${provider.name || provider.id}”：${describeProviderRefs(draft, provider.id)}。请先在「Agent」「Profile 路由」或「常规」中调整引用。`);
        return;
      }
    }
    updateProvider(index, { enabled });
  };

  const removeProvider = (index) => {
    const provider = draft.agentProviders[index];
    const refs = providerReferences(draft, provider.id);
    if (refs.agents.length) {
      setNotice(`无法删除提供方“${provider.name || provider.id}”：${describeProviderRefs(draft, provider.id)}。请先删除或调整相关 Agent。`);
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
    <section aria-label="提供方设置">
      <h3 className="settings-section-title">提供方</h3>
      <p className="settings-section-desc">
        提供方是本地 Agent CLI 的接入配置。「启用」表示允许 Agent 使用它；命令状态来自后台探测，留空命令路径时按类型自动发现。
      </p>
      {notice ? <div className="settings-notice" role="alert">{notice}</div> : null}
      {draft.agentProviders.map((provider, index) => {
        const base = `settings-provider-${index}`;
        return (
          <article className="settings-card" key={index}>
            <div className="settings-card-head">
              <span className="settings-card-icon"><Cube size={20} /></span>
              <div className="settings-card-title">
                <strong>{provider.name || provider.id || "未命名提供方"}</strong>
                <span className="settings-card-meta">{typeLabel(provider.type)} · {provider.enabled ? "已启用" : "已禁用"}</span>
              </div>
              {probePill(provider, probeByProvider.get(provider.id))}
              <button
                className="icon-button"
                aria-label={`删除提供方 ${provider.name || provider.id}`}
                title="删除提供方"
                onClick={() => removeProvider(index)}
              >
                <Trash size={17} />
              </button>
            </div>
            <div className="settings-grid">
              <Field label="名称" htmlFor={`${base}-name`}>
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
              <Field label="类型" htmlFor={`${base}-type`} error={showErrors ? fieldError(errors, "providers", index, "type") : ""}>
                <select
                  id={`${base}-type`}
                  className="settings-select"
                  value={provider.type}
                  onChange={(event) => updateProvider(index, { type: event.target.value })}
                >
                  {!PROVIDER_TYPES.some((item) => item.value === provider.type) && provider.type ? (
                    <option value={provider.type}>{provider.type}（不支持）</option>
                  ) : null}
                  {PROVIDER_TYPES.map((item) => <option key={item.value} value={item.value}>{item.label}</option>)}
                </select>
              </Field>
              <Field label="命令路径" htmlFor={`${base}-command`}>
                <input
                  id={`${base}-command`}
                  className="settings-input"
                  value={provider.command || ""}
                  placeholder="留空则按类型自动发现"
                  onChange={(event) => updateProvider(index, { command: event.target.value })}
                />
              </Field>
              <label className="settings-switch" htmlFor={`${base}-enabled`}>
                <input
                  id={`${base}-enabled`}
                  type="checkbox"
                  checked={provider.enabled}
                  onChange={(event) => toggleEnabled(index, event.target.checked)}
                />
                <span>启用该提供方</span>
              </label>
            </div>
          </article>
        );
      })}
      <button className="settings-add-card" onClick={addProvider}>
        <Plus size={18} />新增提供方
      </button>
    </section>
  );
}
