// SPDX-License-Identifier: AGPL-3.0-or-later
package api

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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

var pinnedSubmission = strings.Repeat("a", 40)
var pinnedPolicy = strings.Repeat("b", 40)

const pinnedSpec = `{"version":"1","tests":[{"name":"fixture","run":"never executed","points":10}]}`

type checkoutCall struct {
	Repo     adapter.RepoRef
	Revision string
}
type pinnedCheckout struct{ calls []checkoutCall }

func (*pinnedCheckout) Fetch(context.Context, adapter.RepoRef, string) error {
	return errors.New("moving-head checkout must never be used by queued grading")
}
func (c *pinnedCheckout) FetchRevision(_ context.Context, repo adapter.RepoRef, revision, dir string) error {
	c.calls = append(c.calls, checkoutCall{repo, revision})
	switch {
	case repo.Name == "policy" && revision == pinnedPolicy:
		return os.WriteFile(filepath.Join(dir, "grading.json"), []byte(pinnedSpec), 0600)
	case repo.Name == "submission" && revision == pinnedSubmission:
		return os.WriteFile(filepath.Join(dir, "answer.txt"), []byte("synthetic"), 0600)
	default:
		return errors.New("checkout did not use the original queue pins")
	}
}

// ordinaryPinnedRunner deliberately implements only the existing PolicyRunner.
type ordinaryPinnedRunner struct {
	calls  int
	effect func(context.Context) error
}

func (*ordinaryPinnedRunner) Name() string { return "fake-policy-boundary" }
func (*ordinaryPinnedRunner) Run(context.Context, gradingspec.Spec, string) (grading.Result, error) {
	return grading.Result{}, errors.New("separate policy dispatch required")
}
func (r *ordinaryPinnedRunner) RunWithPolicy(ctx context.Context, s gradingspec.Spec, input, policy string) (grading.Result, error) {
	r.calls++
	if b, e := os.ReadFile(filepath.Join(policy, "grading.json")); e != nil || string(b) != pinnedSpec {
		return grading.Result{}, errors.New("wrong policy bytes")
	}
	if b, e := os.ReadFile(filepath.Join(input, "answer.txt")); e != nil || string(b) != "synthetic" {
		return grading.Result{}, errors.New("wrong input bytes")
	}
	res := grading.Result{Score: 10, MaxScore: s.MaxScore(), Job: &gradingjob.Context{JobID: "runner-cannot-author-context"}, Policy: &grading.PolicyEvidence{Revision: "runner-cannot-author-policy"}}
	if r.effect != nil {
		return res, r.effect(ctx)
	}
	return res, nil
}

type contextPinnedRunner struct {
	ordinaryPinnedRunner
	jobs     []gradingjob.Context
	policies []grading.PolicyEvidence
}

func (r *contextPinnedRunner) RunJobWithPolicy(ctx context.Context, j gradingjob.Context, p grading.PolicyEvidence, s gradingspec.Spec, input, policy string) (grading.Result, error) {
	r.jobs = append(r.jobs, j)
	r.policies = append(r.policies, p)
	return r.RunWithPolicy(ctx, s, input, policy)
}

type pinnedFixture struct {
	t      *testing.T
	db     *sqlite.Store
	path   string
	server *Server
}

func newPinnedFixture(t *testing.T) *pinnedFixture {
	t.Helper()
	// Runtimeguard's lock and snapshots stay inside this test's disposable scope.
	t.Setenv("TMPDIR", t.TempDir())
	f := &pinnedFixture{t: t, path: filepath.Join(t.TempDir(), "jobs.db")}
	f.open()
	t.Cleanup(func() { _ = f.db.Close() })
	ctx := context.Background()
	for _, err := range []error{
		f.db.CreateClassroom(ctx, &store.Classroom{ID: "c", Name: "Synthetic", Host: adapter.HostGitHub, HostNamespace: "fixture"}),
		f.db.CreateAssignment(ctx, &store.Assignment{ID: "a", ClassroomID: "c", Title: "Synthetic", Slug: "synthetic", Type: store.AssignmentIndividual, GradingSpec: "grading.json", TemplateRef: adapter.TemplateRef{Host: adapter.HostGitHub, Namespace: "teacher", Name: "policy", Ref: pinnedPolicy}}),
		f.db.CreateRosterEntry(ctx, &store.RosterEntry{ID: "r", ClassroomID: "c", Host: adapter.HostGitHub, HostUsername: "fixture", HostUserID: "fixture-1", Status: store.RosterActive}),
		f.db.CreateSubmission(ctx, &store.Submission{ID: "s", AssignmentID: "a", RosterEntryID: "r", Status: "active", LatestCommit: pinnedSubmission, Repo: adapter.RepoRef{Host: adapter.HostGitHub, Namespace: "fixture", Name: "submission"}}),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
	return f
}
func (f *pinnedFixture) open() {
	f.t.Helper()
	var err error
	f.db, err = sqlite.Open(f.path)
	if err != nil {
		f.t.Fatal(err)
	}
	f.server = New(Options{Store: f.db, Queue: provisioning.NewService(f.db), GraderConfigured: true, WebhookSecrets: map[adapter.Host]string{adapter.HostGitHub: testWebhookSecret}})
}
func (f *pinnedFixture) reopen() {
	f.t.Helper()
	if err := f.db.Close(); err != nil {
		f.t.Fatal(err)
	}
	f.open()
}
func (f *pinnedFixture) request(webhook bool) int {
	f.t.Helper()
	var req *http.Request
	if webhook {
		body := githubPush("fixture", "submission", pinnedSubmission)
		req = httptest.NewRequest(http.MethodPost, "/webhooks/github", strings.NewReader(body))
		req.Header.Set("X-Hub-Signature-256", githubSig([]byte(body)))
		req.Header.Set("X-GitHub-Event", "push")
	} else {
		req = httptest.NewRequest(http.MethodPost, "/assignments/a/grade", nil)
	}
	rec := httptest.NewRecorder()
	f.server.ServeHTTP(rec, req)
	return rec.Code
}
func (f *pinnedFixture) worker(r grading.Runner, c *pinnedCheckout) *provisioning.Worker {
	s := grading.NewService(f.db, r, c)
	return &provisioning.Worker{Store: f.db, Grader: s, Backoff: func(int) time.Duration { return 0 }}
}
func (f *pinnedFixture) noGrade() {
	f.t.Helper()
	if _, err := f.db.LatestGradeForSubmission(context.Background(), "s"); !errors.Is(err, store.ErrNotFound) {
		f.t.Fatalf("unexpected grade: %v", err)
	}
}
func (f *pinnedFixture) changeCurrentPolicy() {
	f.t.Helper()
	ctx := context.Background()
	asg, err := f.db.GetAssignment(ctx, "a")
	if err != nil {
		f.t.Fatal(err)
	}
	asg.TemplateRef.Name = "different-policy"
	asg.TemplateRef.Ref = strings.Repeat("c", 40)
	asg.GradingSpec = "changed.json"
	if err = f.db.UpdateAssignment(ctx, asg); err != nil {
		f.t.Fatal(err)
	}
	sub, err := f.db.GetSubmission(ctx, "s")
	if err != nil {
		f.t.Fatal(err)
	}
	sub.LatestCommit = strings.Repeat("d", 40)
	if err = f.db.UpdateSubmission(ctx, sub); err != nil {
		f.t.Fatal(err)
	}
}

func TestPinnedGradeOrdinaryRunnerRetryAndReopen(t *testing.T) {
	for _, webhook := range []bool{false, true} {
		t.Run(fmt.Sprint("webhook=", webhook), func(t *testing.T) {
			f := newPinnedFixture(t)
			want := http.StatusOK
			if webhook {
				want = http.StatusAccepted
			}
			if got := f.request(webhook); got != want {
				t.Fatalf("enqueue=%d", got)
			}
			f.changeCurrentPolicy()
			if webhook && f.request(true) != http.StatusAccepted {
				t.Fatal("duplicate webhook refused after current policy changed")
			}
			first := &ordinaryPinnedRunner{}
			first.effect = func(context.Context) error { return errors.New("simulated lost infrastructure response") }
			checkout := &pinnedCheckout{}
			if did, err := f.worker(first, checkout).RunOnce(context.Background()); !did || err == nil {
				t.Fatalf("first=%v %v", did, err)
			}
			f.noGrade()
			before, err := f.db.ListGradingRunsBySubmission(context.Background(), "s")
			var firstAudit struct {
				Job    *gradingjob.Context     `json:"job"`
				Policy *grading.PolicyEvidence `json:"policy"`
			}
			if err != nil || len(before) != 1 || json.Unmarshal(before[0].Result, &firstAudit) != nil || firstAudit.Job == nil || firstAudit.Job.JobID == "" {
				t.Fatal("missing queue context")
			}
			f.reopen()
			second := &ordinaryPinnedRunner{}
			if did, err := f.worker(second, checkout).RunOnce(context.Background()); !did || err != nil {
				t.Fatalf("retry=%v %v", did, err)
			}
			j := *firstAudit.Job
			if j.Target.Revision != pinnedSubmission || j.Target.Policy.Revision != pinnedPolicy || j.Target.Policy.Path != "grading.json" || j.Target.Policy.Repository.Name != "policy" {
				t.Fatal(j)
			}
			hash := fmt.Sprintf("%x", sha256.Sum256([]byte(pinnedSpec)))
			if firstAudit.Policy == nil || firstAudit.Policy.SHA256 != hash {
				t.Fatal("policy evidence drift")
			}
			g, err := f.db.LatestGradeForSubmission(context.Background(), "s")
			if err != nil {
				t.Fatal(err)
			}
			var result grading.Result
			if err = json.Unmarshal(g.Breakdown, &result); err != nil {
				t.Fatal(err)
			}
			if result.Job == nil || *result.Job != j || result.Policy == nil || *result.Policy != *firstAudit.Policy || result.Score != 10 || result.SubmissionRevision != pinnedSubmission {
				t.Fatalf("unbound result %+v", result)
			}
			runs, err := f.db.ListGradingRunsBySubmission(context.Background(), "s")
			if err != nil || len(runs) != 2 {
				t.Fatalf("audit rows=%d %v", len(runs), err)
			}
			for _, run := range runs {
				var audit struct {
					Job    *gradingjob.Context     `json:"job"`
					Policy *grading.PolicyEvidence `json:"policy"`
				}
				if json.Unmarshal(run.Result, &audit) != nil || audit.Job == nil || *audit.Job != j || audit.Policy == nil || audit.Policy.SHA256 != hash {
					t.Fatalf("unbound audit %+v", run)
				}
			}
			if runs[0].ID == runs[1].ID || runs[0].ID == j.JobID || runs[1].ID == j.JobID {
				t.Fatal("delivery audit IDs became logical job IDs")
			}
			if len(checkout.calls) != 4 {
				t.Fatalf("checkouts=%d", len(checkout.calls))
			}
		})
	}
}

func TestPinnedGradeWebhookDedupAndManualRegrades(t *testing.T) {
	f := newPinnedFixture(t)
	if f.request(true) != http.StatusAccepted || f.request(true) != http.StatusAccepted {
		t.Fatal("webhook refused")
	}
	if f.request(false) != http.StatusOK || f.request(false) != http.StatusOK {
		t.Fatal("manual grade refused")
	}
	r := &contextPinnedRunner{}
	w := f.worker(r, &pinnedCheckout{})
	for range 3 {
		if did, err := w.RunOnce(context.Background()); !did || err != nil {
			t.Fatalf("delivery=%v %v", did, err)
		}
	}
	if did, err := w.RunOnce(context.Background()); did || err != nil {
		t.Fatalf("duplicate webhook queued: %v %v", did, err)
	}
	ids := map[string]bool{}
	for _, j := range r.jobs {
		ids[j.JobID] = true
	}
	if len(ids) != 3 {
		t.Fatalf("manual regrades reused identities: %+v", r.jobs)
	}
}

func TestPinnedGradeOrdinaryPolicyRunnerUnchanged(t *testing.T) {
	f := newPinnedFixture(t)
	if f.request(false) != http.StatusOK {
		t.Fatal("enqueue")
	}
	r := &ordinaryPinnedRunner{}
	if did, err := f.worker(r, &pinnedCheckout{}).RunOnce(context.Background()); !did || err != nil || r.calls != 1 {
		t.Fatalf("ordinary dispatch=%v %v calls=%d", did, err, r.calls)
	}
	g, err := f.db.LatestGradeForSubmission(context.Background(), "s")
	if err != nil {
		t.Fatal(err)
	}
	var result grading.Result
	if json.Unmarshal(g.Breakdown, &result) != nil || result.Job == nil || result.Job.JobID == "runner-cannot-author-context" || result.Policy == nil || result.Policy.Revision != pinnedPolicy {
		t.Fatal("runner supplied audit context")
	}
}

func TestPinnedGradeLockedSubmissionsRemainEligible(t *testing.T) {
	for _, lockBeforeEnqueue := range []bool{true, false} {
		t.Run(fmt.Sprint("lock-before-enqueue=", lockBeforeEnqueue), func(t *testing.T) {
			f := newPinnedFixture(t)
			lock := func() {
				// The real worker uses this state after the host lock succeeds.
				sub, err := f.db.GetSubmission(context.Background(), "s")
				if err != nil {
					t.Fatal(err)
				}
				sub.Status = "locked"
				if err = f.db.UpdateSubmission(context.Background(), sub); err != nil {
					t.Fatal(err)
				}
			}
			if lockBeforeEnqueue {
				lock()
			}
			if got := f.request(false); got != http.StatusOK {
				t.Fatalf("manual locked grading=%d", got)
			}
			if !lockBeforeEnqueue {
				lock()
			}
			r := &contextPinnedRunner{}
			if did, err := f.worker(r, &pinnedCheckout{}).RunOnce(context.Background()); !did || err != nil {
				t.Fatalf("locked queued grading=%v %v", did, err)
			}
			if len(r.jobs) != 1 || r.jobs[0].Target.Revision != pinnedSubmission || r.jobs[0].Target.Policy.Revision != pinnedPolicy {
				t.Fatal("locked grading changed pins")
			}
			if got := f.request(true); got != http.StatusNoContent {
				t.Fatalf("locked webhook unexpectedly admitted: %d", got)
			}
			if _, err := f.db.ClaimNextJob(context.Background()); !errors.Is(err, store.ErrNotFound) {
				t.Fatal("locked webhook queued work")
			}
		})
	}
}

func TestPinnedGradeMalformedAndLegacyTargetsNeverCheckout(t *testing.T) {
	f := newPinnedFixture(t)
	sub, _ := f.db.GetSubmission(context.Background(), "s")
	asg, _ := f.db.GetAssignment(context.Background(), "a")
	valid, err := gradingjob.Capture(sub, asg, pinnedSubmission)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := valid.Encode()
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"legacy-bare":                 "s",
		"legacy-revision-only":        `{"submission_id":"s","revision":"` + pinnedSubmission + `"}`,
		"version":                     strings.Replace(raw, `"version":1`, `"version":2`, 1),
		"moving-policy":               strings.Replace(raw, pinnedPolicy, "main", 1),
		"escaping-path":               strings.Replace(raw, "grading.json", "../grading.json", 1),
		"missing-assignment":          strings.Replace(raw, `"assignment_id":"a"`, `"assignment_id":""`, 1),
		"trailing":                    raw + " {}",
		"unknown-field":               raw[:len(raw)-1] + `,"unexpected":true}`,
		"duplicate-top":               strings.Replace(raw, `"version":1`, `"version":1,"version":1`, 1),
		"oversized":                   raw + strings.Repeat(" ", 8192),
		"boolean-version":             strings.Replace(raw, `"version":1`, `"version":true`, 1),
		"duplicate-unicode-alias":     strings.Replace(raw, `"version":1`, `"verſion":2,"version":1`, 1),
		"duplicate-case-alias":        strings.Replace(raw, `"version":1`, `"Version":2,"version":1`, 1),
		"duplicate-repository":        strings.Replace(raw, `"name":"submission"`, `"name":"other","name":"submission"`, 1),
		"duplicate-policy-repository": strings.Replace(raw, `"name":"policy"`, `"name":"other","name":"policy"`, 1),
	}
	r := &contextPinnedRunner{}
	c := &pinnedCheckout{}
	w := f.worker(r, c)
	for _, kind := range []provisioning.JobType{provisioning.JobGrade, provisioning.JobGradeRevision} {
		for name, bad := range cases {
			t.Run(string(kind)+"/"+name, func(t *testing.T) {
				if err := provisioning.NewService(f.db).Enqueue(context.Background(), kind, bad, string(kind)+name); err != nil {
					t.Fatal(err)
				}
				if did, err := w.RunOnce(context.Background()); !did || !errors.Is(err, gradingjob.ErrInvalid) {
					t.Fatalf("refusal=%v %v", did, err)
				}
				if did, err := w.RunOnce(context.Background()); did || err != nil {
					t.Fatalf("refusal was requeued: %v %v", did, err)
				}
			})
		}
	}
	if len(c.calls) != 0 || r.calls != 0 {
		t.Fatal("invalid context reached execution")
	}
	f.noGrade()
}

func TestPinnedGradeCancellationAndUncertaintyNeverPublish(t *testing.T) {
	for _, cancelParent := range []bool{false, true} {
		t.Run(fmt.Sprint("cancel=", cancelParent), func(t *testing.T) {
			f := newPinnedFixture(t)
			if f.request(false) != http.StatusOK {
				t.Fatal("enqueue")
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			r := &contextPinnedRunner{}
			r.effect = func(context.Context) error {
				if cancelParent {
					cancel()
					return nil
				}
				return errors.New("unknown provider outcome")
			}
			if did, err := f.worker(r, &pinnedCheckout{}).RunOnce(ctx); !did || err == nil {
				t.Fatalf("uncertainty accepted=%v %v", did, err)
			}
			f.noGrade()
			runs, err := f.db.ListGradingRunsBySubmission(context.Background(), "s")
			if err != nil || len(runs) != 1 || runs[0].Status != "failed" {
				t.Fatalf("failure not retained: %+v %v", runs, err)
			}
			var audit struct {
				Job   *gradingjob.Context `json:"job"`
				Error string              `json:"error"`
			}
			if json.Unmarshal(runs[0].Result, &audit) != nil || audit.Job == nil || audit.Error == "" {
				t.Fatal("missing failed job context")
			}
		})
	}
}

func TestPinnedGradeCurrentBindingChangeRefusesBeforeCheckout(t *testing.T) {
	for _, change := range []string{"repository", "failed", "provisioning", "retired", "revoked"} {
		t.Run(change, func(t *testing.T) {
			f := newPinnedFixture(t)
			if f.request(false) != http.StatusOK {
				t.Fatal("enqueue")
			}
			sub, _ := f.db.GetSubmission(context.Background(), "s")
			if change == "repository" {
				sub.Repo.Name = "replacement"
			} else {
				sub.Status = change
			}
			if err := f.db.UpdateSubmission(context.Background(), sub); err != nil {
				t.Fatal(err)
			}
			r := &contextPinnedRunner{}
			c := &pinnedCheckout{}
			if did, err := f.worker(r, c).RunOnce(context.Background()); !did || err == nil {
				t.Fatalf("changed binding=%v %v", did, err)
			}
			if len(c.calls) != 0 || r.calls != 0 {
				t.Fatal("changed binding checked out")
			}
			f.noGrade()
		})
	}
}

func TestPinnedGradeAPIRefusesMissingPins(t *testing.T) {
	for _, missing := range []string{"submission", "policy"} {
		t.Run(missing, func(t *testing.T) {
			f := newPinnedFixture(t)
			if missing == "submission" {
				sub, _ := f.db.GetSubmission(context.Background(), "s")
				sub.LatestCommit = ""
				if err := f.db.UpdateSubmission(context.Background(), sub); err != nil {
					t.Fatal(err)
				}
			} else {
				asg, _ := f.db.GetAssignment(context.Background(), "a")
				asg.TemplateRef.Ref = "main"
				if err := f.db.UpdateAssignment(context.Background(), asg); err != nil {
					t.Fatal(err)
				}
			}
			if got := f.request(false); got != http.StatusConflict {
				t.Fatalf("manual missing pin=%d", got)
			}
			if missing == "policy" && f.request(true) != http.StatusConflict {
				t.Fatal("webhook accepted unpinned policy")
			}
			if _, err := f.db.ClaimNextJob(context.Background()); !errors.Is(err, store.ErrNotFound) {
				t.Fatal("invalid API input queued")
			}
		})
	}
}
