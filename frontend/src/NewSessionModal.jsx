import { useEffect, useMemo, useRef, useState } from "react";
import { FolderOpen, Robot, X } from "@phosphor-icons/react";
import { TITLE_MAX_LENGTH, buildCreatePayload } from "./newSession.js";

// Modal dialog for creating a Session. Replaces the old browser prompt()
// flow with an in-app window that collects the working directory, the agent,
// and an optional title, with validation, loading, and error states.
export function NewSessionModal({
  agents,
  providers,
  defaultAgentId,
  defaultCwd,
  submitting,
  error,
  onSubmit,
  onClose,
  triggerRef,
}) {
  const dialogRef = useRef(null);
  const cwdRef = useRef(null);
  const [title, setTitle] = useState("");
  const [cwd, setCwd] = useState(defaultCwd || "");
  const [agentId, setAgentId] = useState(defaultAgentId || "");
  const [showErrors, setShowErrors] = useState(false);

  const providerName = useMemo(() => {
    const map = new Map((providers || []).map((provider) => [provider.id, provider.name || provider.id]));
    return (agent) => map.get(agent?.providerId) || agent?.providerId || "";
  }, [providers]);

  // Effective agent: the explicit choice while it still exists, otherwise the
  // daemon default or the first available agent.
  const effectiveAgentId = agents.some((agent) => agent.id === agentId)
    ? agentId
    : defaultAgentId || agents[0]?.id || "";

  const { errors, payload } = buildCreatePayload({
    title,
    cwd,
    agentId: effectiveAgentId,
    agents,
  });

  // Focus the first field on open; restore focus to the trigger on unmount.
  useEffect(() => {
    (cwdRef.current || dialogRef.current)?.focus();
    return () => triggerRef?.current?.focus();
  }, [triggerRef]);

  useEffect(() => {
    const onKeyDown = (event) => {
      if (event.key === "Escape" && !submitting) onClose();
    };
    document.addEventListener("keydown", onKeyDown);
    return () => document.removeEventListener("keydown", onKeyDown);
  }, [onClose, submitting]);

  const submit = (event) => {
    event.preventDefault();
    if (submitting) return;
    if (!payload) {
      setShowErrors(true);
      return;
    }
    onSubmit(payload);
  };

  const fieldError = (name) => (showErrors && errors[name] ? errors[name] : "");

  return (
    <div
      className="modal-backdrop"
      onMouseDown={(event) => {
        if (event.target === event.currentTarget && !submitting) onClose();
      }}
    >
      <section
        className="new-session-dialog"
        role="dialog"
        aria-modal="true"
        aria-labelledby="new-session-dialog-title"
        ref={dialogRef}
        tabIndex={-1}
      >
        <header className="new-session-dialog-header">
          <div>
            <h2 id="new-session-dialog-title">New Session</h2>
            <p>Choose a working directory and an agent, then start the conversation.</p>
          </div>
          <button className="icon-button" aria-label="Close new session dialog" onClick={onClose} disabled={submitting}>
            <X size={19} />
          </button>
        </header>

        <form className="new-session-form" onSubmit={submit} noValidate>
          <label className="new-session-field">
            <span className="new-session-label"><FolderOpen size={16} />Working directory</span>
            <input
              ref={cwdRef}
              type="text"
              value={cwd}
              onChange={(event) => setCwd(event.target.value)}
              placeholder="/absolute/path/to/project"
              aria-label="Working directory"
              aria-invalid={Boolean(fieldError("cwd"))}
              aria-describedby="new-session-cwd-hint new-session-cwd-error"
              disabled={submitting}
              spellCheck={false}
            />
            <span className="new-session-hint" id="new-session-cwd-hint">
              Absolute path of an existing local directory. The daemon verifies it before starting the agent.
            </span>
            {fieldError("cwd") ? <span className="new-session-error" id="new-session-cwd-error" role="alert">{fieldError("cwd")}</span> : null}
          </label>

          <label className="new-session-field">
            <span className="new-session-label"><Robot size={16} />Agent</span>
            {agents.length ? (
              <select
                value={effectiveAgentId}
                onChange={(event) => setAgentId(event.target.value)}
                aria-label="Agent"
                aria-invalid={Boolean(fieldError("agent"))}
                disabled={submitting}
              >
                {agents.map((agent) => (
                  <option key={agent.id} value={agent.id}>
                    {agent.name || agent.id}{providerName(agent) ? ` · ${providerName(agent)}` : ""}
                  </option>
                ))}
              </select>
            ) : (
              <span className="new-session-hint new-session-empty" role="status">
                No agents are available. Enable a provider or add an agent in Settings before creating a session.
              </span>
            )}
            {fieldError("agent") ? <span className="new-session-error" role="alert">{fieldError("agent")}</span> : null}
          </label>

          <label className="new-session-field">
            <span className="new-session-label">Title <span className="new-session-optional">optional</span></span>
            <input
              type="text"
              value={title}
              onChange={(event) => setTitle(event.target.value)}
              placeholder="New Session"
              maxLength={TITLE_MAX_LENGTH + 40}
              aria-label="Session title"
              aria-invalid={Boolean(fieldError("title"))}
              disabled={submitting}
              spellCheck={false}
            />
            <span className="new-session-hint">Leave empty to use the default title. Up to {TITLE_MAX_LENGTH} characters.</span>
            {fieldError("title") ? <span className="new-session-error" role="alert">{fieldError("title")}</span> : null}
          </label>

          {error ? <p className="new-session-submit-error" role="alert">{error}</p> : null}

          <footer className="new-session-actions">
            <button type="button" className="settings-button" onClick={onClose} disabled={submitting}>Cancel</button>
            <button
              type="submit"
              className="settings-button settings-button-primary"
              disabled={submitting || !agents.length}
            >
              {submitting ? "Creating…" : "Create Session"}
            </button>
          </footer>
        </form>
      </section>
    </div>
  );
}
