// SPDX-License-Identifier: AGPL-3.0-or-later
package store

import (
	"context"
	"fmt"
	"strings"
)

// ReadGradingInspection reads one statement snapshot on the existing handle.
// Primary-key joins cannot multiply jobs. No result/breakdown/error/log body is
// selected. Substrings bound even corrupted legacy metadata before allocation.
func (s GradingDispatchSQL) ReadGradingInspection(ctx context.Context, q GradingInspectionQuery) ([]GradingInspectionRecord, error) {
	if err := q.Validate(); err != nil {
		return nil, err
	}
	collate := "BINARY"
	if s.Postgres {
		collate = `"C"`
	}
	// SQLite text substr treats NUL as a terminator. Slice BLOB bytes and cast
	// back so malformed suffixes cannot normalize to an apparently valid value.
	bounded := func(column string, limit int) string {
		if s.Postgres {
			return fmt.Sprintf("COALESCE(substr(%s,1,%d),'')", column, limit)
		}
		return fmt.Sprintf("COALESCE(CAST(substr(CAST(%s AS BLOB),1,%d) AS TEXT),'')", column, limit)
	}
	columns := []string{
		bounded("j.id", 257), bounded("j.type", 33), bounded("j.target_ref", 8193), bounded("j.status", 33),
		"d.job_id IS NOT NULL", bounded("d.submission_id", 257), bounded("d.target_sha256", 65), bounded("d.context_sha256", 65), bounded("d.claim_sha256", 65),
		bounded("d.run_id", 257), bounded("d.runner", 257), bounded("d.state", 33), bounded("d.grade_id", 257), bounded("d.result_sha256", 65),
		"r.id IS NOT NULL", bounded("r.submission_id", 257), bounded("r.runner", 257), bounded("r.status", 33),
		"g.id IS NOT NULL", bounded("g.submission_id", 257), bounded("g.run_id", 257),
	}
	rows, err := s.DB.QueryContext(ctx, "SELECT "+strings.Join(columns, ",")+` FROM provisioning_jobs j
 LEFT JOIN grading_dispatches d ON d.job_id=j.id
 LEFT JOIN grading_runs r ON r.id=d.run_id
 LEFT JOIN grades g ON g.id=d.grade_id
 WHERE (j.type IN ('grade','grade_revision') OR d.job_id IS NOT NULL)
 AND (($1<>'' AND j.id=$1) OR ($1='' AND j.id COLLATE `+collate+` > $2))
 ORDER BY j.id COLLATE `+collate+` LIMIT $3`, q.JobID, q.After, q.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]GradingInspectionRecord, 0)
	for rows.Next() {
		var r GradingInspectionRecord
		d := &r.Dispatch
		if err := rows.Scan(&r.Job.ID, &r.Job.Type, &r.Job.TargetRef, &r.Job.Status, &r.HasDispatch,
			&d.SubmissionID, &d.TargetSHA256, &d.ContextSHA256, &d.ClaimSHA256, &d.RunID, &d.Runner, &d.State, &d.GradeID, &d.ResultSHA256,
			&r.RunPresent, &r.RunSubmissionID, &r.RunRunner, &r.RunStatus, &r.GradePresent, &r.GradeSubmissionID, &r.GradeRunID); err != nil {
			return nil, err
		}
		d.JobID = r.Job.ID
		out = append(out, r)
	}
	return out, rows.Err()
}
