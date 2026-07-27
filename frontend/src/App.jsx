import { useEffect, useMemo, useRef, useState } from "react";
import {
  Archive, CaretRight, Check, CircleNotch, ClockCounterClockwise, Copy, Gear, List, PaperPlaneTilt, Plus,
  Play, SidebarSimple, Stop, X,
} from "@phosphor-icons/react";
import { api } from "./api";
import { archiveDisabledReason, archiveListError, isArchived, isArchivable, pickActiveAfterArchive, sessionStatusLabel, sessionsQuery } from "./archive.js";
import { catchUpEvents, projectLiveEvent } from "./events.js";
import { buildTimeline } from "@agenthub/event-timeline";
import { displayTime } from "./display.js";
import { Timeline } from "./Timeline.jsx";
import { NewSessionModal } from "./NewSessionModal.jsx";
import { isResumable, requestSessionResume, resumeErrorForSession } from "./resume.js";
import { SettingsModal } from "./settings/SettingsModal.jsx";

export function App() {
  const [sessions, setSessions] = useState([]);
  const [archivedSessions, setArchivedSessions] = useState([]);
  const [archivedView, setArchivedView] = useState(false);
  // Per-session pending set for the inline list archive button: only the
  // clicked row is busy/disabled, and duplicate submissions are blocked.
  const [listArchivingIds, setListArchivingIds] = useState(() => new Set());
  const [agents, setAgents] = useState([]);
  const [providers, setProviders] = useState([]);
  const [defaultAgentName, setDefaultAgentName] = useState("");
  const [activeId, setActiveId] = useState("");
  const [events, setEvents] = useState([]);
  const [eventReloadKey, setEventReloadKey] = useState(0);
  const [draft, setDraft] = useState("");
  const [resumingId, setResumingId] = useState("");
  const [resumeFailure, setResumeFailure] = useState(null);
  const resumingIdRef = useRef("");
  // On narrow viewports the details panel is hidden entirely (see styles.css);
  // start with it closed there.
  const isNarrow = () => window.matchMedia("(max-width: 760px)").matches;
  const [detailsOpen, setDetailsOpen] = useState(() => !isNarrow());
  // On narrow viewports the sidebar overlays the workspace; start with it
  // hidden and close it again after picking a session.
  const [sidebarOpen, setSidebarOpen] = useState(() => !isNarrow());
  const [settingsOpen, setSettingsOpen] = useState(false);
  const settingsTriggerRef = useRef(null);
  const [newSessionOpen, setNewSessionOpen] = useState(false);
  const newSessionTriggerRef = useRef(null);
  const [creating, setCreating] = useState(false);
  const [createError, setCreateError] = useState("");
  const [error, setError] = useState("");
  const [copied, setCopied] = useState(false);
  const conversationRef = useRef(null);
  const nearBottomRef = useRef(true);

  // The active session may live in either list: archiving the session being
  // viewed must not make it vanish from the workspace.
  const activeSession = useMemo(
    () => sessions.find((item) => item.id === activeId) || archivedSessions.find((item) => item.id === activeId),
    [sessions, archivedSessions, activeId],
  );
  const timeline = useMemo(() => buildTimeline(events), [events]);
  const activeResumeError = resumeErrorForSession(resumeFailure, activeSession);

  const refreshSessions = async () => {
    const body = await api(sessionsQuery(false));
    setSessions(body.sessions);
    setActiveId((current) => current || body.sessions[0]?.id || "");
  };

  const refreshArchivedSessions = async () => {
    const body = await api(sessionsQuery(true));
    setArchivedSessions(body.sessions || []);
  };

  const toggleArchivedView = () => {
    setArchivedView((current) => {
      if (!current) refreshArchivedSessions().catch((value) => setError(value.message));
      return !current;
    });
  };

  const loadAgents = async () => {
    const body = await api("/v1/agents");
    // Agents whose provider is disabled (or missing) are reported as
    // unavailable by the daemon and must not be offered for new sessions;
    // the daemon rejects them as well, this only keeps the choices honest.
    const available = (body.agents || []).filter((agent) => agent.available !== false);
    setAgents(available);
    setProviders(body.providers || []);
    setDefaultAgentName(available[0]?.name || "");
  };

  useEffect(() => {
    Promise.all([refreshSessions(), loadAgents()])
      .catch((value) => setError(value.message));
  }, []);

  useEffect(() => {
    if (!activeId) { setEvents([]); return undefined; }
    let source;
    let disposed = false;
    let cursor = 0;
    let recovering = false;
    const project = (incoming) => {
      setEvents((current) => {
        const known = new Set(current.map((event) => event.id));
        return [...current, ...incoming.filter((event) => !known.has(event.id))];
      });
    };
    const refreshForEvent = (event) => {
      if (/^(session|turn|approval)\./.test(event.type)) {
        refreshSessions().catch(() => {});
        if (event.type === "session.archived") refreshArchivedSessions().catch(() => {});
      }
    };
    const connect = () => {
      if (disposed) return;
      source = new EventSource(`/v1/sessions/${activeId}/events?stream=true&after=${cursor}`);
      // All envelopes arrive on the default message channel. A gap pauses
      // live projection and catches up from the durable REST log.
      source.onmessage = async (message) => {
        if (disposed || recovering) return;
        const event = JSON.parse(message.data);
        if (event.id > cursor + 1) {
          recovering = true;
          source.close();
        }
        try {
          cursor = await projectLiveEvent({ sessionId: activeId, cursor, event, project });
          refreshForEvent(event);
          if (recovering) {
            recovering = false;
            connect();
          }
        } catch (value) {
          if (!disposed) setError(value.message);
        }
      };
      // EventSource reconnects with Last-Event-ID after an overflow or daemon
      // restart. The server replays from the durable store.
      source.onerror = () => {};
    };
    catchUpEvents(activeId)
      .then((history) => {
        if (disposed) return;
        setEvents(history.events);
        cursor = history.cursor;
        connect();
      })
      .catch((value) => setError(value.message));
    return () => { disposed = true; source?.close(); };
  }, [activeId, eventReloadKey]);

  // Keep the conversation pinned to the bottom while the user is already
  // near it; jumping between sessions always lands on the latest events.
  useEffect(() => {
    const node = conversationRef.current;
    if (node && nearBottomRef.current) node.scrollTop = node.scrollHeight;
  }, [timeline, activeId]);

  const onConversationScroll = () => {
    const node = conversationRef.current;
    if (!node) return;
    nearBottomRef.current = node.scrollHeight - node.scrollTop - node.clientHeight <= 48;
  };

  const createSession = async (payload) => {
    if (creating) return;
    setCreating(true);
    setCreateError("");
    try {
      const body = await api("/v1/sessions", { method: "POST", body: JSON.stringify(payload) });
      setNewSessionOpen(false);
      await refreshSessions();
      setActiveId(body.session.id);
    } catch (value) {
      setCreateError(value.message || "Failed to create the session");
    } finally {
      setCreating(false);
    }
  };

  const sendMessage = async () => {
    const text = draft.trim();
    if (!text || !activeSession) return;
    setDraft(""); setError("");
    try {
      await api(`/v1/sessions/${activeSession.id}/messages`, { method: "POST", body: JSON.stringify({ text }) });
    } catch (value) { setDraft(text); setError(value.message); }
  };

  const stopSession = async () => {
    if (!activeSession || isArchived(activeSession)) return;
    try {
      const action = activeSession.currentTurnId ? "interrupt" : "stop";
      await api(`/v1/sessions/${activeSession.id}/${action}`, { method: "POST", body: "{}" });
      await refreshSessions();
    } catch (value) { setError(value.message); }
  };

  const resumeSession = async () => {
    if (!isResumable(activeSession) || resumingIdRef.current === activeSession.id) return;
    const sessionId = activeSession.id;
    resumingIdRef.current = sessionId;
    setResumingId(sessionId);
    setResumeFailure(null);
    setError("");
    try {
      const resumed = await requestSessionResume(sessionId);
      if (resumed) {
        setSessions((current) => current.map((session) => session.id === sessionId ? resumed : session));
      }
      setEventReloadKey((current) => current + 1);
      try {
        await refreshSessions();
      } catch (value) {
        setError(`Session resumed, but its latest state could not be loaded: ${value.message}`);
      }
    } catch (value) {
      setResumeFailure({
        sessionId,
        message: `Failed to resume session: ${value.message}`,
      });
    } finally {
      if (resumingIdRef.current === sessionId) resumingIdRef.current = "";
      setResumingId((current) => current === sessionId ? "" : current);
    }
  };

  // archiveFromList is the one-click archive offered on hover/focus inside
  // the default session list; it is the only archive entry point in the UI.
  // It calls the daemon archive API directly, with no confirmation dialog.
  const archiveFromList = async (session) => {
    if (!session || archivedView || !isArchivable(session)) return;
    if (listArchivingIds.has(session.id)) return;
    setError("");
    setListArchivingIds((current) => new Set(current).add(session.id));
    try {
      await api(`/v1/sessions/${session.id}`, { method: "DELETE", body: "{}" });
      // Refetch the default list so caches drop the archived session, then
      // converge the selection when the archived session was being viewed.
      const body = await api(sessionsQuery(false));
      const remaining = body.sessions || [];
      setSessions(remaining);
      setActiveId((current) => pickActiveAfterArchive(remaining, session.id, current));
      refreshArchivedSessions().catch(() => {});
    } catch (value) {
      // Failure keeps the list item and the current selection; the banner
      // shows the actionable server reason.
      setError(archiveListError(session, value.message));
    } finally {
      setListArchivingIds((current) => {
        const next = new Set(current);
        next.delete(session.id);
        return next;
      });
    }
  };

  const resolveApproval = async (approvalId, decision) => {
    try {
      await api(`/v1/sessions/${activeId}/approvals/${approvalId}`, { method: "POST", body: JSON.stringify({ decision }) });
    } catch (value) { setError(value.message); }
  };

  return (
    <main className={`app-shell ${sidebarOpen ? "" : "sidebar-collapsed"} ${detailsOpen ? "" : "details-collapsed"}`}>
      <aside className="sidebar">
        <div className="sidebar-top">
          <div className="brand">AgentHub</div>
          <button
            className="new-session"
            ref={newSessionTriggerRef}
            onClick={() => { setCreateError(""); setNewSessionOpen(true); }}
          >
            <Plus size={20} />New Session
          </button>
          <div className="session-label">{archivedView ? "Archived Sessions" : "Recent Sessions"}</div>
          <nav className="session-list" aria-label={archivedView ? "Archived sessions" : "Recent sessions"}>
            {(archivedView ? archivedSessions : sessions).map((item) => {
              const itemArchiving = listArchivingIds.has(item.id);
              const itemArchivable = isArchivable(item);
              return (
                <div key={item.id} className={`session-row ${item.id === activeId ? "active" : ""}`}>
                  <button
                    className="session-row-main"
                    aria-current={item.id === activeId ? "true" : undefined}
                    onClick={() => { setActiveId(item.id); if (isNarrow()) setSidebarOpen(false); }}
                  >
                    <span>{item.title}</span><time>{displayTime(item.updatedAt)}</time>
                  </button>
                  {!archivedView && (
                    <button
                      type="button"
                      className="session-row-archive"
                      aria-label={`Archive session ${item.title || item.id}`}
                      aria-busy={itemArchiving || undefined}
                      title={archiveDisabledReason(item) || `Archive session ${item.title || item.id}`}
                      disabled={itemArchiving || !itemArchivable}
                      onClick={(event) => { event.stopPropagation(); archiveFromList(item); }}
                      onDoubleClick={(event) => event.stopPropagation()}
                      onMouseDown={(event) => event.stopPropagation()}
                    >
                      {itemArchiving ? <CircleNotch className="spin" size={15} /> : <Archive size={15} />}
                    </button>
                  )}
                </div>
              );
            })}
            {archivedView && !archivedSessions.length && (
              <p className="session-list-empty" role="status">No archived sessions yet.</p>
            )}
          </nav>
          <button
            className="archive-view-link"
            aria-pressed={archivedView}
            onClick={toggleArchivedView}
          >
            {archivedView ? <ClockCounterClockwise size={19} /> : <Archive size={19} />}
            {archivedView ? "Back to Recent Sessions" : "Archived Sessions"}
          </button>
        </div>
        <button className="settings-link" ref={settingsTriggerRef} onClick={() => setSettingsOpen(true)}><Gear size={20} />Settings</button>
      </aside>

      <section className="workspace">
        <header className="workspace-header">
          <div>
            <h1>{activeSession?.title || "AgentHub"}</h1>
            <div className="running-state"><span className="status-dot" /><span>{activeSession?.agentName || "No agent"}</span><span className="separator-dot">·</span><strong>{sessionStatusLabel(activeSession)}</strong></div>
          </div>
          <div className="header-actions">
            <button className="icon-button mobile-sidebar-toggle" aria-label="Toggle session list" onClick={() => setSidebarOpen((value) => !value)}><List size={20} /></button>
            {activeSession && !isArchived(activeSession) && <button className="icon-button" aria-label="Stop or interrupt session" title="Stop or interrupt" disabled={activeSession.state === "stopping" || activeSession.state === "stopped"} onClick={stopSession}><Stop size={19} /></button>}
            <button className="icon-button details-toggle" aria-label="Toggle details panel" onClick={() => setDetailsOpen((value) => !value)}>{detailsOpen ? <CaretRight size={18} /> : <SidebarSimple size={18} />}</button>
          </div>
        </header>

        <div className="conversation" ref={conversationRef} onScroll={onConversationScroll}>
          {error && <div className="error-banner">{error}<button aria-label="Dismiss error" onClick={() => setError("")}><X size={15} /></button></div>}
          {timeline.length ? (
            <Timeline items={timeline} agent={activeSession?.agentName || "Agent"} onApproval={resolveApproval} />
          ) : (
            <div className="empty-state"><span className="empty-icon"><Plus size={24} /></span><h2>Start a new Session</h2><p>Pick a local agent, set a working directory, and start the conversation.</p></div>
          )}
        </div>

        <div className={`composer ${isResumable(activeSession) ? "composer-resume" : ""}`}>
          {isResumable(activeSession) ? (
            <div className="resume-session-prompt" role="status">
              <div className="resume-session-copy">
                <strong>Session stopped</strong>
                <span>Resume the provider to continue this conversation.</span>
                {activeResumeError && <span className="resume-session-error" role="alert">{activeResumeError}</span>}
              </div>
              <button
                type="button"
                className="resume-session-button"
                aria-label="Resume session"
                aria-busy={resumingId === activeSession.id || undefined}
                disabled={resumingId === activeSession.id}
                onClick={resumeSession}
              >
                {resumingId === activeSession.id
                  ? <><CircleNotch className="spin" size={17} />Resuming…</>
                  : <><Play size={17} weight="fill" />Resume session</>}
              </button>
            </div>
          ) : (
            <>
              <textarea aria-label="Message" value={draft} disabled={!activeSession || activeSession.state === "stopping" || isArchived(activeSession)} onChange={(event) => setDraft(event.target.value)}
                onKeyDown={(event) => { if (event.key === "Enter" && (event.metaKey || event.ctrlKey)) sendMessage(); }} placeholder={isArchived(activeSession) ? "Archived sessions are read-only" : "Type a message…"} />
              <div className="composer-footer">
                <span className="composer-agent">{activeSession?.agentName || "Create a session to chat"}</span>
                <button className="send-button" aria-label="Send message" onClick={sendMessage} disabled={!draft.trim() || !activeSession || activeSession.state === "stopping" || isArchived(activeSession)}><PaperPlaneTilt size={20} weight="fill" /></button>
              </div>
            </>
          )}
        </div>
      </section>

      <aside className="details">
        <div className="details-heading"><strong>Details</strong></div>
        <div className="detail-row"><div><span className="detail-label">Working directory</span><code>{activeSession?.cwd || "—"}</code></div></div>
        <div className="detail-row"><div><span className="detail-label">Provider</span><code>{activeSession?.provider || "—"}</code></div></div>
        <div className="detail-row">
          <div><span className="detail-label">Session ID</span><code className="session-id">{activeSession?.id || "—"}</code></div>
          <button className="icon-button" aria-label="Copy session ID" title="Copy session ID" onClick={async () => { await navigator.clipboard?.writeText(activeSession?.id || ""); setCopied(true); setTimeout(() => setCopied(false), 1000); }}>{copied ? <Check size={18} /> : <Copy size={18} />}</button>
        </div>
        <div className="detail-row"><div><span className="detail-label">Provider session ID</span><code>{activeSession?.providerSessionId || "—"}</code></div></div>
        {activeSession && isArchived(activeSession) && (
          <div className="detail-row archive-row">
            <p className="archived-note">
              <Archive size={16} />This session is archived and read-only. Its files live in{" "}
              <code>sessions/Archive/{activeSession.id}/</code>.
            </p>
          </div>
        )}
      </aside>

      {settingsOpen && (
        <SettingsModal
          triggerRef={settingsTriggerRef}
          onClose={() => setSettingsOpen(false)}
          onSaved={() => loadAgents().catch(() => {})}
        />
      )}

      {newSessionOpen && (
        <NewSessionModal
          agents={agents}
          providers={providers}
          defaultAgentName={defaultAgentName}
          defaultCwd={activeSession?.cwd || ""}
          submitting={creating}
          error={createError}
          onSubmit={createSession}
          onClose={() => { if (!creating) setNewSessionOpen(false); }}
          triggerRef={newSessionTriggerRef}
        />
      )}
    </main>
  );
}
