import { useEffect, useMemo, useRef, useState } from "react";
import {
  CaretRight, Check, Copy, Gear, List, PaperPlaneTilt, Plus,
  SidebarSimple, Stop, TerminalWindow, User, X,
} from "@phosphor-icons/react";
import { api } from "./api";
import { SettingsModal } from "./settings/SettingsModal.jsx";

const eventTypes = [
  "session.created", "session.state", "session.provider", "message.user",
  "message.user.steer", "message.assistant.delta", "message.reasoning.delta",
  "tool.event", "approval.requested", "approval.resolved", "turn.started",
  "turn.completed", "turn.failed", "turn.cancelled", "provider.error", "provider.stderr",
];

function displayTime(value) {
  const date = new Date(value);
  return Number.isNaN(date.valueOf()) ? "" : date.toLocaleTimeString("en-US", { hour: "2-digit", minute: "2-digit" });
}

function messagesFromEvents(events) {
  const messages = [];
  for (const event of events) {
    if (event.type === "message.user" || event.type === "message.user.steer") {
      messages.push({ key: event.id, role: "user", text: event.data?.text || "", time: displayTime(event.time) });
    } else if (event.type === "message.assistant.delta") {
      const previous = messages.at(-1);
      if (previous?.role === "agent" && previous.turnId === event.turnId) {
        previous.text += event.data?.text || "";
      } else {
        messages.push({ key: event.id, role: "agent", turnId: event.turnId, text: event.data?.text || "", time: displayTime(event.time) });
      }
    } else if (event.type === "approval.requested") {
      messages.push({ key: event.id, role: "approval", approvalId: event.data?.approvalId, text: event.data?.method || "Approval requested", time: displayTime(event.time) });
    } else if (event.type === "provider.error") {
      messages.push({ key: event.id, role: "error", text: event.data?.message || "The provider reported an error", time: displayTime(event.time) });
    }
  }
  return messages;
}

function Message({ message, agent, onApproval }) {
  if (message.role === "approval") {
    return (
      <article className="notice-card">
        <strong>Approval required</strong><span>{message.text}</span>
        <div>
          <button onClick={() => onApproval(message.approvalId, "decline")}>Decline</button>
          <button className="primary-small" onClick={() => onApproval(message.approvalId, "accept")}>Allow once</button>
        </div>
      </article>
    );
  }
  const isUser = message.role === "user";
  return (
    <article className={`message ${isUser ? "message-user" : "message-agent"} ${message.role === "error" ? "message-error" : ""}`}>
      <span className={`avatar ${isUser ? "avatar-user" : "avatar-agent"}`}>{isUser ? <User size={17} weight="bold" /> : <TerminalWindow size={19} weight="bold" />}</span>
      <div className="message-body">
        <div className="message-meta"><strong>{isUser ? "You" : agent}</strong><span>{message.time}</span></div>
        <p>{message.text}</p>
      </div>
    </article>
  );
}

export function App() {
  const [sessions, setSessions] = useState([]);
  const [agents, setAgents] = useState([]);
  const [activeId, setActiveId] = useState("");
  const [events, setEvents] = useState([]);
  const [selectedAgent, setSelectedAgent] = useState("");
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
  const [error, setError] = useState("");
  const [copied, setCopied] = useState(false);

  const activeSession = useMemo(() => sessions.find((item) => item.id === activeId), [sessions, activeId]);
  const messages = useMemo(() => messagesFromEvents(events), [events]);

  const refreshSessions = async () => {
    const body = await api("/v1/sessions");
    setSessions(body.sessions);
    setActiveId((current) => current || body.sessions[0]?.id || "");
  };

  useEffect(() => {
    Promise.all([refreshSessions(), api("/v1/agents")])
      .then(([, body]) => {
        setAgents(body.agents || []);
        setSelectedAgent(body.defaultChatAgentId || body.agents?.[0]?.id || "");
      })
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
        const receive = (message) => {
          const event = JSON.parse(message.data);
          setEvents((current) => current.some((item) => item.id === event.id) ? current : [...current, event]);
          if (/^(session|turn|approval)\./.test(event.type)) refreshSessions().catch(() => {});
        };
        eventTypes.forEach((type) => source.addEventListener(type, receive));
        source.onerror = () => {};
      })
      .catch((value) => setError(value.message));
    return () => { disposed = true; source?.close(); };
  }, [activeId]);

  const startNewSession = async () => {
    const cwd = window.prompt("Working directory", activeSession?.cwd || "");
    if (!cwd) return;
    setError("");
    try {
      const body = await api("/v1/sessions", { method: "POST", body: JSON.stringify({ cwd, title: "New Session", agentId: selectedAgent }) });
      await refreshSessions();
      setActiveId(body.session.id);
    } catch (value) { setError(value.message); }
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

  const refreshAgents = async () => {
    try {
      const body = await api("/v1/agents");
      const list = body.agents || [];
      setAgents(list);
      setSelectedAgent((current) => (
        list.some((agent) => agent.id === current)
          ? current
          : body.defaultChatAgentId || list[0]?.id || ""
      ));
    } catch (value) { setError(value.message); }
  };

  return (
    <main className={`app-shell ${sidebarOpen ? "" : "sidebar-collapsed"} ${detailsOpen ? "" : "details-collapsed"}`}>
      <aside className="sidebar">
        <div className="sidebar-top">
          <div className="brand">AgentHub</div>
          <button className="new-session" onClick={startNewSession}><Plus size={20} />New Session</button>
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
            <div className="running-state"><span className="status-dot" /><span>{activeSession?.agentId || selectedAgent || "No agent selected"}</span><span className="separator-dot">·</span><strong>{activeSession?.state || "No session yet"}</strong></div>
          </div>
          <div className="header-actions">
            <button className="icon-button mobile-sidebar-toggle" aria-label="Toggle session list" onClick={() => setSidebarOpen((value) => !value)}><List size={20} /></button>
            {activeSession && <button className="icon-button" aria-label="Stop or interrupt session" title="Stop or interrupt" onClick={stopSession}><Stop size={19} /></button>}
            <button className="icon-button details-toggle" aria-label="Toggle details panel" onClick={() => setDetailsOpen((value) => !value)}>{detailsOpen ? <CaretRight size={18} /> : <SidebarSimple size={18} />}</button>
          </div>
        </header>

        <div className="conversation">
          {error && <div className="error-banner">{error}<button aria-label="Dismiss error" onClick={() => setError("")}><X size={15} /></button></div>}
          {messages.length ? messages.map((message) => <Message key={message.key} message={message} agent={activeSession?.agentId || "Agent"} onApproval={resolveApproval} />) : (
            <div className="empty-state"><span className="empty-icon"><Plus size={24} /></span><h2>Start a new Session</h2><p>Pick a local agent, set a working directory, and start the conversation.</p></div>
          )}
        </div>

        <div className="composer">
          <textarea aria-label="Message" value={draft} disabled={!activeSession || activeSession.state === "stopped"} onChange={(event) => setDraft(event.target.value)}
            onKeyDown={(event) => { if (event.key === "Enter" && (event.metaKey || event.ctrlKey)) sendMessage(); }} placeholder="Type a message…" />
          <div className="composer-footer">
            <select className="agent-select" aria-label="Agent" value={selectedAgent} onChange={(event) => setSelectedAgent(event.target.value)} disabled={Boolean(activeSession)}>
              {agents.map((agent) => <option key={agent.id} value={agent.id}>{agent.name || agent.id}</option>)}
            </select>
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
          onSaved={refreshAgents}
        />
      )}
    </main>
  );
}
