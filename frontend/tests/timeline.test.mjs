import assert from "node:assert/strict";
import test from "node:test";
import { buildTimeline, parseToolEvent, truncateText, humanizeName } from "../src/timeline.js";

let nextId = 1;
function event(type, data, extra = {}) {
  return { id: nextId++, time: "2026-07-25T10:00:00Z", type, sessionId: "ses_test", ...extra, data };
}

function reset() {
  nextId = 1;
}

test("humanizeName and truncateText helpers", () => {
  assert.equal(humanizeName("mcpToolCall"), "Mcp Tool Call");
  assert.equal(humanizeName("web_search"), "Web search");
  assert.equal(humanizeName(""), "");
  assert.equal(truncateText("abc", 10), "abc");
  assert.equal(truncateText("abcdefghij", 5), "abcd…");
});

test("user and assistant messages merge deltas per turn", () => {
  reset();
  const items = buildTimeline([
    event("message.user", { text: "hello" }, { turnId: "turn_1" }),
    event("turn.started", { text: "hello" }, { turnId: "turn_1" }),
    event("message.assistant.delta", { text: "Hi" }, { turnId: "turn_1" }),
    event("message.assistant.delta", { text: " there" }, { turnId: "turn_1" }),
    event("turn.completed", {}, { turnId: "turn_1" }),
  ]);
  assert.deepEqual(items.map((item) => item.kind), ["message", "lifecycle", "message", "lifecycle"]);
  assert.equal(items[2].text, "Hi there");
  assert.equal(items[2].role, "agent");
});

test("reasoning deltas merge into a thinking block and stay active at the tail", () => {
  reset();
  const items = buildTimeline([
    event("message.reasoning.delta", { text: "Let me " }, { turnId: "turn_1" }),
    event("message.reasoning.delta", { text: "think." }, { turnId: "turn_1" }),
  ]);
  assert.equal(items.length, 1);
  assert.equal(items[0].kind, "thinking");
  assert.equal(items[0].text, "Let me think.");
  assert.equal(items[0].active, true);
});

test("thinking is no longer active once later events exist", () => {
  reset();
  const items = buildTimeline([
    event("message.reasoning.delta", { text: "plan" }, { turnId: "turn_1" }),
    event("message.assistant.delta", { text: "answer" }, { turnId: "turn_1" }),
  ]);
  assert.equal(items[0].active, false);
});

test("codex command execution tool call is normalized and completed", () => {
  reset();
  const items = buildTimeline([
    event("tool.event", {
      method: "item/started",
      raw: { item: { id: "item_1", type: "commandExecution", command: ["ls", "-la"], status: "inProgress" } },
    }, { turnId: "turn_1" }),
    event("tool.event", {
      method: "item/completed",
      raw: { item: { id: "item_1", type: "commandExecution", command: ["ls", "-la"], status: "completed", aggregatedOutput: "total 0", exitCode: 0 } },
    }, { turnId: "turn_1" }),
  ]);
  assert.equal(items.length, 1);
  assert.equal(items[0].kind, "tools");
  assert.equal(items[0].calls.length, 1);
  const call = items[0].calls[0];
  assert.equal(call.name, "Command");
  assert.equal(call.summary, "ls -la");
  assert.equal(call.status, "completed");
  assert.equal(call.output, "total 0");
  assert.equal(items[0].collapsed, false); // last item stays open
});

test("codex failed command surfaces the exit code as an error", () => {
  reset();
  const items = buildTimeline([
    event("tool.event", {
      method: "item/completed",
      raw: { item: { id: "item_9", type: "commandExecution", command: "false", status: "completed", exitCode: 1 } },
    }),
  ]);
  assert.equal(items[0].calls[0].status, "failed");
  assert.equal(items[0].calls[0].error, "Exit code 1");
});

test("codex message and reasoning items are not rendered as tool calls", () => {
  reset();
  const items = buildTimeline([
    event("tool.event", { method: "item/started", raw: { item: { id: "a", type: "agentMessage" } } }),
    event("tool.event", { method: "item/started", raw: { item: { id: "b", type: "reasoning" } } }),
    event("tool.event", { method: "item/started", raw: { item: { id: "c", type: "webSearch", query: "agenthub" } } }),
  ]);
  assert.equal(items.length, 1);
  assert.equal(items[0].calls.length, 1);
  assert.equal(items[0].calls[0].name, "Web search");
  assert.equal(items[0].calls[0].summary, "agenthub");
});

test("acp tool_call and tool_call_update correlate by toolCallId", () => {
  reset();
  const items = buildTimeline([
    event("tool.event", {
      method: "session/update",
      raw: { update: { sessionUpdate: "tool_call", toolCallId: "call_1", title: "Read README.md", kind: "read", status: "in_progress" } },
    }),
    event("tool.event", {
      method: "session/update",
      raw: { update: { sessionUpdate: "tool_call_update", toolCallId: "call_1", status: "completed", content: [{ type: "content", content: { type: "text", text: "file body" } }] } },
    }),
  ]);
  assert.equal(items.length, 1);
  assert.equal(items[0].calls.length, 1);
  assert.equal(items[0].calls[0].summary, "Read README.md");
  assert.equal(items[0].calls[0].status, "completed");
  assert.equal(items[0].calls[0].output, "file body");
});

test("acp thought chunks become thinking, message chunks become agent text", () => {
  reset();
  const items = buildTimeline([
    event("message.reasoning.delta", { text: "kimi thinking", method: "session/update" }, { turnId: "turn_1" }),
    event("message.assistant.delta", { text: "kimi answer", method: "session/update" }, { turnId: "turn_1" }),
  ]);
  assert.deepEqual(items.map((item) => item.kind), ["thinking", "message"]);
});

test("pi tool execution start and end pair into one call", () => {
  reset();
  const items = buildTimeline([
    event("tool.event", { method: "tool_execution_start", raw: { toolName: "read", args: { path: "README.md" } } }),
    event("tool.event", { method: "tool_execution_end", raw: { toolName: "read", result: "contents" } }),
  ]);
  assert.equal(items.length, 1);
  assert.equal(items[0].calls.length, 1);
  assert.equal(items[0].calls[0].status, "completed");
  assert.equal(items[0].calls[0].output, "contents");
});

test("pi tool failure is flagged", () => {
  reset();
  const items = buildTimeline([
    event("tool.event", { method: "tool_execution_start", raw: { toolName: "bash", args: { command: "make" } } }),
    event("tool.event", { method: "tool_execution_end", raw: { toolName: "bash", isError: true, error: "boom" } }),
  ]);
  assert.equal(items[0].calls[0].status, "failed");
  assert.equal(items[0].calls[0].error, "boom");
});

test("consecutive tool calls group together and collapse after a later message", () => {
  reset();
  const items = buildTimeline([
    event("tool.event", { method: "item/completed", raw: { item: { id: "1", type: "commandExecution", command: "ls", status: "completed" } } }),
    event("tool.event", { method: "item/completed", raw: { item: { id: "2", type: "commandExecution", command: "pwd", status: "completed" } } }),
    event("message.assistant.delta", { text: "done" }, { turnId: "turn_1" }),
  ]);
  assert.equal(items[0].kind, "tools");
  assert.equal(items[0].calls.length, 2);
  assert.equal(items[0].collapsed, true);
});

test("approvals pair requested and resolved events", () => {
  reset();
  const items = buildTimeline([
    event("approval.requested", { approvalId: "ap_1", method: "item/commandExecution/requestApproval", params: { command: ["rm", "-rf", "build"] } }),
    event("approval.resolved", { approvalId: "ap_1", decision: "accept" }),
  ]);
  assert.equal(items.length, 1);
  assert.equal(items[0].kind, "approval");
  assert.equal(items[0].title, "Run command");
  assert.equal(items[0].detail, "rm -rf build");
  assert.equal(items[0].status, "accepted");
  assert.equal(items[0].decision, "Allowed");
});

test("a pending approval stays actionable", () => {
  reset();
  const items = buildTimeline([
    event("approval.requested", { approvalId: "ap_2", method: "session/request_permission", params: { toolCall: { title: "Write file" } } }),
  ]);
  assert.equal(items[0].status, "pending");
  assert.equal(items[0].title, "Write file");
});

test("provider errors, lifecycle and notable session states are visible", () => {
  reset();
  const items = buildTimeline([
    event("session.created", { id: "ses_test" }),
    event("session.provider", { agentId: "codex-default", provider: "codex" }),
    event("session.state", { state: "ready" }), // low-value transition, folded away
    event("provider.error", { message: "stream reset" }),
    event("turn.failed", { error: "provider died" }, { turnId: "turn_1" }),
    event("session.state", { state: "failed" }),
  ]);
  assert.deepEqual(items.map((item) => item.kind), ["lifecycle", "lifecycle", "error", "lifecycle", "lifecycle"]);
  assert.equal(items[1].text, "Agent connected · codex-default · via codex");
  assert.equal(items[2].text, "stream reset");
  assert.equal(items[3].text, "Turn failed: provider died");
  assert.equal(items[4].text, "Session failed");
  assert.equal(items[4].tone, "danger");
});

test("provider noise folds into a single activity group", () => {
  reset();
  const items = buildTimeline([
    event("provider.event", { method: "thread/tokenUsage/updated" }),
    event("provider.metadata", { method: "account/rateLimits/updated" }),
    event("provider.stderr", { text: "warning: something" }),
    event("message.user", { text: "next" }),
    event("provider.event", { method: "thread/status/changed" }),
  ]);
  assert.deepEqual(items.map((item) => item.kind), ["activity", "message", "activity"]);
  assert.equal(items[0].entries.length, 3);
  assert.equal(items[0].entries[2].label, "stderr: warning: something");
  assert.equal(items[2].entries.length, 1);
});

test("unknown event types get a safe fallback entry instead of disappearing", () => {
  reset();
  const items = buildTimeline([
    event("provider.brand.new.thing", { nested: { value: 1 } }),
    { id: 99, time: "2026-07-25T10:00:00Z", type: "", sessionId: "ses_test" },
  ]);
  assert.equal(items[0].kind, "unknown");
  assert.equal(items[0].type, "provider.brand.new.thing");
  assert.match(items[0].preview, /nested/);
  assert.equal(items[1].kind, "unknown");
  assert.equal(items[1].type, "unknown");
});

test("parseToolEvent handles unknown tool methods with a diagnostic fallback", () => {
  const parsed = parseToolEvent(event("tool.event", { method: "mystery/tool", raw: { id: "x" } }));
  assert.equal(parsed.name, "Tool");
  assert.equal(parsed.summary, "mystery/tool");
});

test("provider turn notifications fold into activity, not unknown entries", () => {
  reset();
  const items = buildTimeline([
    event("provider.turn.started", { method: "turn/started" }),
    event("provider.turn.completed", { method: "turn/completed" }),
  ]);
  assert.equal(items.length, 1);
  assert.equal(items[0].kind, "activity");
  assert.deepEqual(items[0].entries.map((entry) => entry.label), ["turn/started", "turn/completed"]);
});

test("history rebuild and live streaming produce the same timeline", () => {
  reset();
  const streamed = [
    event("message.user", { text: "hi" }, { turnId: "turn_1" }),
    event("message.reasoning.delta", { text: "a" }, { turnId: "turn_1" }),
    event("message.reasoning.delta", { text: "b" }, { turnId: "turn_1" }),
    event("message.assistant.delta", { text: "x" }, { turnId: "turn_1" }),
    event("message.assistant.delta", { text: "y" }, { turnId: "turn_1" }),
  ];
  const live = buildTimeline(streamed);
  reset();
  // Rebuilding from the persisted log yields identical events (ids included).
  const rebuilt = buildTimeline(streamed.map((item, index) => ({ ...item, id: index + 1 })));
  assert.deepEqual(rebuilt, live);
});
