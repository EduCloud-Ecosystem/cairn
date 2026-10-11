// SPDX-License-Identifier: AGPL-3.0-or-later
package store

import (
	"context"
	"encoding/json"
)

// SubmissionHandoff contains private bounded source only while a receipt is pending.
// Submission deletion cascades these records. Version is a compare-and-swap token.
type SubmissionHandoff struct {
	ID, SubmissionID, OwnerID, IdempotencyKey, PayloadDigest, Status string
	CreatedAt, ExpiresAt                                             int64
	Version                                                          int
	Document                                                         []byte
}
type HandoffStore interface {
	ExpireSubmissionHandoffs(context.Context, int64) error
	CreateSubmissionHandoff(context.Context, *SubmissionHandoff, int) error
	GetSubmissionHandoff(context.Context, string) (*SubmissionHandoff, error)
	ListSubmissionHandoffs(context.Context, string) ([]*SubmissionHandoff, error)
	// Transition atomically advances a receipt and inserts an optional durable job.
	TransitionSubmissionHandoff(context.Context, *SubmissionHandoff, int, *ProvisioningJob) error
}

func HandoffWithoutFiles(raw []byte) []byte {
	var doc map[string]any
	if json.Unmarshal(raw, &doc) != nil {
		return []byte("{}")
	}
	if request, ok := doc["request"].(map[string]any); ok {
		delete(request, "files")
	}
	out, _ := json.Marshal(doc)
	return out
}

// HandoffCommit extracts only the frozen Git provenance needed to refresh the
// submission without moving a newer webhook observation backwards.
func HandoffCommit(h *SubmissionHandoff) (base, candidate, observed string, err error) {
	var d struct {
		Candidate string `json:"candidate"`
		Observed  string `json:"observed_revision"`
		Request   struct {
			Base string `json:"base_revision"`
		} `json:"request"`
	}
	if json.Unmarshal(h.Document, &d) != nil || d.Candidate == "" || d.Request.Base == "" {
		return "", "", "", ErrConflict
	}
	return d.Request.Base, d.Candidate, d.Observed, nil
}
