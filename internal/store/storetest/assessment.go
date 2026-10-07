// SPDX-License-Identifier: AGPL-3.0-or-later
package storetest

import (
	"context"
	"errors"
	"github.com/EduCloud-Ecosystem/cairn/internal/store"
	"sync"
	"testing"
	"time"
)

func testAssessments(t *testing.T, s store.Store) {
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	for _, err := range []error{s.CreateClassroom(ctx, &store.Classroom{ID: "c"}), s.CreateAssignment(ctx, &store.Assignment{ID: "a", ClassroomID: "c"}), s.CreateRosterEntry(ctx, &store.RosterEntry{ID: "r", ClassroomID: "c", Status: store.RosterActive}), s.CreateSubmission(ctx, &store.Submission{ID: "s", AssignmentID: "a", RosterEntryID: "r", LatestCommit: "commit", LastActivityAt: &now, Status: "active"}), s.PutAssessmentRubric(ctx, &store.AssessmentRubric{AssignmentID: "a", Digest: "rubric", Document: []byte(`{"title":"one"}`)})} {
		if err != nil {
			t.Fatal(err)
		}
	}
	p := &store.AssessmentRecord{ID: "p", SubmissionID: "s", Revision: "commit", SubmissionActivity: store.AssessmentActivity(&now), RubricDigest: "rubric", Status: "collected", Document: []byte(`{"input":"captured"}`)}
	if err := s.CreateAssessment(ctx, p); err != nil {
		t.Fatal(err)
	}
	p.Status = "pending"
	if err := s.TransitionAssessment(ctx, p, "collected", nil); err != nil {
		t.Fatal(err)
	}
	// Wrong-grade-target cannot change either row.
	p.Status = "approved"
	g := &store.Grade{ID: "g", SubmissionID: "wrong", Score: 7, MaxScore: 10, Breakdown: []byte(`{}`), GradedAt: now}
	if err := s.TransitionAssessment(ctx, p, "pending", g); !errors.Is(err, store.ErrConflict) {
		t.Fatal(err)
	}
	// Insertion failure must roll back the status update.
	g.SubmissionID = "s"
	if err := s.CreateGrade(ctx, g); err != nil {
		t.Fatal(err)
	}
	if err := s.TransitionAssessment(ctx, p, "pending", g); err == nil {
		t.Fatal("duplicate grade accepted")
	}
	stored, _ := s.GetAssessment(ctx, "p")
	if stored.Status != "pending" {
		t.Fatal("grade failure did not roll back")
	}
	// Concurrent approvals create exactly one new grade.
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, gid := range []string{"g2", "g3"} {
		wg.Add(1)
		go func(gid string) {
			defer wg.Done()
			grade := *g
			grade.ID = gid
			errs <- s.TransitionAssessment(ctx, p, "pending", &grade)
		}(gid)
	}
	wg.Wait()
	close(errs)
	success := 0
	for err := range errs {
		if err == nil {
			success++
		} else if !errors.Is(err, store.ErrConflict) {
			t.Fatal(err)
		}
	}
	if success != 1 {
		t.Fatalf("approvals=%d", success)
	}
	grades, err := s.ListGradesBySubmission(ctx, "s")
	if err != nil || len(grades) != 2 {
		t.Fatalf("grades=%d %v", len(grades), err)
	}
	// Returned JSON cannot mutate memory storage.
	stored, _ = s.GetAssessment(ctx, "p")
	stored.Document[0] = 'x'
	again, _ := s.GetAssessment(ctx, "p")
	if again.Document[0] != '{' {
		t.Fatal("store exposed mutable document")
	}
	// Export retention also removes the linked evidence and review.
	if _, err = s.ConfirmExport(ctx, "c", now); err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.PurgeExportedGrades(ctx, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err = s.GetAssessment(ctx, "p"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("grade purge retained approved assessment")
	}
	// A later activity event invalidates even a capture of the same commit.
	stale := *p
	stale.ID = "stale"
	stale.Status = "collected"
	stale.GradeID = ""
	if err = s.CreateAssessment(ctx, &stale); err != nil {
		t.Fatal(err)
	}
	sub, _ := s.GetSubmission(ctx, "s")
	later := now.Add(time.Second)
	sub.LastActivityAt = &later
	if err = s.UpdateSubmission(ctx, sub); err != nil {
		t.Fatal(err)
	}
	stale.Status = "pending"
	if err = s.TransitionAssessment(ctx, &stale, "collected", nil); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("same-commit replay not stale: %v", err)
	}
	stale.Status = "rejected"
	if err = s.TransitionAssessment(ctx, &stale, "collected", nil); err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteRosterEntry(ctx, "r"); err != nil {
		t.Fatal(err)
	}
	rows, err := s.ListAssessments(ctx, "s")
	if err != nil || len(rows) != 0 {
		t.Fatalf("assessment erasure failed: %v %v", rows, err)
	}
}
