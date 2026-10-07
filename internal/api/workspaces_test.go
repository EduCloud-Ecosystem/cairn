// SPDX-License-Identifier: AGPL-3.0-or-later
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/EduCloud-Ecosystem/cairn/internal/store"
	"github.com/EduCloud-Ecosystem/cairn/pkg/adapter"
)

func TestWorkspaceConfigRejectsUnsafeDestinations(t *testing.T) {
	for _, raw := range []string{`[]`, `{"c1":"javascript:alert(1)"}`, `{"c1":"https://user:secret@example.org"}`, `{"c1":"http://example.org"}`, `{"c1":"https://example.org/?token=secret"}`, `{"c1":"https://example.org/#secret"}`, `{"c1":"//example.org"}`, `{"":"https://example.org"}`} {
		if _, err := ParseWorkspaceURLs(raw); err == nil {
			t.Errorf("accepted unsafe config %q", raw)
		}
	}
	for _, raw := range []string{"", `{}`, `{"c1":"https://work.example.org"}`, `{"c1":"http://127.0.0.1:18000"}`} {
		if _, err := ParseWorkspaceURLs(raw); err != nil {
			t.Errorf("valid config rejected: %v", err)
		}
	}
}

func TestWorkspaceLinksStayScopedToOwnClasswork(t *testing.T) {
	srv, st, _ := newTestServer("operator")
	seedStudentData(t, st)
	srv.workspaceURLs = validatedWorkspaceURLs(map[string]string{"c1": "https://intro.example.org", "c2": "https://advanced.example.org"})
	req := httptest.NewRequest(http.MethodGet, "/me/work", nil)
	req.AddCookie(studentCookie(srv, adapter.HostGitHub, "bob"))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	var data struct {
		Work []workItem `json:"work"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &data); err != nil {
		t.Fatal(err)
	}
	if len(data.Work) != 1 || data.Work[0].WorkspaceURL != "https://intro.example.org" {
		t.Fatalf("unexpected workspace access: %s", rec.Body.String())
	}
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/me/work", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous = %d", rec.Code)
	}
	req = httptest.NewRequest(http.MethodGet, "/me/work/s2", nil)
	req.AddCookie(studentCookie(srv, adapter.HostGitHub, "bob"))
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("other class submission = %d", rec.Code)
	}

	roster, err := st.GetRosterEntry(context.Background(), "rb1")
	if err != nil {
		t.Fatal(err)
	}
	roster.Status = store.RosterRemoved
	if err := st.UpdateRosterEntry(context.Background(), roster); err != nil {
		t.Fatal(err)
	}
	req = httptest.NewRequest(http.MethodGet, "/me/work", nil)
	req.AddCookie(studentCookie(srv, adapter.HostGitHub, "bob"))
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	data.Work = nil
	if err := json.Unmarshal(rec.Body.Bytes(), &data); err != nil {
		t.Fatal(err)
	}
	for _, item := range data.Work {
		if item.WorkspaceURL != "" {
			t.Fatal("removed roster member still received a workspace link")
		}
	}
}
