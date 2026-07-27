import {
  Brain, CaretRight, CheckCircle, CircleNotch, Clock, Info, Package,
  ShieldWarning, TerminalWindow, User, WarningCircle, Wrench, XCircle,
} from "@phosphor-icons/react";
import { MarkdownMessage } from "./MarkdownMessage.js";
import { displayTime } from "./timeline.js";

function ToolStatusIcon({ status }) {
  if (status === "running") return <CircleNotch className="spin" size={15} aria-label="Running" />;
  if (status === "failed") return <XCircle size={15} aria-label="Failed" />;
  return <CheckCircle size={15} aria-label="Completed" />;
}

function MessageItem({ item, agent }) {
  const isUser = item.role === "user";
  return (
    <article className={`message ${isUser ? "message-user" : "message-agent"}`}>
      <span className={`avatar ${isUser ? "avatar-user" : "avatar-agent"}`}>
        {isUser ? <User size={17} weight="bold" /> : <TerminalWindow size={19} weight="bold" />}
      </span>
      <div className="message-body">
        <div className="message-meta">
          <strong>{isUser ? "You" : agent}</strong>
          {item.steer ? <span className="message-tag">steer</span> : null}
          <span>{displayTime(item.time)}</span>
        </div>
        {isUser ? <p>{item.text}</p> : <MarkdownMessage text={item.text} />}
      </div>
    </article>
  );
}

function ThinkingItem({ item }) {
  return (
    <details className={`thinking-note ${item.active ? "thinking-active" : ""}`} open={item.active}>
      <summary>
        <Brain size={16} />
        <span>{item.active ? "Thinking…" : "Thinking"}</span>
        <span className="note-time">{displayTime(item.time)}</span>
        <CaretRight className="note-chevron" size={14} />
      </summary>
      <p>{item.text}</p>
    </details>
  );
}

function ToolCallRow({ call }) {
  const hasDetails = Boolean(call.output || call.error || call.rawPreview);
  const label = [call.name, call.summary].filter(Boolean).join(" · ");
  return (
    <details className={`tool-item tool-${call.status}`}>
      <summary>
        <ToolStatusIcon status={call.status} />
        <span className="tool-item-label" title={label}>{label || "Tool call"}</span>
        <span className="note-time">{displayTime(call.time)}</span>
        {hasDetails ? <CaretRight className="note-chevron" size={14} /> : null}
      </summary>
      {hasDetails ? (
        <div className="tool-item-body">
          {call.error ? <p className="tool-item-error" role="alert">{call.error}</p> : null}
          {call.output ? <pre>{call.output}</pre> : null}
          {call.rawPreview ? (
            <details className="tool-raw">
              <summary><Package size={13} />Raw event</summary>
              <pre>{call.rawPreview}</pre>
            </details>
          ) : null}
        </div>
      ) : null}
    </details>
  );
}

function ToolsItem({ item }) {
  const running = item.calls.filter((call) => call.status === "running").length;
  const failed = item.calls.filter((call) => call.status === "failed").length;
  const count = item.calls.length;
  const preview = item.calls
    .slice(0, 2)
    .map((call) => [call.name, call.summary].filter(Boolean).join(" · "))
    .filter(Boolean)
    .join(" · ");
  const remaining = Math.max(0, count - 2);
  return (
    <details className="tool-group" open={!item.collapsed}>
      <summary>
        <span className="tool-group-icon"><Wrench size={15} /></span>
        <span className="tool-group-title">
          {count} tool {count === 1 ? "call" : "calls"}
          {running ? ` · ${running} running` : ""}
          {failed ? ` · ${failed} failed` : ""}
        </span>
        <span className="tool-group-preview">
          {preview}{remaining ? ` · +${remaining} more` : ""}
        </span>
        <CaretRight className="note-chevron" size={14} />
      </summary>
      <div className="tool-list">
        {item.calls.map((call) => <ToolCallRow key={call.key} call={call} />)}
      </div>
    </details>
  );
}

function ApprovalItem({ item, onApproval }) {
  const tone = item.status === "pending" ? "pending" : item.status === "accepted" ? "accepted" : "declined";
  return (
    <article className={`approval-card approval-${tone}`}>
      <div className="approval-heading">
        <ShieldWarning size={17} />
        <strong>{item.title}</strong>
        <span className="note-time">{displayTime(item.time)}</span>
      </div>
      {item.detail ? <code className="approval-detail">{item.detail}</code> : null}
      {item.status === "pending" ? (
        <div className="approval-actions">
          <button onClick={() => onApproval(item.approvalId, "decline")}>Decline</button>
          <button className="primary-small" onClick={() => onApproval(item.approvalId, "accept")}>Allow once</button>
        </div>
      ) : (
        <span className={`approval-status approval-status-${tone}`} role="status">
          {item.status === "accepted" ? <CheckCircle size={14} /> : <XCircle size={14} />}
          {item.decision || (item.status === "accepted" ? "Allowed" : "Declined")}
        </span>
      )}
    </article>
  );
}

function LifecycleItem({ item }) {
  const Icon = item.tone === "danger" ? WarningCircle : item.tone === "ok" ? CheckCircle : item.tone === "info" ? Info : Clock;
  return (
    <div className={`lifecycle-note lifecycle-${item.tone}`} role="status">
      <Icon size={14} />
      <span>{item.text}</span>
      <span className="note-time">{displayTime(item.time)}</span>
    </div>
  );
}

function UnknownItem({ item }) {
  return (
    <details className="unknown-event">
      <summary>
        <Info size={14} />
        <span>Unhandled event: <code>{item.type}</code></span>
        <span className="note-time">{displayTime(item.time)}</span>
        <CaretRight className="note-chevron" size={14} />
      </summary>
      {item.preview ? <pre>{item.preview}</pre> : <p className="unknown-empty">This event carries no payload.</p>}
    </details>
  );
}

export function Timeline({ items, agent, onApproval }) {
  return items.map((item) => {
    switch (item.kind) {
      case "message":
        return <MessageItem key={item.key} item={item} agent={agent} />;
      case "thinking":
        return <ThinkingItem key={item.key} item={item} />;
      case "tools":
        return <ToolsItem key={item.key} item={item} />;
      case "approval":
        return <ApprovalItem key={item.key} item={item} onApproval={onApproval} />;
      case "lifecycle":
        return <LifecycleItem key={item.key} item={item} />;
      case "error":
        return (
          <article key={item.key} className="message message-error">
            <span className="avatar"><WarningCircle size={19} weight="bold" /></span>
            <div className="message-body">
              <div className="message-meta"><strong>Provider error</strong><span>{displayTime(item.time)}</span></div>
              <p>{item.text}</p>
            </div>
          </article>
        );
      case "unknown":
        return <UnknownItem key={item.key} item={item} />;
      default:
        return null;
    }
  });
}
