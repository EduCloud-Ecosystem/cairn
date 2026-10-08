// SPDX-License-Identifier: AGPL-3.0-or-later
package storetest

import (
	"context"
	"errors"
	"github.com/EduCloud-Ecosystem/cairn/internal/store"
	"testing"
)

func testCalibrations(t *testing.T, s store.Store) {
	ctx := context.Background()
	for _, err := range []error{s.CreateClassroom(ctx, &store.Classroom{ID: "c"}), s.CreateAssignment(ctx, &store.Assignment{ID: "a", ClassroomID: "c"}), s.CreateRosterEntry(ctx, &store.RosterEntry{ID: "r", ClassroomID: "c", Status: store.RosterActive}), s.CreateSubmission(ctx, &store.Submission{ID: "s", AssignmentID: "a", RosterEntryID: "r", Status: "active", LatestCommit: "commit"}), s.PutAssessmentRubric(ctx, &store.AssessmentRubric{AssignmentID: "a", Digest: "rubric", Document: []byte(`{}`)})} {
		if err != nil {
			t.Fatal(err)
		}
	}
	c := &store.Calibration{ID: "profile", OwnerID: "teacher", AssignmentID: "a", RubricDigest: "rubric", Revision: 1, Status: "draft", Document: []byte(`{"historical":"private"}`)}
	if err := s.CreateCalibration(ctx, c); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetCalibration(ctx, c.ID, "other"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("owner isolation failed")
	}
	list, err := s.ListCalibrations(ctx, "a", "other")
	if err != nil || len(list) != 0 {
		t.Fatal("owner list leaked")
	}
	c.Revision = 2
	c.SourceSubmissionID = "s"
	if err = s.UpdateCalibration(ctx, c, 1); err != nil {
		t.Fatal(err)
	}
	if err = s.UpdateCalibration(ctx, c, 1); !errors.Is(err, store.ErrConflict) {
		t.Fatal("stale update accepted")
	}
	l := store.GenerationLimits{DailyRequests: 1, LearnerDailyRequests: 3, DailyUnits: 100}
	g := &store.Generation{AssessmentID: "calibration:profile:example", Day: "2026-10-08", ReservedUnits: 60, Status: "running", Model: "synthetic"}
	if err = s.ReserveCalibrationGeneration(ctx, c, g, l); err != nil {
		t.Fatal(err)
	}
	r := &store.AssessmentRecord{ID: "live", SubmissionID: "s", Revision: "commit", RubricDigest: "rubric", Status: "collected", Document: []byte(`{}`)}
	if err = s.CreateAssessment(ctx, r); err != nil {
		t.Fatal(err)
	}
	if err = s.ReserveGeneration(ctx, r, &store.Generation{AssessmentID: r.ID, Day: g.Day, ReservedUnits: 20}, l); !errors.Is(err, store.ErrGenerationLimit) {
		t.Fatal("calibration bypassed shared live quota")
	}
	if err = s.DeleteRosterEntry(ctx, "r"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.GetCalibration(ctx, c.ID, "teacher"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("erasure retained historical profile")
	}
	if _, err = s.GetGeneration(ctx, g.AssessmentID); err != nil {
		t.Fatal("erasure refunded uncertain usage")
	}
}
