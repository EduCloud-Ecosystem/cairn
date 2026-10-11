// SPDX-License-Identifier: AGPL-3.0-or-later
package memory

import (
	"context"
	"github.com/EduCloud-Ecosystem/cairn/internal/store"
)

func cloneLTI(r store.LTIRecord) *store.LTIRecord {
	r.Document = append([]byte(nil), r.Document...)
	return &r
}
func (m *Store) GetLTIRecord(_ context.Context, id string) (*store.LTIRecord, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	r, ok := m.ltiRecords[id]
	if !ok {
		return nil, store.ErrNotFound
	}
	return cloneLTI(r), nil
}
func (m *Store) FindLTIRecord(_ context.Context, key string) (*store.LTIRecord, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, r := range m.ltiRecords {
		if r.UniqueKey == key {
			return cloneLTI(r), nil
		}
	}
	return nil, store.ErrNotFound
}
func (m *Store) SaveLTIRecord(_ context.Context, r *store.LTIRecord, expected int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r.Revision != expected+1 {
		return store.ErrConflict
	}
	if _, ok := m.assignments[r.AssignmentID]; !ok {
		return store.ErrNotFound
	}
	if r.RosterEntryID != "" {
		if _, ok := m.roster[r.RosterEntryID]; !ok {
			return store.ErrNotFound
		}
	}
	if r.GradeID != "" {
		if _, ok := m.grades[r.GradeID]; !ok {
			return store.ErrNotFound
		}
	}
	old, ok := m.ltiRecords[r.ID]
	if expected == 0 {
		if ok {
			return store.ErrConflict
		}
		for _, v := range m.ltiRecords {
			if v.UniqueKey == r.UniqueKey {
				return store.ErrConflict
			}
		}
	} else if !ok || old.Revision != expected || old.Kind != r.Kind || old.UniqueKey != r.UniqueKey || old.AssignmentID != r.AssignmentID || old.RosterEntryID != r.RosterEntryID {
		return store.ErrConflict
	}
	m.ltiRecords[r.ID] = *cloneLTI(*r)
	return nil
}
