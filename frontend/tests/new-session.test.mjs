import assert from "node:assert/strict";
import test from "node:test";
import {
  TITLE_MAX_LENGTH,
  buildCreatePayload,
  normalizeCwd,
  normalizeTitle,
  validateAgent,
  validateCwd,
  validateTitle,
} from "../src/newSession.js";

const agents = [
  { id: "codex-default", name: "Codex", providerId: "codex" },
  { id: "kimi-k3", name: "Kimi K3", providerId: "kimi" },
];

test("cwd is required and trimmed", () => {
  assert.equal(validateCwd(""), "Working directory is required.");
  assert.equal(validateCwd("   "), "Working directory is required.");
  assert.equal(validateCwd("/tmp/project"), "");
  assert.equal(normalizeCwd("  /tmp/project  "), "/tmp/project");
});

test("title allows empty and enforces the length limit", () => {
  assert.equal(validateTitle(""), "");
  assert.equal(validateTitle("  "), "");
  assert.equal(validateTitle("a".repeat(TITLE_MAX_LENGTH)), "");
  assert.match(validateTitle("a".repeat(TITLE_MAX_LENGTH + 1)), /120 characters or fewer/);
  assert.equal(normalizeTitle("  My session  "), "My session");
});

test("agent must be selected and still available", () => {
  assert.equal(validateAgent("", agents), "Select an agent.");
  assert.equal(validateAgent("codex-default", agents), "");
  assert.match(validateAgent("gone", agents), /no longer available/);
  // An empty agent list cannot confirm availability; the dialog blocks
  // submission separately, so validation only requires a non-empty id.
  assert.equal(validateAgent("codex-default", []), "");
});

test("buildCreatePayload omits an empty title and trims fields", () => {
  const { errors, payload } = buildCreatePayload({
    title: "   ",
    cwd: "  /tmp/project ",
    agentId: "kimi-k3",
    agents,
  });
  assert.deepEqual(errors, { title: "", cwd: "", agent: "" });
  assert.deepEqual(payload, { cwd: "/tmp/project", agentId: "kimi-k3" });
});

test("buildCreatePayload keeps a non-empty title", () => {
  const { payload } = buildCreatePayload({ title: "  Bug fix  ", cwd: "/tmp", agentId: "kimi-k3", agents });
  assert.deepEqual(payload, { cwd: "/tmp", agentId: "kimi-k3", title: "Bug fix" });
});

test("buildCreatePayload refuses invalid input", () => {
  const { errors, payload } = buildCreatePayload({ title: "", cwd: "", agentId: "", agents });
  assert.equal(payload, null);
  assert.ok(errors.cwd);
  assert.ok(errors.agent);
});
