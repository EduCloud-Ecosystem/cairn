// SPDX-License-Identifier: AGPL-3.0-or-later
package api

import (
	"context"
	"github.com/EduCloud-Ecosystem/cairn/internal/handoff"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSubmissionReceiptsRemainVisibleForClosedWork(t *testing.T) {
	srv, st, _ := newTestServer("operator")
	seedStudentData(t, st)
	srv.handoff = &handoff.Service{Classrooms: map[string]bool{"c1": true}}
	sub, err := st.GetSubmission(context.Background(), "s1")
	if err != nil {
		t.Fatal(err)
	}
	if item := newEnricher(srv).item(context.Background(), sub); !item.SubmissionsEnabled || !item.SubmissionOpen {
		t.Fatal("active handoff unavailable")
	}
	sub.Status = "locked"
	if item := newEnricher(srv).item(context.Background(), sub); !item.SubmissionsEnabled || item.SubmissionOpen {
		t.Fatal("closing work must retain receipt access without enabling new uploads")
	}
}

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
