// SPDX-License-Identifier: AGPL-3.0-or-later
package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/EduCloud-Ecosystem/cairn/internal/grading"
	"github.com/EduCloud-Ecosystem/cairn/internal/gradingjob"
	"github.com/EduCloud-Ecosystem/cairn/internal/provisioning"
	"github.com/EduCloud-Ecosystem/cairn/internal/store"
	"github.com/EduCloud-Ecosystem/cairn/internal/store/sqlite"
	"github.com/EduCloud-Ecosystem/cairn/pkg/adapter"
	"github.com/EduCloud-Ecosystem/cairn/pkg/gradingspec"
)

func invokeManaged(ctx context.Context, s *grading.Service, job gradingjob.Context) error {
	managed, err := s.GradeManagedJob(ctx, job)
	if !managed {
		return errors.New("expected managed Worker boundary")
	}
	return err
}

func managedService(st store.Store, r grading.Runner, c grading.Checkout) *grading.Service {
	return grading.NewService(st, r, c)
}
func managedWorker(st store.Store, r grading.Runner, c grading.Checkout) *provisioning.Worker {
	return &provisioning.Worker{Store: st, Grader: managedService(st, r, c), Backoff: func(int) time.Duration { return 0 }}
}
func claimManagedFixture(t *testing.T, f *pinnedFixture) gradingjob.Context {
	t.Helper()
	if f.request(false) != http.StatusOK {
		t.Fatal("enqueue")
	}
	j, err := f.db.ClaimNextJob(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	target, err := gradingjob.Decode(j.TargetRef)
	if err != nil {
		t.Fatal(err)
	}
	return gradingjob.Context{JobID: j.ID, Target: target}
}
func requireReconciliation(t *testing.T, err error) {
	t.Helper()
	var got *grading.ReconciliationRequired
	if !errors.As(err, &got) {
		t.Fatalf("want reconciliation, got %v", err)
	}
}
func rawManagedDB(t *testing.T, f *pinnedFixture) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", f.path+"?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}
func mustManagedSQL(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatal(err)
	}
}

func TestManagedGradeWorkerAtomicPublicationAndRetention(t *testing.T) {
	f := newPinnedFixture(t)
	if f.request(false) != http.StatusOK {
		t.Fatal("enqueue")
	}
	r := &contextPinnedRunner{}
	c := &pinnedCheckout{}
	if did, err := managedWorker(f.db, r, c).RunOnce(context.Background()); !did || err != nil {
		t.Fatalf("run %v %v", did, err)
	}
	j := r.jobs[0]
	d, err := f.db.GetGradingDispatch(context.Background(), j.JobID)
	if err != nil || d.State != store.DispatchPublished {
		t.Fatalf("dispatch %+v %v", d, err)
	}
	g, err := f.db.LatestGradeForSubmission(context.Background(), "s")
	if err != nil || g.RunID != d.RunID || g.ID != d.GradeID || store.GradingDigest(g.Breakdown) != d.ResultSHA256 {
		t.Fatal("unbound grade", err)
	}
	q, err := f.db.GetGradingJob(context.Background(), j.JobID)
	if err != nil || q.Status != store.JobSucceeded {
		t.Fatal("queue not atomically successful")
	}
	// Both same mode and changed ordinary mode are terminal with zero checkout.
	for _, runner := range []grading.Runner{&contextPinnedRunner{}, &ordinaryPinnedRunner{}} {
		checkout := &pinnedCheckout{}
		if err := managedService(f.db, runner, checkout).GradeJob(context.Background(), j); err != nil || len(checkout.calls) != 0 {
			t.Fatalf("duplicate %v", err)
		}
	}
	stale := *q
	stale.Status = store.JobPending
	if err := f.db.UpdateJob(context.Background(), &stale); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("stale queue overwrite: %v", err)
	}
	if did, err := managedWorker(f.db, r, c).RunOnce(context.Background()); did || err != nil {
		t.Fatal("reclaimed terminal job")
	}
	if _, err = f.db.ConfirmExport(context.Background(), "c", time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	ng, nr, err := f.db.PurgeExportedGrades(context.Background(), time.Now())
	if err != nil || ng != 1 || nr != 1 {
		t.Fatalf("purge %d %d %v", ng, nr, err)
	}
	if err = f.db.DeleteRosterEntry(context.Background(), "r"); err != nil {
		t.Fatal(err)
	}
	f.reopen()
	after, err := f.db.GetGradingDispatch(context.Background(), j.JobID)
	if err != nil || *after != *d {
		t.Fatal("retention erased barrier", err)
	}
	checkout := &pinnedCheckout{}
	if err = managedService(f.db, &ordinaryPinnedRunner{}, checkout).GradeJob(context.Background(), j); err != nil || len(checkout.calls) != 0 {
		t.Fatal("purged result replay", err)
	}
	var count int
	if err = rawManagedDB(t, f).QueryRow(`SELECT count(*) FROM grades`).Scan(&count); err != nil || count != 0 {
		t.Fatal("purged body recreated", err)
	}
	// Queue deletion must not remove or orphan the tombstone.
	if _, err = rawManagedDB(t, f).Exec(`DELETE FROM provisioning_jobs WHERE id=?`, j.JobID); err == nil {
		t.Fatal("queue deletion erased consumed authority")
	}
}

func TestManagedGradeUncertaintyHeldAcrossReopenAndRunnerChange(t *testing.T) {
	f := newPinnedFixture(t)
	if f.request(false) != http.StatusOK {
		t.Fatal("enqueue")
	}
	r := &contextPinnedRunner{}
	r.effect = func(context.Context) error { return errors.New("uncertain external response") }
	did, err := managedWorker(f.db, r, &pinnedCheckout{}).RunOnce(context.Background())
	if !did {
		t.Fatal("no claim")
	}
	requireReconciliation(t, err)
	job := r.jobs[0]
	f.noGrade()
	d, _ := f.db.GetGradingDispatch(context.Background(), job.JobID)
	if d == nil || d.State != store.DispatchHeld {
		t.Fatal("not held")
	}
	f.reopen()
	c := &pinnedCheckout{}
	ordinary := &ordinaryPinnedRunner{}
	requireReconciliation(t, managedService(f.db, ordinary, c).GradeJob(context.Background(), job))
	if len(c.calls) != 0 || ordinary.calls != 0 {
		t.Fatal("held job escaped through ordinary mode")
	}
	if did, err = managedWorker(f.db, ordinary, c).RunOnce(context.Background()); did || err != nil {
		t.Fatal("held job requeued")
	}
}

type dispatchFaultStore struct {
	store.Store
	claimAck         bool
	finishAck        bool
	beforeFinish     bool
	exitBeforeFinish bool
	exitAfterFinish  bool
}

func (s *dispatchFaultStore) ClaimGradingDispatch(c context.Context, d store.GradingDispatch, r *store.GradingRun) (bool, error) {
	claimed, err := s.Store.ClaimGradingDispatch(c, d, r)
	if err == nil && s.claimAck {
		return false, errors.New("lost claim acknowledgment")
	}
	return claimed, err
}
func (s *dispatchFaultStore) FinishGradingDispatch(c context.Context, d store.GradingDispatch, r *store.GradingRun, g *store.Grade) error {
	if s.exitBeforeFinish {
		os.Exit(72)
	}
	if s.beforeFinish {
		return errors.New("injected before final transaction")
	}
	err := s.Store.FinishGradingDispatch(c, d, r, g)
	if err == nil && s.exitAfterFinish {
		os.Exit(72)
	}
	if err == nil && s.finishAck {
		return errors.New("lost final acknowledgment")
	}
	return err
}
func TestManagedGradeLostAcknowledgmentsNeverRedispatch(t *testing.T) {
	for _, phase := range []string{"claim-ack", "before-final-transaction", "final-ack"} {
		t.Run(phase, func(t *testing.T) {
			f := newPinnedFixture(t)
			job := claimManagedFixture(t, f)
			r := &contextPinnedRunner{}
			c := &pinnedCheckout{}
			st := &dispatchFaultStore{Store: f.db, claimAck: phase == "claim-ack", beforeFinish: phase == "before-final-transaction", finishAck: phase == "final-ack"}
			err := invokeManaged(context.Background(), managedService(st, r, c), job)
			if phase == "final-ack" {
				if err != nil {
					t.Fatal(err)
				}
			} else {
				requireReconciliation(t, err)
				f.noGrade()
			}
			want := 1
			if phase == "claim-ack" {
				want = 0
			}
			if r.calls != want {
				t.Fatalf("runner calls %d", r.calls)
			}
			if phase == "claim-ack" && len(c.calls) != 0 {
				t.Fatal("ambiguous claim reached checkout")
			}
			f.reopen()
			next := &contextPinnedRunner{}
			checkout := &pinnedCheckout{}
			err = managedService(f.db, next, checkout).GradeJob(context.Background(), job)
			if phase == "final-ack" {
				if err != nil {
					t.Fatal(err)
				}
			} else {
				requireReconciliation(t, err)
			}
			if next.calls != 0 || len(checkout.calls) != 0 {
				t.Fatal("reopen replay")
			}
		})
	}
}

func TestManagedGradeTransactionFailuresRollbackTogether(t *testing.T) {
	for _, effect := range []string{"claim", "initial-run", "completed-run", "grade", "terminal-dispatch", "queue"} {
		t.Run(effect, func(t *testing.T) {
			f := newPinnedFixture(t)
			job := claimManagedFixture(t, f)
			db := rawManagedDB(t, f)
			triggers := map[string]string{
				"claim": `BEFORE INSERT ON grading_dispatches`, "initial-run": `BEFORE INSERT ON grading_runs`,
				"completed-run": `BEFORE UPDATE ON grading_runs WHEN NEW.status='completed'`, "grade": `BEFORE INSERT ON grades`,
				"terminal-dispatch": `BEFORE UPDATE ON grading_dispatches WHEN NEW.state='published'`, "queue": `BEFORE UPDATE ON provisioning_jobs WHEN NEW.status='succeeded'`,
			}
			mustManagedSQL(t, db, `CREATE TRIGGER refuse `+triggers[effect]+` BEGIN SELECT RAISE(ABORT,'injected transaction failure'); END`)
			r := &contextPinnedRunner{}
			c := &pinnedCheckout{}
			requireReconciliation(t, invokeManaged(context.Background(), managedService(f.db, r, c), job))
			f.noGrade()
			d, err := f.db.GetGradingDispatch(context.Background(), job.JobID)
			runs, e := f.db.ListGradingRunsBySubmission(context.Background(), "s")
			if e != nil {
				t.Fatal(e)
			}
			before := effect == "claim" || effect == "initial-run"
			if before {
				if !errors.Is(err, store.ErrNotFound) || len(runs) != 0 || r.calls != 0 || len(c.calls) != 0 {
					t.Fatal("partial claim effects")
				}
			} else {
				if err != nil || d.State != store.DispatchClaimed || len(runs) != 1 || runs[0].Status != "running" || r.calls != 1 {
					t.Fatal("partial publication effects")
				}
			}
			q, err := f.db.GetGradingJob(context.Background(), job.JobID)
			if err != nil || q.Status != store.JobInProgress {
				t.Fatal("queue escaped rollback")
			}
			mustManagedSQL(t, db, `DROP TRIGGER refuse`)
			if !before {
				f.reopen()
				requireReconciliation(t, managedService(f.db, &contextPinnedRunner{}, &pinnedCheckout{}).GradeJob(context.Background(), job))
			}
		})
	}
}

func TestManagedGradeConcurrentConnectionsPermitOneRunner(t *testing.T) {
	f := newPinnedFixture(t)
	job := claimManagedFixture(t, f)
	other, err := sqlite.Open(f.path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	done := make(chan error, 2)
	for _, db := range []store.Store{f.db, other} {
		r := &contextPinnedRunner{}
		r.effect = func(context.Context) error { started <- struct{}{}; <-release; return nil }
		go func(db store.Store, r *contextPinnedRunner) {
			done <- invokeManaged(context.Background(), managedService(db, r, &pinnedCheckout{}), job)
		}(db, r)
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("no acknowledged claimant")
	}
	select {
	case err = <-done:
		requireReconciliation(t, err)
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("loser did not refuse")
	}
	close(release)
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
		t.Fatal("two runner invocations")
	default:
	}
}

type crashCheckout struct{ pinnedCheckout }

func (c *crashCheckout) FetchRevision(context.Context, adapter.RepoRef, string, string) error {
	os.Exit(72)
	return nil
}
func TestManagedGradeCrashHelper(t *testing.T) {
	path := os.Getenv("CAIRN_DISPATCH_TEST_DB")
	if path == "" {
		t.Skip("subprocess helper only")
	}
	db, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	phase := os.Getenv("CAIRN_DISPATCH_TEST_PHASE")
	var checkout grading.Checkout = &pinnedCheckout{}
	if phase == "before-checkout" {
		checkout = &crashCheckout{}
	}
	st := &dispatchFaultStore{Store: db, exitBeforeFinish: phase == "after-runner", exitAfterFinish: phase == "after-publication"}
	_, err = managedWorker(st, &contextPinnedRunner{}, checkout).RunOnce(context.Background())
	t.Fatalf("expected simulated process death, got %v", err)
}
func TestManagedGradeCrashReopenNeverReexecutes(t *testing.T) {
	for _, phase := range []string{"before-checkout", "after-runner", "after-publication"} {
		t.Run(phase, func(t *testing.T) {
			f := newPinnedFixture(t)
			if f.request(false) != http.StatusOK {
				t.Fatal("enqueue")
			}
			cmd := exec.Command(os.Args[0], "-test.run=^TestManagedGradeCrashHelper$")
			cmd.Env = append(os.Environ(), "CAIRN_DISPATCH_TEST_DB="+f.path, "CAIRN_DISPATCH_TEST_PHASE="+phase)
			out, err := cmd.CombinedOutput()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 72 {
				t.Fatalf("child %v %s", err, out)
			}
			f.reopen()
			runs, err := f.db.ListGradingRunsBySubmission(context.Background(), "s")
			if err != nil || len(runs) != 1 {
				t.Fatal("missing durable audit", err)
			}
			var audit struct {
				Job gradingjob.Context `json:"job"`
			}
			if json.Unmarshal(runs[0].Result, &audit) != nil {
				t.Fatal("audit")
			}
			r := &contextPinnedRunner{}
			c := &pinnedCheckout{}
			err = managedService(f.db, r, c).GradeJob(context.Background(), audit.Job)
			if phase == "after-publication" {
				if err != nil {
					t.Fatal(err)
				}
			} else {
				requireReconciliation(t, err)
				f.noGrade()
			}
			if r.calls != 0 || len(c.calls) != 0 {
				t.Fatal("crashed job replayed")
			}
		})
	}
}

func TestManagedGradeRejectsUnrelatedQueueBinding(t *testing.T) {
	for _, change := range []string{"target", "type", "pending", "missing"} {
		t.Run(change, func(t *testing.T) {
			f := newPinnedFixture(t)
			job := claimManagedFixture(t, f)
			switch change {
			case "target":
				job.Target.Policy.Path = "other.json"
			case "missing":
				job.JobID = "missing"
			case "type":
				mustManagedSQL(t, rawManagedDB(t, f), `UPDATE provisioning_jobs SET type='lock_repo' WHERE id=?`, job.JobID)
			case "pending":
				mustManagedSQL(t, rawManagedDB(t, f), `UPDATE provisioning_jobs SET status='pending' WHERE id=?`, job.JobID)
			}
			r := &contextPinnedRunner{}
			c := &pinnedCheckout{}
			requireReconciliation(t, invokeManaged(context.Background(), managedService(f.db, r, c), job))
			if r.calls != 0 || len(c.calls) != 0 {
				t.Fatal("unrelated binding reached execution")
			}
			if _, err := f.db.GetGradingDispatch(context.Background(), job.JobID); !errors.Is(err, store.ErrNotFound) {
				t.Fatal("unrelated claim created", err)
			}
		})
	}
}

func TestManagedGradeCancelledAndUnserializableNoGrade(t *testing.T) {
	for _, mode := range []string{"cancel", "nan"} {
		t.Run(mode, func(t *testing.T) {
			f := newPinnedFixture(t)
			job := claimManagedFixture(t, f)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			r := &invalidManagedRunner{mode: mode, cancel: cancel}
			requireReconciliation(t, invokeManaged(ctx, managedService(f.db, r, &pinnedCheckout{}), job))
			f.noGrade()
			d, err := f.db.GetGradingDispatch(context.Background(), job.JobID)
			if err != nil || d.State != store.DispatchHeld {
				t.Fatalf("%+v %v", d, err)
			}
		})
	}
}

// Defined separately to make the invalid result come from the real runner boundary.
type invalidManagedRunner struct {
	contextPinnedRunner
	mode   string
	cancel context.CancelFunc
}

func (r *invalidManagedRunner) RunJobWithPolicy(c context.Context, j gradingjob.Context, p grading.PolicyEvidence, s gradingspec.Spec, i, d string) (grading.Result, error) {
	result, err := r.contextPinnedRunner.RunJobWithPolicy(c, j, p, s, i, d)
	if r.mode == "cancel" {
		r.cancel()
	} else {
		result.Score = math.NaN()
	}
	return result, err
}

func TestManagedGradeAbsentClaimErrorIsHeldByQueueNotInventedTombstone(t *testing.T) {
	f := newPinnedFixture(t)
	if f.request(false) != http.StatusOK {
		t.Fatal("enqueue")
	}
	db := rawManagedDB(t, f)
	mustManagedSQL(t, db, `CREATE TRIGGER refuse_claim BEFORE INSERT ON grading_dispatches BEGIN SELECT RAISE(ABORT,'claim unavailable'); END`)
	r := &contextPinnedRunner{}
	c := &pinnedCheckout{}
	did, err := managedWorker(f.db, r, c).RunOnce(context.Background())
	if !did {
		t.Fatal("queue not claimed")
	}
	requireReconciliation(t, err)
	var jobID, raw string
	if err = db.QueryRow(`SELECT id,target_ref FROM provisioning_jobs`).Scan(&jobID, &raw); err != nil {
		t.Fatal(err)
	}
	target, err := gradingjob.Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	job := gradingjob.Context{JobID: jobID, Target: target}
	mustManagedSQL(t, db, `DROP TRIGGER refuse_claim`)
	f.reopen()
	for range 2 {
		requireReconciliation(t, managedService(f.db, r, c).GradeJob(context.Background(), job))
	}
	if did, err = managedWorker(f.db, r, c).RunOnce(context.Background()); did || err != nil {
		t.Fatal("absent-claim job retried")
	}
	q, err := f.db.GetGradingJob(context.Background(), jobID)
	if err != nil || q.Status != store.JobInProgress {
		t.Fatal("queue hold lost")
	}
	if _, err = f.db.GetGradingDispatch(context.Background(), jobID); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("failed write claimed a tombstone")
	}
	if r.calls != 0 || len(c.calls) != 0 {
		t.Fatal("ambiguous absent claim executed")
	}
}
