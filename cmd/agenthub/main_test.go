package main

import (
	"bytes"
	"strings"
	"testing"
	"unicode"
)

// captureHelp redirects helpOutput for the duration of fn and returns what
// was written.
func captureHelp(t *testing.T, fn func() error) (string, error) {
	t.Helper()
	var buffer bytes.Buffer
	previous := helpOutput
	helpOutput = &buffer
	defer func() { helpOutput = previous }()
	err := fn()
	return buffer.String(), err
}

func TestBareInvocationPrintsRootHelp(t *testing.T) {
	output, err := captureHelp(t, func() error { return run(nil) })
	if err != nil {
		t.Fatalf("run(nil) returned error: %v", err)
	}
	if !strings.Contains(output, "Usage:") {
		t.Fatalf("root help missing usage:\n%s", output)
	}
}

func TestRootHelpCoversCommandsAndConcepts(t *testing.T) {
	for _, command := range []string{"serve", "status", "agents", "run", "chat", "session", "version", "help"} {
		if !strings.Contains(rootHelp, command) {
			t.Errorf("root help does not mention command %q", command)
		}
	}
	for _, concept := range []string{
		"Provider", "Agent", "Profile", "Session", "Turn", "Approval", "Events",
		"daemon", "single writer", "providerSessionId",
		"AGENTHUB_HOME", "AGENTHUB_ENDPOINT",
		"config.json", "events.jsonl", "session.json",
	} {
		if !strings.Contains(rootHelp, concept) {
			t.Errorf("root help does not explain %q", concept)
		}
	}
}

func TestRootHelpEntries(t *testing.T) {
	for _, args := range [][]string{{"help"}, {"--help"}, {"-h"}} {
		output, err := captureHelp(t, func() error { return run(args) })
		if err != nil {
			t.Fatalf("run(%v) returned error: %v", args, err)
		}
		if !strings.Contains(output, "agenthub serve") {
			t.Fatalf("run(%v) did not print root help:\n%s", args, output)
		}
	}
}

func TestHelpTopicsHaveUsageAndExamples(t *testing.T) {
	for name, text := range helpTopics {
		if !strings.Contains(text, "Usage:") {
			t.Errorf("help topic %q has no Usage section", name)
		}
		if !strings.Contains(text, "agenthub "+name) {
			t.Errorf("help topic %q does not show its own command line", name)
		}
	}
	for _, topic := range []string{"serve", "run", "chat", "session create", "session list", "session approve"} {
		if !strings.Contains(helpTopics[topic], "Examples:") {
			t.Errorf("help topic %q has no Examples section", topic)
		}
	}
}

func TestHelpCommandTopics(t *testing.T) {
	cases := map[string]string{
		"serve":          "agenthub serve [--addr host:port]",
		"run":            "--agent id",
		"session":        "create",
		"session list":   "--all",
		"session attach": "agenthub chat --session",
	}
	for topic, want := range cases {
		args := append([]string{"help"}, strings.Split(topic, " ")...)
		output, err := captureHelp(t, func() error { return run(args) })
		if err != nil {
			t.Fatalf("run(%v) returned error: %v", args, err)
		}
		if !strings.Contains(output, want) {
			t.Errorf("help for %q missing %q:\n%s", topic, want, output)
		}
	}
}

func TestSubcommandHelpFlag(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"serve", "--help"}, "agenthub serve [--addr host:port]"},
		{[]string{"status", "--help"}, "agenthub status"},
		{[]string{"agents", "-h"}, "agenthub agents"},
		{[]string{"run", "--help"}, "agenthub run [--cwd dir]"},
		{[]string{"chat", "--help"}, "/interrupt"},
		{[]string{"session", "create", "--help"}, "agenthub session create"},
		{[]string{"session", "list", "--help"}, "--json"},
		{[]string{"session", "show", "--help"}, "agenthub session show <session-id>"},
		{[]string{"session", "approve", "--help"}, "--decision decision"},
		{[]string{"session", "archive", "--help"}, "agenthub session archive <session-id>"},
	}
	for _, testCase := range cases {
		output, err := captureHelp(t, func() error { return run(testCase.args) })
		if err != nil {
			t.Errorf("run(%v) returned error: %v", testCase.args, err)
		}
		if !strings.Contains(output, testCase.want) {
			t.Errorf("run(%v) help missing %q:\n%s", testCase.args, testCase.want, output)
		}
	}
}

func TestSessionGroupHelpEntries(t *testing.T) {
	for _, args := range [][]string{{"session"}, {"session", "help"}} {
		output, err := captureHelp(t, func() error { return run(args) })
		if err != nil {
			t.Fatalf("run(%v) returned error: %v", args, err)
		}
		for _, sub := range []string{"create", "list", "show", "events", "attach", "resume", "interrupt", "stop", "approve", "archive"} {
			if !strings.Contains(output, sub) {
				t.Errorf("run(%v) session help does not list %q", args, sub)
			}
		}
	}
	output, err := captureHelp(t, func() error { return run([]string{"session", "help", "resume"}) })
	if err != nil {
		t.Fatalf("session help resume returned error: %v", err)
	}
	if !strings.Contains(output, "agenthub session resume <session-id>") {
		t.Errorf("session help resume printed wrong topic:\n%s", output)
	}
}

func TestUnknownCommandError(t *testing.T) {
	err := run([]string{"bogus"})
	if err == nil {
		t.Fatal("expected error for unknown command")
	}
	if !strings.Contains(err.Error(), `unknown command "bogus"`) {
		t.Errorf("error does not name the command: %v", err)
	}
	if !strings.Contains(err.Error(), "agenthub help") {
		t.Errorf("error does not point at help: %v", err)
	}
}

func TestUnknownSessionCommandError(t *testing.T) {
	err := run([]string{"session", "bogus"})
	if err == nil {
		t.Fatal("expected error for unknown session command")
	}
	if !strings.Contains(err.Error(), `unknown session command "bogus"`) {
		t.Errorf("error does not name the command: %v", err)
	}
	if !strings.Contains(err.Error(), "agenthub help session") {
		t.Errorf("error does not point at session help: %v", err)
	}
}

func TestUnknownHelpTopicError(t *testing.T) {
	err := run([]string{"help", "bogus"})
	if err == nil {
		t.Fatal("expected error for unknown help topic")
	}
	if !strings.Contains(err.Error(), "unknown help topic") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestUsageErrorsPointAtHelp(t *testing.T) {
	cases := []struct {
		args []string
		want []string
	}{
		{[]string{"status", "extra"}, []string{"usage: agenthub status", "agenthub help status"}},
		{[]string{"agents", "extra"}, []string{"usage: agenthub agents", "agenthub help agents"}},
		{[]string{"run"}, []string{"usage: agenthub run", "agenthub help run"}},
		{[]string{"session", "show"}, []string{"usage: agenthub session show", "agenthub help session show"}},
		{[]string{"session", "events"}, []string{"usage: agenthub session events", "agenthub help session events"}},
		{[]string{"session", "attach"}, []string{"usage: agenthub session attach", "agenthub help session attach"}},
		{[]string{"session", "approve"}, []string{"usage: agenthub session approve", "agenthub help session approve"}},
		{[]string{"session", "list", "extra"}, []string{"usage: agenthub session list", "agenthub help session list"}},
		{[]string{"run", "--bogus"}, []string{"agenthub help run"}},
		{[]string{"serve", "--bogus"}, []string{"agenthub help serve"}},
	}
	for _, testCase := range cases {
		err := run(testCase.args)
		if err == nil {
			t.Errorf("run(%v): expected error", testCase.args)
			continue
		}
		for _, want := range testCase.want {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("run(%v): error missing %q: %v", testCase.args, want, err)
			}
		}
	}
}

func TestHelpTextIsEnglishOnly(t *testing.T) {
	assertNoCJK := func(name, text string) {
		for _, r := range text {
			if unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hiragana, r) || unicode.Is(unicode.Katakana, r) || unicode.Is(unicode.Hangul, r) {
				t.Errorf("help text %q contains non-latin script character %q", name, r)
				return
			}
		}
	}
	assertNoCJK("root", rootHelp)
	for name, text := range helpTopics {
		assertNoCJK(name, text)
	}
}

func TestAgentTagConflictStillReported(t *testing.T) {
	for _, args := range [][]string{
		{"run", "--agent", "a", "--tag", "b", "hello"},
		{"session", "create", "--agent", "a", "--tag", "b"},
	} {
		err := run(args)
		if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
			t.Errorf("run(%v): expected mutual exclusion error, got %v", args, err)
		}
	}
}
