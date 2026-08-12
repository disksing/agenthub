import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";
import { isResumable, requestSessionResume, resumeErrorForSession } from "../src/resume.js";

const srcRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..", "src");

test("only stopped sessions are resumable", () => {
  assert.equal(isResumable({ id: "ses_1", state: "stopped" }), true);
  for (const state of ["ready", "starting", "running", "waiting_approval", "stopping", "failed", "archived"]) {
    assert.equal(isResumable({ id: "ses_1", state }), false, `${state} must not be resumable`);
  }
  assert.equal(isResumable(null), false);
});

test("requestSessionResume posts to the stable session action endpoint", async () => {
  const calls = [];
  const session = { id: "ses_1", state: "ready" };
  const result = await requestSessionResume("ses_1", async (url, options) => {
    calls.push({ url, options });
    return { session };
  });

  assert.equal(result, session);
  assert.deepEqual(calls, [{
    url: "/v1/sessions/ses_1/resume",
    options: { method: "POST", body: "{}" },
  }]);
  await assert.rejects(requestSessionResume("", async () => ({})), /Session ID is required/);
});

test("stopped composer exposes an accessible, retryable resume action", async () => {
  const app = await readFile(path.join(srcRoot, "App.jsx"), "utf8");
  const resumeBlock = app.slice(app.indexOf('className="resume-session-prompt"'), app.indexOf(") : (", app.indexOf('className="resume-session-prompt"')));

  assert.match(app, /isResumable\(activeSession\) \? "composer-resume" : ""/);
  assert.match(resumeBlock, /aria-label="Resume session"/);
  assert.match(resumeBlock, /aria-busy=\{resumingId === activeSession\.id \|\| undefined\}/);
  assert.match(resumeBlock, /disabled=\{resumingId === activeSession\.id\}/);
  assert.match(resumeBlock, /onClick=\{resumeSession\}/);
  assert.match(resumeBlock, /className="resume-session-error" role="alert"/);
  assert.match(app, /if \(!isResumable\(activeSession\) \|\| resumingIdRef\.current === activeSession\.id\) return;/);
  assert.match(app, /resumingIdRef\.current = sessionId;/);
  assert.match(app, /if \(resumingIdRef\.current === sessionId\) resumingIdRef\.current = "";/);
  assert.match(app, /setEventReloadKey\(\(current\) => current \+ 1\)/);
  assert.match(app, /await refreshSessions\(\)/);
  assert.match(app, /setResumeFailure\(\{/);
  assert.match(app, /Failed to resume session:/);
});

test("resume prompt is responsive and keeps the action visible", async () => {
  const css = await readFile(path.join(srcRoot, "styles.css"), "utf8");
  assert.match(css, /\.composer-resume \{/);
  assert.match(css, /\.resume-session-button \{/);
  assert.match(css, /@media \(max-width: 720px\)[\s\S]*\.resume-session-button \{\s*width: 100%;/);
});

test("resumeErrorForSession is null-safe on the first render", () => {
  // Regression: with no failure recorded and no session selected yet, both
  // values are null and the comparison must not dereference the failure.
  assert.equal(resumeErrorForSession(null, null), "");
  assert.equal(resumeErrorForSession(null, { id: "ses_1" }), "");
  assert.equal(resumeErrorForSession(undefined, undefined), "");
});

test("resumeErrorForSession only reports the failure on its own session", () => {
  const failure = { sessionId: "ses_1", message: "Failed to resume session: boom" };
  assert.equal(resumeErrorForSession(failure, { id: "ses_1" }), failure.message);
  assert.equal(resumeErrorForSession(failure, { id: "ses_2" }), "");
  assert.equal(resumeErrorForSession(failure, null), "");
});
