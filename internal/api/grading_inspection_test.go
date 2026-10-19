// SPDX-License-Identifier: AGPL-3.0-or-later
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/EduCloud-Ecosystem/cairn/internal/provisioning"
	"github.com/EduCloud-Ecosystem/cairn/internal/store"
	"github.com/EduCloud-Ecosystem/cairn/internal/store/memory"
	"github.com/EduCloud-Ecosystem/cairn/internal/store/sqlite"
	"github.com/EduCloud-Ecosystem/cairn/pkg/adapter"
)

type inspectionResponse struct {
	Version          int                     `json:"version"`
	Items            []gradingInspectionItem `json:"items"`
	NextAfter        string                  `json:"next_after"`
	ReplayAuthorized bool                    `json:"replay_authorized"`
	Limits           []string                `json:"limits"`
}

func inspectionServer(st store.Store) *Server {
	s := New(Options{Store: st, Queue: provisioning.NewService(st), AuthEnabled: true, AdminUsers: []string{"alice"}, GraderConfigured: true})
	s.sessions["operator"] = session{userID: "op", username: "alice", created: time.Now(), isOperator: true}
	return s
}
func inspectRequest(s *Server, path string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("GET", path, nil)
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: "operator"})
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}
func inspectionDoc(t *testing.T, w *httptest.ResponseRecorder) inspectionResponse {
	t.Helper()
	if w.Code != 200 {
		t.Fatalf("inspection HTTP%d %s", w.Code, w.Body.String())
	}
	var doc inspectionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Version != 1 || doc.ReplayAuthorized || len(doc.Limits) != 5 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("missing inspection limits")
	}
	return doc
}
func newInspectionFixture(t *testing.T, backend string) (store.Store, *Server) {
	t.Helper()
	t.Setenv("TMPDIR", t.TempDir())
	var st store.Store
	if backend == "memory" {
		st = memory.New()
	} else {
		db, err := sqlite.Open(filepath.Join(t.TempDir(), "inspection.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Close() })
		st = db
	}
	ctx := context.Background()
	for _, err := range []error{
		st.CreateClassroom(ctx, &store.Classroom{ID: "c", Name: "Synthetic", Host: adapter.HostGitHub, HostNamespace: "fixture"}),
		st.CreateAssignment(ctx, &store.Assignment{ID: "a", ClassroomID: "c", Title: "Synthetic", Slug: "synthetic", Type: store.AssignmentIndividual, GradingSpec: "grading.json", TemplateRef: adapter.TemplateRef{Host: adapter.HostGitHub, Namespace: "teacher", Name: "policy", Ref: pinnedPolicy}}),
		st.CreateRosterEntry(ctx, &store.RosterEntry{ID: "r", ClassroomID: "c", Host: adapter.HostGitHub, HostUsername: "fixture", HostUserID: "fixture-1", Status: store.RosterActive}),
		st.CreateSubmission(ctx, &store.Submission{ID: "s", AssignmentID: "a", RosterEntryID: "r", Status: "active", LatestCommit: pinnedSubmission, Repo: adapter.RepoRef{Host: adapter.HostGitHub, Namespace: "fixture", Name: "submission"}}),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
	return st, inspectionServer(st)
}
func enqueueInspection(t *testing.T, s *Server) {
	t.Helper()
	r := httptest.NewRequest("POST", "/assignments/a/grade", nil)
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: "operator"})
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal("enqueue", w.Code, w.Body.String())
	}
}

type noClaimInspection struct{ store.Store }

func (s noClaimInspection) ClaimGradingDispatch(context.Context, store.GradingDispatch, *store.GradingRun) (bool, error) {
	return false, errors.New("private write diagnostic")
}
func inspectionStoreSnapshot(t *testing.T, st store.Store) []byte {
	t.Helper()
	ctx := context.Background()
	rows, err := st.ReadGradingInspection(ctx, store.GradingInspectionQuery{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	// Records deliberately do not JSON-serialize; snapshot explicit internal values.
	values := []any{}
	for _, r := range rows {
		j, e := st.GetGradingJob(ctx, r.Job.ID)
		if e != nil {
			t.Fatal(e)
		}
		values = append(values, j, r.Dispatch, r.HasDispatch, r.RunPresent, r.GradePresent)
	}
	runs, err := st.ListGradingRunsBySubmission(ctx, "s")
	if err != nil {
		t.Fatal(err)
	}
	grades, err := st.ListGradesBySubmission(ctx, "s")
	if err != nil {
		t.Fatal(err)
	}
	values = append(values, runs, grades)
	b, err := json.Marshal(values)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func TestGradingInspectionWorkerStatesAndImmutability(t *testing.T) {
	for _, backend := range []string{"memory", "sqlite"} {
		for _, phase := range []string{"published", "held", "claimed", "no-tombstone", "removed"} {
			t.Run(backend+"/"+phase, func(t *testing.T) {
				st, s := newInspectionFixture(t, backend)
				enqueueInspection(t, s)
				runner := &contextPinnedRunner{}
				checkout := &pinnedCheckout{}
				workerStore := st
				switch phase {
				case "held":
					runner.effect = func(context.Context) error { return errors.New("private provider error") }
				case "claimed":
					workerStore = &dispatchFaultStore{Store: st, claimAck: true}
				case "no-tombstone":
					workerStore = noClaimInspection{st}
				}
				did, err := managedWorker(workerStore, runner, checkout).RunOnce(context.Background())
				if !did {
					t.Fatal("no worker claim")
				}
				if phase == "published" || phase == "removed" {
					if err != nil {
						t.Fatal(err)
					}
				} else {
					requireReconciliation(t, err)
				}
				if phase == "removed" {
					if _, err = st.ConfirmExport(context.Background(), "c", time.Now().Add(-time.Hour)); err != nil {
						t.Fatal(err)
					}
					ng, nr, e := st.PurgeExportedGrades(context.Background(), time.Now())
					if e != nil || ng != 1 || nr != 1 {
						t.Fatal(ng, nr, e)
					}
					if e = st.DeleteRosterEntry(context.Background(), "r"); e != nil {
						t.Fatal(e)
					}
				}
				before := inspectionStoreSnapshot(t, st)
				calls, fetches := runner.calls, len(checkout.calls)
				first := inspectionDoc(t, inspectRequest(s, "/operator/grading-jobs"))
				if len(first.Items) != 1 {
					t.Fatal(first)
				}
				item := first.Items[0]
				want := map[string]string{"published": "published_records_present", "held": "held_reconciliation_required", "claimed": "claimed_liveness_unknown", "no-tombstone": "in_progress_without_dispatch", "removed": "published_records_absent"}[phase]
				if item.SubmissionID != "s" {
					t.Fatal("missing minimal submission binding", item)
				}
				if item.Observation != want {
					t.Fatalf("got %+v want %s", item, want)
				}
				for n := 0; n < 3; n++ {
					got := inspectionDoc(t, inspectRequest(s, "/operator/grading-jobs?job_id="+item.JobID))
					if len(got.Items) != 1 || got.Items[0] != item {
						t.Fatal("unstable exact read", got)
					}
				}
				if string(before) != string(inspectionStoreSnapshot(t, st)) || calls != runner.calls || fetches != len(checkout.calls) {
					t.Fatal("inspection mutated state or invoked execution")
				}
				encoded, _ := json.Marshal(first)
				for _, secret := range []string{"private provider", "private write", "fixture", "teacher", "grading.json", "answer.txt", "score", "breakdown", "target_ref", "runner-cannot"} {
					if strings.Contains(string(encoded), secret) {
						t.Fatalf("privacy leak %q", secret)
					}
				}
			})
		}
	}
}

type inspectionSpy struct {
	store.Store
	reads int
	err   error
}

func (s *inspectionSpy) ReadGradingInspection(c context.Context, q store.GradingInspectionQuery) ([]store.GradingInspectionRecord, error) {
	s.reads++
	if s.err != nil {
		return nil, s.err
	}
	return s.Store.ReadGradingInspection(c, q)
}
func TestGradingInspectionAuthorizationBeforeStore(t *testing.T) {
	for _, kind := range []string{"anonymous", "student", "expired", "revoked", "disabled", "empty-operator-id", "allowed"} {
		t.Run(kind, func(t *testing.T) {
			spy := &inspectionSpy{Store: memory.New()}
			s := inspectionServer(spy)
			sess := s.sessions["operator"]
			switch kind {
			case "anonymous":
				delete(s.sessions, "operator")
			case "student":
				sess.isOperator = false
				s.sessions["operator"] = sess
			case "expired":
				sess.created = time.Now().Add(-sessionTTL - time.Second)
				s.sessions["operator"] = sess
			case "revoked":
				delete(s.admins, "alice")
			case "disabled":
				s.authEnabled = false
			case "empty-operator-id":
				sess.userID = ""
				s.sessions["operator"] = sess
			}
			w := inspectRequest(s, "/operator/grading-jobs")
			if kind == "allowed" {
				if w.Code != 200 || spy.reads != 1 {
					t.Fatal(w.Code, spy.reads)
				}
			} else if w.Code != 401 || spy.reads != 0 {
				t.Fatal("unauthorized read", w.Code, spy.reads)
			}
		})
	}
}
func TestGradingInspectionQueryRefusalAndErrors(t *testing.T) {
	spy := &inspectionSpy{Store: memory.New()}
	s := inspectionServer(spy)
	for _, q := range []string{"?x=1", "?limit=0", "?limit=101", "?limit=-1", "?limit=+1", "?limit=01", "?limit=1.0", "?limit=1&limit=2", "?job_id=j&job_id=j", "?after=x&after=x", "?job_id=j&limit=1", "?job_id=j&after=x", "?after=", "?job_id=", "?job_id=a%2Fb", "?limit=2;after=x", "?job_id=%zz", "?after=" + strings.Repeat("x", 257), "?" + strings.Repeat("x", 1025)} {
		w := inspectRequest(s, "/operator/grading-jobs"+q)
		if w.Code != 400 || spy.reads != 0 {
			t.Fatal(q, w.Code, spy.reads)
		}
	}
	if w := inspectRequest(s, "/operator/grading-jobs?job_id=missing"); w.Code != 404 {
		t.Fatal(w.Code)
	}
	spy.err = errors.New("postgres://private-password sourcepath grading-body")
	w := inspectRequest(s, "/operator/grading-jobs")
	if w.Code != 503 || strings.Contains(w.Body.String(), "private") {
		t.Fatal("leaked store failure", w.Body.String())
	}
	for _, method := range []string{"POST", "PATCH", "DELETE"} {
		r := httptest.NewRequest(method, "/operator/grading-jobs", nil)
		r.AddCookie(&http.Cookie{Name: sessionCookie, Value: "operator"})
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if w.Code != 405 {
			t.Fatal(method, w.Code)
		}
	}
}
func TestGradingInspectionPaginationAndLegacy(t *testing.T) {
	for _, backend := range []string{"memory", "sqlite"} {
		t.Run(backend, func(t *testing.T) {
			st, s := newInspectionFixture(t, backend)
			for n, id := range []string{"a", "Z", "_", "a-1", "a_1"} {
				_, err := st.CreateJob(context.Background(), &store.ProvisioningJob{ID: id, Type: "grade", TargetRef: []string{"legacy", strings.Repeat("x", 8193), `{"version":1,"version":1}`}[n%3], Status: store.JobInProgress, IdempotencyKey: id, ScheduledAt: time.Now()})
				if err != nil {
					t.Fatal(err)
				}
			}
			_, err := st.CreateJob(context.Background(), &store.ProvisioningJob{ID: "hidden", Type: "lock_repo", TargetRef: "s", Status: store.JobPending, IdempotencyKey: "hidden", ScheduledAt: time.Now()})
			if err != nil {
				t.Fatal(err)
			}
			ids := []string{}
			after := ""
			for page := 0; page < 4; page++ {
				doc := inspectionDoc(t, inspectRequest(s, "/operator/grading-jobs?limit=2"+after))
				for _, i := range doc.Items {
					if i.BindingStatus != "invalid" || i.Observation != "invalid_binding" {
						t.Fatal(i)
					}
					ids = append(ids, i.JobID)
				}
				if doc.NextAfter == "" {
					break
				}
				after = "&after=" + doc.NextAfter
			}
			if !reflect.DeepEqual(ids, []string{"Z", "_", "a", "a-1", "a_1"}) {
				t.Fatal("ordering/duplicates", ids)
			}
			if w := inspectRequest(s, "/operator/grading-jobs?job_id=hidden"); w.Code != 404 {
				t.Fatal("non grading disclosed", w.Code)
			}
		})
	}
}
func TestGradingInspectionConcurrentAtomicPublication(t *testing.T) {
	for _, backend := range []string{"memory", "sqlite"} {
		t.Run(backend, func(t *testing.T) {
			st, s := newInspectionFixture(t, backend)
			enqueueInspection(t, s)
			runner := &contextPinnedRunner{}
			entered := make(chan struct{})
			release := make(chan struct{})
			runner.effect = func(context.Context) error { close(entered); <-release; return nil }
			done := make(chan error, 1)
			go func() {
				_, err := managedWorker(st, runner, &pinnedCheckout{}).RunOnce(context.Background())
				done <- err
			}()
			<-entered
			before := inspectionDoc(t, inspectRequest(s, "/operator/grading-jobs"))
			if len(before.Items) != 1 || before.Items[0].Observation != "claimed_liveness_unknown" {
				t.Fatal(before)
			}
			close(release)
			for n := 0; n < 50; n++ {
				doc := inspectionDoc(t, inspectRequest(s, "/operator/grading-jobs"))
				if len(doc.Items) != 1 {
					t.Fatal(doc)
				}
				i := doc.Items[0]
				if i.Observation != "claimed_liveness_unknown" && i.Observation != "published_records_present" {
					t.Fatal("torn publication", i)
				}
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			final := inspectionDoc(t, inspectRequest(s, "/operator/grading-jobs"))
			if final.Items[0].Observation != "published_records_present" {
				t.Fatal(final)
			}
		})
	}
}
func TestGradingInspectionSQLiteMutationTrapsAndMissingSchema(t *testing.T) {
	f := newPinnedFixture(t)
	enqueueInspection(t, inspectionServer(f.db))
	if _, err := managedWorker(f.db, &contextPinnedRunner{}, &pinnedCheckout{}).RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	db := rawManagedDB(t, f)
	for _, table := range []string{"provisioning_jobs", "grading_dispatches", "grading_runs", "grades", "submissions", "assignments", "roster_entries"} {
		for _, op := range []string{"INSERT", "UPDATE", "DELETE"} {
			mustManagedSQL(t, db, fmt.Sprintf(`CREATE TRIGGER trap_%s_%s BEFORE %s ON %s BEGIN SELECT RAISE(ABORT,'inspection attempted mutation'); END`, table, op, op, table))
		}
	}
	before := inspectionStoreSnapshot(t, f.db)
	s := inspectionServer(f.db)
	for n := 0; n < 3; n++ {
		doc := inspectionDoc(t, inspectRequest(s, "/operator/grading-jobs"))
		if doc.Items[0].Observation != "published_records_present" {
			t.Fatal(doc)
		}
	}
	if string(before) != string(inspectionStoreSnapshot(t, f.db)) {
		t.Fatal("mutation")
	}
	mustManagedSQL(t, db, `DROP TABLE grading_dispatches`)
	w := inspectRequest(s, "/operator/grading-jobs")
	if w.Code != 503 || strings.Contains(w.Body.String(), "grading_dispatches") {
		t.Fatal("schema failure disclosed", w.Code, w.Body.String())
	}
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name='grading_dispatches'`).Scan(&n); err != nil || n != 0 {
		t.Fatal("inspection recreated schema", n, err)
	}
}

func TestGradingInspectionCorruptBindingsAndBodyPrivacy(t *testing.T) {
	for _, change := range []string{
		`UPDATE grading_dispatches SET target_sha256='` + strings.Repeat("d", 64) + `'`,
		`UPDATE grading_dispatches SET context_sha256='` + strings.Repeat("d", 64) + `'`,
		`UPDATE grading_dispatches SET claim_sha256='private/secret'`,
		`UPDATE grading_dispatches SET result_sha256='private/secret'`,
		`UPDATE grading_dispatches SET submission_id='private/secret'`,
		`UPDATE grading_dispatches SET grade_id='private/secret'`,
		`UPDATE grading_dispatches SET run_id='private/secret'`,
		`UPDATE provisioning_jobs SET status='private/secret'`,
		`UPDATE provisioning_jobs SET target_ref='private/secret'`,
		`UPDATE provisioning_jobs SET type='private/secret'`,
		`UPDATE grading_runs SET runner='private/secret'`,
		`UPDATE grading_runs SET status='private/secret'`,
		`UPDATE grades SET run_id='wrong-run'`,
	} {
		t.Run(fmt.Sprintf("case-%d", strings.Index(change, " SET"))+"-"+store.GradingDigest([]byte(change))[:6], func(t *testing.T) {
			f := newPinnedFixture(t)
			if f.request(false) != 200 {
				t.Fatal("enqueue")
			}
			if _, err := managedWorker(f.db, &contextPinnedRunner{}, &pinnedCheckout{}).RunOnce(context.Background()); err != nil {
				t.Fatal(err)
			}
			db := rawManagedDB(t, f)
			mustManagedSQL(t, db, change)
			w := inspectRequest(inspectionServer(f.db), "/operator/grading-jobs")
			doc := inspectionDoc(t, w)
			if len(doc.Items) != 1 || doc.Items[0].Observation != "invalid_binding" || doc.Items[0].BindingStatus != "invalid" {
				t.Fatal("missed selected-metadata inconsistency", doc)
			}
			if strings.Contains(w.Body.String(), "private/secret") {
				t.Fatal("disclosed corrupt metadata")
			}
		})
	}
	f := newPinnedFixture(t)
	if f.request(false) != 200 {
		t.Fatal("enqueue")
	}
	if _, err := managedWorker(f.db, &contextPinnedRunner{}, &pinnedCheckout{}).RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	db := rawManagedDB(t, f)
	// Bodies are intentionally not read or freshly verified by this endpoint.
	mustManagedSQL(t, db, `UPDATE grading_runs SET result=?,logs_ref=?`, strings.Repeat("private-body", 1<<18), "private/path")
	mustManagedSQL(t, db, `UPDATE grades SET breakdown=?`, strings.Repeat("private-body", 1<<18))
	mustManagedSQL(t, db, `UPDATE provisioning_jobs SET last_error='private-error'`)
	w := inspectRequest(inspectionServer(f.db), "/operator/grading-jobs")
	doc := inspectionDoc(t, w)
	if doc.Items[0].Observation != "published_records_present" || strings.Contains(w.Body.String(), "private") {
		t.Fatal("body inspection/privacy changed")
	}
	// A grade can disappear before its run during existing retention. Report the
	// two observations without inventing corruption or a verified purge cause.
	mustManagedSQL(t, db, `DELETE FROM grades`)
	doc = inspectionDoc(t, inspectRequest(inspectionServer(f.db), "/operator/grading-jobs"))
	if doc.Items[0].Observation != "published_records_absent" || doc.Items[0].RunPresence != "present" || doc.Items[0].GradePresence != "absent" || doc.Items[0].BindingStatus != "consistent" {
		t.Fatal("partial removal misclassified", doc)
	}
}

func TestGradingInspectionRejectsNULSuffix(t *testing.T) {
	for _, column := range []string{"target_ref", "status"} {
		t.Run(column, func(t *testing.T) {
			f := newPinnedFixture(t)
			if f.request(false) != 200 {
				t.Fatal("enqueue")
			}
			if _, err := managedWorker(f.db, &contextPinnedRunner{}, &pinnedCheckout{}).RunOnce(context.Background()); err != nil {
				t.Fatal(err)
			}
			mustManagedSQL(t, rawManagedDB(t, f), `UPDATE provisioning_jobs SET `+column+`=`+column+`||char(0)||'private-suffix'`)
			w := inspectRequest(inspectionServer(f.db), "/operator/grading-jobs")
			doc := inspectionDoc(t, w)
			if len(doc.Items) != 1 || doc.Items[0].BindingStatus != "invalid" || doc.Items[0].Observation != "invalid_binding" {
				t.Fatal("malformed stored bytes normalized to valid metadata", doc)
			}
			if strings.Contains(w.Body.String(), "private-suffix") {
				t.Fatal("private suffix disclosed")
			}
		})
	}
}
