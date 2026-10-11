// SPDX-License-Identifier: AGPL-3.0-or-later
package store

import (
	"context"
	"database/sql"
	"time"
)

const handoffCols = `id,submission_id,owner_id,idempotency_key,payload_digest,status,created_at,expires_at,version,document`

func scanHandoff(row interface{ Scan(...any) error }) (*SubmissionHandoff, error) {
	h := &SubmissionHandoff{}
	var document string
	err := row.Scan(&h.ID, &h.SubmissionID, &h.OwnerID, &h.IdempotencyKey, &h.PayloadDigest, &h.Status, &h.CreatedAt, &h.ExpiresAt, &h.Version, &document)
	h.Document = []byte(document)
	return h, assessmentErr(err)
}
func (s AssessmentSQL) CreateSubmissionHandoff(ctx context.Context, h *SubmissionHandoff, limit int) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = s.handoffAuthorize(ctx, tx, h); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE submission_handoff_control SET id=id WHERE id=1`); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, s.handoffExpirySQL(), h.CreatedAt); err != nil {
		return err
	}
	var count int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM submission_handoffs WHERE submission_id=$1 AND status IN ('preview','queued','publishing','verification_needed')`, h.SubmissionID).Scan(&count); err != nil {
		return err
	}
	if count >= limit {
		return ErrConflict
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO submission_handoffs (`+handoffCols+`) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) ON CONFLICT DO NOTHING`, h.ID, h.SubmissionID, h.OwnerID, h.IdempotencyKey, h.PayloadDigest, h.Status, h.CreatedAt, h.ExpiresAt, h.Version, string(h.Document))
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
	return tx.Commit()
}
func (s AssessmentSQL) GetSubmissionHandoff(ctx context.Context, id string) (*SubmissionHandoff, error) {
	return scanHandoff(s.DB.QueryRowContext(ctx, `SELECT `+handoffCols+` FROM submission_handoffs WHERE id=$1`, id))
}
func (s AssessmentSQL) ListSubmissionHandoffs(ctx context.Context, id string) ([]*SubmissionHandoff, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+handoffCols+` FROM submission_handoffs WHERE submission_id=$1 ORDER BY created_at DESC,id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*SubmissionHandoff{}
	for rows.Next() {
		h, err := scanHandoff(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}
func (s AssessmentSQL) TransitionSubmissionHandoff(ctx context.Context, h *SubmissionHandoff, version int, j *ProvisioningJob) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if h.Status == "queued" {
		if err = s.handoffAuthorize(ctx, tx, h); err != nil {
			return err
		}
		var status string
		var expiry int64
		if err = tx.QueryRowContext(ctx, `SELECT status,expires_at FROM submission_handoffs WHERE id=$1`, h.ID).Scan(&status, &expiry); err != nil {
			return assessmentErr(err)
		}
		if status != "preview" || expiry <= time.Now().Unix() {
			return ErrConflict
		}
	}
	result, err := tx.ExecContext(ctx, `UPDATE submission_handoffs SET status=$1,document=$2,version=version+1 WHERE id=$3 AND version=$4 AND submission_id=$5 AND owner_id=$6`, h.Status, string(h.Document), h.ID, version, h.SubmissionID, h.OwnerID)
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
	if h.Status == "committed" {
		base, candidate, observed, err := HandoffCommit(h)
		if err != nil {
			return err
		}
		var activity any = time.Now().UTC()
		if !s.Postgres {
			activity = time.Now().UTC().Format(time.RFC3339Nano)
		}
		if _, err = tx.ExecContext(ctx, `UPDATE submissions SET latest_commit=$1,last_activity_at=$2 WHERE id=$3 AND (latest_commit='' OR latest_commit IS NULL OR latest_commit=$4 OR latest_commit=$1 OR latest_commit=$5)`, candidate, activity, h.SubmissionID, base, observed); err != nil {
			return err
		}
	}
	if j != nil {
		var scheduled any = j.ScheduledAt
		if !s.Postgres {
			scheduled = j.ScheduledAt.UTC().Format(time.RFC3339Nano)
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO provisioning_jobs (id,type,target_ref,status,attempts,idempotency_key,last_error,scheduled_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT(idempotency_key) DO NOTHING`, j.ID, j.Type, j.TargetRef, string(j.Status), j.Attempts, j.IdempotencyKey, j.LastError, scheduled)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s AssessmentSQL) ExpireSubmissionHandoffs(ctx context.Context, now int64) error {
	_, err := s.DB.ExecContext(ctx, s.handoffExpirySQL(), now)
	return err
}

func (s AssessmentSQL) handoffExpirySQL() string {
	document := `json_remove(document,'$.request.files')`
	if s.Postgres {
		document = `(document::jsonb #- '{request,files}')::text`
	}
	return `UPDATE submission_handoffs SET status='expired',document=` + document + `,version=version+1 WHERE status='preview' AND expires_at<=$1`
}

func (s AssessmentSQL) handoffAuthorize(ctx context.Context, tx *sql.Tx, h *SubmissionHandoff) error {
	query := `SELECT s.status,e.status,e.host_user_id,a.deadline FROM submissions s JOIN roster_entries e ON e.id=s.roster_entry_id JOIN assignments a ON a.id=s.assignment_id WHERE s.id=$1`
	if s.Postgres {
		query += ` FOR UPDATE OF s,e,a`
	}
	var subStatus, rosterStatus, owner string
	var deadline any
	if err := tx.QueryRowContext(ctx, query, h.SubmissionID).Scan(&subStatus, &rosterStatus, &owner, &deadline); err != nil {
		return assessmentErr(err)
	}
	if subStatus != "active" || rosterStatus != "active" || owner == "" || owner != h.OwnerID {
		return ErrConflict
	}
	if deadline != nil {
		value, err := assessmentSQLActivity(deadline)
		if err != nil {
			return err
		}
		at, err := time.Parse(time.RFC3339Nano, value)
		if err != nil {
			return err
		}
		if !time.Now().Before(at) {
			return ErrConflict
		}
	}
	return nil
}
