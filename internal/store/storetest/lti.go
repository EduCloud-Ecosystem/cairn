// SPDX-License-Identifier: AGPL-3.0-or-later
package storetest

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/EduCloud-Ecosystem/cairn/internal/store"
)

func testLTI(t *testing.T, s store.Store) {
	ctx := context.Background()
	for _, e := range []error{s.CreateClassroom(ctx, &store.Classroom{ID: "c"}), s.CreateAssignment(ctx, &store.Assignment{ID: "a", ClassroomID: "c"}), s.CreateRosterEntry(ctx, &store.RosterEntry{ID: "r", ClassroomID: "c", Status: store.RosterActive}), s.CreateSubmission(ctx, &store.Submission{ID: "s", AssignmentID: "a", RosterEntryID: "r", Status: "active"}), s.CreateGrade(ctx, &store.Grade{ID: "g", SubmissionID: "s", Score: 8, MaxScore: 10, GradedAt: time.Now().UTC()})} {
		if e != nil {
			t.Fatal(e)
		}
	}
	r := &store.LTIRecord{ID: "binding", Kind: "binding", UniqueKey: "subject", AssignmentID: "a", RosterEntryID: "r", Revision: 1, Document: []byte(`{"subject":"opaque"}`)}
	if e := s.SaveLTIRecord(ctx, r, 0); e != nil {
		t.Fatal(e)
	}
	r.Document[0] = 'x'
	got, e := s.FindLTIRecord(ctx, "subject")
	if e != nil || got.Document[0] != '{' {
		t.Fatalf("record alias: %+v %v", got, e)
	}
	duplicate := *got
	duplicate.ID = "other"
	if e := s.SaveLTIRecord(ctx, &duplicate, 0); !errors.Is(e, store.ErrConflict) {
		t.Fatalf("subject duplicate: %v", e)
	}
	duplicate = *got
	duplicate.UniqueKey = "another"
	if e := s.SaveLTIRecord(ctx, &duplicate, 0); !errors.Is(e, store.ErrConflict) {
		t.Fatalf("roster duplicate: %v", e)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			next := *got
			next.Revision = 2
			next.Document = []byte(`{"subject":"opaque","updated":true}`)
			results <- s.SaveLTIRecord(ctx, &next, 1)
		}()
	}
	wg.Wait()
	close(results)
	success := 0
	for e := range results {
		if e == nil {
			success++
		} else if !errors.Is(e, store.ErrConflict) {
			t.Fatal(e)
		}
	}
	if success != 1 {
		t.Fatalf("CAS winners=%d", success)
	}
	got, _ = s.GetLTIRecord(ctx, "binding")
	mutated := *got
	mutated.AssignmentID = "other"
	mutated.Revision++
	if e := s.SaveLTIRecord(ctx, &mutated, got.Revision); e == nil {
		t.Fatal("changed identity accepted")
	}
	link := &store.LTIRecord{ID: "link", Kind: "link", UniqueKey: "line", AssignmentID: "a", Revision: 1, Document: []byte(`{}`)}
	if e := s.SaveLTIRecord(ctx, link, 0); e != nil {
		t.Fatal(e)
	}
	delivery := &store.LTIRecord{ID: "delivery", Kind: "delivery", UniqueKey: "delivery", AssignmentID: "a", RosterEntryID: "r", GradeID: "g", Revision: 1, Document: []byte(`{"status":"uncertain"}`)}
	if e := s.SaveLTIRecord(ctx, delivery, 0); e != nil {
		t.Fatal(e)
	}
	now := time.Now().UTC()
	if _, e := s.ConfirmExport(ctx, "c", now); e != nil {
		t.Fatal(e)
	}
	if _, _, e := s.PurgeExportedGrades(ctx, now.Add(time.Second)); e != nil {
		t.Fatal(e)
	}
	if _, e := s.GetLTIRecord(ctx, "delivery"); !errors.Is(e, store.ErrNotFound) {
		t.Fatalf("grade purge retained receipt: %v", e)
	}
	if _, e := s.GetLTIRecord(ctx, "binding"); e != nil {
		t.Fatalf("grade purge removed binding: %v", e)
	}
	if e := s.DeleteRosterEntry(ctx, "r"); e != nil {
		t.Fatal(e)
	}
	if _, e := s.GetLTIRecord(ctx, "binding"); !errors.Is(e, store.ErrNotFound) {
		t.Fatalf("roster purge retained binding: %v", e)
	}
	if _, e := s.GetLTIRecord(ctx, "link"); e != nil {
		t.Fatalf("roster purge removed class mapping: %v", e)
	}
}
