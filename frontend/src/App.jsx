import { useEffect, useMemo, useRef, useState } from "react";
import {
  CaretRight, Check, Copy, Gear, List, PaperPlaneTilt, Plus,
  SidebarSimple, Stop, X,
} from "@phosphor-icons/react";
import { api } from "./api";
import { buildTimeline, displayTime } from "./timeline.js";
import { Timeline } from "./Timeline.jsx";
import { NewSessionModal } from "./NewSessionModal.jsx";
import { SettingsModal } from "./settings/SettingsModal.jsx";

export function App() {
  const [sessions, setSessions] = useState([]);
  const [agents, setAgents] = useState([]);
  const [providers, setProviders] = useState([]);
  const [defaultAgentId, setDefaultAgentId] = useState("");
  const [activeId, setActiveId] = useState("");
  const [events, setEvents] = useState([]);
  const [draft, setDraft] = useState("");
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

  const activeSession = useMemo(() => sessions.find((item) => item.id === activeId), [sessions, activeId]);
  const timeline = useMemo(() => buildTimeline(events), [events]);

  const refreshSessions = async () => {
    const body = await api("/v1/sessions");
    setSessions(body.sessions);
    setActiveId((current) => current || body.sessions[0]?.id || "");
  };

  const loadAgents = async () => {
    const body = await api("/v1/agents");
    setAgents(body.agents || []);
    setProviders(body.providers || []);
    setDefaultAgentId(body.defaultChatAgentId || body.agents?.[0]?.id || "");
  };

  useEffect(() => {
    Promise.all([refreshSessions(), loadAgents()])
      .catch((value) => setError(value.message));
  }, []);

  useEffect(() => {
    if (!activeId) { setEvents([]); return undefined; }
    let source;
    let disposed = false;
    api(`/v1/sessions/${activeId}/events?limit=1000`)
      .then((body) => {
        if (disposed) return;
        setEvents(body.events || []);
        const after = body.events?.at(-1)?.id || 0;
        source = new EventSource(`/v1/sessions/${activeId}/events?stream=true&after=${after}`);
        // All events arrive on the default message channel (see the daemon's
        // writeSSE), so unknown future event types are never dropped here.
        source.onmessage = (message) => {
          const event = JSON.parse(message.data);
          setEvents((current) => current.some((item) => item.id === event.id) ? current : [...current, event]);
          if (/^(session|turn|approval)\./.test(event.type)) refreshSessions().catch(() => {});
        };
        source.onerror = () => {};
      })
      .catch((value) => setError(value.message));
    return () => { disposed = true; source?.close(); };
  }, [activeId]);

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
    if (!activeSession) return;
    try {
      const action = activeSession.currentTurnId ? "interrupt" : "stop";
      await api(`/v1/sessions/${activeSession.id}/${action}`, { method: "POST", body: "{}" });
      await refreshSessions();
    } catch (value) { setError(value.message); }
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
          <div className="session-label">Recent Sessions</div>
          <nav className="session-list" aria-label="Recent sessions">
            {sessions.map((item) => (
              <button key={item.id} className={`session-row ${item.id === activeId ? "active" : ""}`} onClick={() => { setActiveId(item.id); if (isNarrow()) setSidebarOpen(false); }}>
                <span>{item.title}</span><time>{displayTime(item.updatedAt)}</time>
              </button>
            ))}
          </nav>
        </div>
        <button className="settings-link" ref={settingsTriggerRef} onClick={() => setSettingsOpen(true)}><Gear size={20} />Settings</button>
      </aside>

      <section className="workspace">
        <header className="workspace-header">
          <div>
            <h1>{activeSession?.title || "AgentHub"}</h1>
            <div className="running-state"><span className="status-dot" /><span>{activeSession?.agentId || "No agent"}</span><span className="separator-dot">·</span><strong>{activeSession?.state || "No session yet"}</strong></div>
          </div>
          <div className="header-actions">
            <button className="icon-button mobile-sidebar-toggle" aria-label="Toggle session list" onClick={() => setSidebarOpen((value) => !value)}><List size={20} /></button>
            {activeSession && <button className="icon-button" aria-label="Stop or interrupt session" title="Stop or interrupt" onClick={stopSession}><Stop size={19} /></button>}
            <button className="icon-button details-toggle" aria-label="Toggle details panel" onClick={() => setDetailsOpen((value) => !value)}>{detailsOpen ? <CaretRight size={18} /> : <SidebarSimple size={18} />}</button>
          </div>
        </header>

        <div className="conversation" ref={conversationRef} onScroll={onConversationScroll}>
          {error && <div className="error-banner">{error}<button aria-label="Dismiss error" onClick={() => setError("")}><X size={15} /></button></div>}
          {timeline.length ? (
            <Timeline items={timeline} agent={activeSession?.agentId || "Agent"} onApproval={resolveApproval} />
          ) : (
            <div className="empty-state"><span className="empty-icon"><Plus size={24} /></span><h2>Start a new Session</h2><p>Pick a local agent, set a working directory, and start the conversation.</p></div>
          )}
        </div>

        <div className="composer">
          <textarea aria-label="Message" value={draft} disabled={!activeSession || activeSession.state === "stopped"} onChange={(event) => setDraft(event.target.value)}
            onKeyDown={(event) => { if (event.key === "Enter" && (event.metaKey || event.ctrlKey)) sendMessage(); }} placeholder="Type a message…" />
          <div className="composer-footer">
            <span className="composer-agent">{activeSession?.agentId || "Create a session to chat"}</span>
            <button className="send-button" aria-label="Send message" onClick={sendMessage} disabled={!draft.trim() || !activeSession}><PaperPlaneTilt size={20} weight="fill" /></button>
          </div>
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
          defaultAgentId={defaultAgentId}
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
