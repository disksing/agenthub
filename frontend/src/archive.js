// Archive helpers shared by the session list, the details panel and the
// archive confirmation dialog. Kept free of React so the node test runner
// can exercise the rules directly.

export const ARCHIVED_STATE = "archived";

// Sessions in these states hold no running provider work and can be moved
// to the archive. The daemon re-checks everything server-side; this mirrors
// the rule so the UI can disable the action with an explanation.
export const ARCHIVABLE_STATES = new Set(["ready", "stopped", "failed"]);

export function isArchived(session) {
  return session?.state === ARCHIVED_STATE;
}

export function isArchivable(session) {
  if (!session || isArchived(session)) return false;
  if (!ARCHIVABLE_STATES.has(session.state)) return false;
  if (session.currentTurnId) return false;
  if (session.pendingApprovalIds?.length) return false;
  return true;
}

// archiveDisabledReason explains why the Archive action is unavailable, for
// tooltips and aria descriptions. Returns "" when archiving is allowed.
export function archiveDisabledReason(session) {
  if (!session) return "No session is selected.";
  if (isArchived(session)) return "This session is already archived.";
  if (session.currentTurnId) return "Wait for the running turn to finish before archiving.";
  if (session.pendingApprovalIds?.length) return "Resolve the pending approval before archiving.";
  if (!ARCHIVABLE_STATES.has(session.state)) {
    return `Stop the session before archiving (current state: ${session.state || "unknown"}).`;
  }
  return "";
}

// sessionsQuery is the explicit list contract: the default list hides
// archived sessions; archivedOnly lists only them.
export function sessionsQuery(archivedOnly) {
  return archivedOnly ? "/v1/sessions?archived=true" : "/v1/sessions";
}
