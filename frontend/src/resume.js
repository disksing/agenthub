import { api } from "./api.js";

export function isResumable(session) {
  return Boolean(session && session.state === "stopped");
}

// The resume failure banner is shown only on the session it belongs to.
// Both arguments can be null on the first render (no failure recorded yet,
// no session selected yet), so the failure itself must be checked before
// comparing ids — optional chaining alone makes `undefined === undefined`
// match and dereferences the null failure.
export function resumeErrorForSession(failure, session) {
  if (!failure) return "";
  return failure.sessionId === session?.id ? failure.message : "";
}

export async function requestSessionResume(sessionId, request = api) {
  if (!sessionId) throw new Error("Session ID is required");
  const body = await request(`/v1/sessions/${sessionId}/resume`, {
    method: "POST",
    body: "{}",
  });
  return body.session;
}
