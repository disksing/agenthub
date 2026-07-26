import { useEffect, useRef } from "react";
import { Archive, X } from "@phosphor-icons/react";

// In-app confirmation for archiving a session. Replaces any browser-native
// confirmation flow: it explains that the session files move into the Archive
// directory, and provides cancel, keyboard (Escape), loading, error and
// double-submit states.
export function ArchiveConfirmModal({ session, submitting, error, onConfirm, onClose, triggerRef }) {
  const dialogRef = useRef(null);
  const cancelRef = useRef(null);

  // Focus Cancel on open (the safe default); restore focus to the trigger
  // on unmount.
  useEffect(() => {
    (cancelRef.current || dialogRef.current)?.focus();
    return () => triggerRef?.current?.focus();
  }, [triggerRef]);

  useEffect(() => {
    const onKeyDown = (event) => {
      if (event.key === "Escape" && !submitting) onClose();
    };
    document.addEventListener("keydown", onKeyDown);
    return () => document.removeEventListener("keydown", onKeyDown);
  }, [onClose, submitting]);

  return (
    <div
      className="modal-backdrop"
      onMouseDown={(event) => {
        if (event.target === event.currentTarget && !submitting) onClose();
      }}
    >
      <section
        className="confirm-dialog"
        role="alertdialog"
        aria-modal="true"
        aria-labelledby="archive-dialog-title"
        aria-describedby="archive-dialog-description"
        ref={dialogRef}
        tabIndex={-1}
      >
        <header className="new-session-dialog-header">
          <div>
            <h2 id="archive-dialog-title">Archive Session</h2>
            <p id="archive-dialog-description">
              Move “{session?.title || session?.id}” to the archive?
            </p>
          </div>
          <button className="icon-button" aria-label="Close archive dialog" onClick={onClose} disabled={submitting}>
            <X size={19} />
          </button>
        </header>

        <div className="confirm-dialog-body">
          <p>
            The whole session directory, including its full event history, moves to{" "}
            <code>sessions/Archive/{session?.id}/</code>. Nothing is deleted: the session stays readable from
            the Archived view, but it no longer accepts messages, resume, interrupt or approvals.
          </p>
          <p>Archived sessions are hidden from the default session list. Unarchiving is not supported.</p>
          {error ? <p className="new-session-submit-error" role="alert">{error}</p> : null}
        </div>

        <footer className="new-session-actions">
          <button type="button" className="settings-button" onClick={onClose} disabled={submitting} ref={cancelRef}>
            Cancel
          </button>
          <button
            type="button"
            className="settings-button settings-button-primary"
            onClick={() => { if (!submitting) onConfirm(); }}
            disabled={submitting}
          >
            <Archive size={17} />{submitting ? "Archiving…" : "Archive Session"}
          </button>
        </footer>
      </section>
    </div>
  );
}
