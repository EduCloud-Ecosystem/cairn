// SPDX-License-Identifier: AGPL-3.0-or-later
package memory

import (
	"context"

	"github.com/EduCloud-Ecosystem/cairn/internal/store"
)

func cloneCalibration(c store.Calibration) *store.Calibration {
	c.Document = append([]byte(nil), c.Document...)
	return &c
}
func (m *Store) CreateCalibration(_ context.Context, c *store.Calibration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.assignments[c.AssignmentID]; !ok {
		return store.ErrNotFound
	}
	if _, ok := m.calibrations[c.ID]; ok {
		return store.ErrConflict
	}
	m.calibrations[c.ID] = *cloneCalibration(*c)
	return nil
}
func (m *Store) GetCalibration(_ context.Context, id, owner string) (*store.Calibration, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	c, ok := m.calibrations[id]
	if !ok || c.OwnerID != owner {
		return nil, store.ErrNotFound
	}
	return cloneCalibration(c), nil
}
func (m *Store) ListCalibrations(_ context.Context, assignment, owner string) ([]*store.Calibration, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := []*store.Calibration{}
	for _, c := range m.calibrations {
		if c.AssignmentID == assignment && c.OwnerID == owner {
			c.Document = nil
			out = append(out, &c)
		}
	}
	return out, nil
}
func (m *Store) UpdateCalibration(_ context.Context, c *store.Calibration, previous int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	old, ok := m.calibrations[c.ID]
	if !ok || old.OwnerID != c.OwnerID || old.AssignmentID != c.AssignmentID || old.RubricDigest != c.RubricDigest || old.Status != "draft" || old.Revision != previous || c.Revision != previous+1 || (c.Status != "draft" && c.Status != "ready") {
		return store.ErrConflict
	}
	if c.SourceSubmissionID != "" {
		if _, ok := m.submissions[c.SourceSubmissionID]; !ok {
			return store.ErrNotFound
		}
		if m.calibrationSources[c.ID] == nil {
			m.calibrationSources[c.ID] = map[string]bool{}
		}
		m.calibrationSources[c.ID][c.SourceSubmissionID] = true
	}
	v := *cloneCalibration(*c)
	v.SourceSubmissionID = ""
	m.calibrations[c.ID] = v
	return nil
}
func (m *Store) DeleteCalibration(_ context.Context, id, owner string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.calibrations[id]
	if !ok || c.OwnerID != owner {
		return store.ErrNotFound
	}
	delete(m.calibrations, id)
	delete(m.calibrationSources, id)
	return nil
}
func (m *Store) ReserveCalibrationGeneration(_ context.Context, c *store.Calibration, g *store.Generation, l store.GenerationLimits) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	old, ok := m.calibrations[c.ID]
	if !ok || old.OwnerID != c.OwnerID {
		return store.ErrNotFound
	}
	if old.Revision != c.Revision || old.Status != "draft" {
		return store.ErrConflict
	}
	if m.generationPaused || g.ReservedUnits <= 0 || l.DailyRequests <= 0 || l.DailyUnits <= 0 {
		return store.ErrGenerationLimit
	}
	if _, ok = m.generations[g.AssessmentID]; ok {
		return store.ErrGenerationLimit
	}
	total, units := 0, 0
	for _, v := range m.generations {
		if v.Day == g.Day {
			total++
			units += v.ReservedUnits
		}
	}
	if total >= l.DailyRequests || g.ReservedUnits > l.DailyUnits-units {
		return store.ErrGenerationLimit
	}
	v := *g
	v.Status = "running"
	m.generations[g.AssessmentID] = v
	m.generationLearners[g.AssessmentID] = store.GenerationLearnerKey(g.Day, "calibration:"+c.OwnerID)
	return nil
}
