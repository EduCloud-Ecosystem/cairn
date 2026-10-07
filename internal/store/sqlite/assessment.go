// SPDX-License-Identifier: AGPL-3.0-or-later
package sqlite

import (
	"context"
	"github.com/EduCloud-Ecosystem/cairn/internal/store"
)

func (s *Store) assessmentSQL() store.AssessmentSQL {
	return store.AssessmentSQL{DB: s.db, Postgres: false}
}
func (s *Store) PutAssessmentRubric(ctx context.Context, r *store.AssessmentRubric) error {
	return s.assessmentSQL().PutAssessmentRubric(ctx, r)
}
func (s *Store) GetAssessmentRubric(ctx context.Context, id string) (*store.AssessmentRubric, error) {
	return s.assessmentSQL().GetAssessmentRubric(ctx, id)
}
func (s *Store) CreateAssessment(ctx context.Context, r *store.AssessmentRecord) error {
	return s.assessmentSQL().CreateAssessment(ctx, r)
}
func (s *Store) GetAssessment(ctx context.Context, id string) (*store.AssessmentRecord, error) {
	return s.assessmentSQL().GetAssessment(ctx, id)
}
func (s *Store) ListAssessments(ctx context.Context, id string) ([]*store.AssessmentRecord, error) {
	return s.assessmentSQL().ListAssessments(ctx, id)
}
func (s *Store) TransitionAssessment(ctx context.Context, r *store.AssessmentRecord, from string, g *store.Grade) error {
	return s.assessmentSQL().TransitionAssessment(ctx, r, from, g)
}
