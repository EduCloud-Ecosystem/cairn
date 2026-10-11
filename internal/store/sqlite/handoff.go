// SPDX-License-Identifier: AGPL-3.0-or-later
package sqlite

import (
	"context"
	"github.com/EduCloud-Ecosystem/cairn/internal/store"
)

func (s *Store) CreateSubmissionHandoff(c context.Context, h *store.SubmissionHandoff, l int) error {
	return s.assessmentSQL().CreateSubmissionHandoff(c, h, l)
}
func (s *Store) GetSubmissionHandoff(c context.Context, id string) (*store.SubmissionHandoff, error) {
	return s.assessmentSQL().GetSubmissionHandoff(c, id)
}
func (s *Store) ListSubmissionHandoffs(c context.Context, id string) ([]*store.SubmissionHandoff, error) {
	return s.assessmentSQL().ListSubmissionHandoffs(c, id)
}
func (s *Store) TransitionSubmissionHandoff(c context.Context, h *store.SubmissionHandoff, v int, j *store.ProvisioningJob) error {
	return s.assessmentSQL().TransitionSubmissionHandoff(c, h, v, j)
}

func (s *Store) ExpireSubmissionHandoffs(ctx context.Context, now int64) error {
	return s.assessmentSQL().ExpireSubmissionHandoffs(ctx, now)
}
