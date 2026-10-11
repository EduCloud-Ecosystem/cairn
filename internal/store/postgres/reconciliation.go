// SPDX-License-Identifier: AGPL-3.0-or-later
package postgres

import (
	"context"
	"github.com/EduCloud-Ecosystem/cairn/internal/store"
)

func (s *Store) BindLegacyIdentity(ctx context.Context, a store.IdentityReconciliation) (bool, error) {
	return (store.ReconciliationSQL{DB: s.db, Postgres: true}).BindLegacyIdentity(ctx, a)
}
func (s *Store) ListIdentityReconciliations(ctx context.Context) ([]store.IdentityReconciliation, error) {
	return (store.ReconciliationSQL{DB: s.db, Postgres: true}).ListIdentityReconciliations(ctx)
}
