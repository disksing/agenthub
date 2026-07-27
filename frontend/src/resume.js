import { api } from "./api.js";

export function isResumable(session) {
  return Boolean(session && session.state === "stopped");
}

export async function requestSessionResume(sessionId, request = api) {
  if (!sessionId) throw new Error("Session ID is required");
  const body = await request(`/v1/sessions/${sessionId}/resume`, {
    method: "POST",
    body: "{}",
  });
  return body.session;
}
