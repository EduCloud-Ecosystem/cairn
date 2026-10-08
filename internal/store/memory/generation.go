// SPDX-License-Identifier: AGPL-3.0-or-later
package memory

import (
	"context"
	"github.com/EduCloud-Ecosystem/cairn/internal/store"
)

func (m *Store) ReserveGeneration(_ context.Context, r *store.AssessmentRecord, g *store.Generation, l store.GenerationLimits) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.assessments[r.ID]
	if !ok {
		return store.ErrNotFound
	}
	sub := m.submissions[p.SubmissionID]
	re := m.roster[sub.RosterEntryID]
	if p.Status != "collected" || sub.LatestCommit != r.Revision || m.assessmentRubrics[sub.AssignmentID].Digest != r.RubricDigest || store.AssessmentActivity(sub.LastActivityAt) != r.SubmissionActivity || sub.Status != "active" || re.Status != store.RosterActive {
		return store.ErrConflict
	}
	if m.generationPaused || g.AssessmentID != r.ID || g.ReservedUnits <= 0 {
		return store.ErrGenerationLimit
	}
	if _, ok := m.generations[r.ID]; ok {
		return store.ErrGenerationLimit
	}
	learnerKey := store.GenerationLearnerKey(g.Day, re.ID)
	total, units, learner := 0, 0, 0
	for id, v := range m.generations {
		if v.Day == g.Day {
			total++
			units += v.ReservedUnits
			if m.generationLearners[id] == learnerKey {
				learner++
			}
		}
	}
	if total >= l.DailyRequests || learner >= l.LearnerDailyRequests || g.ReservedUnits > l.DailyUnits-units {
		return store.ErrGenerationLimit
	}
	v := *g
	v.Status = "running"
	m.generations[r.ID] = v
	m.generationLearners[r.ID] = learnerKey
	return nil
}
func (m *Store) FinishGeneration(_ context.Context, g *store.Generation) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.generations[g.AssessmentID]
	if !ok || v.Status != "running" {
		return store.ErrConflict
	}
	v.Status = g.Status
	v.ErrorCode = g.ErrorCode
	v.InputTokens = g.InputTokens
	v.OutputTokens = g.OutputTokens
	v.ResponseID = g.ResponseID
	m.generations[g.AssessmentID] = v
	return nil
}
func (m *Store) GetGeneration(_ context.Context, id string) (*store.Generation, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	v, ok := m.generations[id]
	if !ok {
		return nil, store.ErrNotFound
	}
	return &v, nil
}
func (m *Store) GenerationPaused(_ context.Context) (bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.generationPaused, nil
}
func (m *Store) SetGenerationPaused(_ context.Context, v bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.generationPaused = v
	return nil
}
