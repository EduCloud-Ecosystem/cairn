# Durable dispatch for queued job-aware runners

This opt-in protocol applies only to queued grading through a runner implementing
`JobPolicyRunner`. Ordinary `PolicyRunner` delivery behavior and runner selection
stay unchanged. It does not install the synthetic Outfitter adapter or authorize
compute. Queue identity is not a budget or reservation.

Before any checkout or runner call, Service checks the exact queue target and
atomically creates one dispatch tombstone and its running audit row. Only a
positively acknowledged new claim permits execution. A persisted claim never expires or
reopens. A failed/ambiguous acknowledgment is reread-only, even when the record
appears to be this caller's claim. A crash immediately after claiming can therefore
consume the opportunity without executing anything; this is intentionally held.

A successful result publishes its grade, completed run, terminal tombstone and
queue success in one transaction. Results must serialize within 8 MiB; that
protocol bound is not a storage quota. An invalid/oversized result is held without
a grade. Failure/cancellation retains a failed audit,
a held tombstone and failed queue job without a grade. Ambiguous final commits
are reread-only: an exact published result is success, everything else requires
reconciliation. Generic Worker retries cannot overwrite managed state.

The tombstone stores only identifiers, context/target/result/token digests,
runner identity and state. The result digest binds Service-encoded JSON before
backend JSONB normalization; it is not a raw database-output byte hash. It contains no source or grade body. Grade/run purge
and roster erasure do not erase the tombstone. A published-but-purged result
stays terminal: no redispatch or recreation of educational records. Existing
claimed/held jobs return a typed reconciliation-required error before checkout.
The store exposes inspection by job ID; there is no status-recovery or reset API.

Only the trusted Worker's `GradeManagedJob` entry initiates this protocol.
Direct `Service.GradeJob` calls for an optional job-aware runner are inspect-only.
If an ambiguous claim write leaves no tombstone, its already-claimed queue row
remains `in_progress`, which the Worker never reclaims. That is queue-owned
holding, not proof that a failed write created a permanent dispatch barrier.
Direct calls to the internal managed entry, manual queue resets, old binaries and
out-of-band database edits are not supported recovery or qualified retry routes.

No automatic replay, new job, provider cleanup, refund or status recovery occurs.
The current runner interface has no external status API. An operator must review
external uncertainty before authorizing a separate new job. Backup rollback or
activating a second restored admitting database can lose/duplicate authority and
remain OPEN. This protocol only establishes same-store transaction/crash behavior.
Production runner installation, real databases, providers and private data remain
outside this change's acceptance.

Verification uses real Worker/Service/temporary SQLite with fake checkout and
runner boundaries, concurrent independent connections, subprocess crash/reopen,
transaction/acknowledgment failure injection, exact duplicates, binding conflicts,
retention and ordinary-runner regression controls. PostgreSQL implements the same
store contract; runtime evidence requires the existing bounded CI database job.
