import { Field, fieldError } from "./fields";
import { availableAgents } from "./ProfilesPanel";

export function GeneralPanel({ draft, errors, showErrors, mutate }) {
  const usable = availableAgents(draft);
  const current = draft.defaultChatAgentId || "";
  const currentUsable = usable.some((agent) => agent.id === current);

  return (
    <section aria-label="General settings">
      <h3 className="settings-section-title">General</h3>
      <p className="settings-section-desc">Default behavior for sessions.</p>
      <div className="settings-grid settings-grid-single">
        <Field
          label="Default chat agent"
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
            <option value="">(None)</option>
            {current && !currentUsable ? <option value={current}>{current} (unavailable)</option> : null}
            {usable.map((agent) => <option key={agent.id} value={agent.id}>{agent.name || agent.id}</option>)}
          </select>
        </Field>
      </div>
      <dl className="settings-meta">
        <div><dt>Config version</dt><dd>v{draft.version}</dd></div>
        <div><dt>Size</dt><dd>{draft.agentProviders.length} providers · {draft.agents.length} agents · {draft.agentProfiles.length} profiles</dd></div>
      </dl>
    </section>
  );
}
