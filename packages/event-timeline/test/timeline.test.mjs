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

function activityChild(item, kind) {
  assert.equal(item.kind, "activity");
  const child = item.items.find((candidate) => candidate.kind === kind);
  assert.ok(child, `activity is missing ${kind} child`);
  return child;
}

const thinkingChild = (item) => activityChild(item, "thinking");
const toolsChild = (item) => activityChild(item, "tools");

test("exports stable package and canonical event contract versions", () => {
  assert.equal(VERSION, "2.0.0");
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

test("canonical input messages preserve provenance sender and steer metadata", () => {
  reset();
  const items = buildTimeline([
    event("message.input", {
      text: "scheduled wake-up",
      role: "system",
      sender: { name: "Workflow Coordinator" },
      steer: false,
    }, { turnId: "turn_system" }),
    event("message.input", {
      text: "continue from the worker",
      role: "agent",
      sender: { id: "worker-1", name: "Worker Agent", sessionId: "ses_worker" },
      steer: true,
    }, { turnId: "turn_system" }),
  ]);
  assert.deepEqual(items.map((item) => item.role), ["system", "agent"]);
  assert.equal(items[0].sender.name, "Workflow Coordinator");
  assert.equal(items[1].sender.sessionId, "ses_worker");
  assert.equal(items[1].steer, true);
});

test("opaque input payload is preserved without interpretation", () => {
  reset();
  const payload = { schema: "pua.resource-message.v1", role: "custom", text: "original" };
  const items = buildTimeline([
    event("message.input", {
      schemaVersion: 2,
      text: "provider-facing text",
      payload,
      steer: true,
    }, { turnId: "turn_opaque" }),
  ]);
  assert.equal(items[0].text, "provider-facing text");
  assert.equal(items[0].role, "user");
  assert.equal(items[0].steer, true);
  assert.deepEqual(items[0].payload, payload);
});

test("legacy user message events remain user messages", () => {
  reset();
  const items = buildTimeline([
    event("message.user", { text: "old client" }),
    event("message.user.steer", { text: "old steer" }),
  ]);
  assert.deepEqual(items.map((item) => item.role), ["user", "user"]);
  assert.equal(items[1].steer, true);
});

test("reasoning deltas merge into a thinking block and stay active at the tail", () => {
  reset();
  const items = buildTimeline([
    event("message.reasoning.delta", { text: "Let me " }, { turnId: "turn_1" }),
    event("message.reasoning.delta", { text: "think." }, { turnId: "turn_1" }),
  ]);
  assert.equal(items.length, 1);
  assert.equal(items[0].kind, "activity");
  assert.equal(thinkingChild(items[0]).text, "Let me think.");
  assert.equal(items[0].thinkingCount, 1);
  assert.equal(items[0].reasoningUpdateCount, 2);
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

test("a steered message does not split the open thinking block", () => {
  reset();
  const items = buildTimeline([
    event("message.reasoning.delta", { text: "Let me " }, { turnId: "turn_1" }),
    event("message.input", { text: "steer", steer: true }, { turnId: "turn_1" }),
    event("message.reasoning.delta", { text: "think." }, { turnId: "turn_1" }),
  ]);
  assert.deepEqual(items.map((item) => item.kind), ["activity", "message"]);
  assert.equal(thinkingChild(items[0]).text, "Let me think.");
  // The agent is still reasoning, so the block stays active even though the
  // steered message is the last item.
  assert.equal(items[0].active, true);
  assert.equal(items[1].steer, true);
});

test("a legacy steered message does not split the open thinking block", () => {
  reset();
  const items = buildTimeline([
    event("message.reasoning.delta", { text: "a" }, { turnId: "turn_1" }),
    event("message.user.steer", { text: "steer" }, { turnId: "turn_1" }),
    event("message.reasoning.delta", { text: "b" }, { turnId: "turn_1" }),
  ]);
  assert.deepEqual(items.map((item) => item.kind), ["activity", "message"]);
  assert.equal(thinkingChild(items[0]).text, "ab");
  assert.equal(items[0].active, true);
});

test("thinking closes when assistant text or tool activity follows", () => {
  reset();
  const items = buildTimeline([
    event("message.reasoning.delta", { text: "a" }, { turnId: "turn_1" }),
    event("message.input", { text: "steer", steer: true }, { turnId: "turn_1" }),
    event("message.assistant.delta", { text: "answer" }, { turnId: "turn_1" }),
    event("message.reasoning.delta", { text: "b" }, { turnId: "turn_1" }),
  ]);
  assert.deepEqual(items.map((item) => item.kind), ["activity", "message", "message", "activity"]);
  assert.equal(thinkingChild(items[0]).text, "a");
  assert.equal(items[0].active, false);
  assert.equal(thinkingChild(items[3]).text, "b");
  assert.equal(items[3].active, true);
});

test("a message that starts a new turn closes the previous thinking block", () => {
  reset();
  const items = buildTimeline([
    event("message.reasoning.delta", { text: "a" }, { turnId: "turn_1" }),
    event("turn.completed", {}, { turnId: "turn_1" }),
    event("message.input", { text: "next question" }, { turnId: "turn_2" }),
    event("message.reasoning.delta", { text: "b" }, { turnId: "turn_2" }),
  ]);
  assert.deepEqual(items.map((item) => item.kind), ["activity", "lifecycle", "message", "activity"]);
  assert.equal(items[0].active, false);
  assert.equal(items[3].active, true);
});

test("thinking block keeps the first delta time as startTime for the duration label", () => {
  reset();
  const items = buildTimeline([
    event("message.reasoning.delta", { text: "Let me " }, { turnId: "turn_1", time: "2026-07-25T10:00:00Z" }),
    event("message.reasoning.delta", { text: "think." }, { turnId: "turn_1", time: "2026-07-25T10:01:02Z" }),
    event("message.assistant.delta", { text: "answer" }, { turnId: "turn_1" }),
  ]);
  assert.equal(items[0].kind, "activity");
  assert.equal(items[0].startTime, "2026-07-25T10:00:00Z");
  assert.equal(items[0].time, "2026-07-25T10:01:02Z");
});

test("folded reasoning events keep their persisted startTime", () => {
  reset();
  const items = buildTimeline([
    event("message.reasoning.delta", { text: "Let me think." }, {
      turnId: "turn_1",
      time: "2026-07-25T10:01:02Z",
      startTime: "2026-07-25T10:00:00Z",
    }),
    event("message.assistant.delta", { text: "answer" }, { turnId: "turn_1" }),
  ]);
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
  assert.equal(items[0].kind, "activity");
  assert.equal(items[0].toolCallCount, 1);
  const tools = toolsChild(items[0]);
  assert.equal(tools.calls.length, 1);
  const call = tools.calls[0];
  assert.equal(call.name, "Command");
  assert.equal(call.summary, "ls -la");
  assert.equal(call.status, "completed");
  assert.equal(call.output, "total 0");
  assert.equal("collapsed" in items[0], false);
});

test("orphan command output deltas stay hidden in truncated history", () => {
  reset();
  const items = buildTimeline([
    event("message.assistant.delta", { text: "Long commands " }, { turnId: "turn_1" }),
    event("tool.event", {
      method: "item/commandExecution/outputDelta",
      raw: { itemId: "call_before_window", delta: "compile output" },
    }, { turnId: "turn_1" }),
    event("message.assistant.delta", { text: "are still running." }, { turnId: "turn_1" }),
    event("tool.event", {
      method: "item/started",
      raw: { item: { id: "call_visible", type: "commandExecution", command: "git status", status: "inProgress" } },
    }, { turnId: "turn_1" }),
    event("tool.event", {
      method: "item/commandExecution/outputDelta",
      raw: { itemId: "call_visible", delta: "working tree clean" },
    }, { turnId: "turn_1" }),
    event("tool.event", {
      method: "item/completed",
      raw: {
        item: {
          id: "call_visible",
          type: "commandExecution",
          command: "git status",
          status: "completed",
          aggregatedOutput: "working tree clean",
          exitCode: 0,
        },
      },
    }, { turnId: "turn_1" }),
  ]);

  assert.deepEqual(items.map((item) => item.kind), ["message", "activity"]);
  assert.equal(items[0].text, "Long commands are still running.");
  const tools = toolsChild(items[1]);
  assert.equal(tools.calls.length, 1);
  assert.equal(tools.calls[0].callId, "call_visible");
  assert.equal(tools.calls[0].name, "Command");
  assert.equal(tools.calls[0].output, "working tree clean");
});

test("codex failed command surfaces the exit code as an error", () => {
  reset();
  const items = buildTimeline([
    event("tool.event", {
      method: "item/completed",
      raw: { item: { id: "item_9", type: "commandExecution", command: "false", status: "completed", exitCode: 1 } },
    }),
  ]);
  assert.equal(toolsChild(items[0]).calls[0].status, "failed");
  assert.equal(toolsChild(items[0]).calls[0].error, "Exit code 1");
});

test("codex message and reasoning items are not rendered as tool calls", () => {
  reset();
  const items = buildTimeline([
    event("tool.event", { method: "item/started", raw: { item: { id: "a", type: "agentMessage" } } }),
    event("tool.event", { method: "item/started", raw: { item: { id: "b", type: "reasoning" } } }),
    event("tool.event", { method: "item/started", raw: { item: { id: "c", type: "webSearch", query: "agenthub" } } }),
  ]);
  assert.equal(items.length, 1);
  const tools = toolsChild(items[0]);
  assert.equal(tools.calls.length, 1);
  assert.equal(tools.calls[0].name, "Web search");
  assert.equal(tools.calls[0].summary, "agenthub");
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
  const tools = toolsChild(items[0]);
  assert.equal(tools.calls.length, 1);
  assert.equal(tools.calls[0].summary, "Read README.md");
  assert.equal(tools.calls[0].status, "completed");
  assert.equal(tools.calls[0].output, "file body");
});

test("acp thought chunks become thinking, message chunks become agent text", () => {
  reset();
  const items = buildTimeline([
    event("message.reasoning.delta", { text: "kimi thinking", method: "session/update" }, { turnId: "turn_1" }),
    event("message.assistant.delta", { text: "kimi answer", method: "session/update" }, { turnId: "turn_1" }),
  ]);
  assert.deepEqual(items.map((item) => item.kind), ["activity", "message"]);
});

test("pi tool execution start and end pair into one call", () => {
  reset();
  const items = buildTimeline([
    event("tool.event", { method: "tool_execution_start", raw: { toolName: "read", args: { path: "README.md" } } }),
    event("tool.event", { method: "tool_execution_end", raw: { toolName: "read", result: "contents" } }),
  ]);
  assert.equal(items.length, 1);
  const tools = toolsChild(items[0]);
  assert.equal(tools.calls.length, 1);
  assert.equal(tools.calls[0].status, "completed");
  assert.equal(tools.calls[0].output, "contents");
});

test("pi tool failure is flagged", () => {
  reset();
  const items = buildTimeline([
    event("tool.event", { method: "tool_execution_start", raw: { toolName: "bash", args: { command: "make" } } }),
    event("tool.event", { method: "tool_execution_end", raw: { toolName: "bash", isError: true, error: "boom" } }),
  ]);
  assert.equal(toolsChild(items[0]).calls[0].status, "failed");
  assert.equal(toolsChild(items[0]).calls[0].error, "boom");
});

test("consecutive tool calls group without projecting host expansion state", () => {
  reset();
  const items = buildTimeline([
    event("tool.event", { method: "item/completed", raw: { item: { id: "1", type: "commandExecution", command: "ls", status: "completed" } } }),
    event("tool.event", { method: "item/completed", raw: { item: { id: "2", type: "commandExecution", command: "pwd", status: "completed" } } }),
    event("message.assistant.delta", { text: "done" }, { turnId: "turn_1" }),
  ]);
  assert.equal(items[0].kind, "activity");
  assert.equal(toolsChild(items[0]).calls.length, 2);
  assert.equal(items[0].active, false);
  assert.equal("collapsed" in items[0], false);
});

test("completed tools stay active at the open tail and fold on a terminal boundary", () => {
  reset();
  const activityEvents = [
    event("tool.event", { method: "item/completed", raw: { item: { id: "1", type: "commandExecution", command: "true", status: "completed" } } }),
  ];
  assert.equal(buildTimeline(activityEvents)[0].active, true);
  assert.equal(buildTimeline([...activityEvents, event("turn.completed", {}, { turnId: "turn_1" })])[0].active, false);
});

test("alternating thinking and tools form one ordered activity", () => {
  reset();
  const items = buildTimeline([
    event("message.reasoning.delta", { text: "plan" }, { turnId: "turn_1" }),
    event("tool.event", { method: "item/completed", raw: { item: { id: "1", type: "commandExecution", command: "pwd", status: "completed" } } }, { turnId: "turn_1" }),
    event("message.reasoning.delta", { text: "check" }, { turnId: "turn_1" }),
    event("tool.event", { method: "item/completed", raw: { item: { id: "2", type: "webSearch", query: "AgentHub", status: "completed" } } }, { turnId: "turn_1" }),
    event("message.assistant.delta", { text: "done" }, { turnId: "turn_1" }),
  ]);
  assert.deepEqual(items.map((item) => item.kind), ["activity", "message"]);
  assert.deepEqual(items[0].items.map((item) => item.kind), ["thinking", "tools", "thinking", "tools"]);
  assert.equal(items[0].thinkingCount, 2);
  assert.equal(items[0].reasoningUpdateCount, 2);
  assert.equal(items[0].toolCallCount, 2);
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
  assert.deepEqual(items.map((item) => item.kind), ["activity"]);
  assert.equal(toolsChild(items[0]).calls.length, 2);
  assert.equal(toolsChild(items[0]).calls[0].status, "completed");
});

test("message delivery facts stay out of the conversation timeline", () => {
  reset();
  const items = buildTimeline([
    event("message.input", { role: "user", text: "hello" }, { turnId: "turn_1" }),
    event("message.delivery", { state: "attempting", attempt: 1 }, { turnId: "turn_1" }),
    event("message.assistant.delta", { text: "Hi" }, { turnId: "turn_1" }),
    event("message.delivery", { state: "accepted", attempt: 1 }, { turnId: "turn_1" }),
  ]);
  assert.deepEqual(items.map((item) => item.kind), ["message", "message"]);
  assert.equal(items[0].text, "hello");
  assert.equal(items[1].text, "Hi");
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
  assert.equal(toolsChild(items[0]).calls[0].name, "Tool");
  assert.equal(toolsChild(items[0]).calls[0].summary, "mystery/tool");
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
  assert.deepEqual(items.map((item) => item.kind), ["activity", "message"]);
  assert.equal(toolsChild(items[0]).calls.length, 1);
  assert.equal(toolsChild(items[0]).calls[0].status, "completed");
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
  assert.equal(toolsChild(completed[0]).calls[0].status, "completed");

  reset();
  const failed = buildTimeline([
    event("tool.event", {
      method: "item/started",
      raw: { item: { id: "call_failed", type: "commandExecution", command: "make", status: "inProgress" } },
    }),
    event("turn.failed", { error: "provider stopped" }, { turnId: "turn_1" }),
  ]);
  assert.equal(toolsChild(failed[0]).calls[0].status, "failed");

  reset();
  const stoppedNormally = buildTimeline([
    event("tool.event", {
      method: "item/started",
      raw: { item: { id: "call_stopped", type: "commandExecution", command: "make", status: "inProgress" } },
    }),
    event("session.state", { state: "stopped", reason: "completed" }),
  ]);
  assert.equal(toolsChild(stoppedNormally[0]).calls[0].status, "completed");
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
