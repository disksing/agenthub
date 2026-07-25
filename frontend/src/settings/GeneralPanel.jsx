import { Field, fieldError } from "./fields";
import { availableAgents } from "./ProfilesPanel";

export function GeneralPanel({ draft, errors, showErrors, mutate }) {
  const usable = availableAgents(draft);
  const current = draft.defaultChatAgentId || "";
  const currentUsable = usable.some((agent) => agent.id === current);

  return (
    <section aria-label="常规设置">
      <h3 className="settings-section-title">常规</h3>
      <p className="settings-section-desc">会话相关的默认行为。</p>
      <div className="settings-grid settings-grid-single">
        <Field
          label="默认聊天 Agent"
          htmlFor="settings-default-agent"
          error={showErrors ? fieldError(errors, "general", null, "defaultChatAgentId") : ""}
        >
          <select
            id="settings-default-agent"
            className="settings-select"
            value={current}
            onChange={(event) => mutate((next) => {
              if (event.target.value) next.defaultChatAgentId = event.target.value;
              else delete next.defaultChatAgentId;
            })}
          >
            <option value="">（不设置）</option>
            {current && !currentUsable ? <option value={current}>{current}（不可用）</option> : null}
            {usable.map((agent) => <option key={agent.id} value={agent.id}>{agent.name || agent.id}</option>)}
          </select>
        </Field>
      </div>
      <dl className="settings-meta">
        <div><dt>配置版本</dt><dd>v{draft.version}</dd></div>
        <div><dt>规模</dt><dd>{draft.agentProviders.length} 个提供方 · {draft.agents.length} 个 Agent · {draft.agentProfiles.length} 条 Profile</dd></div>
      </dl>
    </section>
  );
}
