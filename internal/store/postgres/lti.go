// SPDX-License-Identifier: AGPL-3.0-or-later
package postgres

import (
	"context"
	"github.com/EduCloud-Ecosystem/cairn/internal/store"
)

func (s *Store) GetLTIRecord(ctx context.Context, id string) (*store.LTIRecord, error) {
	return s.assessmentSQL().GetLTIRecord(ctx, id)
}
func (s *Store) FindLTIRecord(ctx context.Context, key string) (*store.LTIRecord, error) {
	return s.assessmentSQL().FindLTIRecord(ctx, key)
}
func (s *Store) SaveLTIRecord(ctx context.Context, r *store.LTIRecord, e int) error {
	return s.assessmentSQL().SaveLTIRecord(ctx, r, e)
}
