// SPDX-License-Identifier: AGPL-3.0-or-later
package memory

import (
	"context"
	"sort"

	"github.com/EduCloud-Ecosystem/cairn/internal/store"
)

func (m *Store) ReadGradingInspection(ctx context.Context, q store.GradingInspectionQuery) ([]store.GradingInspectionRecord, error) {
	if err := q.Validate(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ids := make([]string, 0)
	for id, j := range m.jobs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		_, has := m.gradingDispatches[id]
		if j.Type != "grade" && j.Type != "grade_revision" && !has {
			continue
		}
		if (q.JobID != "" && id == q.JobID) || (q.JobID == "" && id > q.After) {
			at := sort.SearchStrings(ids, id)
			if at < q.Limit {
				ids = append(ids, "")
				copy(ids[at+1:], ids[at:])
				ids[at] = id
				if len(ids) > q.Limit {
					ids = ids[:q.Limit]
				}
			}
		}
	}
	out := make([]store.GradingInspectionRecord, 0, len(ids))
	for _, id := range ids {
		j := m.jobs[id]
		r := store.GradingInspectionRecord{Job: store.ProvisioningJob{ID: j.ID, Type: j.Type, TargetRef: j.TargetRef, Status: j.Status}}
		r.Dispatch, r.HasDispatch = m.gradingDispatches[id]
		if r.HasDispatch {
			run, ok := m.gradingRuns[r.Dispatch.RunID]
			r.RunPresent = ok
			r.RunSubmissionID = run.SubmissionID
			r.RunRunner = run.Runner
			r.RunStatus = run.Status
			g, ok := m.grades[r.Dispatch.GradeID]
			r.GradePresent = ok
			r.GradeSubmissionID = g.SubmissionID
			r.GradeRunID = g.RunID
		}
		out = append(out, r)
	}
	return out, nil
}
