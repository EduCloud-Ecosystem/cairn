// SPDX-License-Identifier: AGPL-3.0-or-later
package sqlite

import (
	"context"
	"github.com/EduCloud-Ecosystem/cairn/internal/store"
)

func (s *Store) CreateCalibration(ctx context.Context, c *store.Calibration) error {
	return s.assessmentSQL().CreateCalibration(ctx, c)
}
func (s *Store) GetCalibration(ctx context.Context, id, owner string) (*store.Calibration, error) {
	return s.assessmentSQL().GetCalibration(ctx, id, owner)
}
func (s *Store) ListCalibrations(ctx context.Context, assignment, owner string) ([]*store.Calibration, error) {
	return s.assessmentSQL().ListCalibrations(ctx, assignment, owner)
}
func (s *Store) UpdateCalibration(ctx context.Context, c *store.Calibration, previous int) error {
	return s.assessmentSQL().UpdateCalibration(ctx, c, previous)
}
func (s *Store) DeleteCalibration(ctx context.Context, id, owner string) error {
	return s.assessmentSQL().DeleteCalibration(ctx, id, owner)
}
func (s *Store) ReserveCalibrationGeneration(ctx context.Context, c *store.Calibration, g *store.Generation, l store.GenerationLimits) error {
	return s.assessmentSQL().ReserveCalibrationGeneration(ctx, c, g, l)
}
