// SPDX-License-Identifier: AGPL-3.0-or-later
package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// GradingDispatchSQL uses the queue row as the transaction serialization point.
// Lock it with a write before reads (also safe across independent SQLite pools).
type GradingDispatchSQL struct {
	DB       *sql.DB
	Postgres bool
}

const dispatchCols = `job_id,submission_id,target_sha256,context_sha256,claim_sha256,run_id,runner,state,grade_id,result_sha256`

func scanDispatch(row interface{ Scan(...any) error }) (*GradingDispatch, error) {
	d := &GradingDispatch{}
	err := row.Scan(&d.JobID, &d.SubmissionID, &d.TargetSHA256, &d.ContextSHA256, &d.ClaimSHA256, &d.RunID, &d.Runner, &d.State, &d.GradeID, &d.ResultSHA256)
	return d, assessmentErr(err)
}
func (s GradingDispatchSQL) GetGradingJob(ctx context.Context, id string) (*ProvisioningJob, error) {
	j := &ProvisioningJob{}
	err := s.DB.QueryRowContext(ctx, `SELECT id,type,target_ref,status FROM provisioning_jobs WHERE id=$1`, id).Scan(&j.ID, &j.Type, &j.TargetRef, &j.Status)
	return j, assessmentErr(err)
}
func (s GradingDispatchSQL) GetGradingDispatch(ctx context.Context, id string) (*GradingDispatch, error) {
	return scanDispatch(s.DB.QueryRowContext(ctx, `SELECT `+dispatchCols+` FROM grading_dispatches WHERE job_id=$1`, id))
}
func lockGradingJob(ctx context.Context, tx *sql.Tx, id string) (*ProvisioningJob, error) {
	if _, err := tx.ExecContext(ctx, `UPDATE provisioning_jobs SET id=id WHERE id=$1`, id); err != nil {
		return nil, err
	}
	j := &ProvisioningJob{}
	err := tx.QueryRowContext(ctx, `SELECT id,type,target_ref,status FROM provisioning_jobs WHERE id=$1`, id).Scan(&j.ID, &j.Type, &j.TargetRef, &j.Status)
	return j, assessmentErr(err)
}
func (s GradingDispatchSQL) timestamp(t *time.Time) any {
	if t == nil {
		return nil
	}
	if s.Postgres {
		return *t
	}
	return t.UTC().Format(time.RFC3339Nano)
}
func (s GradingDispatchSQL) body(b []byte) any {
	if s.Postgres {
		return string(b)
	}
	return b
}
func (s GradingDispatchSQL) ClaimGradingDispatch(ctx context.Context, d GradingDispatch, r *GradingRun) (bool, error) {
	if err := ValidateDispatchClaim(d, r); err != nil {
		return false, err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	j, err := lockGradingJob(ctx, tx, d.JobID)
	if err != nil {
		return false, err
	}
	old, err := scanDispatch(tx.QueryRowContext(ctx, `SELECT `+dispatchCols+` FROM grading_dispatches WHERE job_id=$1`, d.JobID))
	if err == nil {
		if old.ContextSHA256 != d.ContextSHA256 || old.TargetSHA256 != d.TargetSHA256 || old.SubmissionID != d.SubmissionID || old.Runner != d.Runner {
			return false, ErrConflict
		}
		return false, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return false, err
	}
	if !DispatchMatchesQueue(d, j) {
		return false, ErrConflict
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO grading_dispatches (`+dispatchCols+`) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, d.JobID, d.SubmissionID, d.TargetSHA256, d.ContextSHA256, d.ClaimSHA256, d.RunID, d.Runner, d.State, d.GradeID, d.ResultSHA256); err != nil {
		return false, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO grading_runs(id,submission_id,status,runner,started_at,finished_at,result,logs_ref) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, r.ID, r.SubmissionID, r.Status, r.Runner, s.timestamp(r.StartedAt), nil, s.body(r.Result), r.LogsRef); err != nil {
		return false, err
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}
func (s GradingDispatchSQL) FinishGradingDispatch(ctx context.Context, d GradingDispatch, r *GradingRun, g *Grade) error {
	if err := ValidateDispatchFinish(d, r, g); err != nil {
		return err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	j, err := lockGradingJob(ctx, tx, d.JobID)
	if err != nil {
		return err
	}
	old, err := scanDispatch(tx.QueryRowContext(ctx, `SELECT `+dispatchCols+` FROM grading_dispatches WHERE job_id=$1`, d.JobID))
	if err != nil {
		return err
	}
	if !SameDispatchClaim(*old, d) {
		return ErrConflict
	}
	if old.State != DispatchClaimed {
		if *old == d {
			return nil
		}
		return ErrConflict
	}
	if !DispatchMatchesQueue(d, j) {
		return ErrConflict
	}
	result, err := tx.ExecContext(ctx, `UPDATE grading_runs SET status=$1,finished_at=$2,result=$3 WHERE id=$4 AND submission_id=$5 AND runner=$6 AND status='running'`, r.Status, s.timestamp(r.FinishedAt), s.body(r.Result), r.ID, r.SubmissionID, r.Runner)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrConflict
	}
	if g != nil {
		if _, err = tx.ExecContext(ctx, `INSERT INTO grades(id,submission_id,score,max_score,breakdown,run_id,graded_at) VALUES($1,$2,$3,$4,$5,$6,$7)`, g.ID, g.SubmissionID, g.Score, g.MaxScore, s.body(g.Breakdown), g.RunID, s.timestamp(&g.GradedAt)); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE grading_dispatches SET state=$1,grade_id=$2,result_sha256=$3 WHERE job_id=$4`, d.State, d.GradeID, d.ResultSHA256, d.JobID); err != nil {
		return err
	}
	status, last := JobFailed, "grading dispatch held; reconciliation required"
	if d.State == DispatchPublished {
		status = JobSucceeded
		last = ""
	}
	if _, err = tx.ExecContext(ctx, `UPDATE provisioning_jobs SET status=$1,attempts=attempts+1,last_error=$2 WHERE id=$3`, string(status), last, d.JobID); err != nil {
		return err
	}
	return tx.Commit()
}

// UpdateUnmanagedJob must read the barrier in a fresh statement AFTER taking
// the queue row lock. PostgreSQL READ COMMITTED's UPDATE snapshot must not be
// used to test a different row that a concurrent claim may just have committed.
func (s GradingDispatchSQL) UpdateUnmanagedJob(ctx context.Context, j *ProvisioningJob) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = lockGradingJob(ctx, tx, j.ID); err != nil {
		return err
	}
	var count int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM grading_dispatches WHERE job_id=$1`, j.ID).Scan(&count); err != nil {
		return err
	}
	if count != 0 {
		return ErrConflict
	}
	if _, err = tx.ExecContext(ctx, `UPDATE provisioning_jobs SET status=$1,attempts=$2,last_error=$3,scheduled_at=$4 WHERE id=$5`, string(j.Status), j.Attempts, j.LastError, s.timestamp(&j.ScheduledAt), j.ID); err != nil {
		return err
	}
	return tx.Commit()
}
