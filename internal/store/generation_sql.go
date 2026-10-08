// SPDX-License-Identifier: AGPL-3.0-or-later
package store

import (
	"context"
)

func (s AssessmentSQL) ReserveGeneration(ctx context.Context, r *AssessmentRecord, g *Generation, limits GenerationLimits) error {
	if g.AssessmentID != r.ID || g.ReservedUnits <= 0 || limits.DailyRequests <= 0 || limits.LearnerDailyRequests <= 0 || limits.DailyUnits <= 0 {
		return ErrGenerationLimit
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// UPDATE takes the shared control lock on both SQL backends, including across
	// multiple SQLite connections/processes; all quota decisions follow that lock.
	if _, err = tx.ExecContext(ctx, `UPDATE assessment_provider_control SET paused=paused WHERE id=1`); err != nil {
		return err
	}
	var paused int
	if err = tx.QueryRowContext(ctx, `SELECT paused FROM assessment_provider_control WHERE id=1`).Scan(&paused); err != nil {
		return err
	}
	if paused != 0 {
		return ErrGenerationLimit
	}
	var status, revision, digest, subStatus, rosterStatus, learner string
	var activity any
	query := `SELECT p.status,s.latest_commit,b.digest,s.status,e.status,e.id,s.last_activity_at FROM assessments p JOIN submissions s ON s.id=p.submission_id JOIN assessment_rubrics b ON b.assignment_id=s.assignment_id JOIN roster_entries e ON e.id=s.roster_entry_id WHERE p.id=$1`
	if s.Postgres {
		query += ` FOR UPDATE OF p,s,b,e`
	}
	if err = tx.QueryRowContext(ctx, query, r.ID).Scan(&status, &revision, &digest, &subStatus, &rosterStatus, &learner, &activity); err != nil {
		return assessmentErr(err)
	}
	activityKey, err := assessmentSQLActivity(activity)
	if err != nil {
		return err
	}
	if status != "collected" || revision != r.Revision || digest != r.RubricDigest || activityKey != r.SubmissionActivity || subStatus != "active" || rosterStatus != "active" {
		return ErrConflict
	}
	learner = GenerationLearnerKey(g.Day, learner)
	var total, units, perLearner, existing int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(reserved_units),0),COALESCE(SUM(CASE WHEN learner_id=$1 THEN 1 ELSE 0 END),0) FROM assessment_generations WHERE day=$2`, learner, g.Day).Scan(&total, &units, &perLearner); err != nil {
		return err
	}
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM assessment_generations WHERE assessment_id=$1`, r.ID).Scan(&existing); err != nil {
		return err
	}
	if existing > 0 || total >= limits.DailyRequests || perLearner >= limits.LearnerDailyRequests || g.ReservedUnits > limits.DailyUnits-units {
		return ErrGenerationLimit
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO assessment_generations (assessment_id,learner_id,day,reserved_units,status,error_code,input_tokens,output_tokens,response_id,model) VALUES ($1,$2,$3,$4,'running','',0,0,'',$5)`, r.ID, learner, g.Day, g.ReservedUnits, g.Model)
	if err != nil {
		return err
	}
	return tx.Commit()
}
func (s AssessmentSQL) FinishGeneration(ctx context.Context, g *Generation) error {
	res, err := s.DB.ExecContext(ctx, `UPDATE assessment_generations SET status=$1,error_code=$2,input_tokens=$3,output_tokens=$4,response_id=$5 WHERE assessment_id=$6 AND status='running'`, g.Status, g.ErrorCode, g.InputTokens, g.OutputTokens, g.ResponseID, g.AssessmentID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err == nil && n != 1 {
		return ErrConflict
	}
	return err
}
func (s AssessmentSQL) GetGeneration(ctx context.Context, id string) (*Generation, error) {
	g := &Generation{}
	err := s.DB.QueryRowContext(ctx, `SELECT assessment_id,day,reserved_units,status,error_code,input_tokens,output_tokens,response_id,model FROM assessment_generations WHERE assessment_id=$1`, id).Scan(&g.AssessmentID, &g.Day, &g.ReservedUnits, &g.Status, &g.ErrorCode, &g.InputTokens, &g.OutputTokens, &g.ResponseID, &g.Model)
	return g, assessmentErr(err)
}
func (s AssessmentSQL) GenerationPaused(ctx context.Context) (bool, error) {
	var paused int
	err := s.DB.QueryRowContext(ctx, `SELECT paused FROM assessment_provider_control WHERE id=1`).Scan(&paused)
	return paused != 0, err
}
func (s AssessmentSQL) SetGenerationPaused(ctx context.Context, paused bool) error {
	v := 0
	if paused {
		v = 1
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE assessment_provider_control SET paused=$1 WHERE id=1`, v)
	return err
}
