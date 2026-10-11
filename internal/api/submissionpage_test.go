// SPDX-License-Identifier: AGPL-3.0-or-later
package api

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSubmissionShellCannotBeFramedOrLoadExternalScripts(t *testing.T) {
	srv, _, _ := newTestServer("operator")
	page := httptest.NewRecorder()
	srv.ServeHTTP(page, httptest.NewRequest("GET", "/me/submit", nil))
	if page.Code != 200 {
		t.Fatalf("shell: %d", page.Code)
	}
	policy := page.Header().Get("Content-Security-Policy")
	for _, required := range []string{"frame-ancestors 'none'", "script-src 'self'", "connect-src 'self'", "base-uri 'none'"} {
		if !strings.Contains(policy, required) {
			t.Errorf("missing %s", required)
		}
	}
	if page.Header().Get("Cache-Control") != "no-store" || page.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatal("shell must not leak handoff metadata")
	}
	asset := httptest.NewRecorder()
	srv.ServeHTTP(asset, httptest.NewRequest("GET", "/me/submission.js", nil))
	if asset.Code != 200 || !strings.HasPrefix(asset.Header().Get("Content-Type"), "text/javascript") {
		t.Fatal("script unavailable")
	}
}
