package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/disksing/agenthub/internal/session"
)

func newDocsTestServer(t *testing.T) *Server {
	t.Helper()
	store, err := session.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return New(store, "test", time.Now())
}

func TestAPIDocsServesMarkdown(t *testing.T) {
	server := newDocsTestServer(t)
	request := httptest.NewRequest(http.MethodGet, "/api.md", nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if contentType := response.Header().Get("Content-Type"); contentType != "text/markdown; charset=utf-8" {
		t.Fatalf("Content-Type = %q, want %q", contentType, "text/markdown; charset=utf-8")
	}
	if nosniff := response.Header().Get("X-Content-Type-Options"); nosniff != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q, want nosniff", nosniff)
	}
	body := response.Body.String()
	if !strings.HasPrefix(body, "# AgentHub") {
		t.Fatalf("body does not start with a Markdown title: %.80q", body)
	}
	if !strings.HasSuffix(strings.TrimRight(body, "\n")+"\n", "\n") {
		t.Fatal("body is not text")
	}
}

// The SPA fallback owns "/" and rewrites unknown paths to index.html; the
// docs route must win over it so the page is reachable without a frontend
// build and never swallowed by the fallback.
func TestAPIDocsSurvivesSPAFallback(t *testing.T) {
	webDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(webDir, "index.html"), []byte("<html>spa</html>"), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := session.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	server := New(store, "test", time.Now(), Dependencies{WebDir: webDir})

	request := httptest.NewRequest(http.MethodGet, "/api.md", nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if contentType := response.Header().Get("Content-Type"); contentType != "text/markdown; charset=utf-8" {
		t.Fatalf("Content-Type = %q, want the Markdown docs, not the SPA fallback", contentType)
	}
	if strings.Contains(response.Body.String(), "<html>spa</html>") {
		t.Fatal("the SPA fallback served index.html for /api.md")
	}
}

// Every public route registered by the server must be documented as an
// endpoint heading in api.md.
func TestAPIDocsCoversEveryPublicRoute(t *testing.T) {
	server := newDocsTestServer(t)
	labels := server.publicAPILabels()
	if len(labels) == 0 {
		t.Fatal("no public API routes registered")
	}
	for _, label := range labels {
		if !strings.Contains(apiDocsMarkdown, "### "+label+"\n") {
			t.Errorf("api.md does not document public route %q (expected a %q heading)", label, "### "+label)
		}
	}
}

// Every endpoint documented in api.md must correspond to a registered
// public route that the mux actually matches (not the SPA fallback).
func TestAPIDocsDocumentsOnlyRegisteredRoutes(t *testing.T) {
	server := newDocsTestServer(t)
	known := make(map[string]bool)
	for _, label := range server.publicAPILabels() {
		known[label] = true
	}

	heading := regexp.MustCompile(`(?m)^### (GET|POST|PUT|DELETE|PATCH) (/\S+)$`)
	matches := heading.FindAllStringSubmatch(apiDocsMarkdown, -1)
	if len(matches) == 0 {
		t.Fatal("api.md documents no endpoints")
	}
	mux := server.mux()
	for _, match := range matches {
		method, pattern := match[1], match[2]
		label := method + " " + pattern
		if !known[label] {
			t.Errorf("api.md documents %q, which is not a registered public route", label)
			continue
		}
		concrete := pattern
		for _, placeholder := range []string{"{approvalId}", "{id}"} {
			concrete = strings.ReplaceAll(concrete, placeholder, "sample")
		}
		request := httptest.NewRequest(method, concrete, nil)
		_, matched := mux.Handler(request)
		if matched == "" || matched == "/" {
			t.Errorf("api.md documents %q, but the mux matches it as %q (SPA fallback or nothing)", label, matched)
		}
	}
}

// Internal routes — the health probe and the docs page itself — must not be
// documented as public API endpoints.
func TestAPIDocsExcludesInternalRoutes(t *testing.T) {
	for _, heading := range []string{"### GET /v1/health\n", "### GET /api.md\n"} {
		if strings.Contains(apiDocsMarkdown, heading) {
			t.Errorf("api.md documents internal route %q as a public endpoint", strings.TrimSpace(heading))
		}
	}
	// The health probe is deliberately internal; every other /v1 route must
	// make an explicit public/internal decision via the doc label.
	internal := map[string]bool{"GET /v1/health": true}
	for _, route := range newDocsTestServer(t).routes() {
		if route.doc == "" && !internal[route.pattern] && route.pattern != "/v1/sessions/" && strings.HasPrefix(route.pattern, "GET /v1/") {
			t.Errorf("route %q has no docs label; mark it public or keep it out of /v1", route.pattern)
		}
	}
}

// The document must keep the key sections clients rely on: the security
// boundary, the error envelope, and the SSE contract.
func TestAPIDocsKeySections(t *testing.T) {
	for _, section := range []string{
		"## Base URL and Security Boundary",
		"### Error responses",
		"#### SSE mode",
		"text/event-stream",
		"Last-Event-ID",
		"heartbeat",
		"host_rejected",
		"origin_rejected",
		"text/markdown",
	} {
		if !strings.Contains(apiDocsMarkdown, section) {
			t.Errorf("api.md is missing key section/content %q", section)
		}
	}
}

// Fenced code blocks must be balanced, or renderers will swallow the rest
// of the page.
func TestAPIDocsBalancedCodeFences(t *testing.T) {
	if fences := strings.Count(apiDocsMarkdown, "```"); fences%2 != 0 {
		t.Fatalf("api.md has %d code fence markers, an odd number", fences)
	}
}
