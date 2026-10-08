// SPDX-License-Identifier: AGPL-3.0-or-later
package store

import "context"

func (s AssessmentSQL) CreateCalibration(ctx context.Context, c *Calibration) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO calibrations (id,owner_id,assignment_id,rubric_digest,revision,status,document) VALUES ($1,$2,$3,$4,1,'draft',$5)`, c.ID, c.OwnerID, c.AssignmentID, c.RubricDigest, string(c.Document))
	return err
}
func (s AssessmentSQL) GetCalibration(ctx context.Context, id, owner string) (*Calibration, error) {
	c := &Calibration{}
	var body string
	err := s.DB.QueryRowContext(ctx, `SELECT id,owner_id,assignment_id,rubric_digest,revision,status,document FROM calibrations WHERE id=$1 AND owner_id=$2`, id, owner).Scan(&c.ID, &c.OwnerID, &c.AssignmentID, &c.RubricDigest, &c.Revision, &c.Status, &body)
	c.Document = []byte(body)
	return c, assessmentErr(err)
}
func (s AssessmentSQL) ListCalibrations(ctx context.Context, assignment, owner string) ([]*Calibration, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id,owner_id,assignment_id,rubric_digest,revision,status FROM calibrations WHERE assignment_id=$1 AND owner_id=$2 ORDER BY id`, assignment, owner)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Calibration{}
	for rows.Next() {
		c := &Calibration{}
		if err = rows.Scan(&c.ID, &c.OwnerID, &c.AssignmentID, &c.RubricDigest, &c.Revision, &c.Status); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
func (s AssessmentSQL) UpdateCalibration(ctx context.Context, c *Calibration, previous int) error {
	if c.Revision != previous+1 || (c.Status != "draft" && c.Status != "ready") {
		return ErrConflict
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE calibrations SET revision=$1,status=$2,document=$3 WHERE id=$4 AND owner_id=$5 AND assignment_id=$6 AND rubric_digest=$7 AND revision=$8 AND status='draft'`, c.Revision, c.Status, string(c.Document), c.ID, c.OwnerID, c.AssignmentID, c.RubricDigest, previous)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrConflict
	}
	if c.SourceSubmissionID != "" {
		if _, err = tx.ExecContext(ctx, `INSERT INTO calibration_sources (calibration_id,submission_id) VALUES ($1,$2) ON CONFLICT (calibration_id,submission_id) DO NOTHING`, c.ID, c.SourceSubmissionID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Lock source rows before removing calibration copies, so a concurrent capture
// cannot attach a new historical copy after the erasure scan.
func (s AssessmentSQL) DeleteRosterWithCalibration(ctx context.Context, id string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	query := `SELECT id FROM submissions WHERE roster_entry_id=$1`
	if s.Postgres {
		query += ` FOR UPDATE`
	}
	rows, err := tx.QueryContext(ctx, query, id)
	if err != nil {
		return err
	}
	for rows.Next() {
		var sid string
		if err = rows.Scan(&sid); err != nil {
			rows.Close()
			return err
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM calibrations WHERE id IN (SELECT c.calibration_id FROM calibration_sources c JOIN submissions s ON s.id=c.submission_id WHERE s.roster_entry_id=$1)`, id); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM roster_entries WHERE id=$1`, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrNotFound
	}
	return tx.Commit()
}

func (s AssessmentSQL) DeleteCalibration(ctx context.Context, id, owner string) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM calibrations WHERE id=$1 AND owner_id=$2`, id, owner)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err == nil && n != 1 {
		return ErrNotFound
	}
	return err
}

// Uses the same provider lock and ledger as live assessments. Historical runs
// cannot bypass the shared daily request/unit cap or pause, even after deletion.
func (s AssessmentSQL) ReserveCalibrationGeneration(ctx context.Context, c *Calibration, g *Generation, l GenerationLimits) error {
	if g.ReservedUnits <= 0 || l.DailyRequests <= 0 || l.DailyUnits <= 0 {
		return ErrGenerationLimit
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
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
	query := `SELECT revision,status FROM calibrations WHERE id=$1 AND owner_id=$2`
	if s.Postgres {
		query += ` FOR UPDATE`
	}
	var revision int
	var status string
	if err = tx.QueryRowContext(ctx, query, c.ID, c.OwnerID).Scan(&revision, &status); err != nil {
		return assessmentErr(err)
	}
	if revision != c.Revision || status != "draft" {
		return ErrConflict
	}
	var total, units, existing int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(reserved_units),0) FROM assessment_generations WHERE day=$1`, g.Day).Scan(&total, &units); err != nil {
		return err
	}
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM assessment_generations WHERE assessment_id=$1`, g.AssessmentID).Scan(&existing); err != nil {
		return err
	}
	if existing > 0 || total >= l.DailyRequests || g.ReservedUnits > l.DailyUnits-units {
		return ErrGenerationLimit
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO assessment_generations (assessment_id,learner_id,day,reserved_units,status,error_code,input_tokens,output_tokens,response_id,model) VALUES ($1,$2,$3,$4,'running','',0,0,'',$5)`, g.AssessmentID, GenerationLearnerKey(g.Day, "calibration:"+c.OwnerID), g.Day, g.ReservedUnits, g.Model)
	if err != nil {
		return err
	}
	return tx.Commit()
}
