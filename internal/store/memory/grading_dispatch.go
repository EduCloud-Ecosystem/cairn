// SPDX-License-Identifier: AGPL-3.0-or-later
package memory

import (
	"context"
	"github.com/EduCloud-Ecosystem/cairn/internal/store"
)

func (m *Store) GetGradingJob(ctx context.Context, id string) (*store.ProvisioningJob, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	j, ok := m.jobs[id]
	if !ok {
		return nil, store.ErrNotFound
	}
	return &j, nil
}
func (m *Store) GetGradingDispatch(ctx context.Context, id string) (*store.GradingDispatch, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	d, ok := m.gradingDispatches[id]
	if !ok {
		return nil, store.ErrNotFound
	}
	return &d, nil
}
func cloneDispatchRun(r *store.GradingRun) store.GradingRun {
	c := *r
	c.Result = append([]byte(nil), r.Result...)
	if r.StartedAt != nil {
		t := *r.StartedAt
		c.StartedAt = &t
	}
	if r.FinishedAt != nil {
		t := *r.FinishedAt
		c.FinishedAt = &t
	}
	return c
}
func (m *Store) ClaimGradingDispatch(ctx context.Context, d store.GradingDispatch, r *store.GradingRun) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := store.ValidateDispatchClaim(d, r); err != nil {
		return false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if old, ok := m.gradingDispatches[d.JobID]; ok {
		if old.ContextSHA256 != d.ContextSHA256 || old.TargetSHA256 != d.TargetSHA256 || old.SubmissionID != d.SubmissionID || old.Runner != d.Runner {
			return false, store.ErrConflict
		}
		return false, nil
	}
	j, ok := m.jobs[d.JobID]
	if !ok || !store.DispatchMatchesQueue(d, &j) {
		return false, store.ErrConflict
	}
	if _, ok = m.submissions[d.SubmissionID]; !ok {
		return false, store.ErrNotFound
	}
	if _, ok = m.gradingRuns[r.ID]; ok {
		return false, store.ErrConflict
	}
	for _, old := range m.gradingDispatches {
		if old.RunID == d.RunID {
			return false, store.ErrConflict
		}
	}
	m.gradingDispatches[d.JobID] = d
	m.gradingRuns[r.ID] = cloneDispatchRun(r)
	return true, nil
}
func (m *Store) FinishGradingDispatch(ctx context.Context, d store.GradingDispatch, r *store.GradingRun, g *store.Grade) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := store.ValidateDispatchFinish(d, r, g); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	old, ok := m.gradingDispatches[d.JobID]
	if !ok {
		return store.ErrNotFound
	}
	if !store.SameDispatchClaim(old, d) {
		return store.ErrConflict
	}
	if old.State != store.DispatchClaimed {
		if old == d {
			return nil
		}
		return store.ErrConflict
	}
	j, ok := m.jobs[d.JobID]
	if !ok || !store.DispatchMatchesQueue(d, &j) {
		return store.ErrConflict
	}
	prior, ok := m.gradingRuns[r.ID]
	if !ok || prior.Status != "running" || prior.SubmissionID != r.SubmissionID || prior.Runner != r.Runner {
		return store.ErrConflict
	}
	if g != nil {
		if _, ok = m.grades[g.ID]; ok {
			return store.ErrConflict
		}
		if _, ok = m.submissions[g.SubmissionID]; !ok {
			return store.ErrNotFound
		}
	}
	m.gradingRuns[r.ID] = cloneDispatchRun(r)
	m.gradingDispatches[d.JobID] = d
	j.Status = store.JobFailed
	j.LastError = "grading dispatch held; reconciliation required"
	j.Attempts++
	if g != nil {
		copy := *g
		copy.Breakdown = append([]byte(nil), g.Breakdown...)
		m.grades[g.ID] = copy
		j.Status = store.JobSucceeded
		j.LastError = ""
	}
	m.jobs[j.ID] = j
	return nil
}
