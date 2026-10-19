// SPDX-License-Identifier: AGPL-3.0-or-later
package grading

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/EduCloud-Ecosystem/cairn/internal/gradingjob"
	"github.com/EduCloud-Ecosystem/cairn/internal/id"
	"github.com/EduCloud-Ecosystem/cairn/internal/store"
	"github.com/EduCloud-Ecosystem/cairn/pkg/adapter"
)

// ReconciliationRequired never authorizes another checkout or runner call.
// State may be unavailable after a database error. The queue stays consumed or
// in-progress; it is never put back into pending by the managed worker path.
type ReconciliationRequired struct {
	JobID string
	State string
	Cause error
}

func (e *ReconciliationRequired) Error() string {
	return fmt.Sprintf("grading job %s requires reconciliation (state=%s): %v", e.JobID, e.State, e.Cause)
}
func (e *ReconciliationRequired) Unwrap() error { return e.Cause }
func held(jobID, state string, err error) error { return &ReconciliationRequired{jobID, state, err} }

// GradeManagedJob is the real Worker's optional state-ownership boundary. Check
// existing tombstones before runner selection so configuration changes cannot
// turn a consumed job into an ordinary retry. False means the old path owns it.
func (s *Service) GradeManagedJob(ctx context.Context, job gradingjob.Context) (bool, error) {
	return s.gradeManagedJob(ctx, job, true)
}

func (s *Service) gradeManagedJob(ctx context.Context, job gradingjob.Context, initiate bool) (bool, error) {
	if err := job.Validate(); err != nil {
		return false, err
	}
	old, err := s.Store.GetGradingDispatch(ctx, job.JobID)
	if err == nil {
		return true, s.dispatchExisting(ctx, job, old)
	}
	if !errors.Is(err, store.ErrNotFound) {
		return true, held(job.JobID, "unavailable", err)
	}
	if _, ok := s.Runner.(JobPolicyRunner); !ok {
		return false, nil
	}
	if !initiate {
		return true, held(job.JobID, "unclaimed", errors.New("managed dispatch requires the trusted queue Worker entry; direct GradeJob is inspect-only"))
	}
	return true, s.dispatchNew(ctx, job)
}
func (s *Service) dispatchExisting(ctx context.Context, job gradingjob.Context, d *store.GradingDispatch) error {
	b, _ := json.Marshal(job)
	j, err := s.Store.GetGradingJob(ctx, job.JobID)
	if err != nil {
		return held(job.JobID, d.State, err)
	}
	target, err := gradingjob.Decode(j.TargetRef)
	if err != nil || target != job.Target || d.ContextSHA256 != store.GradingDigest(b) || d.TargetSHA256 != store.GradingDigest([]byte(j.TargetRef)) || d.SubmissionID != job.Target.SubmissionID || (j.Type != "grade" && j.Type != "grade_revision") {
		return held(job.JobID, d.State, store.ErrConflict)
	}
	if d.State == store.DispatchPublished && j.Status == store.JobSucceeded && d.ResultSHA256 != "" && d.GradeID != "" {
		return nil
	}
	return held(job.JobID, d.State, errors.New("consumed dispatch; no automatic replay or external status recovery"))
}
func (s *Service) dispatchReread(job gradingjob.Context, expected *store.GradingDispatch, cause error) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	d, err := s.Store.GetGradingDispatch(ctx, job.JobID)
	if err != nil {
		return held(job.JobID, "unavailable", errors.Join(cause, err))
	}
	if expected != nil && *d == *expected && d.State == store.DispatchPublished {
		return s.dispatchExisting(ctx, job, d)
	}
	return held(job.JobID, d.State, cause)
}
func (s *Service) dispatchNew(ctx context.Context, job gradingjob.Context) error {
	j, err := s.Store.GetGradingJob(ctx, job.JobID)
	if err != nil {
		return held(job.JobID, "unclaimed", err)
	}
	target, err := gradingjob.Decode(j.TargetRef)
	if err != nil || target != job.Target || (j.Type != "grade" && j.Type != "grade_revision") || j.Status != store.JobInProgress {
		return held(job.JobID, "unclaimed", store.ErrConflict)
	}
	body, _ := json.Marshal(job)
	token := make([]byte, 32)
	if _, err = rand.Read(token); err != nil {
		return held(job.JobID, "unclaimed", err)
	}
	d := store.GradingDispatch{JobID: job.JobID, SubmissionID: job.Target.SubmissionID, TargetSHA256: store.GradingDigest([]byte(j.TargetRef)), ContextSHA256: store.GradingDigest(body), ClaimSHA256: store.GradingDigest(token), RunID: id.New(), Runner: s.Runner.Name(), State: store.DispatchClaimed}
	initial, _ := json.Marshal(struct {
		Job gradingjob.Context `json:"job"`
	}{job})
	start := s.now()
	run := &store.GradingRun{ID: d.RunID, SubmissionID: d.SubmissionID, Runner: d.Runner, Status: "running", StartedAt: &start, Result: initial}
	claimed, err := s.Store.ClaimGradingDispatch(ctx, d, run)
	// An ambiguous acknowledgment never grants permission, even if our claim
	// token can subsequently be read. Only an acknowledged new claim can run.
	if err != nil {
		return s.dispatchReread(job, nil, err)
	}
	if !claimed {
		old, err := s.Store.GetGradingDispatch(ctx, job.JobID)
		if err != nil {
			return held(job.JobID, "unavailable", err)
		}
		return s.dispatchExisting(ctx, job, old)
	}
	timeout := s.TotalTimeout
	if timeout <= 0 {
		timeout = 15 * time.Minute
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	res, gradeErr := s.dispatchExecute(runCtx, job)
	res.Job = &job
	if gradeErr == nil && runCtx.Err() != nil {
		gradeErr = runCtx.Err()
	}
	result, marshalErr := json.Marshal(res)
	if gradeErr == nil && (marshalErr != nil || len(result) > store.MaxDispatchResultBytes) {
		gradeErr = errors.New("grading result cannot be serialized within the publication bound")
	}
	finish := s.now()
	run.FinishedAt = &finish
	var grade *store.Grade
	if gradeErr != nil {
		run.Status = "failed"
		// Failure diagnostics are bounded; full external errors remain outside the
		// tombstone and never become a grade or a dispatch retry signal.
		message := gradeErr.Error()
		if len(message) > 4096 {
			message = message[:4096]
		}
		run.Result, _ = json.Marshal(struct {
			Job    gradingjob.Context `json:"job"`
			Error  string             `json:"error"`
			Policy *PolicyEvidence    `json:"policy,omitempty"`
		}{job, message, res.Policy})
		d.State = store.DispatchHeld
	} else {
		run.Status = "completed"
		run.Result = result
		d.State = store.DispatchPublished
		d.GradeID = id.New()
		grade = &store.Grade{ID: d.GradeID, SubmissionID: d.SubmissionID, Score: res.Score, MaxScore: res.MaxScore, Breakdown: result, RunID: d.RunID, GradedAt: finish}
	}
	d.ResultSHA256 = store.GradingDigest(run.Result)
	persistCtx, persistCancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer persistCancel()
	commitCtx := persistCtx
	if grade != nil {
		commitCtx = runCtx
	}
	if err = s.Store.FinishGradingDispatch(commitCtx, d, run, grade); err != nil {
		return s.dispatchReread(job, &d, err)
	}
	if gradeErr != nil {
		return held(job.JobID, store.DispatchHeld, gradeErr)
	}
	return nil
}
func (s *Service) dispatchExecute(ctx context.Context, job gradingjob.Context) (Result, error) {
	sub, err := s.Store.GetSubmission(ctx, job.Target.SubmissionID)
	if err != nil {
		return Result{}, err
	}
	if !gradingjob.EligibleStatus(sub.Status) || sub.AssignmentID != job.Target.AssignmentID || sub.Repo != job.Target.Repository {
		return Result{}, errors.New("queued grading submission binding is no longer eligible")
	}
	asg, err := s.Store.GetAssignment(ctx, sub.AssignmentID)
	if err != nil {
		return Result{}, err
	}
	pinned := *asg
	p := job.Target.Policy
	pinned.TemplateRef = adapter.TemplateRef{Host: p.Repository.Host, Namespace: p.Repository.Namespace, Name: p.Repository.Name, Ref: p.Revision}
	pinned.GradingSpec = p.Path
	return s.execute(ctx, sub.Repo, &pinned, job.Target.Revision, &job)
}
