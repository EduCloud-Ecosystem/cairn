// SPDX-License-Identifier: AGPL-3.0-or-later
package storetest

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/EduCloud-Ecosystem/cairn/internal/store"
)

func testGenerations(t *testing.T, s store.Store) {
	ctx := context.Background()
	for _, err := range []error{s.CreateClassroom(ctx, &store.Classroom{ID: "c"}), s.CreateAssignment(ctx, &store.Assignment{ID: "a", ClassroomID: "c"}), s.CreateRosterEntry(ctx, &store.RosterEntry{ID: "r", ClassroomID: "c", Status: store.RosterActive}), s.CreateSubmission(ctx, &store.Submission{ID: "s", AssignmentID: "a", RosterEntryID: "r", LatestCommit: "commit", Status: "active"}), s.PutAssessmentRubric(ctx, &store.AssessmentRubric{AssignmentID: "a", Digest: "rubric", Document: []byte(`{}`)})} {
		if err != nil {
			t.Fatal(err)
		}
	}
	records := []*store.AssessmentRecord{}
	for _, id := range []string{"p1", "p2", "p3"} {
		p := &store.AssessmentRecord{ID: id, SubmissionID: "s", Revision: "commit", RubricDigest: "rubric", Status: "collected", Document: []byte(`{}`)}
		if err := s.CreateAssessment(ctx, p); err != nil {
			t.Fatal(err)
		}
		records = append(records, p)
	}
	l := store.GenerationLimits{DailyRequests: 10, LearnerDailyRequests: 1, DailyUnits: 100}
	if err := s.SetGenerationPaused(ctx, true); err != nil {
		t.Fatal(err)
	}
	g := &store.Generation{AssessmentID: "p1", Day: "2026-10-07", ReservedUnits: 50, Model: "fixture"}
	if err := s.ReserveGeneration(ctx, records[0], g, l); !errors.Is(err, store.ErrGenerationLimit) {
		t.Fatal("pause did not prevent reservation")
	}
	s.SetGenerationPaused(ctx, false)
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			v := *g
			v.AssessmentID = records[i].ID
			results <- s.ReserveGeneration(ctx, records[i], &v, l)
		}(i)
	}
	wg.Wait()
	close(results)
	n := 0
	for err := range results {
		if err == nil {
			n++
		} else if !errors.Is(err, store.ErrGenerationLimit) {
			t.Fatal(err)
		}
	}
	if n != 1 {
		t.Fatalf("quota admitted %d requests", n)
	}
	if err := s.ReserveGeneration(ctx, records[2], &store.Generation{AssessmentID: "p3", Day: g.Day, ReservedUnits: 50}, l); !errors.Is(err, store.ErrGenerationLimit) {
		t.Fatal("learner quota bypass")
	}
	// Erasing evidence does not refund uncertain paid usage.
	if err := s.DeleteRosterEntry(ctx, "r"); err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, id := range []string{"p1", "p2"} {
		a, e := s.GetGeneration(ctx, id)
		if e == nil {
			found++
			a.Status = "failed"
			a.ErrorCode = "timeout"
			if e = s.FinishGeneration(ctx, a); e != nil {
				t.Fatal(e)
			}
			if e = s.FinishGeneration(ctx, a); !errors.Is(e, store.ErrConflict) {
				t.Fatal("finished request changed again")
			}
		}
	}
	if found != 1 {
		t.Fatal("erasure reset reservation")
	}
}
