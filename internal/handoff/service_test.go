// SPDX-License-Identifier: AGPL-3.0-or-later
package handoff

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/EduCloud-Ecosystem/cairn/internal/store"
	"github.com/EduCloud-Ecosystem/cairn/internal/store/memory"
	"github.com/EduCloud-Ecosystem/cairn/internal/store/sqlite"
	"github.com/EduCloud-Ecosystem/cairn/pkg/adapter"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeWriter struct {
	mu                        sync.Mutex
	inspect, prepare, publish int
	outcome                   Outcome
	err                       error
	beforePublish             func(WriteRequest, string)
	block                     chan struct{}
}

func (w *fakeWriter) Inspect(_ context.Context, _ adapter.RepoRef, files []File) (Snapshot, error) {
	w.mu.Lock()
	w.inspect++
	w.mu.Unlock()
	if w.block != nil {
		<-w.block
	}
	out := Snapshot{Branch: "refs/heads/main", BaseRevision: strings.Repeat("a", 40)}
	for _, f := range files {
		h := sha256.Sum256(f.Content)
		out.Changes = append(out.Changes, Change{Path: f.Path, SHA256: hex.EncodeToString(h[:]), Bytes: len(f.Content), Action: "replace"})
	}
	return out, nil
}
func (w *fakeWriter) Prepare(_ context.Context, r WriteRequest) (string, error) {
	w.mu.Lock()
	w.prepare++
	w.mu.Unlock()
	return strings.Repeat("b", 40), nil
}
func (w *fakeWriter) Publish(_ context.Context, r WriteRequest, sha string) (Outcome, error) {
	w.mu.Lock()
	w.publish++
	w.mu.Unlock()
	if w.beforePublish != nil {
		w.beforePublish(r, sha)
	}
	if w.outcome == "" {
		return Committed, w.err
	}
	return w.outcome, w.err
}
func fixture(t *testing.T, st store.Store) (*Service, Actor, *fakeWriter) {
	t.Helper()
	ctx := context.Background()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(st.CreateUser(ctx, &store.User{ID: "op", Host: adapter.HostForgejo, HostUsername: "op", HostUserID: "1"}))
	must(st.CreateClassroom(ctx, &store.Classroom{ID: "c", CreatedBy: "op", Host: adapter.HostForgejo, HostNamespace: "org"}))
	must(st.CreateAssignment(ctx, &store.Assignment{ID: "a", ClassroomID: "c", Slug: "exercise", GradingSpec: "grading.yaml", Type: store.AssignmentIndividual}))
	must(st.CreateRosterEntry(ctx, &store.RosterEntry{ID: "r", ClassroomID: "c", Host: adapter.HostForgejo, HostUsername: "student", HostUserID: "42", Status: store.RosterActive}))
	must(st.CreateSubmission(ctx, &store.Submission{ID: "s", AssignmentID: "a", RosterEntryID: "r", Status: "active", Repo: adapter.RepoRef{Host: adapter.HostForgejo, Namespace: "org", Name: "exercise"}}))
	w := &fakeWriter{}
	return &Service{Store: st, Writer: w, GraderConfigured: true}, Actor{Host: adapter.HostForgejo, Username: "student", ID: "42"}, w
}
func testFiles() []File { return []File{{Path: "answer.py", Content: []byte("print(42)\n")}} }
func TestReceiptIdempotencyFrozenIntentAndGradeDedup(t *testing.T) {
	ctx := context.Background()
	s, a, w := fixture(t, memory.New())
	first, err := s.Preview(ctx, a, "s", "request-001", testFiles())
	if err != nil {
		t.Fatal(err)
	}
	repeated, err := s.Preview(ctx, a, "s", "request-001", testFiles())
	if err != nil || repeated.ID != first.ID || w.inspect != 1 {
		t.Fatal(repeated, err, w.inspect)
	}
	if _, err = s.Preview(ctx, a, "s", "request-001", []File{{Path: "answer.py", Content: []byte("changed")}}); !errors.Is(err, store.ErrConflict) {
		t.Fatal("key reuse accepted", err)
	}
	confirmed, err := s.Confirm(ctx, a, "s", first.ID)
	if err != nil || confirmed.Status != "queued" {
		t.Fatal(confirmed, err)
	}
	_, err = s.Confirm(ctx, a, "s", first.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate the webhook arriving first; success must not queue a second grade.
	_, err = s.Store.CreateJob(ctx, &store.ProvisioningJob{ID: "webhook", Type: "grade_revision", Status: store.JobPending, TargetRef: "existing", IdempotencyKey: "grade:webhook:s:" + strings.Repeat("b", 40)})
	if err != nil {
		t.Fatal(err)
	}
	w.beforePublish = func(request WriteRequest, sha string) {
		h, e := s.Store.GetSubmissionHandoff(ctx, first.ID)
		if e != nil || h.Status != "publishing" {
			t.Fatal("intent not durable before push", e)
		}
		var d document
		_ = json.Unmarshal(h.Document, &d)
		if d.Candidate != sha || d.Request.CreatedAt != first.CreatedAt || d.Request.BaseRevision != first.BaseRevision {
			t.Fatal("frozen request changed")
		}
	}
	if err = s.Process(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.Process(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	sub, _ := s.Store.GetSubmission(ctx, "s")
	if sub.LatestCommit != strings.Repeat("b", 40) || sub.LastActivityAt == nil {
		t.Fatal("verified commit not available for assessment capture")
	}
	got, err := s.Get(ctx, a, "s", first.ID)
	if err != nil || got.Status != "committed" || w.publish != 1 {
		t.Fatal(got, err, w.publish)
	}
	stored, _ := s.Store.GetSubmissionHandoff(ctx, first.ID)
	if strings.Contains(string(stored.Document), "cHJpbnQoNDIp") {
		t.Fatal("terminal receipt retained source")
	}
	count := 0
	for {
		job, e := s.Store.ClaimNextJob(ctx)
		if errors.Is(e, store.ErrNotFound) {
			break
		}
		if e != nil {
			t.Fatal(e)
		}
		if job.Type == "grade_revision" {
			count++
		}
	}
	if count != 1 {
		t.Fatal("duplicate grades", count)
	}
}
func TestAdmissionOwnershipExpiryLimitsAndAccessRecheck(t *testing.T) {
	ctx := context.Background()
	s, a, w := fixture(t, memory.New())
	now := time.Now()
	s.Now = func() time.Time { return now }
	other := a
	other.ID = "43"
	if _, err := s.Preview(ctx, other, "s", "request-001", testFiles()); !errors.Is(err, store.ErrNotFound) {
		t.Fatal(err)
	}
	var first Receipt
	for _, key := range []string{"request-001", "request-002", "request-003"} {
		r, err := s.Preview(ctx, a, "s", key, testFiles())
		if err != nil {
			t.Fatal(err)
		}
		first = r
	}
	if _, err := s.Preview(ctx, a, "s", "request-004", testFiles()); !errors.Is(err, store.ErrConflict) || w.inspect != 3 {
		t.Fatal(err, w.inspect)
	}
	now = now.Add(16 * time.Minute)
	if _, err := s.Confirm(ctx, a, "s", first.ID); !errors.Is(err, ErrExpired) {
		t.Fatal(err)
	}
	fresh, err := s.Preview(ctx, a, "s", "request-005", testFiles())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Confirm(ctx, a, "s", fresh.ID); err != nil {
		t.Fatal(err)
	}
	sub, _ := s.Store.GetSubmission(ctx, "s")
	sub.Status = "locked"
	_ = s.Store.UpdateSubmission(ctx, sub)
	if err = s.Process(ctx, fresh.ID); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(ctx, a, "s", fresh.ID)
	if got.Status != "refused" || w.prepare != 0 || w.publish != 0 {
		t.Fatal(got, w)
	}
}
func TestFilesRejectEscapeAutomationAndBudgets(t *testing.T) {
	for _, p := range []string{"../answer.py", "/answer.py", ".git/config", "x/.github/workflows/a.yml", "x\\answer.py", "a//b.py", "grading.yaml", "Dockerfile", "requirements.txt", "é.py"} {
		if ValidateFiles([]File{{Path: p}}, "grading.yaml") == nil {
			t.Fatal("accepted", p)
		}
	}
	for _, files := range [][]File{{{Path: "a.py"}, {Path: "A.py"}}, {{Path: "a"}, {Path: "a/b.py"}}, {{Path: "a.py", Content: make([]byte, MaxFileBytes+1)}}, make([]File, MaxFiles+1)} {
		if ValidateFiles(files, "") == nil {
			t.Fatal("invalid files accepted")
		}
	}
	total := []File{}
	for _, p := range []string{"a.py", "b.py", "c.py", "d.py", "e.py"} {
		total = append(total, File{Path: p, Content: make([]byte, MaxFileBytes)})
	}
	if ValidateFiles(total, "") == nil {
		t.Fatal("aggregate limit bypass")
	}
}
func TestSQLiteRestartReusesPersistedCandidateAndReportsUncertainty(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "cairn.db")
	db, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s, a, w := fixture(t, db)
	r, err := s.Preview(ctx, a, "s", "request-001", testFiles())
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Confirm(ctx, a, "s", r.ID)
	if err != nil {
		t.Fatal(err)
	}
	h, _ := db.GetSubmissionHandoff(ctx, r.ID)
	var d document
	_ = json.Unmarshal(h.Document, &d)
	d.Candidate = strings.Repeat("b", 40)
	h.Status = "publishing"
	h.Document, _ = json.Marshal(d)
	if err = db.TransitionSubmissionHandoff(ctx, h, h.Version, nil); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	db, err = sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s = &Service{Store: db, Writer: w}
	w.outcome = VerificationNeeded
	if err = s.Process(ctx, r.ID); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, a, "s", r.ID)
	if err != nil || got.Status != "verification_needed" || w.prepare != 0 || w.publish != 1 {
		t.Fatal(got, err, w)
	}
}

func TestPreviouslyAttemptedPublicationAfterRevocationRemainsUncertain(t *testing.T) {
	ctx := context.Background()
	s, a, w := fixture(t, memory.New())
	r, err := s.Preview(ctx, a, "s", "request-001", testFiles())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Confirm(ctx, a, "s", r.ID); err != nil {
		t.Fatal(err)
	}
	h, _ := s.Store.GetSubmissionHandoff(ctx, r.ID)
	var d document
	_ = json.Unmarshal(h.Document, &d)
	d.Attempted = true
	d.Candidate = strings.Repeat("b", 40)
	h.Status = "publishing"
	h.Document, _ = json.Marshal(d)
	if err = s.Store.TransitionSubmissionHandoff(ctx, h, h.Version, nil); err != nil {
		t.Fatal(err)
	}
	roster, _ := s.Store.GetRosterEntry(ctx, "r")
	roster.Status = store.RosterRemoved
	_ = s.Store.UpdateRosterEntry(ctx, roster)
	if err = s.Process(ctx, r.ID); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(ctx, a, "s", r.ID)
	if got.Status != "verification_needed" || w.publish != 0 {
		t.Fatal(got, w.publish)
	}
}
func TestRetryMarkerAndLatestCommitNeverRegress(t *testing.T) {
	ctx := context.Background()
	s, a, w := fixture(t, memory.New())
	r, err := s.Preview(ctx, a, "s", "request-001", testFiles())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Confirm(ctx, a, "s", r.ID); err != nil {
		t.Fatal(err)
	}
	h, _ := s.Store.GetSubmissionHandoff(ctx, r.ID)
	var d document
	_ = json.Unmarshal(h.Document, &d)
	d.Attempted = true
	d.Candidate = strings.Repeat("b", 40)
	h.Status = "publishing"
	h.Document, _ = json.Marshal(d)
	if err = s.Store.TransitionSubmissionHandoff(ctx, h, h.Version, nil); err != nil {
		t.Fatal(err)
	}
	sub, _ := s.Store.GetSubmission(ctx, "s")
	sub.LatestCommit = strings.Repeat("c", 40)
	_ = s.Store.UpdateSubmission(ctx, sub)
	w.beforePublish = func(request WriteRequest, _ string) {
		if !request.Retry {
			t.Fatal("prior publication not signaled")
		}
	}
	if err = s.Process(ctx, r.ID); err != nil {
		t.Fatal(err)
	}
	sub, _ = s.Store.GetSubmission(ctx, "s")
	if sub.LatestCommit != strings.Repeat("c", 40) {
		t.Fatal("newer webhook observation regressed")
	}
}
func TestPreviewConcurrencyIsBoundedBeforeInspection(t *testing.T) {
	ctx := context.Background()
	s, a, w := fixture(t, memory.New())
	w.block = make(chan struct{})
	done := make(chan struct{}, 2)
	for _, key := range []string{"request-001", "request-002"} {
		go func(key string) { _, _ = s.Preview(ctx, a, "s", key, testFiles()); done <- struct{}{} }(key)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		w.mu.Lock()
		count := w.inspect
		w.mu.Unlock()
		if count == 2 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if _, err := s.Preview(ctx, a, "s", "request-003", testFiles()); !errors.Is(err, ErrUnavailable) {
		t.Fatal("unbounded inspect", err)
	}
	close(w.block)
	<-done
	<-done
}

func TestPreviewFreezesObservedCacheAndRefreshesMissedWebhook(t *testing.T) {
	ctx := context.Background()
	s, a, _ := fixture(t, memory.New())
	sub, _ := s.Store.GetSubmission(ctx, "s")
	sub.LatestCommit = strings.Repeat("c", 40)
	_ = s.Store.UpdateSubmission(ctx, sub)
	r, err := s.Preview(ctx, a, "s", "request-001", testFiles())
	if err != nil {
		t.Fatal(err)
	}
	h, _ := s.Store.GetSubmissionHandoff(ctx, r.ID)
	var d document
	_ = json.Unmarshal(h.Document, &d)
	if d.ObservedRevision != strings.Repeat("c", 40) || d.Request.BaseRevision != strings.Repeat("a", 40) {
		t.Fatal("cached and remote revisions were not frozen separately", d)
	}
	if _, err = s.Confirm(ctx, a, "s", r.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.Process(ctx, r.ID); err != nil {
		t.Fatal(err)
	}
	sub, _ = s.Store.GetSubmission(ctx, "s")
	if sub.LatestCommit != strings.Repeat("b", 40) {
		t.Fatal("missed webhook left assessment on old revision", sub.LatestCommit)
	}
}
