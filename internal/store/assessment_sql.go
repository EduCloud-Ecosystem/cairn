// SPDX-License-Identifier: AGPL-3.0-or-later
package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// AssessmentSQL shares portable statements across SQLite and PostgreSQL.
// PostgreSQL locks all freshness rows; SQLite's single connection serializes
// the transaction. Neither API handlers nor model output can bypass this gate.
type AssessmentSQL struct {
	DB       *sql.DB
	Postgres bool
}

func (s AssessmentSQL) PutAssessmentRubric(ctx context.Context, r *AssessmentRubric) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO assessment_rubrics (assignment_id,digest,document) VALUES ($1,$2,$3) ON CONFLICT (assignment_id) DO UPDATE SET digest=excluded.digest, document=excluded.document`, r.AssignmentID, r.Digest, string(r.Document))
	return err
}
func (s AssessmentSQL) GetAssessmentRubric(ctx context.Context, id string) (*AssessmentRubric, error) {
	r := &AssessmentRubric{}
	var body string
	err := s.DB.QueryRowContext(ctx, `SELECT assignment_id,digest,document FROM assessment_rubrics WHERE assignment_id=$1`, id).Scan(&r.AssignmentID, &r.Digest, &body)
	r.Document = []byte(body)
	return r, assessmentErr(err)
}
func assessmentErr(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}
func (s AssessmentSQL) CreateAssessment(ctx context.Context, r *AssessmentRecord) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO assessments (id,submission_id,revision,rubric_digest,submission_activity,status,document) VALUES ($1,$2,$3,$4,$5,$6,$7)`, r.ID, r.SubmissionID, r.Revision, r.RubricDigest, r.SubmissionActivity, r.Status, string(r.Document))
	return err
}
func (s AssessmentSQL) GetAssessment(ctx context.Context, id string) (*AssessmentRecord, error) {
	r := &AssessmentRecord{}
	var body string
	err := s.DB.QueryRowContext(ctx, `SELECT id,submission_id,revision,rubric_digest,submission_activity,status,document,COALESCE(grade_id,'') FROM assessments WHERE id=$1`, id).Scan(&r.ID, &r.SubmissionID, &r.Revision, &r.RubricDigest, &r.SubmissionActivity, &r.Status, &body, &r.GradeID)
	r.Document = []byte(body)
	return r, assessmentErr(err)
}
func (s AssessmentSQL) ListAssessments(ctx context.Context, sub string) ([]*AssessmentRecord, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id,submission_id,revision,rubric_digest,submission_activity,status,document,COALESCE(grade_id,'') FROM assessments WHERE submission_id=$1 ORDER BY id`, sub)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*AssessmentRecord{}
	for rows.Next() {
		r := &AssessmentRecord{}
		var body string
		if err := rows.Scan(&r.ID, &r.SubmissionID, &r.Revision, &r.RubricDigest, &r.SubmissionActivity, &r.Status, &body, &r.GradeID); err != nil {
			return nil, err
		}
		r.Document = []byte(body)
		out = append(out, r)
	}
	return out, rows.Err()
}
func (s AssessmentSQL) TransitionAssessment(ctx context.Context, r *AssessmentRecord, from string, g *Grade) error {
	if !ValidAssessmentTransition(from, r.Status, g, r.SubmissionID) {
		return ErrConflict
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	q := `SELECT p.status,p.submission_id,p.revision,p.rubric_digest,s.latest_commit,s.status,r.digest,e.status,p.submission_activity,s.last_activity_at FROM assessments p JOIN submissions s ON s.id=p.submission_id JOIN assessment_rubrics r ON r.assignment_id=s.assignment_id JOIN roster_entries e ON e.id=s.roster_entry_id WHERE p.id=$1`
	if s.Postgres {
		q += ` FOR UPDATE OF p,s,r,e`
	}
	var status, sub, revision, digest, currentRevision, subStatus, currentDigest, rosterStatus string
	var activity string
	var currentActivity any
	err = tx.QueryRowContext(ctx, q, r.ID).Scan(&status, &sub, &revision, &digest, &currentRevision, &subStatus, &currentDigest, &rosterStatus, &activity, &currentActivity)
	if err != nil {
		return assessmentErr(err)
	}
	var currentActivityKey string
	switch t := currentActivity.(type) {
	case time.Time:
		currentActivityKey = AssessmentActivity(&t)
	case string:
		parsed, e := time.Parse(time.RFC3339Nano, t)
		if e != nil {
			return e
		}
		currentActivityKey = AssessmentActivity(&parsed)
	}
	if activity != r.SubmissionActivity {
		return ErrConflict
	}
	if status != from || sub != r.SubmissionID || revision != r.Revision || digest != r.RubricDigest {
		return ErrConflict
	}
	if r.Status != "rejected" && (activity != currentActivityKey || revision != currentRevision || digest != currentDigest || subStatus != "active" || rosterStatus != string(RosterActive)) {
		return ErrConflict
	}
	if g != nil {
		var at any = g.GradedAt.UTC().Format(time.RFC3339Nano)
		var breakdown any = []byte(g.Breakdown)
		if s.Postgres {
			at = g.GradedAt
			breakdown = string(g.Breakdown)
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO grades (id,submission_id,score,max_score,breakdown,run_id,graded_at) VALUES ($1,$2,$3,$4,$5,$6,$7)`, g.ID, g.SubmissionID, g.Score, g.MaxScore, breakdown, g.RunID, at); err != nil {
			return err
		}
	}
	var gradeID any
	if g != nil {
		gradeID = g.ID
	}
	if _, err = tx.ExecContext(ctx, `UPDATE assessments SET status=$1,document=$2,grade_id=$3 WHERE id=$4`, r.Status, string(r.Document), gradeID, r.ID); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	if g != nil {
		r.GradeID = g.ID
	}
	return nil
}
