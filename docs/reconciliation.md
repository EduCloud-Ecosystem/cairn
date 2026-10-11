# Operator identity and repository reconciliation

The `cairn reconcile` CLI inventories records that need investigation and can fill
an **empty** legacy roster provider ID after explicit historical identity review.
It preserves roster IDs, claims, repositories, submissions, grades and calibration
records. It cannot clear or replace an existing provider ID, adopt a repository,
change collaborators, revoke access or enqueue provisioning.

Use this command on the control-plane host as an authorized database operator.
The CLI uses the same `CAIRN_STORE`, `CAIRN_SQLITE_PATH` or `CAIRN_DATABASE_URL`
configuration as the server. Possession of database credentials is the authority
boundary; `--operator` is an accountable audit label, not a login mechanism. Do not
expose the command through an unauthenticated endpoint. Back up the database first.
SQLite opening applies its normal additive schema upgrades; PostgreSQL needs
migration `0011_reconciliation.up.sql` (or the normal `CAIRN_DB_AUTOMIGRATE=1`
startup path). Memory storage is unsuitable and rejected for CLI apply.

## Inventory first

```sh
cairn reconcile inventory --out /private/reconciliation/inventory.json
cairn reconcile inventory --probe --out /private/reconciliation/provider-inventory.json
```

Choose an existing private directory outside the repository. Output files are
created exclusively with mode `0600`; an existing file is never overwritten.
An error can leave an empty output file. Keep reports and evidence in the
institution's protected incident records, not in Git or public issues. Commands
have a two-minute ceiling and provider probes use configured adapter credentials.
Without `--probe`, inventory makes no provider calls. Inventory never changes
learner rows or remote resources. It is a point-in-time triage list, not an atomic
snapshot of the entire deployment and not a complete access audit.

The report identifies missing stable identities, legacy/mismatched repository
references, and incomplete provisioning. With `--probe`, it additionally checks
bound username/ID pairs and repository existence. A missing binding with an
existing expected repository is a *possible orphan*, never proof of Cairn
ownership. Lookup failures are recorded separately from absence. An active or
locked submission with the current deterministic binding is not reported as
incomplete merely because it is locked.

Provider collaborator lists, inherited group/org access, pending invitations and
queue history are **not** collected. The report includes these limitations even
when there are no issues. Review those provider records separately by immutable
identity before retrying a failed grant.

## Bind one legacy identity

A current username lookup does not establish who owned past coursework. Names can
be recycled. Independently establish the original learner's immutable provider ID
using a historical provider export or retained account record, supplemented by
instructor verification. If this evidence is unavailable or disagrees with the
current username's account, leave the record unbound and preserve the work for
instructor review. Never take the current username's ID and treat it as historical
proof.

Keep that evidence in a private file. The command records its SHA-256, not its
contents or path. The hash binds review to unchanged material; it does not prove
that the evidence is accurate. Explicit operator review remains essential.

```sh
cairn reconcile plan-identity \
  --roster ROSTER_ID \
  --provider-id HISTORICAL_NUMERIC_PROVIDER_ID \
  --operator OPERATOR_ID \
  --evidence-file /private/reconciliation/identity-evidence.txt \
  --historical-identity-verified \
  --out /private/reconciliation/identity-plan.json
```

Review the resulting roster, classroom, host, username and proposed immutable ID.
The configured provider must currently confirm the exact username/ID pair. The
plan and durable receipt also bind the provider instance URL; changing instances
between planning and applying rejects the repair. Plans
expire after one hour and bind the complete roster state to a digest. A plan
contains no automatic repository actions. Its digest detects accidental edits;
it is not a signature or substitute for trusted operator access.

```sh
cairn reconcile apply \
  --plan /private/reconciliation/identity-plan.json \
  --operator OPERATOR_ID \
  --evidence-file /private/reconciliation/identity-evidence.txt \
  --historical-identity-verified \
  --out /private/reconciliation/identity-receipt.json
```

Apply verifies unchanged evidence, operator, plan, current roster and the live
provider mapping again. A serializable database transaction checks the roster
snapshot, rejects a removed or already-bound record and an ID already used by
another roster entry in the classroom, then updates only the empty provider-ID
column and inserts an audit receipt atomically. A partial unique index enforces
the same classroom/host/provider-ID uniqueness for ordinary claims and imports,
including a normal enrollment racing a repair on another row. Concurrent changes, provider
failures and receipt-write failures stop the repair without partial mutation.
A provider rename after verification cannot redirect the persisted numeric ID;
subsequent access grants still require their normal provider verification.

Replaying the same completed plan returns `already_applied: true` without a second
write, including after expiry. If output was lost after a successful transaction,
rerun with a new receipt path or inspect the durable audit:

```sh
cairn reconcile audit --out /private/reconciliation/audit.json
```

Database receipts include plan ID, roster ID, prior-state digest, provider instance, provider ID,
operator, evidence digest and time. They follow roster erasure through a cascading
foreign key (and equivalent memory-store behavior). Exported files and the source
evidence remain the operator's responsibility under the same retention policy.

## Existing duplicate bindings block an upgrade

The new uniqueness invariant does not choose between historical duplicates. If
an old database already binds the same nonempty provider ID to multiple roster
rows in one classroom/host, migration fails and rolls back without clearing IDs
or deleting coursework. Because normal SQLite opening performs migration, the
new CLI inventory cannot open that database until the conflict is resolved.

Use a read-only SQL client against the preserved database (or a verified backup)
to list those rows **without starting Cairn or applying migrations**. For SQLite,
open `sqlite3 -readonly /private/cairn.db`; for PostgreSQL, use `psql` with an
appropriate read-only role and start `BEGIN READ ONLY;`. Run this common query:

```sql
SELECT r.id, r.classroom_id, r.host, r.host_username, r.host_user_id
FROM roster_entries AS r
WHERE r.host_user_id <> ''
  AND EXISTS (
    SELECT 1 FROM roster_entries AS other
    WHERE other.classroom_id = r.classroom_id
      AND other.host = r.host
      AND other.host_user_id = r.host_user_id
      AND other.id <> r.id
  )
ORDER BY r.classroom_id, r.host, r.host_user_id, r.id;
```

For PostgreSQL, end with `ROLLBACK;`. Protect the query output as identity data.
Compare historical identity and submission ownership evidence with the instructor.
Do not remove or blank one ID just to satisfy the index. A duplicate may require
a separately reviewed data migration that preserves every submission and dependent
record; this CLI does not implement merging learners or choosing an owner. Keep
the previous deployment paused until that decision and verified recovery are ready.

## Repository uncertainty: preserve and investigate

The current adapter contract stores a repository path, not a provider-immutable
repository ID or a verified creation receipt. Automatic adoption after an unknown
creation outcome would therefore be unsafe. The CLI deliberately leaves these
cases unresolved while producing exact submission IDs and candidate paths for
investigation.

1. Pause affected provisioning and preserve the existing submission row and all
   candidate repository data. Record the incident and take a database backup.
2. Compare the provider's immutable repository ID, creation time, organization,
   creator/audit event and template/source history with the Cairn submission and
   worker logs. A matching name or template alone does not establish ownership.
3. Inspect collaborators, inherited memberships and pending invitations by
   immutable account ID. Include accepted invitations and unknown-outcome requests.
   Reconcile unintended access through the provider's administrative workflow;
   record exact accounts and invitation IDs, then read back the resulting access.
4. For an already-persisted correct binding, verify its roster identity and access
   before the existing retry workflow. Legacy path differences do not justify
   overwriting the binding to match a new naming convention.
5. For an unbound orphan or conflicting historical mapping, retain the work and
   keep provisioning stopped. A reviewed migration needs independently verified
   ownership and a transactional binding check; this release does not implement
   that migration. Never delete the candidate just to make a fresh create pass.

See [repository identity boundaries](provisioning-identity.md) for residual
GitHub/Forgejo username-based grant races. Revocation cannot recover content that
an unintended recipient has already read.

## Validation scope

Hermetic provider fixtures and memory/SQLite service tests cover recycled names,
provider failures, roster drift during verification, expired/tampered plans, changed provider instances,
evidence changes, duplicate IDs, immutable-ID replacement rejection, transactional
receipt failure, restart/retry and unchanged coursework/grades. Shared store
conformance tests exercise atomic competing repairs, ordinary claim/create races, and audit erasure on memory,
SQLite and the opt-in PostgreSQL integration target. Live provider identity or
rename incidents are not claimed as tested by these fixtures.
