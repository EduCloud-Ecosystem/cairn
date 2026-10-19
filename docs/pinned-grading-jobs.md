# Pinned grading queue context

## Contract

New manual and webhook grading jobs store a version-1 target in the existing
queue `target_ref` JSON. It freezes the submission/assignment identity,
submission repository and full commit, and instructor repository, full commit
and grading-spec path **before enqueue**. Manual grading uses the recorded full
submission commit; it does not resolve a moving branch during execution.
An eligible submission without a recorded full `LatestCommit` makes the manual
request return HTTP 409. The operator must first record/reconcile the intended
commit through the existing submission workflow. There is no automatic Git
lookup or extra compute attempt to repair it. All targets are validated before
enqueue, but individual enqueue writes are not a whole-batch transaction: a
store failure can still leave a partial batch, which the operator must inspect.

The queue row ID identifies the logical job across delivery retries. The worker
passes it as a typed value to the grading service, which checks current
submission ownership/status but executes the queued policy, not the assignment's
newest policy. Manual requests and queued execution accept both `active` and
`locked` provisioned submissions, so deadline locks do not prevent grading.
Other statuses cannot execute. Push admission remains active-only.
Ordinary policy runners keep their existing interface. A runner
may explicitly implement the job-aware policy interface to receive the context.
The service records the job context and independently computed policy evidence,
including on failure where available. A `GradingRun` remains a delivery audit
row; neither its ID nor the queue delivery counter authorizes compute attempts.

There is no database migration: existing queue text and grading-result JSON
store this context. No new queue, accounting ledger or default runner is added.

## Old queue entries

Bare submission IDs and old revision-only JSON have no frozen policy. The worker
refuses them before calling the grader/checkout and marks them failed with an
operator-facing explanation. It never fills missing pins from current state,
automatically re-enqueues, or grants another compute attempt.

An operator must inspect any earlier execution/accounting state before choosing
an explicit new manual grade request under the intended current pins. Do not
edit the old target or reset its status to manufacture a retry. A new manual
request is a new logical job; repeated webhook delivery for the same submission
commit remains deduplicated and cannot silently refresh its policy.

## Boundaries still open

This fixes loss of queue identity and policy drift. It does not install the
synthetic Outfitter runner or authorize course/cloud execution. The generic
worker still retries infrastructure errors; ordinary runners can execute again.
The optional job-aware path now has a [durable dispatch/publication protocol](durable-grading-dispatch.md):
its acknowledged claim precedes checkout, consumed jobs never enter generic
retry, and grade/run/queue publication is atomic. Ordinary runner grade/run
writes remain separate. Status-only external recovery, operator reconciliation,
attempt authorization and actual broker integration remain open. Crashed
in-progress grade jobs are not reclaimed. Pinned inputs do not grant compute. Direct legacy `Grade`/`GradeRevision`
library calls remain available and do not claim durable queue context.

## Verification

Use temporary SQLite state and fake checkout/runner boundaries through the real
API, queue, worker and grading service. Check immutable pins after policy edits,
stable identity after retry/reopen, webhook deduplication versus explicit manual
regrades, malformed/legacy refusal, cancellation and infrastructure failure with
no grade, and unchanged ordinary policy-runner dispatch. No engine or provider
is required by these tests.
