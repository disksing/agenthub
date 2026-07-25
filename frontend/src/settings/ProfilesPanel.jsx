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
    <section aria-label="Profile routing settings">
      <h3 className="settings-section-title">Profiles</h3>
      <p className="settings-section-desc">
        A profile is a generic route that selects an agent by key: the tag carried when a session is created matches a key here and picks the corresponding agent.
      </p>
      {!draft.agentProfiles.length ? (
        <div className="settings-empty">No profiles yet. Add one to map a key to an agent.</div>
      ) : null}
      {draft.agentProfiles.length ? (
        <div className="settings-table">
          <div className="settings-table-row settings-table-head" aria-hidden="true">
            <span>Key</span><span>Description</span><span>Agent</span><span />
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
                    placeholder="e.g. fast, review"
                    onChange={(event) => updateProfile(index, { key: event.target.value })}
                  />
                  {keyError ? <p className="settings-field-error" role="alert">{keyError}</p> : null}
                </div>
                <div>
                  <input
                    id={`${base}-description`}
                    aria-label={`Profile ${index + 1} description`}
                    className="settings-input"
                    value={profile.description || ""}
                    placeholder="Optional"
                    onChange={(event) => updateProfile(index, { description: event.target.value })}
                  />
                </div>
                <div>
                  <select
                    id={`${base}-agent`}
                    aria-label={`Profile ${index + 1} agent`}
                    className="settings-select"
                    value={profile.agentId}
                    onChange={(event) => updateProfile(index, { agentId: event.target.value })}
                  >
                    {!usable.some((agent) => agent.id === profile.agentId) ? (
                      <option value={profile.agentId}>{profile.agentId || "Select an agent"}{profile.agentId ? " (unavailable)" : ""}</option>
                    ) : null}
                    {usable.map((agent) => <option key={agent.id} value={agent.id}>{agent.name || agent.id}</option>)}
                  </select>
                  {agentError ? <p className="settings-field-error" role="alert">{agentError}</p> : null}
                </div>
                <div>
                  <button
                    className="icon-button"
                    aria-label={`Delete profile ${profile.key || index + 1}`}
                    title="Delete profile"
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
        title={usable.length ? "" : "No agents available: at least one agent under an enabled provider is required"}
      >
        <Plus size={18} />Add profile
      </button>
      {!usable.length ? (
        <p className="settings-section-desc">No agents available: add a profile only after at least one agent under an enabled provider exists.</p>
      ) : null}
    </section>
  );
}
