// SPDX-License-Identifier: AGPL-3.0-or-later
package storetest

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/EduCloud-Ecosystem/cairn/internal/store"
	"github.com/EduCloud-Ecosystem/cairn/pkg/adapter"
	"strings"
	"sync"
	"testing"
	"time"
)

func testHandoff(t *testing.T, st store.Store) {
	ctx := context.Background()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(st.CreateUser(ctx, &store.User{ID: "op", Host: adapter.HostForgejo, HostUsername: "op", HostUserID: "1"}))
	must(st.CreateClassroom(ctx, &store.Classroom{ID: "c", CreatedBy: "op", Host: adapter.HostForgejo, HostNamespace: "org"}))
	must(st.CreateAssignment(ctx, &store.Assignment{ID: "a", ClassroomID: "c", Slug: "exercise", Type: store.AssignmentIndividual}))
	must(st.CreateRosterEntry(ctx, &store.RosterEntry{ID: "r", ClassroomID: "c", Host: adapter.HostForgejo, HostUsername: "student", HostUserID: "42", Status: store.RosterActive}))
	must(st.CreateSubmission(ctx, &store.Submission{ID: "s", AssignmentID: "a", RosterEntryID: "r", Status: "active"}))
	now := time.Now().Unix()
	h := &store.SubmissionHandoff{ID: "h", SubmissionID: "s", OwnerID: "42", IdempotencyKey: "key", PayloadDigest: "digest", Status: "preview", CreatedAt: now, ExpiresAt: now + 900, Version: 1, Document: []byte(`{"request":{"files":[{"content":"private-source"}]},"changes":[{"path":"answer.py"}]}`)}
	must(st.CreateSubmissionHandoff(ctx, h, 3))
	duplicate := *h
	duplicate.ID = "duplicate"
	if !errors.Is(st.CreateSubmissionHandoff(ctx, &duplicate, 3), store.ErrConflict) {
		t.Fatal("duplicate key accepted")
	}
	copy, err := st.GetSubmissionHandoff(ctx, "h")
	must(err)
	copy.Document[0] = 'x'
	again, err := st.GetSubmissionHandoff(ctx, "h")
	must(err)
	if again.Document[0] != '{' {
		t.Fatal("mutable store payload escaped")
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, jobID := range []string{"j1", "j2"} {
		wg.Add(1)
		go func(jobID string) {
			defer wg.Done()
			v := *h
			v.Status = "queued"
			results <- st.TransitionSubmissionHandoff(ctx, &v, 1, &store.ProvisioningJob{ID: jobID, Type: "submission_handoff", TargetRef: h.ID, Status: store.JobPending, IdempotencyKey: "handoff:h", ScheduledAt: time.Now()})
		}(jobID)
	}
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		} else if !errors.Is(err, store.ErrConflict) {
			t.Fatal(err)
		}
	}
	if success != 1 {
		t.Fatal("transition not atomic", success)
	}
	job, err := st.ClaimNextJob(ctx)
	must(err)
	if job.TargetRef != "h" {
		t.Fatal(job)
	}
	if _, err := st.ClaimNextJob(ctx); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("duplicate live job", err)
	}
	job.ScheduledAt = time.Now().Add(-11 * time.Minute)
	must(st.UpdateJob(ctx, job))
	reclaimed, err := st.ClaimNextJob(ctx)
	must(err)
	if reclaimed.ID != job.ID {
		t.Fatal("lost handoff not reclaimed")
	}
	expired := *h
	expired.ID = "expired"
	expired.IdempotencyKey = "old-key"
	expired.CreatedAt = now - 1000
	expired.ExpiresAt = now - 1
	must(st.CreateSubmissionHandoff(ctx, &expired, 3))
	must(st.ExpireSubmissionHandoffs(ctx, now))
	got, err := st.GetSubmissionHandoff(ctx, expired.ID)
	must(err)
	if got.Status != "expired" || strings.Contains(string(got.Document), "private-source") || !strings.Contains(string(got.Document), "answer.py") {
		t.Fatal("expiry must retain receipt metadata but erase source", string(got.Document))
	}

	// A missed webhook can leave cached A while the preview's real remote base
	// is B. Finalization may refresh exactly the observed A, but never concurrent D.
	for _, concurrent := range []bool{false, true} {
		sub, e := st.GetSubmission(ctx, "s")
		must(e)
		sub.LatestCommit = strings.Repeat("a", 40)
		must(st.UpdateSubmission(ctx, sub))
		key := "missed"
		if concurrent {
			key = "concurrent"
		}
		raw, _ := json.Marshal(map[string]any{"observed_revision": strings.Repeat("a", 40), "candidate": strings.Repeat("c", 40), "request": map[string]any{"base_revision": strings.Repeat("b", 40)}})
		record := &store.SubmissionHandoff{ID: key, SubmissionID: "s", OwnerID: "42", IdempotencyKey: key, PayloadDigest: key, Status: "preview", CreatedAt: now, ExpiresAt: now + 900, Version: 1, Document: raw}
		must(st.CreateSubmissionHandoff(ctx, record, 3))
		expected := strings.Repeat("c", 40)
		if concurrent {
			sub.LatestCommit = strings.Repeat("d", 40)
			must(st.UpdateSubmission(ctx, sub))
			expected = sub.LatestCommit
		}
		record.Status = "committed"
		must(st.TransitionSubmissionHandoff(ctx, record, 1, nil))
		sub, e = st.GetSubmission(ctx, "s")
		must(e)
		if sub.LatestCommit != expected {
			t.Fatal("receipt finalization lost cached-revision CAS", sub.LatestCommit, expected)
		}
	}
	must(st.DeleteRosterEntry(ctx, "r"))
	if _, err := st.GetSubmissionHandoff(ctx, "h"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("learner erasure retained source", err)
	}
}
