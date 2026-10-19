// SPDX-License-Identifier: AGPL-3.0-or-later
package sqlite

import (
	"context"
	"github.com/EduCloud-Ecosystem/cairn/internal/store"
)

func (s *Store) gradingDispatchSQL() store.GradingDispatchSQL {
	return store.GradingDispatchSQL{DB: s.db, Postgres: false}
}
func (s *Store) GetGradingJob(c context.Context, id string) (*store.ProvisioningJob, error) {
	return s.gradingDispatchSQL().GetGradingJob(c, id)
}
func (s *Store) GetGradingDispatch(c context.Context, id string) (*store.GradingDispatch, error) {
	return s.gradingDispatchSQL().GetGradingDispatch(c, id)
}
func (s *Store) ClaimGradingDispatch(c context.Context, d store.GradingDispatch, r *store.GradingRun) (bool, error) {
	return s.gradingDispatchSQL().ClaimGradingDispatch(c, d, r)
}
func (s *Store) FinishGradingDispatch(c context.Context, d store.GradingDispatch, r *store.GradingRun, g *store.Grade) error {
	return s.gradingDispatchSQL().FinishGradingDispatch(c, d, r, g)
}
