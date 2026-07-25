import { Plus, Trash } from "@phosphor-icons/react";
import { fieldError } from "./fields";

export function availableAgents(draft) {
  const enabledProviders = new Set(
    draft.agentProviders.filter((provider) => provider.enabled).map((provider) => provider.id),
  );
  return draft.agents.filter((agent) => enabledProviders.has(agent.providerId));
}

export function ProfilesPanel({ draft, errors, showErrors, mutate }) {
  const usable = availableAgents(draft);

  const updateProfile = (index, patch) => {
    mutate((next) => {
      Object.assign(next.agentProfiles[index], patch);
    });
  };

  const removeProfile = (index) => {
    mutate((next) => {
      next.agentProfiles.splice(index, 1);
    });
  };

  const addProfile = () => {
    mutate((next) => {
      next.agentProfiles.push({ key: "", agentId: usable[0]?.id || "" });
    });
  };

  return (
    <section aria-label="Profile 路由设置">
      <h3 className="settings-section-title">Profile 路由</h3>
      <p className="settings-section-desc">
        Profile 是「按 key/tag 选择 Agent 的通用路由」：创建 Session 时携带的 tag 会匹配这里的 key，从而选用对应的 Agent。
      </p>
      {!draft.agentProfiles.length ? (
        <div className="settings-empty">还没有 Profile。新增一条，把 key 映射到指定 Agent。</div>
      ) : null}
      {draft.agentProfiles.length ? (
        <div className="settings-table">
          <div className="settings-table-row settings-table-head" aria-hidden="true">
            <span>Key</span><span>描述</span><span>Agent</span><span />
          </div>
          {draft.agentProfiles.map((profile, index) => {
            const base = `settings-profile-${index}`;
            const keyError = showErrors ? fieldError(errors, "profiles", index, "key") : "";
            const agentError = showErrors ? fieldError(errors, "profiles", index, "agentId") : "";
            return (
              <div className="settings-table-row" key={index}>
                <div>
                  <input
                    id={`${base}-key`}
                    aria-label={`Profile ${index + 1} key`}
                    className="settings-input"
                    value={profile.key}
                    placeholder="如 fast、review"
                    onChange={(event) => updateProfile(index, { key: event.target.value })}
                  />
                  {keyError ? <p className="settings-field-error" role="alert">{keyError}</p> : null}
                </div>
                <div>
                  <input
                    id={`${base}-description`}
                    aria-label={`Profile ${index + 1} 描述`}
                    className="settings-input"
                    value={profile.description || ""}
                    placeholder="可选"
                    onChange={(event) => updateProfile(index, { description: event.target.value })}
                  />
                </div>
                <div>
                  <select
                    id={`${base}-agent`}
                    aria-label={`Profile ${index + 1} Agent`}
                    className="settings-select"
                    value={profile.agentId}
                    onChange={(event) => updateProfile(index, { agentId: event.target.value })}
                  >
                    {!usable.some((agent) => agent.id === profile.agentId) ? (
                      <option value={profile.agentId}>{profile.agentId || "请选择 Agent"}{profile.agentId ? "（不可用）" : ""}</option>
                    ) : null}
                    {usable.map((agent) => <option key={agent.id} value={agent.id}>{agent.name || agent.id}</option>)}
                  </select>
                  {agentError ? <p className="settings-field-error" role="alert">{agentError}</p> : null}
                </div>
                <div>
                  <button
                    className="icon-button"
                    aria-label={`删除 Profile ${profile.key || index + 1}`}
                    title="删除 Profile"
                    onClick={() => removeProfile(index)}
                  >
                    <Trash size={17} />
                  </button>
                </div>
              </div>
            );
          })}
        </div>
      ) : null}
      <button
        className="settings-add-card"
        onClick={addProfile}
        disabled={!usable.length}
        title={usable.length ? "" : "暂无可用 Agent：需要至少一个已启用提供方下的 Agent"}
      >
        <Plus size={18} />新增 Profile
      </button>
      {!usable.length ? (
        <p className="settings-section-desc">暂无可用 Agent：需要至少一个已启用提供方下的 Agent 才能新增 Profile。</p>
      ) : null}
    </section>
  );
}
