// SPDX-License-Identifier: AGPL-3.0-or-later
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

type ReconciliationSQL struct {
	DB       *sql.DB
	Postgres bool
}

func (s ReconciliationSQL) BindLegacyIdentity(ctx context.Context, a IdentityReconciliation) (bool, error) {
	if err := ValidateIdentityReconciliation(a); err != nil {
		return false, err
	}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var previous string
	err = tx.QueryRowContext(ctx, `SELECT document FROM reconciliation_audit WHERE plan_id=$1`, a.PlanID).Scan(&previous)
	if err == nil {
		var old IdentityReconciliation
		if json.Unmarshal([]byte(previous), &old) != nil || old.RosterID != a.RosterID || old.Snapshot != a.Snapshot || old.ProviderID != a.ProviderID || old.ProviderAuthority != a.ProviderAuthority || old.Operator != a.Operator || old.EvidenceSHA256 != a.EvidenceSHA256 {
			return false, ErrConflict
		}
		return true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	q := `SELECT id,classroom_id,host,host_username,host_user_id,COALESCE(email_hash,''),status,claimed_at FROM roster_entries WHERE id=$1`
	if s.Postgres {
		q += ` FOR UPDATE`
	}
	var r RosterEntry
	// Both drivers accept a nullable time via their native scan representation.
	var claimed any
	if err = tx.QueryRowContext(ctx, q, a.RosterID).Scan(&r.ID, &r.ClassroomID, &r.Host, &r.HostUsername, &r.HostUserID, &r.EmailHash, &r.Status, &claimed); err != nil {
		return false, assessmentErr(err)
	}
	switch v := claimed.(type) {
	case time.Time:
		r.ClaimedAt = &v
	case string:
		t, e := time.Parse(time.RFC3339Nano, v)
		if e != nil {
			return false, e
		}
		r.ClaimedAt = &t
	case []byte:
		t, e := time.Parse(time.RFC3339Nano, string(v))
		if e != nil {
			return false, e
		}
		r.ClaimedAt = &t
	}
	if r.HostUserID != "" || r.Status == RosterRemoved || RosterSnapshot(&r) != a.Snapshot {
		return false, ErrConflict
	}
	var n int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM roster_entries WHERE classroom_id=$1 AND host=$2 AND host_user_id=$3 AND id<>$4`, r.ClassroomID, string(r.Host), a.ProviderID, r.ID).Scan(&n); err != nil {
		return false, err
	}
	if n != 0 {
		return false, ErrConflict
	}
	res, err := tx.ExecContext(ctx, `UPDATE roster_entries SET host_user_id=$1 WHERE id=$2 AND host_user_id=''`, a.ProviderID, r.ID)
	if err != nil {
		return false, err
	}
	n64, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	if n64 != 1 {
		return false, ErrConflict
	}
	b, err := json.Marshal(a)
	if err != nil {
		return false, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO reconciliation_audit(plan_id,roster_id,document) VALUES ($1,$2,$3)`, a.PlanID, a.RosterID, string(b)); err != nil {
		return false, err
	}
	return false, tx.Commit()
}
func (s ReconciliationSQL) ListIdentityReconciliations(ctx context.Context) ([]IdentityReconciliation, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT document FROM reconciliation_audit ORDER BY plan_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []IdentityReconciliation{}
	for rows.Next() {
		var b string
		var a IdentityReconciliation
		if err = rows.Scan(&b); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(b), &a); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
