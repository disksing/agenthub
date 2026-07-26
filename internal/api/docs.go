package api

import (
	_ "embed"
	"io"
	"net/http"
)

// apiDocsMarkdown is the embedded Markdown API reference served at
// GET /api.md. It is static and read-only: it ships inside the daemon
// binary, needs no frontend build and no external documentation service.
// The route goes through the same host guard and request middleware as
// every other route, so serving documentation never weakens the daemon's
// security posture. docs_test.go verifies that the document covers exactly
// the public routes registered in routes() and sessionOps().
//
//go:embed api.md
var apiDocsMarkdown string

// apiDocs serves the Markdown API reference. The content type follows
// RFC 7763 with an explicit UTF-8 charset.
func (s *Server) apiDocs(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, apiDocsMarkdown)
}
