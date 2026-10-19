# Operator inspection of queued grading

`GET /operator/grading-jobs` inspects the already-open server store. It requires
authentication to be enabled and a current, allowlisted operator session. Anonymous,
student, expired, revoked and synthetic development sessions cannot read it.
It does not open databases, migrate schemas or invoke grading/provider operations.

Use `?job_id=ID` for one exact grading job, or `?after=ID&limit=N` for a page
(default 25, maximum 100). Exact lookup cannot be combined with pagination.
Unknown, repeated and malformed parameters and query strings over 1 KiB are rejected. IDs use ASCII letters,
digits, underscore and hyphen, at most 256 bytes. Listing uses ascending binary
lexicographic queue IDs and one extra row for `next_after`. Each request is one
metadata snapshot; pages are not a frozen whole-history inventory and concurrent
insertions before a cursor can be missed. A logged job ID can be inspected directly.

The version-1 response contains `items`, optional `next_after`, fixed `limits`,
and `replay_authorized: false`. Each item contains validated opaque IDs, queue and
dispatch state enums, available stored SHA-256 digests, `binding_status`,
`run_presence`, `grade_presence` and a fixed `observation` code. The endpoint never
returns target JSON, repository/spec paths, runner free text, grade scores/bodies,
run results/log paths, queue error text or raw database errors. SQL selects only
bounded metadata; target decoding is bounded to 8 KiB and malformed/oversize targets
produce an invalid binding observation. It never interprets educational bodies.
SQL uses a single joined SELECT (primary-key joins); memory uses one read lock.
`binding_status` is `consistent`, `not_recorded` or `invalid`; it describes only
selected metadata, never result-body integrity. Presence is `present`, `absent`
or `not_linked`. Unknown stored state values become the fixed `unknown` enum.
The submission ID comes from validated target or tombstone metadata. Invalid
field values are not echoed.

Malformed queries receive 400, unavailable operator authentication 401, an exact
missing/non-grading job 404, and unavailable store/schema/unsafe job identifiers
503. Failures contain fixed public codes. Responses carry `Cache-Control: no-store`.
There is no database creation or migration fallback.

Interpret observations conservatively:

- `published_records_present`: terminal publication is recorded and its linked
  records are present; result bodies and their integrity were not reverified.
- `published_records_absent`: publication remains terminal; one or both linked
  records are absent. Removal cause is unknown, even when compatible with purge.
- `claimed_liveness_unknown`: a dispatch was consumed, but worker liveness and
  external execution/cleanup remain unknown.
- `held_reconciliation_required`: the dispatch is held; inspection cannot recover it.
- `in_progress_without_dispatch`: no tombstone is recorded. The view cannot
  establish runner mode, execution history or a safe retry.
- `no_dispatch_recorded`: an ordinary/pending/terminal queue state without a
  tombstone; it is not evidence of provider non-execution or compute settlement.
- `invalid_binding`: malformed pins or inconsistent selected metadata; no repair
  or normalization is performed.

Presence observations are independent: retention currently removes grade and run
records in separate steps. Absence alone is not an integrity failure or a reason
to recreate educational records. No observation authorizes replay, reset, new
admission, cleanup, settlement or refund. This adds no external status API and does
not qualify backup rollback or a second restored admitting store.

Validation must exercise real Worker/Service-produced states with fake execution
boundaries, both stores' snapshot semantics, privacy/authorization, pagination,
concurrent publication and repeated-read immutability. Local PostgreSQL runtime is
not implied by compile/skip checks; existing configured hosted CI tests that backend.

A local regression control found that SQLite text substring operations can hide
an embedded NUL suffix in corrupted target/status metadata. The corrected reader
slices bounded BLOB bytes before converting back to text, preserving the suffix
for validation. Both original failing subcases and corrected checks are retained
in the change's evidence. This was a new inspection-path defect, not evidence of
a failure in the accepted dispatch writer.

Exact local check commands, counts, source hashes, explicit skips and the retained
pre-correction witness are recorded in
[`validation.json`](evidence/2026-10-19-grading-inspection/validation.json). These
are temporary synthetic stores and fake runner/checkout boundaries, not a live
operator or provider qualification.
