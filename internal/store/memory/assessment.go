// SPDX-License-Identifier: AGPL-3.0-or-later
package memory

import (
	"context"
	"github.com/EduCloud-Ecosystem/cairn/internal/store"
)

func (m *Store) PutAssessmentRubric(_ context.Context, r *store.AssessmentRubric) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.assignments[r.AssignmentID]; !ok {
		return store.ErrNotFound
	}
	v := *r
	v.Document = append([]byte(nil), r.Document...)
	m.assessmentRubrics[r.AssignmentID] = v
	return nil
}
func (m *Store) GetAssessmentRubric(_ context.Context, id string) (*store.AssessmentRubric, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	v, ok := m.assessmentRubrics[id]
	if !ok {
		return nil, store.ErrNotFound
	}
	v.Document = append([]byte(nil), v.Document...)
	return &v, nil
}
func cloneAssessment(v store.AssessmentRecord) *store.AssessmentRecord {
	v.Document = append([]byte(nil), v.Document...)
	return &v
}
func (m *Store) CreateAssessment(_ context.Context, r *store.AssessmentRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.submissions[r.SubmissionID]; !ok {
		return store.ErrNotFound
	}
	if _, ok := m.assessments[r.ID]; ok {
		return store.ErrConflict
	}
	m.assessments[r.ID] = *cloneAssessment(*r)
	return nil
}
func (m *Store) GetAssessment(_ context.Context, id string) (*store.AssessmentRecord, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	v, ok := m.assessments[id]
	if !ok {
		return nil, store.ErrNotFound
	}
	return cloneAssessment(v), nil
}
func (m *Store) ListAssessments(_ context.Context, sub string) ([]*store.AssessmentRecord, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := []*store.AssessmentRecord{}
	for _, v := range m.assessments {
		if v.SubmissionID == sub {
			out = append(out, cloneAssessment(v))
		}
	}
	return out, nil
}
func (m *Store) TransitionAssessment(_ context.Context, r *store.AssessmentRecord, from string, g *store.Grade) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.assessments[r.ID]
	if !ok {
		return store.ErrNotFound
	}
	if !store.ValidAssessmentTransition(from, r.Status, g, r.SubmissionID) || v.Status != from || v.SubmissionID != r.SubmissionID || v.Revision != r.Revision || v.RubricDigest != r.RubricDigest || v.SubmissionActivity != r.SubmissionActivity {
		return store.ErrConflict
	}
	sub := m.submissions[v.SubmissionID]
	rubric := m.assessmentRubrics[sub.AssignmentID]
	if r.Status != "rejected" && (store.AssessmentActivity(sub.LastActivityAt) != v.SubmissionActivity || sub.LatestCommit != v.Revision || rubric.Digest != v.RubricDigest || sub.Status != "active" || m.roster[sub.RosterEntryID].Status != store.RosterActive) {
		return store.ErrConflict
	}
	if g != nil {
		if _, ok := m.grades[g.ID]; ok {
			return store.ErrConflict
		}
		grade := *g
		grade.Breakdown = append([]byte(nil), g.Breakdown...)
		m.grades[g.ID] = grade
		r.GradeID = g.ID
	}
	m.assessments[r.ID] = *cloneAssessment(*r)
	return nil
}
