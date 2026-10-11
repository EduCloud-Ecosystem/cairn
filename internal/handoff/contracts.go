// SPDX-License-Identifier: AGPL-3.0-or-later
package handoff

import (
	"context"
	"github.com/EduCloud-Ecosystem/cairn/pkg/adapter"
	"time"
)

type File struct {
	Path    string `json:"path"`
	Content []byte `json:"content"`
}
type Change struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Bytes  int    `json:"bytes"`
	Action string `json:"action"` // create or replace
}
type Snapshot struct {
	Branch       string   `json:"branch"`
	BaseRevision string   `json:"base_revision"`
	Changes      []Change `json:"changes"`
}
type WriteRequest struct {
	Retry        bool            `json:"retry,omitempty"` // prior publication may have reached the remote; never alters commit bytes
	Repo         adapter.RepoRef `json:"repo"`
	Branch       string          `json:"branch"`
	BaseRevision string          `json:"base_revision"`
	Files        []File          `json:"files"`
	ReceiptID    string          `json:"receipt_id"`
	CreatedAt    time.Time       `json:"created_at"` // frozen UTC, whole seconds
}
type Outcome string

const (
	Committed          Outcome = "committed"
	Conflict           Outcome = "conflict"
	VerificationNeeded Outcome = "verification_needed"
)

type Writer interface {
	Inspect(context.Context, adapter.RepoRef, []File) (Snapshot, error)
	Prepare(context.Context, WriteRequest) (string, error)
	Publish(context.Context, WriteRequest, string) (Outcome, error)
}
