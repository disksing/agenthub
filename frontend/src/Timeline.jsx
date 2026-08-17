import { useState } from "react";
import {
  Brain, CaretRight, CheckCircle, CircleNotch, Clock, Info, Package,
  ShieldWarning, TerminalWindow, User, WarningCircle, Wrench, XCircle,
} from "@phosphor-icons/react";
import { MarkdownMessage } from "./MarkdownMessage.js";
import { displayDuration, displayTime } from "./display.js";

function ToolStatusIcon({ status }) {
  if (status === "running") return <CircleNotch className="spin" size={15} aria-label="Running" />;
  if (status === "failed") return <XCircle size={15} aria-label="Failed" />;
  return <CheckCircle size={15} aria-label="Completed" />;
}

function MessageItem({ item, agent }) {
	const role = item.role || "user";
	const isAssistant = role === "assistant";
	const sender = item.sender || {};
	const senderName = sender.name || sender.id || "";
	const name = isAssistant
		? agent
		: senderName || (role === "system" ? "System" : role === "agent" ? "Agent" : "You");
	const Icon = isAssistant ? TerminalWindow : role === "system" ? Info : User;
	const sourceSession = role === "agent"
		? sender.sessionId || "source session unavailable"
		: sender.sessionId || "";
	const sourceId = sender.id && sender.name ? `id ${sender.id}` : "";
	const sourceLabel = `${role} message from ${name}`;
	return (
		<article className={`message message-${role}`} aria-label={sourceLabel}>
			<span className={`avatar avatar-${role}`} aria-hidden="true">
				<Icon size={isAssistant ? 19 : 17} weight="bold" />
			</span>
			<div className="message-body">
				<div className="message-meta">
					<strong title={name}>{name}</strong>
					{role === "system" || role === "agent" ? <span className="message-role-tag">{role}</span> : null}
					{item.steer ? <span className="message-tag">steer</span> : null}
					{sourceSession ? <span className="message-source" title={sourceSession}>{role === "agent" ? `from session ${sourceSession}` : sourceSession}</span> : null}
					{sourceId ? <span className="message-source" title={sourceId}>{sourceId}</span> : null}
					<span>{displayTime(item.time)}</span>
				</div>
				{isAssistant ? <MarkdownMessage text={item.text} /> : <p>{item.text}</p>}
			</div>
		</article>
	);
}

function ThinkingItem({ item, embedded = false }) {
  const duration = item.active ? "" : displayDuration(item.startTime, item.time);
  const title = item.active ? "Thinking…" : duration ? `Thought for ${duration}` : "Thought";
  if (embedded) {
    return (
      <section className={`activity-thinking ${item.active ? "thinking-active" : ""}`}>
        <div className="activity-child-heading"><Brain size={16} /><span>{title}</span></div>
        <p>{item.text}</p>
      </section>
    );
  }
  return (
    <details className={`thinking-note ${item.active ? "thinking-active" : ""}`} open={item.active}>
      <summary>
        <Brain size={16} />
        <span>{title}</span>
        <span className="note-time">{displayTime(item.time)}</span>
        <CaretRight className="note-chevron" size={14} />
      </summary>
      <p>{item.text}</p>
    </details>
  );
}

function ToolCallRow({ call, expanded = false }) {
  const hasDetails = Boolean(call.output || call.error || call.rawPreview);
  const label = [call.name, call.summary].filter(Boolean).join(" · ");
  return (
    <details className={`tool-item tool-${call.status}`} open={expanded}>
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

function ToolsItem({ item, isOpen, embedded = false }) {
  const running = item.calls.filter((call) => call.status === "running").length;
  const failed = item.calls.filter((call) => call.status === "failed").length;
  const count = item.calls.length;
  const preview = item.calls
    .slice(0, 2)
    .map((call) => [call.name, call.summary].filter(Boolean).join(" · "))
    .filter(Boolean)
    .join(" · ");
  const remaining = Math.max(0, count - 2);
  const heading = (
    <>
      <span className="tool-group-icon"><Wrench size={15} /></span>
      <span className="tool-group-title">
        {count} tool {count === 1 ? "call" : "calls"}
        {running ? ` · ${running} running` : ""}
        {failed ? ` · ${failed} failed` : ""}
      </span>
      <span className="tool-group-preview">
        {preview}{remaining ? ` · +${remaining} more` : ""}
      </span>
    </>
  );
  if (embedded) {
    return (
      <section className="activity-tools">
        <div className="activity-child-heading">{heading}</div>
        <div className="tool-list">
          {item.calls.map((call) => <ToolCallRow key={call.key} call={call} expanded />)}
        </div>
      </section>
    );
  }
  return (
    <details className="tool-group" open={isOpen}>
      <summary>
        {heading}
        <CaretRight className="note-chevron" size={14} />
      </summary>
      <div className="tool-list">
        {item.calls.map((call) => <ToolCallRow key={call.key} call={call} />)}
      </div>
    </details>
  );
}

function ActivityItem({ item }) {
  const thoughts = Number(item.thinkingCount) || 0;
  const tools = Number(item.toolCallCount) || 0;
  const title = [
    thoughts ? `${thoughts} ${thoughts === 1 ? "thought" : "thoughts"}` : "",
    tools ? `${tools} tool ${tools === 1 ? "call" : "calls"}` : "",
  ].filter(Boolean).join(" · ") || "Agent activity";
  const preview = item.items
    .filter((child) => child.kind === "tools")
    .flatMap((child) => child.calls || [])
    .slice(0, 2)
    .map((call) => [call.name, call.summary].filter(Boolean).join(" · "))
    .filter(Boolean)
    .join(" · ");
  return (
    <details className={`activity-group ${item.active ? "activity-active" : ""}`} open={item.active}>
      <summary>
        <span className="activity-group-icon"><Wrench size={15} /></span>
        <span className="activity-group-title">{title}</span>
        {preview ? <span className="activity-group-preview">{preview}</span> : null}
        <span className="note-time">{displayTime(item.time)}</span>
        <CaretRight className="note-chevron" size={14} />
      </summary>
      <div className="activity-list">
        {item.items.map((child, index) => child.kind === "thinking"
          ? <ThinkingItem key={`${child.key}:thinking:${index}`} item={child} embedded />
          : <ToolsItem key={`${child.key}:tools:${index}`} item={child} embedded />)}
      </div>
    </details>
  );
}

function ApprovalItem({ item, onApproval }) {
  const [reply, setReply] = useState("");
  const tone = item.status === "pending" ? "pending" : item.status === "accepted" ? "accepted" : "declined";
  const options = Array.isArray(item.options) ? item.options : [];
  const submitReply = (event) => {
    event.preventDefault();
    const text = reply.trim();
    if (text) onApproval(item.approvalId, { text });
  };
  return (
    <article className={`approval-card approval-${tone}`}>
      <div className="approval-heading">
        <ShieldWarning size={17} />
        <strong>{item.title}</strong>
        <span className="note-time">{displayTime(item.time)}</span>
      </div>
      {item.question ? <p className="approval-question">{item.question}</p> : null}
      {item.detail ? <code className="approval-detail">{item.detail}</code> : null}
      {item.status === "pending" ? (
        <>
          {options.length ? (
            <div className="approval-options">
              {options.map((option) => (
                <button
                  key={option.optionId}
                  className={option.kind && option.kind.startsWith("reject") ? "approval-option-reject" : ""}
                  onClick={() => onApproval(item.approvalId, { optionId: option.optionId })}
                >
                  {option.name || humanizeApprovalKind(option.kind) || option.optionId}
                </button>
              ))}
            </div>
          ) : (
            <div className="approval-actions">
              <button onClick={() => onApproval(item.approvalId, { decision: "decline" })}>Decline</button>
              <button className="primary-small" onClick={() => onApproval(item.approvalId, { decision: "accept" })}>Allow once</button>
            </div>
          )}
          {item.question ? (
            <form className="approval-reply" onSubmit={submitReply}>
              <input
                value={reply}
                placeholder="Reply with a custom answer…"
                aria-label="Custom reply"
                onChange={(event) => setReply(event.target.value)}
              />
              <button type="submit" disabled={!reply.trim()}>Send</button>
            </form>
          ) : null}
        </>
      ) : (
        <span className={`approval-status approval-status-${tone}`} role="status">
          {item.status === "accepted" ? <CheckCircle size={14} /> : <XCircle size={14} />}
          {item.decision || (item.status === "accepted" ? "Allowed" : "Declined")}
          {item.reply ? `: ${item.reply}` : ""}
        </span>
      )}
    </article>
  );
}

function humanizeApprovalKind(kind) {
  return String(kind || "").replace(/[_-]+/g, " ").trim();
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
  return items.map((item, index) => {
    switch (item.kind) {
      case "message":
        return <MessageItem key={item.key} item={item} agent={agent} />;
      case "thinking":
        return <ThinkingItem key={item.key} item={item} />;
      case "tools":
        return (
          <ToolsItem
            key={item.key}
            item={item}
            isOpen={
              index === items.length - 1 ||
              item.calls.some((call) => call.status === "running")
            }
          />
        );
      case "activity":
        return <ActivityItem key={item.key} item={item} />;
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
