// SPDX-License-Identifier: AGPL-3.0-or-later

package sqlite_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/EduCloud-Ecosystem/cairn/internal/store"
	"github.com/EduCloud-Ecosystem/cairn/internal/store/sqlite"
	"github.com/EduCloud-Ecosystem/cairn/internal/store/storetest"
	"github.com/EduCloud-Ecosystem/cairn/pkg/adapter"
)

func open(t *testing.T) store.Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	st, err := sqlite.Open(path)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestSQLiteConformance(t *testing.T) {
	storetest.Run(t, open)
}

// TestSQLiteDataSurvivesReopen verifies that data written before Close is
// visible after reopening the same file.
func TestSQLiteDataSurvivesReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "persist.db")
	ctx := context.Background()

	// Write.
	st1, err := sqlite.Open(path)
	if err != nil {
		t.Fatalf("open 1: %v", err)
	}
	if err := st1.CreateClassroom(ctx, &store.Classroom{
		ID:            "c1",
		Name:          "CS101",
		Host:          adapter.HostGitHub,
		HostNamespace: "cs101-org",
	}); err != nil {
		t.Fatalf("CreateClassroom: %v", err)
	}
	if err := st1.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Reopen and read.
	st2, err := sqlite.Open(path)
	if err != nil {
		t.Fatalf("open 2: %v", err)
	}
	defer func() { _ = st2.Close() }()

	got, err := st2.GetClassroom(ctx, "c1")
	if err != nil {
		t.Fatalf("GetClassroom after reopen: %v", err)
	}
	if got.Name != "CS101" {
		t.Errorf("Name = %q, want CS101", got.Name)
	}
}

// TestSQLiteUniqueViolationMapsToErrConflict verifies that a duplicate
// (assignment_id, roster_entry_id) insert returns store.ErrConflict.
func TestSQLiteUniqueViolationMapsToErrConflict(t *testing.T) {
	path := filepath.Join(t.TempDir(), "conflict.db")
	st, err := sqlite.Open(path)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	defer func() { _ = st.Close() }()

	ctx := context.Background()
	_ = st.CreateClassroom(ctx, &store.Classroom{ID: "c1", Name: "CS101", Host: adapter.HostGitHub, HostNamespace: "org"})
	_ = st.CreateAssignment(ctx, &store.Assignment{
		ID: "a1", ClassroomID: "c1", Title: "HW1", Slug: "hw-1",
		TemplateRef: adapter.TemplateRef{Host: adapter.HostGitHub, Namespace: "org", Name: "tpl"},
		Type:        store.AssignmentIndividual, GradingSpec: "grading.json",
	})
	_ = st.CreateRosterEntry(ctx, &store.RosterEntry{ID: "r1", ClassroomID: "c1", Host: adapter.HostGitHub, HostUsername: "octocat", Status: store.RosterInvited})

	if err := st.CreateSubmission(ctx, &store.Submission{ID: "s1", AssignmentID: "a1", RosterEntryID: "r1", Status: "provisioning"}); err != nil {
		t.Fatalf("first CreateSubmission: %v", err)
	}
	err = st.CreateSubmission(ctx, &store.Submission{ID: "s2", AssignmentID: "a1", RosterEntryID: "r1", Status: "provisioning"})
	if !errors.Is(err, store.ErrConflict) {
		t.Fatalf("duplicate CreateSubmission: got %v, want ErrConflict", err)
	}
}

func TestAssessmentSurvivesReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "assessment.db")
	ctx := context.Background()
	s, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, err := range []error{s.CreateClassroom(ctx, &store.Classroom{ID: "c"}), s.CreateAssignment(ctx, &store.Assignment{ID: "a", ClassroomID: "c"}), s.CreateRosterEntry(ctx, &store.RosterEntry{ID: "r", ClassroomID: "c"}), s.CreateSubmission(ctx, &store.Submission{ID: "s", AssignmentID: "a", RosterEntryID: "r"}), s.PutAssessmentRubric(ctx, &store.AssessmentRubric{AssignmentID: "a", Digest: "d", Document: []byte(`{"title":"Saved rubric"}`)}), s.CreateAssessment(ctx, &store.AssessmentRecord{ID: "p", SubmissionID: "s", Revision: "commit", RubricDigest: "d", Status: "collected", Document: []byte(`{"artifact":"captured source"}`)})} {
		if err != nil {
			t.Fatal(err)
		}
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, err := s.GetAssessment(ctx, "p")
	if err != nil || string(got.Document) != `{"artifact":"captured source"}` {
		t.Fatalf("reopened assessment %+v %v", got, err)
	}
	rubric, err := s.GetAssessmentRubric(ctx, "a")
	if err != nil || rubric.Digest != "d" {
		t.Fatalf("reopened rubric %+v %v", rubric, err)
	}
}

func TestGenerationBudgetAndPauseSurviveRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "generation.db")
	ctx := context.Background()
	s, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, err := range []error{
		s.CreateClassroom(ctx, &store.Classroom{ID: "c"}),
		s.CreateAssignment(ctx, &store.Assignment{ID: "a", ClassroomID: "c"}),
		s.CreateRosterEntry(ctx, &store.RosterEntry{ID: "r", ClassroomID: "c", Status: store.RosterActive}),
		s.CreateSubmission(ctx, &store.Submission{ID: "s", AssignmentID: "a", RosterEntryID: "r", LatestCommit: "commit", Status: "active"}),
		s.PutAssessmentRubric(ctx, &store.AssessmentRubric{AssignmentID: "a", Digest: "d", Document: []byte(`{}`)}),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
	r := &store.AssessmentRecord{ID: "p", SubmissionID: "s", Revision: "commit", RubricDigest: "d", Status: "collected", Document: []byte(`{}`)}
	if err := s.CreateAssessment(ctx, r); err != nil {
		t.Fatal(err)
	}
	limits := store.GenerationLimits{DailyRequests: 10, LearnerDailyRequests: 3, DailyUnits: 100}
	if err := s.ReserveGeneration(ctx, r, &store.Generation{AssessmentID: "p", Day: "2026-10-07", ReservedUnits: 60}, limits); err != nil {
		t.Fatal(err)
	}
	if err := s.SetGenerationPaused(ctx, true); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if paused, err := s.GenerationPaused(ctx); err != nil || !paused {
		t.Fatal("lost pause on restart")
	}
	if err := s.SetGenerationPaused(ctx, false); err != nil {
		t.Fatal(err)
	}
	if a, err := s.GetGeneration(ctx, "p"); err != nil || a.Status != "running" {
		t.Fatal("lost uncertain reservation on restart")
	}
	r.ID = "p2"
	if err := s.CreateAssessment(ctx, r); err != nil {
		t.Fatal(err)
	}
	g := &store.Generation{AssessmentID: "p2", Day: "2026-10-07", ReservedUnits: 50}
	if err := s.ReserveGeneration(ctx, r, g, limits); !errors.Is(err, store.ErrGenerationLimit) {
		t.Fatal("usage budget reset on restart")
	}
	g.ReservedUnits = 30
	limits.DailyRequests = 1
	if err := s.ReserveGeneration(ctx, r, g, limits); !errors.Is(err, store.ErrGenerationLimit) {
		t.Fatal("request budget reset on restart")
	}
	limits.DailyRequests = 10
	if err := s.ReserveGeneration(ctx, r, g, limits); err != nil {
		t.Fatalf("remaining budget unavailable: %v", err)
	}
}
