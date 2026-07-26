// Timeline builder: turns the persisted Session event log into display items
// for the conversation view. This module is pure (no DOM, no React) so it can
// be unit tested directly with node --test.
//
// The same builder is used for live SSE events and for events reloaded from
// the daemon after a page refresh, so both paths render identical output.

const MAX_PREVIEW = 400;
const MAX_OUTPUT = 12000;

export function displayTime(value) {
  const date = new Date(value);
  return Number.isNaN(date.valueOf())
    ? ""
    : date.toLocaleTimeString("en-US", { hour: "2-digit", minute: "2-digit" });
}

export function truncateText(value, max = MAX_PREVIEW) {
  const text = String(value ?? "");
  return text.length > max ? `${text.slice(0, max - 1)}…` : text;
}

function safePreview(data) {
  if (data === undefined || data === null) return "";
  try {
    return truncateText(JSON.stringify(data));
  } catch {
    return "";
  }
}

// Humanizes identifiers such as "mcpToolCall" or "web_search" into
// "Mcp tool call" style labels.
export function humanizeName(value) {
  const text = String(value || "")
    .replace(/[_-]+/g, " ")
    .replace(/([a-z0-9])([A-Z])/g, "$1 $2")
    .trim();
  if (!text) return "";
  return text.charAt(0).toUpperCase() + text.slice(1);
}

function joinCommand(value) {
  if (Array.isArray(value)) return value.filter((part) => typeof part === "string").join(" ");
  return typeof value === "string" ? value : "";
}

function firstString(...values) {
  for (const value of values) {
    if (typeof value === "string" && value.trim()) return value.trim();
  }
  return "";
}

function normalizeToolStatus(status) {
  const value = String(status || "").toLowerCase();
  if (["completed", "complete", "done", "success", "succeeded"].includes(value)) return "completed";
  if (["failed", "failure", "error", "declined", "denied", "cancelled", "canceled"].includes(value)) return "failed";
  return "running";
}

function contentText(content) {
  if (!Array.isArray(content)) return "";
  const parts = [];
  for (const block of content) {
    if (typeof block?.text === "string") parts.push(block.text);
    else if (typeof block?.content?.text === "string") parts.push(block.content.text);
    else if (block?.type === "diff" && typeof block?.path === "string") parts.push(`Edit ${block.path}`);
  }
  return parts.filter(Boolean).join("\n");
}

// Parses one normalized tool.call update out of a provider `tool.event`.
// Returns null for item types that are surfaced through other event types
// (chat messages, reasoning) and therefore must not appear as tool calls.
export function parseToolEvent(event) {
  const data = event?.data ?? {};
  const method = typeof data.method === "string" ? data.method : "";
  const raw = data.raw && typeof data.raw === "object" ? data.raw : {};
  const time = event?.time || "";

  // Codex app-server item notifications.
  if (method.startsWith("item/") || method.startsWith("command/")) {
    if (method === "item/commandExecution/outputDelta" || method === "command/exec/outputDelta") {
      const callId = firstString(raw.itemId, raw.callId, raw.id);
      if (!callId) return null;
      return {
        callId, method, time, deltaOnly: true,
        output: typeof raw.delta === "string" ? raw.delta : "",
      };
    }
    const item = raw.item && typeof raw.item === "object" ? raw.item : raw;
    const itemType = firstString(item.type);
    if (["userMessage", "agentMessage", "reasoning"].includes(itemType)) return null;
    const callId = firstString(item.id, raw.itemId);
    let name = humanizeName(itemType) || "Tool";
    let summary = "";
    let output = "";
    let error = "";
    if (itemType === "commandExecution") {
      name = "Command";
      summary = joinCommand(item.command) || firstString(item.cmd);
      output = firstString(item.aggregatedOutput, item.output);
      if (typeof item.exitCode === "number" && item.exitCode !== 0) {
        error = `Exit code ${item.exitCode}`;
      }
    } else if (itemType === "fileChange") {
      name = "File change";
      const paths = Array.isArray(item.changes)
        ? item.changes.map((change) => change?.path).filter(Boolean)
        : [];
      summary = paths.join(", ");
    } else if (itemType === "mcpToolCall") {
      name = "MCP";
      summary = [item.server, item.tool].filter((part) => typeof part === "string" && part).join(" / ");
      output = typeof item.result === "string" ? item.result : safePreview(item.result);
      error = firstString(item.error?.message, typeof item.error === "string" ? item.error : "");
    } else if (itemType === "webSearch") {
      name = "Web search";
      summary = firstString(item.query);
    } else {
      summary = firstString(item.title, item.name, joinCommand(item.command), item.path);
      output = firstString(item.output, item.aggregatedOutput);
    }
    let status = normalizeToolStatus(item.status);
    if (method === "item/started") status = "running";
    if (method === "item/completed" && status === "running") status = "completed";
    if (error && status === "completed") status = "failed";
    return {
      callId, method, time, name, status, error,
      summary: truncateText(summary.replace(/\s+/g, " ").trim(), 120),
      output: truncateText(output, MAX_OUTPUT),
    };
  }

  // ACP session/update tool_call and tool_call_update notifications.
  const update = raw.update && typeof raw.update === "object" ? raw.update : raw;
  const kind = firstString(update.sessionUpdate);
  if (kind === "tool_call" || kind === "tool_call_update") {
    const callId = firstString(update.toolCallId, update.id);
    const input = update.rawInput && typeof update.rawInput === "object" ? update.rawInput : {};
    const summary = firstString(
      update.title,
      joinCommand(input.command),
      input.path,
      input.filePath,
      humanizeName(update.kind),
    );
    return {
      callId, method, time,
      name: humanizeName(update.kind) || "Tool",
      status: normalizeToolStatus(update.status || (kind === "tool_call" ? "in_progress" : "")),
      summary: truncateText(summary.replace(/\s+/g, " ").trim(), 120),
      output: truncateText(contentText(update.content), MAX_OUTPUT),
      error: "",
    };
  }

  // Pi RPC tool execution notifications.
  if (method === "tool_execution_start" || method === "tool_execution_end") {
    const toolName = firstString(raw.toolName, raw.name, raw.tool);
    const args = raw.args && typeof raw.args === "object" ? raw.args : {};
    const summary = firstString(
      joinCommand(args.command),
      args.path,
      args.filePath,
      typeof args === "object" ? "" : "",
    );
    const failed = raw.isError === true || Boolean(firstString(raw.error));
    return {
      callId: firstString(raw.toolCallId, raw.callId, toolName),
      method,
      time,
      name: humanizeName(toolName) || "Tool",
      status: method === "tool_execution_start" ? "running" : failed ? "failed" : "completed",
      summary: truncateText(summary.replace(/\s+/g, " ").trim(), 120),
      output: truncateText(firstString(typeof raw.result === "string" ? raw.result : "", contentText(raw.result?.content)), MAX_OUTPUT),
      error: firstString(raw.error),
    };
  }

  // Unknown tool event: still surface it instead of dropping it.
  return {
    callId: firstString(raw.toolCallId, raw.itemId, raw.id),
    method,
    time,
    name: "Tool",
    status: method.includes("start") ? "running" : "completed",
    summary: method,
    output: "",
    error: "",
  };
}

function summarizeApproval(event) {
  const data = event?.data ?? {};
  const method = firstString(data.method);
  const params = data.params && typeof data.params === "object" ? data.params : {};
  const command = joinCommand(params.command) || joinCommand(params?.rawInput?.command);
  if (command) return { title: "Run command", detail: truncateText(command, 160) };
  const paths = Array.isArray(params.changes)
    ? params.changes.map((change) => change?.path).filter(Boolean)
    : [];
  if (params.toolCall && typeof params.toolCall === "object") {
    const title = firstString(params.toolCall.title, params.toolCall.kind && humanizeName(params.toolCall.kind));
    return { title: title || "Permission requested", detail: "" };
  }
  if (paths.length) return { title: "Apply file changes", detail: truncateText(paths.join(", "), 160) };
  if (method.includes("permissions")) return { title: "Grant permissions", detail: firstString(params.reason) };
  if (method.includes("fileChange")) return { title: "Apply file changes", detail: firstString(params.reason) };
  return { title: "Approval requested", detail: firstString(params.reason, method) };
}

const DECISION_LABELS = {
  accept: "Allowed",
  acceptForSession: "Allowed for this session",
  decline: "Declined",
  cancel: "Cancelled",
};

const NOTABLE_STATES = {
  failed: "Session failed",
  stopped: "Session stopped",
  archived: "Session archived",
};

// Low-value provider notifications that stay available but folded into a
// collapsible activity group so high-frequency updates do not add noise.
// provider.turn.* mirrors the manager-level turn.started/turn.completed
// lifecycle events, which are the ones rendered as timeline notes.
function isActivityType(type) {
  return type === "provider.event" || type === "provider.metadata" || type === "plan.event" ||
    type === "provider.stderr" || type === "provider.turn.started" || type === "provider.turn.completed";
}

function mergeToolCall(previous, update) {
  const next = { ...previous };
  if (update.name) next.name = update.name;
  if (update.summary) next.summary = update.summary;
  if (update.status) next.status = update.status;
  if (update.error) next.error = update.error;
  if (update.deltaOnly) {
    next.output = truncateText((next.output || "") + (update.output || ""), MAX_OUTPUT);
  } else if (update.output) {
    next.output = update.output;
  }
  next.time = update.time || previous.time;
  next.key = previous.key;
  return next;
}

function newToolCall(update, event) {
  return {
    key: event.id,
    callId: update.callId || "",
    name: update.name || "Tool",
    summary: update.summary || "",
    status: update.status || "completed",
    output: update.output || "",
    error: update.error || "",
    method: update.method || "",
    time: update.time || event.time || "",
    rawPreview: safePreview(event?.data?.raw),
  };
}

export function buildTimeline(events) {
  const items = [];
  const approvalsById = new Map();

  const pushActivity = (entry) => {
    const last = items.at(-1);
    if (last?.kind === "activity") {
      last.entries.push(entry);
      last.time = entry.time;
    } else {
      items.push({ kind: "activity", key: entry.key, entries: [entry], time: entry.time });
    }
  };

  for (const event of events || []) {
    const type = event?.type || "";
    const data = event?.data ?? {};
    const time = event?.time || "";
    switch (type) {
      case "message.user":
      case "message.user.steer":
        items.push({
          kind: "message", role: "user", key: event.id, time,
          steer: type === "message.user.steer",
          text: typeof data.text === "string" ? data.text : "",
        });
        break;
      case "message.assistant.delta": {
        const last = items.at(-1);
        const text = typeof data.text === "string" ? data.text : "";
        if (last?.kind === "message" && last.role === "agent" && last.turnId === event.turnId) {
          last.text += text;
          last.time = time;
        } else {
          items.push({ kind: "message", role: "agent", key: event.id, turnId: event.turnId || "", text, time });
        }
        break;
      }
      case "message.reasoning.delta": {
        const last = items.at(-1);
        const text = typeof data.text === "string" ? data.text : "";
        if (last?.kind === "thinking" && last.turnId === (event.turnId || "")) {
          last.text += text;
          last.time = time;
        } else {
          items.push({ kind: "thinking", key: event.id, turnId: event.turnId || "", text, time, active: false });
        }
        break;
      }
      case "tool.event": {
        const update = parseToolEvent(event);
        if (!update) break;
        const last = items.at(-1);
        const group = last?.kind === "tools" ? last : null;
        const calls = group ? group.calls : null;
        const existing = update.callId && calls
          ? calls.find((call) => call.callId === update.callId)
          : null;
        if (existing) {
          Object.assign(existing, mergeToolCall(existing, update));
          group.time = time;
        } else if (group) {
          group.calls.push(newToolCall(update, event));
          group.time = time;
        } else {
          items.push({ kind: "tools", key: event.id, calls: [newToolCall(update, event)], time });
        }
        break;
      }
      case "approval.requested": {
        const { title, detail } = summarizeApproval(event);
        const item = {
          kind: "approval", key: event.id, time,
          approvalId: firstString(data.approvalId),
          title, detail,
          status: "pending",
          decision: "",
        };
        if (item.approvalId) approvalsById.set(item.approvalId, item);
        items.push(item);
        break;
      }
      case "approval.resolved": {
        const approvalId = firstString(data.approvalId);
        const decision = firstString(data.decision) || "decline";
        const target = approvalId ? approvalsById.get(approvalId) : null;
        if (target) {
          target.status = decision === "accept" || decision === "acceptForSession" ? "accepted" : "declined";
          target.decision = DECISION_LABELS[decision] || humanizeName(decision);
          target.time = time;
        } else {
          items.push({
            kind: "approval", key: event.id, time, approvalId,
            title: "Approval resolved",
            detail: "",
            status: decision === "accept" || decision === "acceptForSession" ? "accepted" : "declined",
            decision: DECISION_LABELS[decision] || humanizeName(decision),
          });
        }
        break;
      }
      case "provider.error":
        items.push({
          kind: "error", key: event.id, time,
          text: firstString(data.message, "The provider reported an error"),
        });
        break;
      case "turn.started":
        items.push({ kind: "lifecycle", tone: "muted", key: event.id, time, text: "Turn started" });
        break;
      case "turn.completed":
        items.push({ kind: "lifecycle", tone: "ok", key: event.id, time, text: "Turn completed" });
        break;
      case "turn.failed":
        items.push({
          kind: "lifecycle", tone: "danger", key: event.id, time,
          text: `Turn failed${firstString(data.error, data.message) ? `: ${firstString(data.error, data.message)}` : ""}`,
        });
        break;
      case "turn.cancelled":
        items.push({ kind: "lifecycle", tone: "muted", key: event.id, time, text: "Turn interrupted" });
        break;
      case "session.created":
        items.push({ kind: "lifecycle", tone: "muted", key: event.id, time, text: "Session created" });
        break;
      case "session.provider": {
        // agentId is the legacy field written before agent ids were removed.
        const agent = firstString(data.agentName, data.agentId);
        const providerName = firstString(data.provider);
        const parts = ["Agent connected"];
        if (agent) parts.push(agent);
        if (providerName) parts.push(`via ${providerName}`);
        items.push({ kind: "lifecycle", tone: "muted", key: event.id, time, text: parts.join(" · ") });
        break;
      }
      case "session.state": {
        const label = NOTABLE_STATES[data.state];
        if (label) items.push({ kind: "lifecycle", tone: data.state === "failed" ? "danger" : "muted", key: event.id, time, text: label });
        break;
      }
      case "session.archived":
        items.push({ kind: "lifecycle", tone: "muted", key: event.id, time, text: "Session archived" });
        break;
      default: {
        if (isActivityType(type)) {
          const label = type === "provider.stderr"
            ? `stderr: ${truncateText(firstString(data.text), 120)}`
            : firstString(data.method, type);
          pushActivity({ key: event.id, label: label || type, time });
        } else {
          items.push({
            kind: "unknown", key: event.id, time, type: type || "unknown",
            preview: safePreview(data),
          });
        }
      }
    }
  }

  // A trailing thinking block means the agent is still reasoning.
  const last = items.at(-1);
  if (last?.kind === "thinking") last.active = true;

  // Collapse finished tool groups once the conversation has moved on.
  items.forEach((item, index) => {
    if (item.kind !== "tools") return;
    const settled = item.calls.every((call) => call.status !== "running");
    item.collapsed = settled && index < items.length - 1;
  });

  return items;
}
