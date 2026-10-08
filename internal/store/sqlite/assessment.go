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

func (s *Store) ReserveGeneration(ctx context.Context, r *store.AssessmentRecord, g *store.Generation, l store.GenerationLimits) error {
	return s.assessmentSQL().ReserveGeneration(ctx, r, g, l)
}

func (s *Store) FinishGeneration(ctx context.Context, g *store.Generation) error {
	return s.assessmentSQL().FinishGeneration(ctx, g)
}

func (s *Store) GetGeneration(ctx context.Context, id string) (*store.Generation, error) {
	return s.assessmentSQL().GetGeneration(ctx, id)
}

func (s *Store) GenerationPaused(ctx context.Context) (bool, error) {
	return s.assessmentSQL().GenerationPaused(ctx)
}

func (s *Store) SetGenerationPaused(ctx context.Context, paused bool) error {
	return s.assessmentSQL().SetGenerationPaused(ctx, paused)
}
