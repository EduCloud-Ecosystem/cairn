// SPDX-License-Identifier: AGPL-3.0-or-later
package store

import (
	"context"
	"encoding/json"
)

// LTIRecord stores only opaque LMS identifiers, mappings and numeric delivery
// receipts. No launch tokens, names, emails, provider content or signing keys.
// Roster and grade references participate in the existing erasure lifecycle.
type LTIRecord struct {
	ID            string          `json:"id"`
	Kind          string          `json:"kind"`
	UniqueKey     string          `json:"-"`
	AssignmentID  string          `json:"assignment_id"`
	RosterEntryID string          `json:"-"`
	GradeID       string          `json:"grade_id,omitempty"`
	Revision      int             `json:"revision"`
	Document      json.RawMessage `json:"document"`
}

type LTIStore interface {
	GetLTIRecord(context.Context, string) (*LTIRecord, error)
	FindLTIRecord(context.Context, string) (*LTIRecord, error)
	// expected=0 creates; otherwise compare-and-swap. Identity and mapping scope
	// cannot change. A delivery may advance to a new grade only via a CAS update.
	SaveLTIRecord(context.Context, *LTIRecord, int) error
}
