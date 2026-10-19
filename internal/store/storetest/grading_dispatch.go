// SPDX-License-Identifier: AGPL-3.0-or-later
package storetest

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/EduCloud-Ecosystem/cairn/internal/gradingjob"
	"github.com/EduCloud-Ecosystem/cairn/internal/store"
	"github.com/EduCloud-Ecosystem/cairn/pkg/adapter"
)

func testGradingDispatch(t *testing.T, s store.Store) {
	t.Helper()
	ctx := context.Background()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(s.CreateClassroom(ctx, &store.Classroom{ID: "c"}))
	must(s.CreateAssignment(ctx, &store.Assignment{ID: "a", ClassroomID: "c"}))
	must(s.CreateRosterEntry(ctx, &store.RosterEntry{ID: "r", ClassroomID: "c", Status: store.RosterActive}))
	must(s.CreateSubmission(ctx, &store.Submission{ID: "s", AssignmentID: "a", RosterEntryID: "r", Status: "active"}))
	target := gradingjob.Target{Version: 1, SubmissionID: "s", AssignmentID: "a", Repository: adapter.RepoRef{Host: adapter.HostGitHub, Namespace: "fixture", Name: "submission"}, Revision: strings.Repeat("a", 40), Policy: gradingjob.Policy{Repository: adapter.RepoRef{Host: adapter.HostGitHub, Namespace: "teacher", Name: "policy"}, Revision: strings.Repeat("b", 40), Path: "grading.json"}}
	raw, err := target.Encode()
	must(err)
	job := gradingjob.Context{JobID: "j", Target: target}
	body, err := json.Marshal(job)
	must(err)
	initial, err := json.Marshal(struct {
		Job gradingjob.Context `json:"job"`
	}{job})
	must(err)
	_, err = s.CreateJob(ctx, &store.ProvisioningJob{ID: "j", Type: "grade", TargetRef: raw, Status: store.JobInProgress, IdempotencyKey: "j", ScheduledAt: time.Now()})
	must(err)
	at := time.Now().UTC()
	d := store.GradingDispatch{JobID: "j", SubmissionID: "s", TargetSHA256: store.GradingDigest([]byte(raw)), ContextSHA256: store.GradingDigest(body), ClaimSHA256: strings.Repeat("c", 64), RunID: "run", Runner: "fake", State: store.DispatchClaimed}
	run := &store.GradingRun{ID: "run", SubmissionID: "s", Runner: "fake", Status: "running", StartedAt: &at, Result: initial}
	wrong := d
	wrong.TargetSHA256 = strings.Repeat("d", 64)
	if ok, err := s.ClaimGradingDispatch(ctx, wrong, run); ok || !errors.Is(err, store.ErrConflict) {
		t.Fatalf("unrelated target claim %v %v", ok, err)
	}
	ok, err := s.ClaimGradingDispatch(ctx, d, run)
	must(err)
	if !ok {
		t.Fatal("no claim")
	}
	assertGradingInspection(t, s, store.DispatchClaimed, true, false)
	// Caller-owned buffers must not mutate persisted audit state.
	original := append([]byte(nil), run.Result...)
	run.Result[0] = '!'
	runs, err := s.ListGradingRunsBySubmission(ctx, "s")
	must(err)
	var storedJSON, originalJSON any
	if len(runs) != 1 || json.Unmarshal(runs[0].Result, &storedJSON) != nil || json.Unmarshal(original, &originalJSON) != nil || !reflect.DeepEqual(storedJSON, originalJSON) {
		t.Fatal("mutable claim aliases")
	}
	// PostgreSQL JSONB normalizes object order/spacing; compare document values.

	run.Result = original
	duplicate := d
	duplicate.RunID = "second-run"
	duplicate.ClaimSHA256 = strings.Repeat("e", 64)
	copy := *run
	copy.ID = duplicate.RunID
	ok, err = s.ClaimGradingDispatch(ctx, duplicate, &copy)
	must(err)
	if ok {
		t.Fatal("second claimant")
	}
	q, err := s.GetGradingJob(ctx, "j")
	must(err)
	q.Status = store.JobPending
	if err = s.UpdateJob(ctx, q); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("generic queue update %v", err)
	}
	if _, err = s.ClaimNextJob(ctx); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("consumed queue claimed")
	}
	result, err := json.Marshal(struct {
		Job      gradingjob.Context `json:"job"`
		Score    float64            `json:"score"`
		MaxScore float64            `json:"max_score"`
	}{job, 1, 1})
	must(err)
	run.Status = "completed"
	run.Result = result
	run.FinishedAt = &at
	d.State = store.DispatchPublished
	d.GradeID = "g"
	d.ResultSHA256 = store.GradingDigest(result)
	grade := &store.Grade{ID: "g", SubmissionID: "s", RunID: "run", Score: 1, MaxScore: 1, Breakdown: result, GradedAt: at}
	wrong = d
	wrong.ClaimSHA256 = strings.Repeat("d", 64)
	if err = s.FinishGradingDispatch(ctx, wrong, run, grade); !errors.Is(err, store.ErrConflict) {
		t.Fatal("foreign owner finalized", err)
	}
	must(s.FinishGradingDispatch(ctx, d, run, grade))
	assertGradingInspection(t, s, store.DispatchPublished, true, true)
	must(s.FinishGradingDispatch(ctx, d, run, grade))
	grades, err := s.ListGradesBySubmission(ctx, "s")
	must(err)
	if len(grades) != 1 {
		t.Fatal("duplicate publication")
	}
	q, err = s.GetGradingJob(ctx, "j")
	must(err)
	if q.Status != store.JobSucceeded {
		t.Fatal("queue not published")
	}
	bad := d
	bad.ResultSHA256 = strings.Repeat("f", 64)
	if err = s.FinishGradingDispatch(ctx, bad, run, grade); !errors.Is(err, store.ErrConflict) {
		t.Fatal("changed result accepted", err)
	}
	_, err = s.ConfirmExport(ctx, "c", at.Add(-time.Hour))
	must(err)
	ng, nr, err := s.PurgeExportedGrades(ctx, at)
	must(err)
	if ng != 1 || nr != 1 {
		t.Fatalf("purge %d %d", ng, nr)
	}
	must(s.DeleteRosterEntry(ctx, "r"))
	assertGradingInspection(t, s, store.DispatchPublished, false, false)
	got, err := s.GetGradingDispatch(ctx, "j")
	must(err)
	if *got != d {
		t.Fatal("erasure destroyed barrier")
	}
	// Exact finalization replay after purge is a no-op, never a body recreation.
	must(s.FinishGradingDispatch(ctx, d, run, grade))
	grades, err = s.ListGradesBySubmission(ctx, "s")
	must(err)
	if len(grades) != 0 {
		t.Fatal("purged grade resurrected")
	}
}
