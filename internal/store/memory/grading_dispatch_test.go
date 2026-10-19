// SPDX-License-Identifier: AGPL-3.0-or-later
package memory

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/EduCloud-Ecosystem/cairn/internal/store"
)

// Reports the first successful cancellation check before the blocked mutex.
// Returning nil models that sample even if cancellation follows immediately.
type observedContext struct {
	context.Context
	first chan struct{}
	calls atomic.Int32
}

func (c *observedContext) Err() error {
	if c.calls.Add(1) == 1 {
		close(c.first)
		return nil
	}
	return c.Context.Err()
}
func TestDispatchCancellationWhileWaitingForMutex(t *testing.T) {
	for _, finish := range []bool{false, true} {
		t.Run(map[bool]string{false: "claim", true: "finish"}[finish], func(t *testing.T) {
			s := New()
			base := context.Background()
			now := time.Now().UTC()
			if err := s.CreateSubmission(base, &store.Submission{ID: "s"}); err != nil {
				t.Fatal(err)
			}
			if _, err := s.CreateJob(base, &store.ProvisioningJob{ID: "j", Type: "grade", TargetRef: "fixture", Status: store.JobInProgress}); err != nil {
				t.Fatal(err)
			}
			job := json.RawMessage(`{"job_id":"j","target":{"submission_id":"s"}}`)
			body, _ := json.Marshal(struct {
				Job json.RawMessage `json:"job"`
			}{job})
			d := store.GradingDispatch{JobID: "j", SubmissionID: "s", TargetSHA256: store.GradingDigest([]byte("fixture")), ContextSHA256: store.GradingDigest(job), ClaimSHA256: store.GradingDigest([]byte("owner")), RunID: "run", Runner: "fake", State: store.DispatchClaimed}
			r := &store.GradingRun{ID: "run", SubmissionID: "s", Runner: "fake", Status: "running", StartedAt: &now, Result: body}
			var grade *store.Grade
			if finish {
				if ok, err := s.ClaimGradingDispatch(base, d, r); err != nil || !ok {
					t.Fatal(ok, err)
				}
				r.Status = "completed"
				r.FinishedAt = &now
				d.State = store.DispatchPublished
				d.GradeID = "g"
				d.ResultSHA256 = store.GradingDigest(body)
				grade = &store.Grade{ID: "g", SubmissionID: "s", RunID: "run", Score: 1, MaxScore: 1, Breakdown: body, GradedAt: now}
			}
			ctx, cancel := context.WithCancel(base)
			defer cancel()
			observed := &observedContext{Context: ctx, first: make(chan struct{})}
			s.mu.Lock()
			done := make(chan error, 1)
			go func() {
				if finish {
					done <- s.FinishGradingDispatch(observed, d, r, grade)
				} else {
					_, err := s.ClaimGradingDispatch(observed, d, r)
					done <- err
				}
			}()
			<-observed.first
			cancel()
			s.mu.Unlock()
			if err := <-done; !errors.Is(err, context.Canceled) {
				t.Fatalf("cancelled mutex waiter mutated store: %v", err)
			}
			if _, err := s.LatestGradeForSubmission(base, "s"); !errors.Is(err, store.ErrNotFound) {
				t.Fatal("cancelled waiter published grade")
			}
			got, err := s.GetGradingDispatch(base, "j")
			if finish {
				if err != nil || got.State != store.DispatchClaimed {
					t.Fatal("cancelled finish changed claim")
				}
			} else if !errors.Is(err, store.ErrNotFound) {
				t.Fatal("cancelled waiter claimed")
			}
		})
	}
}
