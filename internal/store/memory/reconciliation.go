// SPDX-License-Identifier: AGPL-3.0-or-later
package memory

import (
	"context"
	"github.com/EduCloud-Ecosystem/cairn/internal/store"
	"sort"
)

func (m *Store) BindLegacyIdentity(_ context.Context, a store.IdentityReconciliation) (bool, error) {
	if err := store.ValidateIdentityReconciliation(a); err != nil {
		return false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if old, ok := m.reconciliationAudit[a.PlanID]; ok {
		if old.RosterID != a.RosterID || old.Snapshot != a.Snapshot || old.ProviderID != a.ProviderID || old.ProviderAuthority != a.ProviderAuthority || old.Operator != a.Operator || old.EvidenceSHA256 != a.EvidenceSHA256 {
			return false, store.ErrConflict
		}
		return true, nil
	}
	r, ok := m.roster[a.RosterID]
	if !ok {
		return false, store.ErrNotFound
	}
	if r.HostUserID != "" || r.Status == store.RosterRemoved || store.RosterSnapshot(&r) != a.Snapshot {
		return false, store.ErrConflict
	}
	for _, other := range m.roster {
		if other.ID != r.ID && other.ClassroomID == r.ClassroomID && other.Host == r.Host && other.HostUserID == a.ProviderID {
			return false, store.ErrConflict
		}
	}
	r.HostUserID = a.ProviderID
	m.roster[r.ID] = r
	m.reconciliationAudit[a.PlanID] = a
	return false, nil
}
func (m *Store) ListIdentityReconciliations(_ context.Context) ([]store.IdentityReconciliation, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := []store.IdentityReconciliation{}
	for _, a := range m.reconciliationAudit {
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PlanID < out[j].PlanID })
	return out, nil
}

// boundIdentityConflict requires the caller to hold m.mu. Empty IDs remain
// intentionally non-unique so historical rows can be reconciled separately.
func (m *Store) boundIdentityConflict(r store.RosterEntry) bool {
	if r.HostUserID == "" {
		return false
	}
	for _, other := range m.roster {
		if other.ID != r.ID && other.ClassroomID == r.ClassroomID && other.Host == r.Host && other.HostUserID == r.HostUserID {
			return true
		}
	}
	return false
}
