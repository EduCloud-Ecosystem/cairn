// SPDX-License-Identifier: AGPL-3.0-or-later
package reconcile_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/EduCloud-Ecosystem/cairn/internal/reconcile"
	"github.com/EduCloud-Ecosystem/cairn/internal/store"
	"github.com/EduCloud-Ecosystem/cairn/internal/store/memory"
	"github.com/EduCloud-Ecosystem/cairn/internal/store/sqlite"
	"github.com/EduCloud-Ecosystem/cairn/pkg/adapter"
)

type provider struct {
	id        string
	authority string
	fail      bool
	exists    bool
	during    func()
}

func (p *provider) IdentityAuthority() string {
	if p.authority != "" {
		return p.authority
	}
	return "https://provider.example"
}
func (p *provider) VerifyIdentity(_ context.Context, _ string, id string) error {
	if p.during != nil {
		p.during()
	}
	if p.fail {
		return errors.New("provider unavailable")
	}
	if id != p.id {
		return adapter.ErrIdentityMismatch
	}
	return nil
}
func (p *provider) RepoExists(context.Context, adapter.RepoRef) (bool, error) {
	if p.fail {
		return false, errors.New("provider unavailable")
	}
	return p.exists, nil
}
func fixture(t *testing.T, st store.Store) (reconcile.Service, *provider) {
	t.Helper()
	ctx := context.Background()
	now := time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(st.CreateUser(ctx, &store.User{ID: "instructor", Host: adapter.HostGitHub, HostUsername: "teacher", HostUserID: "10"}))
	must(st.CreateClassroom(ctx, &store.Classroom{ID: "class", Host: adapter.HostGitHub, HostNamespace: "course", CreatedBy: "instructor", CreatedAt: now}))
	must(st.CreateAssignment(ctx, &store.Assignment{ID: "assignment", ClassroomID: "class", Slug: "task", Type: store.AssignmentIndividual, CreatedAt: now}))
	must(st.CreateRosterEntry(ctx, &store.RosterEntry{ID: "learner", ClassroomID: "class", Host: adapter.HostGitHub, HostUsername: "alice", Status: store.RosterActive, ClaimedAt: &now}))
	must(st.CreateSubmission(ctx, &store.Submission{ID: "submission", AssignmentID: "assignment", RosterEntryID: "learner", Status: "failed", LatestCommit: "preserved-sha"}))
	must(st.CreateGrade(ctx, &store.Grade{ID: "grade", SubmissionID: "submission", Score: 8, MaxScore: 10, GradedAt: now}))
	p := &provider{id: "101"}
	return reconcile.Service{Store: st, Providers: map[adapter.Host]reconcile.Provider{adapter.HostGitHub: p}, Now: func() time.Time { return now }}, p
}
func TestIdentityReconciliation(t *testing.T) {
	factories := map[string]func(*testing.T) store.Store{"memory": func(t *testing.T) store.Store { return memory.New() }, "sqlite": func(t *testing.T) store.Store {
		st, e := sqlite.Open(filepath.Join(t.TempDir(), "test.db"))
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() { st.Close() })
		return st
	}}
	for name, newStore := range factories {
		t.Run(name, func(t *testing.T) {
			for _, scenario := range []string{"success", "recycled-name", "provider-instance-changed", "provider-failure", "drift-during-verify", "expired", "changed-evidence", "duplicate-id", "bound-id", "tampered-plan"} {
				t.Run(scenario, func(t *testing.T) {
					ctx := context.Background()
					st := newStore(t)
					svc, p := fixture(t, st)
					digest := strings.Repeat("a", 64)
					plan, err := svc.PlanIdentity(ctx, "learner", "101", "operator", digest)
					if err != nil {
						t.Fatal(err)
					}
					switch scenario {
					case "provider-instance-changed":
						p.authority = "https://other.example"
					case "recycled-name":
						p.id = "202"
					case "provider-failure":
						p.fail = true
					case "drift-during-verify":
						p.during = func() {
							r, _ := st.GetRosterEntry(ctx, "learner")
							r.HostUsername = "renamed"
							if e := st.UpdateRosterEntry(ctx, r); e != nil {
								t.Fatal(e)
							}
						}
					case "expired":
						svc.Now = func() time.Time { return plan.ExpiresAt }
					case "changed-evidence":
						digest = strings.Repeat("b", 64)
					case "duplicate-id":
						if e := st.CreateRosterEntry(ctx, &store.RosterEntry{ID: "other", ClassroomID: "class", Host: adapter.HostGitHub, HostUsername: "other", HostUserID: "101", Status: store.RosterActive}); e != nil {
							t.Fatal(e)
						}
					case "bound-id":
						rs := st.(store.ReconciliationStore)
						if _, e := rs.BindLegacyIdentity(ctx, store.IdentityReconciliation{PlanID: "another", RosterID: plan.RosterID, Snapshot: plan.Snapshot, ProviderID: "999", Operator: "operator", EvidenceSHA256: digest, AppliedAt: plan.CreatedAt}); e != nil {
							t.Fatal(e)
						}
					case "tampered-plan":
						plan.ProviderID = "202"
					}
					already, err := svc.Apply(ctx, plan, "operator", digest)
					r, _ := st.GetRosterEntry(ctx, "learner")
					audits, e := st.(store.ReconciliationStore).ListIdentityReconciliations(ctx)
					if e != nil {
						t.Fatal(e)
					}
					if scenario == "success" {
						if err != nil || already || r.HostUserID != "101" || len(audits) != 1 {
							t.Fatalf("apply %v %v roster=%+v audits=%+v", already, err, r, audits)
						}
						svc.Now = func() time.Time { return plan.ExpiresAt.Add(time.Hour) }
						already, err = svc.Apply(ctx, plan, "operator", digest)
						if err != nil || !already {
							t.Fatalf("retry %v %v", already, err)
						}
						if _, e = svc.PlanIdentity(ctx, "learner", "101", "operator", digest); e == nil {
							t.Fatal("must not plan overwrite")
						}
					} else if err == nil {
						t.Fatalf("%s must fail", scenario)
					} else if scenario != "bound-id" && (r.HostUserID != "" || len(audits) != 0) {
						t.Fatalf("partial mutation: %+v %+v", r, audits)
					}
					sub, e := st.GetSubmission(ctx, "submission")
					if e != nil || sub.LatestCommit != "preserved-sha" || sub.Status != "failed" {
						t.Fatalf("coursework changed: %+v %v", sub, e)
					}
					grades, e := st.ListGradesBySubmission(ctx, "submission")
					if e != nil || len(grades) != 1 || grades[0].Score != 8 {
						t.Fatalf("grades changed: %+v %v", grades, e)
					}
				})
			}
		})
	}
}
func TestInventoryDoesNotAdoptOrSuppressFailures(t *testing.T) {
	ctx := context.Background()
	st := memory.New()
	svc, p := fixture(t, st)
	p.exists = true
	inv, err := svc.Inventory(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	codes := map[string]bool{}
	for _, i := range inv.Issues {
		codes[i.Code] = true
	}
	for _, want := range []string{"unbound_identity", "unbound_repository", "possible_orphan_repository"} {
		if !codes[want] {
			t.Errorf("missing %s: %+v", want, inv)
		}
	}
	sub, _ := st.GetSubmission(ctx, "submission")
	if sub.Repo != (adapter.RepoRef{}) {
		t.Fatal("inventory adopted repo")
	}
	p.fail = true
	inv, err = svc.Inventory(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, i := range inv.Issues {
		found = found || i.Code == "repository_probe_failed"
	}
	if !found {
		t.Fatal("partial provider failure omitted")
	}
	sub.Repo = adapter.RepoRef{Host: adapter.HostGitHub, Namespace: "other", Name: "legacy"}
	st.UpdateSubmission(ctx, sub)
	inv, err = svc.Inventory(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	found = false
	for _, i := range inv.Issues {
		found = found || i.Code == "legacy_or_mismatched_repository"
	}
	if !found {
		t.Fatal("mismatch omitted")
	}
}
func TestSQLiteAuditFailureRollsBackIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.db")
	st, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	svc, _ := fixture(t, st)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`CREATE TRIGGER fail_audit BEFORE INSERT ON reconciliation_audit BEGIN SELECT RAISE(ABORT, 'simulated receipt write failure'); END`); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	digest := strings.Repeat("a", 64)
	plan, err := svc.PlanIdentity(ctx, "learner", "101", "operator", digest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Apply(ctx, plan, "operator", digest); err == nil {
		t.Fatal("audit failure ignored")
	}
	r, err := st.GetRosterEntry(ctx, "learner")
	if err != nil || r.HostUserID != "" {
		t.Fatalf("binding committed without receipt: %+v %v", r, err)
	}
	if _, err = db.Exec(`DROP TRIGGER fail_audit`); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Apply(ctx, plan, "operator", digest); err != nil {
		t.Fatal(err)
	}
	if err = st.Close(); err != nil {
		t.Fatal(err)
	}
	restored, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	svc.Store = restored
	already, err := svc.Apply(ctx, plan, "operator", digest)
	if err != nil || !already {
		t.Fatalf("restart retry: %v %v", already, err)
	}
}

func TestSQLiteDuplicateLegacyBindingsBlockUpgradeWithoutErasure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "duplicate.db")
	st, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	svc, _ := fixture(t, st)
	ctx := context.Background()
	plan, err := svc.PlanIdentity(ctx, "learner", "101", "operator", strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Apply(ctx, plan, "operator", plan.EvidenceSHA256); err != nil {
		t.Fatal(err)
	}
	if err = st.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`DROP INDEX idx_roster_bound_identity`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO roster_entries(id,classroom_id,host,host_username,status,host_user_id) VALUES ('duplicate','class','github','renamed','active','101')`); err != nil {
		t.Fatal(err)
	}
	if reopened, e := sqlite.Open(path); e == nil {
		reopened.Close()
		t.Fatal("upgrade accepted duplicate immutable identities")
	}
	var n int
	if err = db.QueryRow(`SELECT count(*) FROM roster_entries WHERE host_user_id='101'`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("duplicates were altered: count=%d err=%v", n, err)
	}
	if err = db.QueryRow(`SELECT count(*) FROM grades WHERE id='grade'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("coursework erased: count=%d err=%v", n, err)
	}
}
