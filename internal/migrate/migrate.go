// Package migrate moves AgentHub user data from the legacy default
// locations (for example ~/Library/Application Support/agenthub and
// ~/Library/Logs/AgentHub on macOS) into the unified ~/.agenthub layout.
//
// The migration is lossless, resumable and conflict-safe:
//
//   - Only the legacy default locations are ever read; explicit custom
//     directories (AGENTHUB_HOME) are never touched.
//   - Session stores move directory-by-directory with atomic rename on the
//     same filesystem, or through a staging directory (copy → fsync →
//     per-entry verification → atomic publish) across filesystems. The old
//     data is removed only after the new location is fully verified.
//   - If both the old and the new session store contain sessions, startup
//     fails with a conflict report instead of merging, overwriting or
//     picking a winner.
//   - A journal file (migration.json inside the new root) records each
//     phase, so a crash, power loss or partial copy can be completed or
//     safely restarted on the next launch. A finished migration is
//     idempotent.
//   - Log files move individually; when the target name is taken the old
//     file is kept as a timestamped backup next to it, never overwritten.
//
// The journal and every log line name paths and counts only: session
// contents, tokens and configuration values are never recorded.
package migrate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Plan describes one directory migration from a legacy location (From) to
// the unified layout (To). An empty From disables the plan.
type Plan struct {
	From string
	To   string
}

// Options configures one migration run.
type Options struct {
	// JournalPath is the migration journal, kept inside the new data root
	// (for example ~/.agenthub/migration.json).
	JournalPath string
	// Sessions moves the legacy session store (including its Archive/
	// subdirectory) to the new session store.
	Sessions Plan
	// Logs moves legacy service log files into the new logs directory.
	Logs Plan
	// LegacyStateFiles lists transient state files (server.json,
	// server.lock) left in the legacy data directory. They are removed
	// after a successful session migration so no stale endpoint discovery
	// survives.
	LegacyStateFiles []string
	// LegacyServerFile, when set, is the legacy daemon state file. Before
	// anything moves, a recorded pid that is still alive aborts the run:
	// migrating under a running legacy daemon would split the data.
	LegacyServerFile string
	// LegacyDataDir, when set, is removed after migration if it has become
	// empty. It is never removed while anything still lives inside.
	LegacyDataDir string
	// now and logf are test hooks.
	now  func() time.Time
	logf func(format string, args ...any)
}

// ConflictError reports that both the legacy and the new session store hold
// data. It is a hard stop: the daemon must not start and the user resolves
// the duplication by hand.
type ConflictError struct {
	Message string
}

func (e *ConflictError) Error() string { return e.Message }

// Report summarizes one run for startup logging.
type Report struct {
	SessionsMigrated   bool
	SessionsActive     int
	SessionsArchived   int
	LogsMoved          []string
	LogsBackedUp       []string
	LegacyStateRemoved []string
}

func (r Report) Changed() bool {
	return r.SessionsMigrated || len(r.LogsMoved) > 0 || len(r.LegacyStateRemoved) > 0
}

// Journal is the on-disk record of migration phases. It lives inside the
// new data root and contains no sensitive data.
type Journal struct {
	Version  int             `json:"version"`
	Sessions *JournalSection `json:"sessions,omitempty"`
	Logs     *JournalSection `json:"logs,omitempty"`
}

// JournalSection records the phase of one plan: "copying" (staging in
// progress), "publishing" (staging verified, moving into place) or
// "completed".
type JournalSection struct {
	State      string    `json:"state"`
	From       string    `json:"from"`
	To         string    `json:"to"`
	Staging    string    `json:"staging,omitempty"`
	Active     int       `json:"active,omitempty"`
	Archived   int       `json:"archived,omitempty"`
	FinishedAt time.Time `json:"finishedAt,omitempty"`
}

const (
	journalVersion   = 1
	stateMoving      = "moving"
	stateCopying     = "copying"
	statePublishing  = "publishing"
	stateCompleted   = "completed"
	stagingDirName   = ".migrate-sessions-staging"
	backupTimeFormat = "20060102T150405Z"
	sessionDirPrefix = "ses_"
	archiveDirName   = "Archive"
)

// rename is os.Rename; tests override it to simulate cross-filesystem
// moves (EXDEV) and rename failures.
var rename = os.Rename

// Run executes the configured plans. It returns a *ConflictError when both
// sides of the session store hold data; any other error also aborts startup
// because continuing could split data across two locations.
func Run(opts Options) (Report, error) {
	if opts.now == nil {
		opts.now = func() time.Time { return time.Now().UTC() }
	}
	if opts.logf == nil {
		opts.logf = func(string, ...any) {}
	}
	journal, err := loadJournal(opts.JournalPath)
	if err != nil {
		return Report{}, err
	}
	report := Report{}
	if err := guardLegacyDaemon(opts.LegacyServerFile); err != nil {
		return Report{}, err
	}
	if err := migrateSessions(&opts, journal, &report); err != nil {
		return Report{}, err
	}
	if err := migrateLogs(&opts, journal, &report); err != nil {
		return Report{}, err
	}
	if opts.LegacyDataDir != "" {
		removeIfEmpty(opts.LegacyDataDir)
	}
	return report, nil
}

// guardLegacyDaemon refuses to migrate while an older daemon, identified
// by its legacy server.json, is still running.
func guardLegacyDaemon(stateFile string) error {
	if stateFile == "" {
		return nil
	}
	data, err := os.ReadFile(stateFile)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read legacy daemon state: %w", err)
	}
	var state struct {
		PID int `json:"pid"`
	}
	if err := json.Unmarshal(data, &state); err != nil || state.PID <= 0 {
		return nil
	}
	if processAlive(state.PID) {
		return fmt.Errorf("an older AgentHub daemon (pid %d) is still running with the legacy data layout; stop it before starting this version so the migration does not split your data", state.PID)
	}
	return nil
}

// processAlive reports whether a process with the given pid exists.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	return syscall.Kill(pid, 0) == nil
}

// ---------------------------------------------------------------------------
// Session store
// ---------------------------------------------------------------------------

func migrateSessions(opts *Options, journal *Journal, report *Report) error {
	plan := opts.Sessions
	if plan.From == "" || plan.From == plan.To {
		return nil
	}
	if err := rejectSymlink(plan.From, "legacy session store"); err != nil {
		return err
	}
	if err := rejectSymlink(plan.To, "new session store"); err != nil {
		return err
	}

	section := journal.Sessions
	if section != nil && section.State == stateCompleted {
		// A previous run finished; only stray leftovers may remain.
		return finalizeSessions(opts, journal, report, section)
	}
	if section != nil && section.From == plan.From && section.To == plan.To &&
		(section.State == stateMoving || section.State == stateCopying || section.State == statePublishing) {
		return resumeSessions(opts, journal, report, section)
	}
	// A stale journal from a different layout is ignored: the migration is
	// re-planned from the current filesystem state.

	fromActive, fromArchived, err := scanStore(plan.From)
	if err != nil {
		return fmt.Errorf("scan legacy session store: %w", err)
	}
	toActive, toArchived, err := scanStore(plan.To)
	if err != nil {
		return fmt.Errorf("scan new session store: %w", err)
	}
	if len(fromActive) == 0 && len(fromArchived) == 0 {
		// Nothing to move. Record completion so the empty legacy directory
		// is cleaned up once and future runs stay quiet.
		journal.Sessions = &JournalSection{From: plan.From, To: plan.To}
		return completeSessions(opts, journal, report, journal.Sessions)
	}
	if len(toActive) > 0 || len(toArchived) > 0 {
		return &ConflictError{Message: conflictMessage(plan, fromActive, fromArchived, toActive, toArchived)}
	}
	return moveStore(opts, journal, report, plan, len(fromActive), len(fromArchived))
}

func conflictMessage(plan Plan, fromActive, fromArchived, toActive, toArchived []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "session store migration conflict: both the legacy store %s and the new store %s contain sessions", plan.From, plan.To)
	list := func(label string, ids []string) {
		if len(ids) == 0 {
			return
		}
		fmt.Fprintf(&b, "\n  %s: %s", label, strings.Join(ids, ", "))
	}
	list("legacy active", fromActive)
	list("legacy archived", fromArchived)
	list("new active", toActive)
	list("new archived", toArchived)
	overlap := intersect(append(append([]string{}, fromActive...), fromArchived...), append(append([]string{}, toActive...), toArchived...))
	list("present on both sides", overlap)
	b.WriteString("\nAgentHub never merges or overwrites session data. Stop the daemon, inspect both directories, remove the side you do not need (or move it aside), then start again.")
	return b.String()
}

// scanStore lists session ids directly under root and under root/Archive.
// A missing root is an empty store.
func scanStore(root string) (active, archived []string, err error) {
	active, err = scanSessionIDs(root)
	if err != nil {
		return nil, nil, err
	}
	archived, err = scanSessionIDs(filepath.Join(root, archiveDirName))
	if err != nil {
		return nil, nil, err
	}
	return active, archived, nil
}

func scanSessionIDs(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, entry := range entries {
		if entry.Type()&fs.ModeSymlink != 0 {
			return nil, fmt.Errorf("unsafe symlink in session store: %s", filepath.Join(dir, entry.Name()))
		}
		if entry.IsDir() && strings.HasPrefix(entry.Name(), sessionDirPrefix) {
			ids = append(ids, entry.Name())
		}
	}
	sort.Strings(ids)
	return ids, nil
}

// moveStore performs a fresh session migration, preferring atomic renames
// and falling back to a staged copy across filesystems.
func moveStore(opts *Options, journal *Journal, report *Report, plan Plan, active, archived int) error {
	section := &JournalSection{
		From:     plan.From,
		To:       plan.To,
		Active:   active,
		Archived: archived,
		State:    stateMoving,
	}
	journal.Sessions = section
	if err := saveJournal(opts.JournalPath, journal); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(plan.To), 0o700); err != nil {
		return err
	}
	if _, err := os.Stat(plan.To); errors.Is(err, fs.ErrNotExist) {
		// The new store does not exist yet: one atomic directory rename.
		if err := rename(plan.From, plan.To); err == nil {
			syncDir(filepath.Dir(plan.To))
			opts.logf("migrated session store %s -> %s (%d active, %d archived)", plan.From, plan.To, active, archived)
			return completeSessions(opts, journal, report, section)
		} else if !errors.Is(err, syscall.EXDEV) {
			return fmt.Errorf("move session store: %w", err)
		}
		// Cross-filesystem: fall through to the staged copy.
	} else if err != nil {
		return err
	} else {
		// The new store exists but is empty: move entries one by one.
		if err := moveEntries(plan.From, plan.To); err == nil {
			syncDir(plan.To)
			opts.logf("migrated session store %s -> %s (%d active, %d archived)", plan.From, plan.To, active, archived)
			return completeSessions(opts, journal, report, section)
		} else if !errors.Is(err, syscall.EXDEV) {
			return err
		}
	}
	return stagedCopy(opts, journal, report, plan, section)
}

// stagedCopy copies the legacy store into a staging directory next to the
// target, verifies every entry, then publishes it. The journal records the
// phase so an interrupted run can be resumed or restarted safely.
func stagedCopy(opts *Options, journal *Journal, report *Report, plan Plan, section *JournalSection) error {
	staging := filepath.Join(filepath.Dir(plan.To), stagingDirName)
	section.State = stateCopying
	section.Staging = staging
	if err := saveJournal(opts.JournalPath, journal); err != nil {
		return err
	}
	if err := os.RemoveAll(staging); err != nil {
		return err
	}
	if err := copyTree(plan.From, staging); err != nil {
		return fmt.Errorf("copy session store (old data left untouched at %s): %w", plan.From, err)
	}
	if err := verifyTrees(plan.From, staging); err != nil {
		return fmt.Errorf("verify session store copy (old data left untouched at %s): %w", plan.From, err)
	}
	section.State = statePublishing
	if err := saveJournal(opts.JournalPath, journal); err != nil {
		return err
	}
	if err := publishStaging(staging, plan.To); err != nil {
		return fmt.Errorf("publish migrated session store (verified copy kept at %s, old data untouched at %s): %w", staging, plan.From, err)
	}
	if err := os.RemoveAll(plan.From); err != nil {
		return fmt.Errorf("remove legacy session store after verified migration (new data is safe at %s): %w", plan.To, err)
	}
	opts.logf("migrated session store %s -> %s across filesystems (%d active, %d archived)", plan.From, plan.To, section.Active, section.Archived)
	return completeSessions(opts, journal, report, section)
}

// resumeSessions continues after a crash between journal phases.
func resumeSessions(opts *Options, journal *Journal, report *Report, section *JournalSection) error {
	staging := section.Staging
	fromExists := dirExists(section.From)
	stagingExists := staging != "" && dirExists(staging)
	switch {
	case section.State == stateMoving:
		// Renames are atomic per entry; continue moving whatever is left.
		fromActive, fromArchived, err := scanStore(section.From)
		if err != nil {
			return fmt.Errorf("scan legacy session store: %w", err)
		}
		if len(fromActive) > 0 || len(fromArchived) > 0 {
			plan := Plan{From: section.From, To: section.To}
			if _, err := os.Stat(section.To); errors.Is(err, fs.ErrNotExist) {
				if err := os.MkdirAll(filepath.Dir(section.To), 0o700); err != nil {
					return err
				}
				if err := rename(section.From, section.To); err == nil {
					syncDir(filepath.Dir(section.To))
					return completeSessions(opts, journal, report, section)
				} else if !errors.Is(err, syscall.EXDEV) {
					return fmt.Errorf("move session store: %w", err)
				}
				return stagedCopy(opts, journal, report, plan, section)
			} else if err != nil {
				return err
			}
			if err := moveEntries(section.From, section.To); err == nil {
				syncDir(section.To)
				return completeSessions(opts, journal, report, section)
			} else if !errors.Is(err, syscall.EXDEV) {
				return err
			}
			return stagedCopy(opts, journal, report, plan, section)
		}
		// The legacy side is empty: the data must already be at the target.
		return finishFromTarget(opts, journal, report, section)
	case section.State == stateCopying:
		if !fromExists {
			// The copy was unverified and the source is gone: the staging
			// area cannot be trusted. Keep it for manual inspection.
			return fmt.Errorf("migration journal claims an unfinished copy from %s but the legacy store is gone; inspect %s manually", section.From, staging)
		}
		if stagingExists {
			// The copy may be partial; the legacy data is still the source
			// of truth, so discard the staging area and start over.
			if err := os.RemoveAll(staging); err != nil {
				return err
			}
		}
		journal.Sessions = nil
		return migrateSessions(opts, journal, report)
	case section.State == statePublishing:
		// Verification passed before the crash. Publish whatever remains
		// in the staging area (per-entry moves are resumable), then drop
		// the legacy tree exactly as the uninterrupted path would.
		if stagingExists {
			if err := publishStaging(staging, section.To); err != nil {
				return fmt.Errorf("publish migrated session store (verified copy kept at %s, old data untouched at %s): %w", staging, section.From, err)
			}
		}
		if fromExists {
			if err := os.RemoveAll(section.From); err != nil {
				return fmt.Errorf("remove legacy session store after verified migration (new data is safe at %s): %w", section.To, err)
			}
		}
		return completeSessions(opts, journal, report, section)
	default:
		// Staging is gone and the legacy store is gone: the data must
		// already be at the target; verify before declaring completion.
		return finishFromTarget(opts, journal, report, section)
	}
}

// finishFromTarget completes a migration whose legacy side is already
// empty, verifying the target actually holds sessions first.
func finishFromTarget(opts *Options, journal *Journal, report *Report, section *JournalSection) error {
	toActive, toArchived, err := scanStore(section.To)
	if err != nil {
		return err
	}
	if len(toActive) == 0 && len(toArchived) == 0 {
		return fmt.Errorf("migration journal claims %s but neither %s nor %s holds sessions; inspect both locations manually", section.State, section.From, section.To)
	}
	return completeSessions(opts, journal, report, section)
}

// completeSessions records a finished migration and cleans up leftovers.
func completeSessions(opts *Options, journal *Journal, report *Report, section *JournalSection) error {
	return finalizeSessions(opts, journal, report, section)
}

func finalizeSessions(opts *Options, journal *Journal, report *Report, section *JournalSection) error {
	plan := opts.Sessions
	finishing := section != nil && section.State != stateCompleted
	// A completed migration whose legacy store grew new sessions again (a
	// downgrade started the old binary) is a conflict, not a resume.
	fromActive, fromArchived, err := scanStore(plan.From)
	if err != nil {
		return fmt.Errorf("scan legacy session store: %w", err)
	}
	if len(fromActive) > 0 || len(fromArchived) > 0 {
		toActive, toArchived, err := scanStore(plan.To)
		if err != nil {
			return fmt.Errorf("scan new session store: %w", err)
		}
		return &ConflictError{Message: conflictMessage(plan, fromActive, fromArchived, toActive, toArchived)}
	}
	for _, file := range opts.LegacyStateFiles {
		if err := os.Remove(file); err == nil {
			report.LegacyStateRemoved = append(report.LegacyStateRemoved, file)
		} else if !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("remove legacy state file %s: %w", file, err)
		}
	}
	removeIfEmpty(filepath.Join(plan.From, archiveDirName))
	removeIfEmpty(plan.From)
	if section != nil && section.State != stateCompleted {
		section.State = stateCompleted
		section.Staging = ""
		section.FinishedAt = opts.now()
		if err := saveJournal(opts.JournalPath, journal); err != nil {
			return err
		}
	}
	if finishing && (section.Active > 0 || section.Archived > 0) {
		report.SessionsMigrated = true
		report.SessionsActive = section.Active
		report.SessionsArchived = section.Archived
	}
	return nil
}

// moveEntries renames every entry of from into to, which must exist.
func moveEntries(from, to string) error {
	entries, err := os.ReadDir(from)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("unsafe symlink in session store: %s", filepath.Join(from, entry.Name()))
		}
		if err := rename(filepath.Join(from, entry.Name()), filepath.Join(to, entry.Name())); err != nil {
			return fmt.Errorf("move %s: %w", entry.Name(), err)
		}
	}
	syncDir(to)
	return nil
}

// publishStaging moves a verified staging tree to the target store. The
// staging area always sits next to the target, so these renames are on the
// same filesystem and cannot fail with EXDEV.
func publishStaging(staging, to string) error {
	if _, err := os.Stat(to); errors.Is(err, fs.ErrNotExist) {
		if err := os.Rename(staging, to); err != nil {
			return err
		}
		syncDir(filepath.Dir(to))
		return nil
	} else if err != nil {
		return err
	}
	entries, err := os.ReadDir(staging)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := os.Rename(filepath.Join(staging, entry.Name()), filepath.Join(to, entry.Name())); err != nil {
			return fmt.Errorf("publish %s: %w", entry.Name(), err)
		}
	}
	syncDir(to)
	return os.RemoveAll(staging)
}

// copyTree recursively copies a directory, rejecting symlinks and
// tightening permissions to 0700 for directories and 0600 for files, and
// fsyncs every file.
func copyTree(from, to string) error {
	from = filepath.Clean(from)
	return filepath.WalkDir(from, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(from, path)
		if err != nil {
			return err
		}
		target := filepath.Join(to, rel)
		if entry.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("unsafe symlink: %s", path)
		}
		if entry.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("unsupported special file in session store: %s", path)
		}
		return copyFile(path, target)
	})
}

func copyFile(from, to string) error {
	source, err := os.Open(from)
	if err != nil {
		return err
	}
	defer source.Close()
	target, err := os.OpenFile(to, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(target, source); err != nil {
		target.Close()
		return err
	}
	if err := target.Sync(); err != nil {
		target.Close()
		return err
	}
	if err := target.Close(); err != nil {
		return err
	}
	info, err := source.Stat()
	if err == nil && info.Mode().Perm() < 0o600 {
		// Keep stricter source permissions.
		_ = os.Chmod(to, info.Mode().Perm())
	}
	return nil
}

// verifyTrees compares two directory trees entry by entry: same relative
// paths, same file sizes and same SHA-256 contents.
func verifyTrees(a, b string) error {
	manifestA, err := treeManifest(a)
	if err != nil {
		return err
	}
	manifestB, err := treeManifest(b)
	if err != nil {
		return err
	}
	if len(manifestA) != len(manifestB) {
		return fmt.Errorf("entry count differs: %d vs %d", len(manifestA), len(manifestB))
	}
	for rel, sumA := range manifestA {
		sumB, ok := manifestB[rel]
		if !ok {
			return fmt.Errorf("missing copied entry %s", rel)
		}
		if sumA != sumB {
			return fmt.Errorf("copied entry %s differs", rel)
		}
	}
	return nil
}

// treeManifest maps relative file paths to "size:sha256" digests.
func treeManifest(root string) (map[string]string, error) {
	manifest := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		manifest[rel] = strconv.Itoa(len(data)) + ":" + hex.EncodeToString(sum[:])
		return nil
	})
	return manifest, err
}

// ---------------------------------------------------------------------------
// Service logs
// ---------------------------------------------------------------------------

func migrateLogs(opts *Options, journal *Journal, report *Report) error {
	plan := opts.Logs
	if plan.From == "" || plan.From == plan.To {
		return nil
	}
	if journal.Logs != nil && journal.Logs.State == stateCompleted && !dirExists(plan.From) {
		return nil
	}
	if err := rejectSymlink(plan.From, "legacy logs directory"); err != nil {
		return err
	}
	if err := rejectSymlink(plan.To, "new logs directory"); err != nil {
		return err
	}
	entries, err := os.ReadDir(plan.From)
	if errors.Is(err, fs.ErrNotExist) {
		return completeLogs(opts, journal)
	}
	if err != nil {
		return fmt.Errorf("scan legacy logs: %w", err)
	}
	if err := os.MkdirAll(plan.To, 0o700); err != nil {
		return err
	}
	moved := false
	for _, entry := range entries {
		if entry.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("unsafe symlink in legacy logs: %s", filepath.Join(plan.From, entry.Name()))
		}
		if !entry.Type().IsRegular() {
			// Subdirectories and special files are left untouched.
			continue
		}
		source := filepath.Join(plan.From, entry.Name())
		target := filepath.Join(plan.To, entry.Name())
		if _, err := os.Stat(target); err == nil {
			// Never overwrite a live log: keep the legacy file as a
			// timestamped backup next to it.
			target = filepath.Join(plan.To, entry.Name()+".migrated-"+opts.now().Format(backupTimeFormat))
			if _, err := os.Stat(target); err == nil {
				opts.logf("legacy log %s kept in place: backup name %s already exists", source, target)
				continue
			}
			if err := moveFile(source, target); err != nil {
				return fmt.Errorf("back up legacy log %s: %w", source, err)
			}
			report.LogsBackedUp = append(report.LogsBackedUp, target)
			moved = true
			continue
		} else if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		if err := moveFile(source, target); err != nil {
			return fmt.Errorf("move legacy log %s: %w", source, err)
		}
		report.LogsMoved = append(report.LogsMoved, target)
		moved = true
	}
	if moved {
		opts.logf("migrated service logs %s -> %s (%d moved, %d kept as backups)", plan.From, plan.To, len(report.LogsMoved), len(report.LogsBackedUp))
	}
	removeIfEmpty(plan.From)
	return completeLogs(opts, journal)
}

// moveFile renames a file, falling back to copy+verify+remove across
// filesystems. Renaming a log the service still has open is safe: the open
// descriptor follows the inode, so appends continue in the moved file.
func moveFile(from, to string) error {
	if err := rename(from, to); err == nil {
		syncDir(filepath.Dir(to))
		return nil
	} else if !errors.Is(err, syscall.EXDEV) {
		return err
	}
	if err := copyFile(from, to); err != nil {
		return err
	}
	same, err := sameFileContent(from, to)
	if err != nil {
		return err
	}
	if !same {
		return fmt.Errorf("copied log %s differs from %s", to, from)
	}
	return os.Remove(from)
}

func sameFileContent(a, b string) (bool, error) {
	manifestA, err := treeManifest(a)
	if err != nil {
		return false, err
	}
	manifestB, err := treeManifest(b)
	if err != nil {
		return false, err
	}
	if len(manifestA) != len(manifestB) {
		return false, nil
	}
	for rel, sum := range manifestA {
		if manifestB[rel] != sum {
			return false, nil
		}
	}
	return true, nil
}

func completeLogs(opts *Options, journal *Journal) error {
	section := journal.Logs
	if section == nil {
		section = &JournalSection{From: opts.Logs.From, To: opts.Logs.To}
		journal.Logs = section
	}
	if section.State == stateCompleted {
		return nil
	}
	section.State = stateCompleted
	section.FinishedAt = opts.now()
	return saveJournal(opts.JournalPath, journal)
}

// ---------------------------------------------------------------------------
// Journal and small helpers
// ---------------------------------------------------------------------------

func loadJournal(path string) (*Journal, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return &Journal{Version: journalVersion}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read migration journal: %w", err)
	}
	var journal Journal
	if err := json.Unmarshal(data, &journal); err != nil {
		return nil, fmt.Errorf("decode migration journal %s: %w", path, err)
	}
	return &journal, nil
}

func saveJournal(path string, journal *Journal) error {
	journal.Version = journalVersion
	data, err := json.MarshalIndent(journal, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".migration-*")
	if err != nil {
		return err
	}
	name := temp.Name()
	defer os.Remove(name)
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, path); err != nil {
		return err
	}
	syncDir(filepath.Dir(path))
	return nil
}

func rejectSymlink(path, what string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect %s: %w", what, err)
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		return fmt.Errorf("unsafe symlink: %s (%s)", path, what)
	}
	return nil
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// removeIfEmpty removes a directory only when it has no entries left.
func removeIfEmpty(path string) {
	_ = os.Remove(path) // fails silently unless empty
}

func syncDir(path string) {
	file, err := os.Open(path)
	if err != nil {
		return
	}
	_ = file.Sync()
	_ = file.Close()
}

func intersect(a, b []string) []string {
	set := map[string]bool{}
	for _, value := range b {
		set[value] = true
	}
	var both []string
	for _, value := range a {
		if set[value] {
			both = append(both, value)
		}
	}
	sort.Strings(both)
	return both
}
