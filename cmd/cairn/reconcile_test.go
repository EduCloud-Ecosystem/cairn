// SPDX-License-Identifier: AGPL-3.0-or-later
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/EduCloud-Ecosystem/cairn/internal/reconcile"
	"github.com/EduCloud-Ecosystem/cairn/internal/store"
	"github.com/EduCloud-Ecosystem/cairn/internal/store/sqlite"
	"github.com/EduCloud-Ecosystem/cairn/pkg/adapter"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestReconcileInventoryPrivateNoOverwrite(t *testing.T) {
	t.Setenv("CAIRN_STORE", "sqlite")
	t.Setenv("CAIRN_SQLITE_PATH", filepath.Join(t.TempDir(), "test.db"))
	out := filepath.Join(t.TempDir(), "inventory.json")
	if err := runReconcile([]string{"inventory", "--out", out}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(out)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("mode = %o", info.Mode().Perm())
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var report reconcile.Inventory
	if err = json.Unmarshal(b, &report); err != nil {
		t.Fatal(err)
	}
	if report.Version != 1 || len(report.Limitations) == 0 {
		t.Fatalf("incomplete %+v", report)
	}
	if err = runReconcile([]string{"inventory", "--out", out}); err == nil {
		t.Fatal("overwrote report")
	}
	if err = runReconcile([]string{"plan-identity", "--out", filepath.Join(t.TempDir(), "plan.json")}); err == nil {
		t.Fatal("accepted missing historical evidence attestation")
	}
}
func TestReconciliationPlanRejectsTrailingOrUnknownJSON(t *testing.T) {
	for _, body := range []string{`{} {}`, `{"unexpected":"field"}`} {
		p := filepath.Join(t.TempDir(), "plan.json")
		if err := os.WriteFile(p, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		var plan reconcile.Plan
		if err := readReconciliationPlan(p, &plan); err == nil {
			t.Fatal("invalid plan accepted")
		}
	}
}

func TestReconcileCLIReviewedIdentityWorkflow(t *testing.T) {
	t.Setenv("CAIRN_STORE", "sqlite")
	path := filepath.Join(t.TempDir(), "test.db")
	t.Setenv("CAIRN_SQLITE_PATH", path)
	t.Setenv("CAIRN_GITHUB_APP_ID", "")
	t.Setenv("CAIRN_GITLAB_TOKEN", "")
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/users/learner" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(500)
			return
		}
		fmt.Fprint(w, `{"id":42,"login":"learner"}`)
	}))
	defer srv.Close()
	t.Setenv("CAIRN_FORGEJO_BASE_URL", srv.URL)
	t.Setenv("CAIRN_FORGEJO_TOKEN", "test-fixture")
	st, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	now := time.Now().UTC()
	for _, e := range []error{st.CreateUser(ctx, &store.User{ID: "owner", Host: adapter.HostForgejo, HostUsername: "owner", HostUserID: "1"}), st.CreateClassroom(ctx, &store.Classroom{ID: "course", Host: adapter.HostForgejo, CreatedBy: "owner"}), st.CreateRosterEntry(ctx, &store.RosterEntry{ID: "legacy", ClassroomID: "course", Host: adapter.HostForgejo, HostUsername: "learner", Status: store.RosterActive, ClaimedAt: &now})} {
		if e != nil {
			t.Fatal(e)
		}
	}
	st.Close()
	dir := t.TempDir()
	evidence := filepath.Join(dir, "evidence.txt")
	if err = os.WriteFile(evidence, []byte("Synthetic historical provider export: immutable ID 42; independently reviewed."), 0600); err != nil {
		t.Fatal(err)
	}
	plan := filepath.Join(dir, "plan.json")
	base := []string{"--operator", "owner", "--evidence-file", evidence, "--historical-identity-verified"}
	args := append([]string{"plan-identity", "--roster", "legacy", "--provider-id", "42", "--out", plan}, base...)
	if err = runReconcile(args); err != nil {
		t.Fatal(err)
	}
	args = append([]string{"apply", "--plan", plan, "--out", filepath.Join(dir, "receipt.json")}, base...)
	if err = runReconcile(args); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("provider reads=%d", calls)
	}
	reopened, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	r, err := reopened.GetRosterEntry(ctx, "legacy")
	if err != nil || r.HostUserID != "42" {
		t.Fatalf("binding %+v %v", r, err)
	}
	audit, err := reopened.ListIdentityReconciliations(ctx)
	if err != nil || len(audit) != 1 || audit[0].Operator != "owner" {
		t.Fatalf("audit %+v %v", audit, err)
	}
}
