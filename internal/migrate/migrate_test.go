package migrate

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/disksing/agenthub/internal/session"
)

// fixture builds a legacy layout under root: dataDir/sessions with the
// given active and archived session ids, each holding a minimal valid
// events.jsonl, plus log files under logsDir.
func fixture(t *testing.T, dataDir, logsDir string, active, archived []string) {
	t.Helper()
	for _, id := range active {
		writeSession(t, filepath.Join(dataDir, "sessions", id), id)
	}
	for _, id := range archived {
		writeSession(t, filepath.Join(dataDir, "sessions", archiveDirName, id), id)
	}
	if logsDir != "" {
		if err := os.MkdirAll(logsDir, 0o700); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"stdout.log", "stderr.log"} {
			if err := os.WriteFile(filepath.Join(logsDir, name), []byte(name+" contents\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
}

// writeSession creates a session directory the real session store can
// open: a session.created event in events.jsonl.
func writeSession(t *testing.T, dir, id string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	event := map[string]any{
		"id":        1,
		"time":      time.Now().UTC(),
		"type":      "session.created",
		"sessionId": id,
		"data": map[string]any{
			"id":        id,
			"title":     "Migrated " + id,
			"cwd":       "/tmp",
			"agentName": "Codex",
			"state":     "ready",
			"createdAt": time.Now().UTC(),
			"updatedAt": time.Now().UTC(),
		},
	}
	data, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

func testOptions(home string) Options {
	legacyData := filepath.Join(home, "Library", "Application Support", "agenthub")
	root := filepath.Join(home, ".agenthub")
	return Options{
		JournalPath: filepath.Join(root, "migration.json"),
		Sessions: Plan{
			From: filepath.Join(legacyData, "sessions"),
			To:   filepath.Join(root, "sessions"),
		},
		Logs: Plan{
			From: filepath.Join(home, "Library", "Logs", "AgentHub"),
			To:   filepath.Join(root, "logs"),
		},
		LegacyStateFiles: []string{
			filepath.Join(legacyData, "server.json"),
			filepath.Join(legacyData, "server.lock"),
		},
		LegacyDataDir: legacyData,
	}
}

func mustRun(t *testing.T, opts Options) Report {
	t.Helper()
	report, err := Run(opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return report
}

func legacySessions(opts Options) string { return opts.Sessions.From }
func newSessions(opts Options) string    { return opts.Sessions.To }
func legacyLogs(opts Options) string     { return opts.Logs.From }
func legacyDataDir(opts Options) string  { return opts.LegacyDataDir }
func stagingDir(opts Options) string {
	return filepath.Join(filepath.Dir(opts.Sessions.To), stagingDirName)
}

func TestFreshInstallWithoutLegacyData(t *testing.T) {
	home := t.TempDir()
	opts := testOptions(home)
	report := mustRun(t, opts)
	if report.SessionsMigrated || len(report.LogsMoved) > 0 {
		t.Fatalf("unexpected migration activity: %+v", report)
	}
	// A journal still records the completed no-op so reruns stay quiet.
	journal, err := loadJournal(opts.JournalPath)
	if err != nil {
		t.Fatal(err)
	}
	if journal.Sessions == nil || journal.Sessions.State != stateCompleted {
		t.Fatalf("journal sessions = %+v", journal.Sessions)
	}
}

func TestMigratesActiveSessionsIntoExistingEmptyStore(t *testing.T) {
	home := t.TempDir()
	opts := testOptions(home)
	fixture(t, legacyDataDir(opts), "", []string{"ses_a1", "ses_a2"}, nil)
	// The daemon creates the new store before migrating.
	if err := os.MkdirAll(newSessions(opts), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacyDataDir(opts), "server.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	report := mustRun(t, opts)
	if !report.SessionsMigrated || report.SessionsActive != 2 || report.SessionsArchived != 0 {
		t.Fatalf("report = %+v", report)
	}
	for _, id := range []string{"ses_a1", "ses_a2"} {
		if _, err := os.Stat(filepath.Join(newSessions(opts), id, "events.jsonl")); err != nil {
			t.Errorf("migrated session %s: %v", id, err)
		}
	}
	if dirExists(legacySessions(opts)) {
		t.Error("legacy session store still exists")
	}
	if _, err := os.Stat(filepath.Join(legacyDataDir(opts), "server.json")); !errors.Is(err, os.ErrNotExist) {
		t.Error("legacy server.json not removed")
	}
	if dirExists(legacyDataDir(opts)) {
		t.Error("legacy data dir not removed after becoming empty")
	}
}

func TestMigratesArchivedSessionsOnly(t *testing.T) {
	home := t.TempDir()
	opts := testOptions(home)
	fixture(t, legacyDataDir(opts), "", nil, []string{"ses_z9"})
	report := mustRun(t, opts)
	if report.SessionsArchived != 1 {
		t.Fatalf("report = %+v", report)
	}
	if _, err := os.Stat(filepath.Join(newSessions(opts), archiveDirName, "ses_z9", "events.jsonl")); err != nil {
		t.Fatal(err)
	}
}

func TestMigratesActiveAndArchivedTogether(t *testing.T) {
	home := t.TempDir()
	opts := testOptions(home)
	fixture(t, legacyDataDir(opts), "", []string{"ses_a"}, []string{"ses_z"})
	mustRun(t, opts)
	store, err := session.Open(newSessions(opts))
	if err != nil {
		t.Fatal(err)
	}
	active := store.List(false)
	if len(active) != 1 || active[0].ID != "ses_a" {
		t.Fatalf("active = %+v", active)
	}
	all := store.List(true)
	if len(all) != 2 {
		t.Fatalf("all = %+v", all)
	}
	archived, err := store.Get("ses_z")
	if err != nil {
		t.Fatal(err)
	}
	if archived.State != session.StateArchived {
		t.Fatalf("archived state = %q", archived.State)
	}
	// The event log survived the move and the projection rebuilt.
	events, err := store.EventsAfter("ses_a", 0, 10)
	if err != nil || len(events) != 1 || events[0].Type != "session.created" {
		t.Fatalf("events = %+v, %v", events, err)
	}
	if _, err := os.Stat(filepath.Join(newSessions(opts), "ses_a", "session.json")); err != nil {
		t.Error("session.json projection not rebuilt")
	}
}

func TestConflictWhenBothStoresHoldSessions(t *testing.T) {
	home := t.TempDir()
	opts := testOptions(home)
	fixture(t, legacyDataDir(opts), "", []string{"ses_old"}, []string{"ses_oldarch"})
	fixture(t, filepath.Join(home, ".agenthub"), "", []string{"ses_new"}, nil)
	_, err := Run(opts)
	var conflict *ConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("err = %v", err)
	}
	for _, want := range []string{"ses_old", "ses_new", "ses_oldarch", "never merges"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("conflict message missing %q:\n%s", want, err)
		}
	}
	// Nothing moved on either side.
	if !dirExists(filepath.Join(legacySessions(opts), "ses_old")) {
		t.Error("legacy session removed despite conflict")
	}
	if !dirExists(filepath.Join(newSessions(opts), "ses_new")) {
		t.Error("new session removed despite conflict")
	}
}

func TestConflictOnDuplicateSessionID(t *testing.T) {
	home := t.TempDir()
	opts := testOptions(home)
	fixture(t, legacyDataDir(opts), "", []string{"ses_dup"}, nil)
	fixture(t, filepath.Join(home, ".agenthub"), "", []string{"ses_dup"}, nil)
	_, err := Run(opts)
	var conflict *ConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(err.Error(), "present on both sides") {
		t.Errorf("conflict should name the overlap:\n%s", err)
	}
}

func TestRejectsSymlinkedLegacyStore(t *testing.T) {
	home := t.TempDir()
	opts := testOptions(home)
	real := filepath.Join(home, "elsewhere")
	fixture(t, real, "", []string{"ses_a"}, nil)
	if err := os.MkdirAll(filepath.Dir(legacySessions(opts)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(real, "sessions"), legacySessions(opts)); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(opts); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("err = %v", err)
	}
}

func TestRenameFailureKeepsOldData(t *testing.T) {
	home := t.TempDir()
	opts := testOptions(home)
	fixture(t, legacyDataDir(opts), "", []string{"ses_a"}, nil)
	boom := &os.PathError{Op: "rename", Err: errors.New("boom")}
	restore := rename
	rename = func(string, string) error { return boom }
	defer func() { rename = restore }()
	if _, err := Run(opts); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v", err)
	}
	if !dirExists(filepath.Join(legacySessions(opts), "ses_a")) {
		t.Error("legacy session lost after rename failure")
	}
}

// withEXDEV makes every rename fail with EXDEV, forcing the staged copy.
func withEXDEV(t *testing.T) {
	t.Helper()
	restore := rename
	rename = func(string, string) error {
		return &os.PathError{Op: "rename", Err: syscall.EXDEV}
	}
	t.Cleanup(func() { rename = restore })
}

func TestCrossFilesystemStagedCopy(t *testing.T) {
	home := t.TempDir()
	opts := testOptions(home)
	fixture(t, legacyDataDir(opts), "", []string{"ses_a"}, []string{"ses_z"})
	withEXDEV(t)
	report := mustRun(t, opts)
	if !report.SessionsMigrated || report.SessionsActive != 1 || report.SessionsArchived != 1 {
		t.Fatalf("report = %+v", report)
	}
	if dirExists(legacySessions(opts)) {
		t.Error("legacy store kept after verified copy")
	}
	if dirExists(stagingDir(opts)) {
		t.Error("staging area not cleaned up")
	}
	store, err := session.Open(newSessions(opts))
	if err != nil {
		t.Fatal(err)
	}
	if len(store.List(true)) != 2 {
		t.Fatalf("sessions = %+v", store.List(true))
	}
	info, err := os.Stat(filepath.Join(newSessions(opts), "ses_a", "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("events.jsonl mode = %o, want 600", info.Mode().Perm())
	}
}

func TestInterruptedCopyRestartsFromLegacyData(t *testing.T) {
	home := t.TempDir()
	opts := testOptions(home)
	fixture(t, legacyDataDir(opts), "", []string{"ses_a"}, nil)
	withEXDEV(t)
	// Simulate a crash mid-copy: journal says copying, staging holds a
	// partial tree.
	journal := &Journal{Sessions: &JournalSection{
		State: stateCopying, From: legacySessions(opts), To: newSessions(opts), Staging: stagingDir(opts), Active: 1,
	}}
	if err := saveJournal(opts.JournalPath, journal); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(stagingDir(opts), "ses_a"), 0o700); err != nil {
		t.Fatal(err)
	}
	report := mustRun(t, opts)
	if !report.SessionsMigrated {
		t.Fatalf("report = %+v", report)
	}
	if _, err := os.Stat(filepath.Join(newSessions(opts), "ses_a", "events.jsonl")); err != nil {
		t.Fatal(err)
	}
	if dirExists(legacySessions(opts)) {
		t.Error("legacy store kept after recovery")
	}
}

func TestInterruptedPublishContinuesFromStaging(t *testing.T) {
	home := t.TempDir()
	opts := testOptions(home)
	fixture(t, legacyDataDir(opts), "", []string{"ses_a"}, nil)
	withEXDEV(t)
	// Simulate a crash after verification but before the publish: journal
	// says publishing and the staging area holds the full copy.
	journal := &Journal{Sessions: &JournalSection{
		State: statePublishing, From: legacySessions(opts), To: newSessions(opts), Staging: stagingDir(opts), Active: 1,
	}}
	if err := saveJournal(opts.JournalPath, journal); err != nil {
		t.Fatal(err)
	}
	if err := copyTree(legacySessions(opts), stagingDir(opts)); err != nil {
		t.Fatal(err)
	}
	report := mustRun(t, opts)
	if !report.SessionsMigrated {
		t.Fatalf("report = %+v", report)
	}
	if _, err := os.Stat(filepath.Join(newSessions(opts), "ses_a", "events.jsonl")); err != nil {
		t.Fatal(err)
	}
	if dirExists(legacySessions(opts)) || dirExists(stagingDir(opts)) {
		t.Error("legacy store or staging left behind")
	}
}

func TestInterruptedMoveContinuesRemainingEntries(t *testing.T) {
	home := t.TempDir()
	opts := testOptions(home)
	fixture(t, legacyDataDir(opts), "", []string{"ses_a", "ses_b"}, nil)
	if err := os.MkdirAll(newSessions(opts), 0o700); err != nil {
		t.Fatal(err)
	}
	// Simulate a crash between entry renames: ses_a already moved, ses_b
	// not, journal says moving.
	if err := os.Rename(filepath.Join(legacySessions(opts), "ses_a"), filepath.Join(newSessions(opts), "ses_a")); err != nil {
		t.Fatal(err)
	}
	journal := &Journal{Sessions: &JournalSection{
		State: stateMoving, From: legacySessions(opts), To: newSessions(opts), Active: 2,
	}}
	if err := saveJournal(opts.JournalPath, journal); err != nil {
		t.Fatal(err)
	}
	report := mustRun(t, opts)
	if !report.SessionsMigrated || report.SessionsActive != 2 {
		t.Fatalf("report = %+v", report)
	}
	for _, id := range []string{"ses_a", "ses_b"} {
		if !dirExists(filepath.Join(newSessions(opts), id)) {
			t.Errorf("session %s missing after recovery", id)
		}
	}
}

func TestCopyingWithVanishedLegacyStoreFailsLoudly(t *testing.T) {
	home := t.TempDir()
	opts := testOptions(home)
	journal := &Journal{Sessions: &JournalSection{
		State: stateCopying, From: legacySessions(opts), To: newSessions(opts), Staging: stagingDir(opts),
	}}
	if err := saveJournal(opts.JournalPath, journal); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(opts); err == nil || !strings.Contains(err.Error(), "legacy store is gone") {
		t.Fatalf("err = %v", err)
	}
}

func TestSecondRunIsIdempotent(t *testing.T) {
	home := t.TempDir()
	opts := testOptions(home)
	fixture(t, legacyDataDir(opts), legacyLogs(opts), []string{"ses_a"}, []string{"ses_z"})
	first := mustRun(t, opts)
	second := mustRun(t, opts)
	if !first.SessionsMigrated || second.SessionsMigrated {
		t.Fatalf("first = %+v, second = %+v", first, second)
	}
	if len(second.LogsMoved) > 0 {
		t.Fatalf("second run moved logs again: %+v", second)
	}
}

func TestCompletedMigrationDetectsDowngradeRegrowth(t *testing.T) {
	home := t.TempDir()
	opts := testOptions(home)
	fixture(t, legacyDataDir(opts), "", []string{"ses_a"}, nil)
	mustRun(t, opts)
	// A downgrade ran the old binary again and created a session at the
	// legacy location; the next start must stop instead of splitting data.
	fixture(t, legacyDataDir(opts), "", []string{"ses_newold"}, nil)
	if _, err := Run(opts); err == nil {
		t.Fatal("expected conflict after legacy store regrew")
	} else {
		var conflict *ConflictError
		if !errors.As(err, &conflict) {
			t.Fatalf("err = %v", err)
		}
	}
}

func TestCustomDataDirIsNeverMigrated(t *testing.T) {
	home := t.TempDir()
	opts := testOptions(home)
	fixture(t, legacyDataDir(opts), legacyLogs(opts), []string{"ses_a"}, nil)
	legacyStore := filepath.Join(legacyDataDir(opts), "sessions", "ses_a")
	// An explicit layout disables every plan: From == To or empty.
	opts.Sessions = Plan{}
	opts.Logs = Plan{}
	opts.LegacyStateFiles = nil
	opts.LegacyDataDir = ""
	report := mustRun(t, opts)
	if report.Changed() {
		t.Fatalf("report = %+v", report)
	}
	if !dirExists(legacyStore) {
		t.Error("legacy data touched despite disabled plans")
	}
}

func TestMigratesLogsAndBacksUpNameCollisions(t *testing.T) {
	home := t.TempDir()
	opts := testOptions(home)
	fixture(t, legacyDataDir(opts), legacyLogs(opts), nil, nil)
	// The new log target already has a live stdout.log.
	logsDir := opts.Logs.To
	if err := os.MkdirAll(logsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(logsDir, "stdout.log"), []byte("new log\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	report := mustRun(t, opts)
	if len(report.LogsMoved) != 1 || len(report.LogsBackedUp) != 1 {
		t.Fatalf("report = %+v", report)
	}
	data, err := os.ReadFile(filepath.Join(logsDir, "stdout.log"))
	if err != nil || string(data) != "new log\n" {
		t.Fatalf("new log overwritten: %q, %v", data, err)
	}
	backup := filepath.Base(report.LogsBackedUp[0])
	if !strings.HasPrefix(backup, "stdout.log.migrated-") {
		t.Fatalf("backup name = %q", backup)
	}
	if _, err := os.Stat(filepath.Join(logsDir, "stderr.log")); err != nil {
		t.Error("stderr.log not moved")
	}
}

func TestCrossFilesystemLogMove(t *testing.T) {
	home := t.TempDir()
	opts := testOptions(home)
	fixture(t, legacyDataDir(opts), legacyLogs(opts), nil, nil)
	withEXDEV(t)
	report := mustRun(t, opts)
	if len(report.LogsMoved) != 2 {
		t.Fatalf("report = %+v", report)
	}
	if dirExists(legacyLogs(opts)) {
		t.Error("legacy logs dir kept after copy")
	}
	data, err := os.ReadFile(filepath.Join(opts.Logs.To, "stdout.log"))
	if err != nil || string(data) != "stdout.log contents\n" {
		t.Fatalf("moved log = %q, %v", data, err)
	}
}

func TestJournalContainsNoSessionContents(t *testing.T) {
	home := t.TempDir()
	opts := testOptions(home)
	fixture(t, legacyDataDir(opts), "", []string{"ses_a"}, nil)
	mustRun(t, opts)
	data, err := os.ReadFile(opts.JournalPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "Migrated ses_a") {
		t.Error("journal leaks session titles")
	}
	info, err := os.Stat(opts.JournalPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("journal mode = %o, want 600", info.Mode().Perm())
	}
}

func ExampleConflictError_Error() {
	err := &ConflictError{Message: conflictMessage(
		Plan{From: "/old/sessions", To: "/new/sessions"},
		[]string{"ses_1"}, nil, []string{"ses_2"}, nil,
	)}
	fmt.Println(strings.SplitN(err.Error(), "\n", 2)[0])
	// Output: session store migration conflict: both the legacy store /old/sessions and the new store /new/sessions contain sessions
}
