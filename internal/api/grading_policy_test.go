// SPDX-License-Identifier: AGPL-3.0-or-later
package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/EduCloud-Ecosystem/cairn/internal/store"
	"github.com/EduCloud-Ecosystem/cairn/pkg/adapter"
)

func TestOnlyOperatorCanPinGradingPolicy(t *testing.T) {
	srv, st := newAuthServer("alice")
	ctx := context.Background()
	if err := st.CreateAssignment(ctx, &store.Assignment{ID: "a1", TemplateRef: adapter.TemplateRef{Host: adapter.HostGitHub, Namespace: "teacher", Name: "template"}}); err != nil {
		t.Fatal(err)
	}
	const revision = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	valid := `{"template_commit":"` + revision + `","grading_spec":"grading.json"}`
	for _, cookie := range []*http.Cookie{nil, studentCookie(srv, adapter.HostGitHub, "bob")} {
		req := httptest.NewRequest(http.MethodPatch, "/assignments/a1/grading-policy", strings.NewReader(valid))
		if cookie != nil {
			req.AddCookie(cookie)
		}
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("non-operator policy change = %d", rec.Code)
		}
	}
	operator := login(t, srv)
	for _, body := range []string{`{"template_commit":"main"}`, `{"template_commit":"` + revision + `","grading_spec":"../escape"}`, `{"template_commit":"` + revision + `","grading_spec":".git/config"}`, valid} {
		req := httptest.NewRequest(http.MethodPatch, "/assignments/a1/grading-policy", strings.NewReader(body))
		req.AddCookie(operator)
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		want := http.StatusBadRequest
		if body == valid {
			want = http.StatusOK
		}
		if rec.Code != want {
			t.Fatalf("status = %d want %d: %s", rec.Code, want, rec.Body.String())
		}
	}
	asg, _ := st.GetAssignment(ctx, "a1")
	if asg.TemplateRef.Ref != revision || asg.GradingSpec != "grading.json" {
		t.Fatalf("policy did not persist: %+v", asg)
	}
}

func TestUnpinnedGradingRequestDoesNotQueueWork(t *testing.T) {
	srv, st, q := newTestServer("alice")
	if err := st.CreateAssignment(context.Background(), &store.Assignment{ID: "a1"}); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/assignments/a1/grade", nil))
	if rec.Code != http.StatusConflict || len(q.jobs) != 0 {
		t.Fatalf("unpinned grading status=%d jobs=%d", rec.Code, len(q.jobs))
	}
}
