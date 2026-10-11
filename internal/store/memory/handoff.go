// SPDX-License-Identifier: AGPL-3.0-or-later
package memory

import (
	"context"
	"github.com/EduCloud-Ecosystem/cairn/internal/store"
	"sort"
	"time"
)

func copyHandoff(h store.SubmissionHandoff) *store.SubmissionHandoff {
	h.Document = append([]byte(nil), h.Document...)
	return &h
}
func (m *Store) CreateSubmissionHandoff(_ context.Context, h *store.SubmissionHandoff, limit int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.submissions[h.SubmissionID]; !ok {
		return store.ErrNotFound
	}
	if err := m.handoffAuthorize(h); err != nil {
		return err
	}
	count := 0
	for id, v := range m.handoffs {
		if v.Status == "preview" && v.ExpiresAt <= h.CreatedAt {
			v.Status = "expired"
			v.Document = store.HandoffWithoutFiles(v.Document)
			v.Version++
			m.handoffs[id] = v
		}
		if v.SubmissionID == h.SubmissionID {
			if v.OwnerID == h.OwnerID && v.IdempotencyKey == h.IdempotencyKey {
				return store.ErrConflict
			}
			if v.Status == "preview" || v.Status == "queued" || v.Status == "publishing" || v.Status == "verification_needed" {
				count++
			}
		}
	}
	if _, ok := m.handoffs[h.ID]; ok || count >= limit {
		return store.ErrConflict
	}
	m.handoffs[h.ID] = *copyHandoff(*h)
	return nil
}
func (m *Store) GetSubmissionHandoff(_ context.Context, id string) (*store.SubmissionHandoff, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	h, ok := m.handoffs[id]
	if !ok {
		return nil, store.ErrNotFound
	}
	return copyHandoff(h), nil
}
func (m *Store) ListSubmissionHandoffs(_ context.Context, id string) ([]*store.SubmissionHandoff, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := []*store.SubmissionHandoff{}
	for _, h := range m.handoffs {
		if h.SubmissionID == id {
			out = append(out, copyHandoff(h))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt == out[j].CreatedAt {
			return out[i].ID < out[j].ID
		}
		return out[i].CreatedAt > out[j].CreatedAt
	})
	return out, nil
}
func (m *Store) TransitionSubmissionHandoff(_ context.Context, h *store.SubmissionHandoff, v int, j *store.ProvisioningJob) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	old, ok := m.handoffs[h.ID]
	if !ok || old.Version != v || old.SubmissionID != h.SubmissionID || old.OwnerID != h.OwnerID {
		return store.ErrConflict
	}
	if h.Status == "queued" {
		if old.Status != "preview" || old.ExpiresAt <= time.Now().Unix() {
			return store.ErrConflict
		}
		if err := m.handoffAuthorize(h); err != nil {
			return err
		}
	}
	if j != nil && !m.jobIdem[j.IdempotencyKey] {
		if _, exists := m.jobs[j.ID]; exists {
			return store.ErrConflict
		}
	}
	if h.Status == "committed" {
		base, candidate, observed, err := store.HandoffCommit(h)
		if err != nil {
			return err
		}
		sub := m.submissions[h.SubmissionID]
		if sub.LatestCommit == "" || sub.LatestCommit == base || sub.LatestCommit == candidate || sub.LatestCommit == observed {
			now := time.Now().UTC()
			sub.LatestCommit = candidate
			sub.LastActivityAt = &now
			m.submissions[sub.ID] = sub
		}
	}
	if j != nil && !m.jobIdem[j.IdempotencyKey] {
		if _, exists := m.jobs[j.ID]; exists {
			return store.ErrConflict
		}
		m.jobs[j.ID] = *j
		m.jobIdem[j.IdempotencyKey] = true
	}
	old.Status = h.Status
	old.Document = append([]byte(nil), h.Document...)
	old.Version++
	m.handoffs[h.ID] = old
	return nil
}

func (m *Store) ExpireSubmissionHandoffs(_ context.Context, now int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, h := range m.handoffs {
		if h.Status == "preview" && h.ExpiresAt <= now {
			h.Status = "expired"
			h.Document = store.HandoffWithoutFiles(h.Document)
			h.Version++
			m.handoffs[id] = h
		}
	}
	return nil
}

func (m *Store) handoffAuthorize(h *store.SubmissionHandoff) error {
	sub, ok := m.submissions[h.SubmissionID]
	if !ok {
		return store.ErrNotFound
	}
	r := m.roster[sub.RosterEntryID]
	a := m.assignments[sub.AssignmentID]
	if sub.Status != "active" || r.Status != store.RosterActive || h.OwnerID == "" || h.OwnerID != r.HostUserID || (a.Deadline != nil && !time.Now().Before(*a.Deadline)) {
		return store.ErrConflict
	}
	return nil
}
