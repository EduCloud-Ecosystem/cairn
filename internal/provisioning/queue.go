// SPDX-License-Identifier: AGPL-3.0-or-later

// Package provisioning defines the durable, idempotent job queue that fronts
// every Git-host write. Routing all host mutations through this queue is what
// keeps a large class from exceeding host API rate limits, and the idempotency
// key makes each job individually retry-safe. The v1 implementation is
// Postgres-backed so the self-host footprint stays at a single datastore.
// See DESIGN.md sections 4 and 9.
package provisioning

import "context"

// JobType enumerates the kinds of provisioning work.
type JobType string

const (
	JobCreateRepo      JobType = "create_repo"
	JobAddCollaborator JobType = "add_collaborator"
	JobLockRepo        JobType = "lock_repo"
	JobUnlockRepo      JobType = "unlock_repo"
	JobEnsureWebhook   JobType = "ensure_webhook"
	JobGrade           JobType = "grade"
	JobGradeRevision   JobType = "grade_revision"
)

// Queue accepts provisioning jobs for asynchronous, rate-limited execution.
type Queue interface {
	// Enqueue schedules a job. A repeated enqueue with the same idempotencyKey
	// is a no-op, so callers may safely retry.
	Enqueue(ctx context.Context, t JobType, targetRef, idempotencyKey string) error
}
