import assert from "node:assert/strict";
import test from "node:test";
import {
  API_EVENT_CONTRACT_VERSION,
  VERSION,
  buildTimeline,
} from "../src/index.js";

let nextId = 1;
function event(type, data, extra = {}) {
  return { id: nextId++, time: "2026-07-25T10:00:00Z", type, sessionId: "ses_test", ...extra, data };
}

function reset() {
  nextId = 1;
}

test("exports stable package and canonical event contract versions", () => {
  assert.equal(VERSION, "1.0.0");
  assert.equal(API_EVENT_CONTRACT_VERSION, "agenthub.api.v1");
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
  assert.equal(items[2].role, "assistant");
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

test("thinking block keeps the first delta time as startTime for the duration label", () => {
  reset();
  const items = buildTimeline([
    event("message.reasoning.delta", { text: "Let me " }, { turnId: "turn_1", time: "2026-07-25T10:00:00Z" }),
    event("message.reasoning.delta", { text: "think." }, { turnId: "turn_1", time: "2026-07-25T10:01:02Z" }),
    event("message.assistant.delta", { text: "answer" }, { turnId: "turn_1" }),
  ]);
  assert.equal(items[0].kind, "thinking");
  assert.equal(items[0].startTime, "2026-07-25T10:00:00Z");
  assert.equal(items[0].time, "2026-07-25T10:01:02Z");
});

test("assistant deltas without a turn id merge into one message", () => {
  reset();
  // Late provider chunks recorded after a turn terminal carry no turnId;
  // they must still merge instead of rendering one bubble per delta.
  const items = buildTimeline([
    event("turn.failed", { error: "prompt timed out" }, { turnId: "turn_1" }),
    event("message.assistant.delta", { text: "任务" }),
    event("message.assistant.delta", { text: "完成" }),
    event("message.assistant.delta", { text: "。" }),
  ]);
  assert.deepEqual(items.map((item) => item.kind), ["lifecycle", "message"]);
  assert.equal(items[1].role, "assistant");
  assert.equal(items[1].text, "任务完成。");
  assert.equal(items[1].turnId, "");
});

test("assistant deltas after a turn keep their own message per turn", () => {
  reset();
  const items = buildTimeline([
    event("message.assistant.delta", { text: "one" }, { turnId: "turn_1" }),
    event("message.assistant.delta", { text: "two" }, { turnId: "turn_2" }),
  ]);
  assert.equal(items.length, 2);
  assert.equal(items[0].text, "one");
  assert.equal(items[1].text, "two");
});

test("empty deltas do not create empty bubbles", () => {
  reset();
  const items = buildTimeline([
    event("message.reasoning.delta", { text: "" }),
    event("message.assistant.delta", { text: "" }),
    event("message.assistant.delta", { text: "answer" }),
  ]);
  assert.deepEqual(items.map((item) => item.kind), ["message"]);
  assert.equal(items[0].text, "answer");
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
  assert.equal("collapsed" in items[0], false);
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

test("consecutive tool calls group without projecting host expansion state", () => {
  reset();
  const items = buildTimeline([
    event("tool.event", { method: "item/completed", raw: { item: { id: "1", type: "commandExecution", command: "ls", status: "completed" } } }),
    event("tool.event", { method: "item/completed", raw: { item: { id: "2", type: "commandExecution", command: "pwd", status: "completed" } } }),
    event("message.assistant.delta", { text: "done" }, { turnId: "turn_1" }),
  ]);
  assert.equal(items[0].kind, "tools");
  assert.equal(items[0].calls.length, 2);
  assert.equal("collapsed" in items[0], false);
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

test("a question approval surfaces the question text and options", () => {
  reset();
  const items = buildTimeline([
    event("approval.requested", {
      approvalId: "ap_q",
      method: "session/request_permission",
      params: {
        toolCall: {
          toolCallId: "0:tool_1",
          title: "AskUserQuestion",
          content: [{ type: "content", content: { type: "text", text: "Which color do you prefer?" } }],
        },
        options: [
          { optionId: "q0_opt_0", name: "red", kind: "allow_once" },
          { optionId: "q0_opt_1", name: "blue", kind: "allow_once" },
          { optionId: "q0_skip", name: "Skip", kind: "reject_once" },
        ],
      },
    }),
  ]);
  assert.equal(items.length, 1);
  assert.equal(items[0].kind, "approval");
  assert.equal(items[0].status, "pending");
  assert.equal(items[0].question, "Which color do you prefer?");
  assert.deepEqual(items[0].options, [
    { optionId: "q0_opt_0", name: "red", kind: "allow_once" },
    { optionId: "q0_opt_1", name: "blue", kind: "allow_once" },
    { optionId: "q0_skip", name: "Skip", kind: "reject_once" },
  ]);
});

test("a resolved question shows the selected option name", () => {
  reset();
  const items = buildTimeline([
    event("approval.requested", {
      approvalId: "ap_q",
      method: "session/request_permission",
      params: {
        toolCall: { title: "AskUserQuestion", content: [{ type: "content", content: { type: "text", text: "Pick one" } }] },
        options: [
          { optionId: "q0_opt_0", name: "red", kind: "allow_once" },
          { optionId: "q0_opt_1", name: "blue", kind: "allow_once" },
        ],
      },
    }),
    event("approval.resolved", { approvalId: "ap_q", decision: "accept", optionId: "q0_opt_1" }),
  ]);
  assert.equal(items.length, 1);
  assert.equal(items[0].status, "accepted");
  assert.equal(items[0].decision, "Answered: blue");
});

test("a custom text reply resolves the approval as replied", () => {
  reset();
  const items = buildTimeline([
    event("approval.requested", {
      approvalId: "ap_q",
      method: "session/request_permission",
      params: {
        toolCall: { title: "AskUserQuestion", content: [{ type: "content", content: { type: "text", text: "Pick one" } }] },
        options: [{ optionId: "q0_opt_0", name: "red", kind: "allow_once" }],
      },
    }),
    event("approval.resolved", { approvalId: "ap_q", decision: "text", text: "my own answer" }),
  ]);
  assert.equal(items.length, 1);
  assert.equal(items[0].status, "accepted");
  assert.equal(items[0].decision, "Replied");
  assert.equal(items[0].reply, "my own answer");
});

test("provider errors, lifecycle and notable session states are visible", () => {
  reset();
  const items = buildTimeline([
    event("session.created", { id: "ses_test" }),
    event("session.provider", { agentName: "Codex", provider: "codex" }),
    event("session.state", { state: "ready" }), // low-value transition, folded away
    event("provider.error", { message: "stream reset" }),
    event("turn.failed", { error: "provider died" }, { turnId: "turn_1" }),
    event("session.state", { state: "failed" }),
  ]);
  assert.deepEqual(items.map((item) => item.kind), ["lifecycle", "lifecycle", "error", "lifecycle", "lifecycle"]);
  assert.equal(items[1].text, "Agent connected · Codex · via codex");
  assert.equal(items[2].text, "stream reset");
  assert.equal(items[3].text, "Turn failed: provider died");
  assert.equal(items[4].text, "Session failed");
  assert.equal(items[4].tone, "danger");
});

test("retryable provider errors render as informational reconnecting lifecycle", () => {
  reset();
  const items = buildTimeline([
    event("provider.error", {
      message: "Reconnecting... 2/5",
      details: "stream disconnected before completion: tls handshake eof",
      willRetry: true,
    }, { turnId: "turn_1" }),
    event("message.assistant.delta", { text: "recovered" }, { turnId: "turn_1" }),
    event("turn.completed", {}, { turnId: "turn_1" }),
  ]);
  assert.deepEqual(items.map((item) => item.kind), ["lifecycle", "message", "lifecycle"]);
  assert.equal(items[0].tone, "info");
  assert.equal(
    items[0].text,
    "Reconnecting... 2/5 · stream disconnected before completion: tls handshake eof",
  );
  assert.equal(items[2].text, "Turn completed");
});

test("terminal provider errors include normalized details", () => {
  reset();
  const items = buildTimeline([
    event("provider.error", {
      message: "stream disconnected",
      details: "retry budget exhausted",
      willRetry: false,
    }, { turnId: "turn_1" }),
  ]);
  assert.equal(items.length, 1);
  assert.equal(items[0].kind, "error");
  assert.equal(items[0].text, "stream disconnected · retry budget exhausted");
});

test("strict stopped lifecycle shows stopping and machine reason", () => {
  reset();
  const items = buildTimeline([
    event("session.state", { state: "stopping" }),
    event("session.state", { state: "stopped", reason: "provider_error" }),
  ]);
  assert.equal(items[0].text, "Stopping provider");
  assert.equal(items[1].text, "Session stopped · provider error");
  assert.equal(items[1].tone, "danger");
});

test("provider noise stays out of the timeline and does not split tool groups", () => {
  reset();
  const items = buildTimeline([
    event("tool.event", {
      method: "item/started",
      raw: { item: { id: "call_1", type: "commandExecution", command: "make", status: "inProgress" } },
    }),
    event("provider.event", { method: "thread/tokenUsage/updated" }),
    event("provider.metadata", { method: "account/rateLimits/updated" }),
    event("provider.stderr", { text: "warning: something" }),
    event("provider.process.started", { pid: 123 }),
    event("tool.event", {
      method: "item/completed",
      raw: { item: { id: "call_1", type: "commandExecution", command: "make", status: "completed" } },
    }),
    event("tool.event", {
      method: "item/completed",
      raw: { item: { id: "call_2", type: "commandExecution", command: "test", status: "completed" } },
    }),
  ]);
  assert.deepEqual(items.map((item) => item.kind), ["tools"]);
  assert.equal(items[0].calls.length, 2);
  assert.equal(items[0].calls[0].status, "completed");
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

test("unknown tool methods produce a diagnostic fallback", () => {
  const items = buildTimeline([
    event("tool.event", { method: "mystery/tool", raw: { id: "x" } }),
  ]);
  assert.equal(items[0].calls[0].name, "Tool");
  assert.equal(items[0].calls[0].summary, "mystery/tool");
});

test("provider turn notifications are omitted in favor of normalized lifecycle events", () => {
  reset();
  const items = buildTimeline([
    event("provider.turn.started", { method: "turn/started" }),
    event("provider.turn.completed", { method: "turn/completed" }),
  ]);
  assert.deepEqual(items, []);
});

test("tool completion updates an earlier group across visible events", () => {
  reset();
  const items = buildTimeline([
    event("tool.event", {
      method: "item/started",
      raw: { item: { id: "call_1", type: "commandExecution", command: "make", status: "inProgress" } },
    }),
    event("message.assistant.delta", { text: "Working on it." }, { turnId: "turn_1" }),
    event("tool.event", {
      method: "item/completed",
      raw: { item: { id: "call_1", type: "commandExecution", command: "make", status: "completed" } },
    }),
  ]);
  assert.deepEqual(items.map((item) => item.kind), ["tools", "message"]);
  assert.equal(items[0].calls.length, 1);
  assert.equal(items[0].calls[0].status, "completed");
});

test("turn terminal events settle tools whose provider terminal update is missing", () => {
  reset();
  const completed = buildTimeline([
    event("tool.event", {
      method: "item/started",
      raw: { item: { id: "call_done", type: "commandExecution", command: "make", status: "inProgress" } },
    }),
    event("turn.completed", {}, { turnId: "turn_1" }),
  ]);
  assert.equal(completed[0].calls[0].status, "completed");

  reset();
  const failed = buildTimeline([
    event("tool.event", {
      method: "item/started",
      raw: { item: { id: "call_failed", type: "commandExecution", command: "make", status: "inProgress" } },
    }),
    event("turn.failed", { error: "provider stopped" }, { turnId: "turn_1" }),
  ]);
  assert.equal(failed[0].calls[0].status, "failed");

  reset();
  const stoppedNormally = buildTimeline([
    event("tool.event", {
      method: "item/started",
      raw: { item: { id: "call_stopped", type: "commandExecution", command: "make", status: "inProgress" } },
    }),
    event("session.state", { state: "stopped", reason: "completed" }),
  ]);
  assert.equal(stoppedNormally[0].calls[0].status, "completed");
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

// Events recorded before agent ids were removed still render their legacy
// agentId reference.
test("legacy session.provider agentId still renders", () => {
  reset();
  const items = buildTimeline([
    event("session.provider", { agentId: "codex-default", provider: "codex" }),
  ]);
  assert.equal(items[0].text, "Agent connected · codex-default · via codex");
});
