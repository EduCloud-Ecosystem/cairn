// SPDX-License-Identifier: AGPL-3.0-or-later
package api

import (
	_ "embed"
	"net/http"
)

//go:embed submissionpage.html
var submissionPageHTML string

//go:embed submissionpage.js
var submissionPageJS string

// This public shell contains no student data. Every data request still requires
// the provider-authenticated session and the backend's own-submission checks.
func (s *Server) handleSubmissionPage(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'unsafe-inline'; connect-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'")
	_, _ = w.Write([]byte(submissionPageHTML))
}

func (s *Server) handleSubmissionScript(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write([]byte(submissionPageJS))
}
