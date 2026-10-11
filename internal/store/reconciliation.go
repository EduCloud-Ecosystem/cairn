// SPDX-License-Identifier: AGPL-3.0-or-later
package store

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"time"
)

// IdentityReconciliation is a privileged, narrowly scoped repair. It can only
// fill an empty provider ID; the complete roster snapshot must still match.
type IdentityReconciliation struct {
	PlanID            string    `json:"plan_id"`
	RosterID          string    `json:"roster_id"`
	Snapshot          string    `json:"snapshot"`
	ProviderAuthority string    `json:"provider_authority"`
	ProviderID        string    `json:"provider_id"`
	Operator          string    `json:"operator"`
	EvidenceSHA256    string    `json:"evidence_sha256"`
	AppliedAt         time.Time `json:"applied_at"`
}

type ReconciliationStore interface {
	BindLegacyIdentity(context.Context, IdentityReconciliation) (alreadyApplied bool, err error)
	ListIdentityReconciliations(context.Context) ([]IdentityReconciliation, error)
}

func RosterSnapshot(r *RosterEntry) string {
	b, _ := json.Marshal(struct {
		Roster     *RosterEntry `json:"roster"`
		ProviderID string       `json:"provider_id"`
	}{r, r.HostUserID})
	return fmt.Sprintf("%x", sha256.Sum256(b))
}

func ValidateIdentityReconciliation(a IdentityReconciliation) error {
	if a.PlanID == "" || a.RosterID == "" || len(a.Snapshot) != 64 || a.ProviderID == "" || a.Operator == "" || len(a.EvidenceSHA256) != 64 || a.AppliedAt.IsZero() {
		return fmt.Errorf("incomplete identity reconciliation")
	}
	return nil
}
