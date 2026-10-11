// SPDX-License-Identifier: AGPL-3.0-or-later
package store

import "context"

func (s AssessmentSQL) ltiRecord(ctx context.Context, column, key string) (*LTIRecord, error) {
	r := &LTIRecord{}
	var body string
	err := s.DB.QueryRowContext(ctx, `SELECT id,kind,unique_key,assignment_id,COALESCE(roster_entry_id,''),COALESCE(grade_id,''),revision,document FROM lti_records WHERE `+column+`=$1`, key).Scan(&r.ID, &r.Kind, &r.UniqueKey, &r.AssignmentID, &r.RosterEntryID, &r.GradeID, &r.Revision, &body)
	r.Document = []byte(body)
	return r, assessmentErr(err)
}
func (s AssessmentSQL) GetLTIRecord(ctx context.Context, id string) (*LTIRecord, error) {
	return s.ltiRecord(ctx, "id", id)
}
func (s AssessmentSQL) FindLTIRecord(ctx context.Context, key string) (*LTIRecord, error) {
	return s.ltiRecord(ctx, "unique_key", key)
}
func (s AssessmentSQL) SaveLTIRecord(ctx context.Context, r *LTIRecord, expected int) error {
	if r.Revision != expected+1 {
		return ErrConflict
	}
	query := `UPDATE lti_records SET document=$1,grade_id=NULLIF($2,''),revision=$3 WHERE id=$4 AND kind=$5 AND unique_key=$6 AND assignment_id=$7 AND COALESCE(roster_entry_id,'')=$8 AND revision=$9`
	args := []any{string(r.Document), r.GradeID, r.Revision, r.ID, r.Kind, r.UniqueKey, r.AssignmentID, r.RosterEntryID, expected}
	if expected == 0 {
		query = `INSERT INTO lti_records(document,grade_id,revision,id,kind,unique_key,assignment_id,roster_entry_id) VALUES($1,NULLIF($2,''),$3,$4,$5,$6,$7,NULLIF($8,'')) ON CONFLICT DO NOTHING`
		args = args[:8]
	}
	res, err := s.DB.ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err == nil && n != 1 {
		return ErrConflict
	}
	return err
}
