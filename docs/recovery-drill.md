# Populated upgrade and recovery rehearsal

This is a repeatable **local component acceptance test**, not evidence of an
installed backup service, off-host durability, or a production recovery time.
It uses only synthetic education records. It never reads a configured Cairn
DSN, provider credential, production database, or LMS endpoint.

## Run

From the repository root, using the Go version in `go.mod`:

```sh
go test -race ./tools/recovery -count=1 -v
CAIRN_RECOVERY_POSTGRES=1 go test -race ./tools/recovery -count=1 -v
```

The first command always runs SQLite drills. The second requires Docker and
runs both SQLite and PostgreSQL. PostgreSQL uses a uniquely named disposable
`postgres:17-alpine` container, a loopback-only ephemeral port, and the client's
`pg_dump`/`pg_restore` from that same container (matching server major version).
No external DSN is accepted. Databases and snapshots are temporary; cleanup
removes the container and test directory. The password in the test is a
synthetic, one-use container credential, not a deployment secret.

An interrupted Go process may prevent cleanup. Inventory the exact containers
created by this drill before removing anything:

```sh
docker ps -a --filter label=org.educloud.cairn.recovery-drill=true
```

Remove only a confirmed abandoned drill container by its displayed ID. The
label is for inventory and is not permission to delete another active run.

## Evidence and acceptance gates

- **Historical SQLite baseline:** a frozen schema from initial commit `d2fa742`
  plus populated classroom, instructor, roster, submission, grade, grading run
  and provisioning job. No export receipt or immutable learner ID existed in
  this schema. Upgrade must retain the grade, keep export confirmation NULL,
  and keep the legacy learner unbound until explicit reconciliation.
- **Pre-Brightspace baseline:** frozen SQLite schema from `1854e01`; PostgreSQL
  applies embedded migrations 0001–0008. Both contain coursework, an approved
  assessment linked to its numeric grade, immutable ready calibration and its
  source link, paused provider state, token accounting, export receipt and an
  in-progress provisioning job.
- **Backup before upgrade:** SQLite `VACUUM INTO` produces a consistent database
  snapshot even with WAL enabled. PostgreSQL uses a custom-format `pg_dump`.
  Restore the old snapshot into a fresh database, verify every legacy column
  and row, then upgrade it with the current production migration entry point.
- **Current-state recovery:** bind a verified learner identity with its reconciliation audit receipt and add LTI link,
  binding and uncertain delivery receipt; back up again, change the source,
  and restore to another fresh database. Compare all tables, columns and rows
  with the snapshot manifest, then read grades, calibration, assessments,
  learner identity and LTI records through the production store interface.
  Comparison includes every current table, including reconciliation audit.
- **Failure handling:** an existing SQLite snapshot cannot be overwritten;
  truncated SQLite artifacts fail integrity checks; malformed schema causes an
  atomic SQLite migration rollback and a corrected retry succeeds. PostgreSQL also
  rejects a malformed schema without leaving a partial migration. A truncated
  PostgreSQL restore using `--single-transaction --exit-on-error` must fail with
  no partial tables. A cancelled PostgreSQL migration cannot alter restored data.
- **Integrity and repeatability:** SQLite integrity and foreign-key checks pass;
  rerunning production migrations retains logical contents. The verbose test
  logs a SHA-256 of the complete logical manifest for each comparison. This is
  an equality witness for the synthetic fixture, not a signed backup artifact.

This tests a database recovery point, not replay of external side effects.
An uncertain LTI delivery stays uncertain, and an in-progress repository job
stays in progress; operators must reconcile external state before replay.
Restoring a database must never itself send grades or repeat provisioning.

## Deployment runbook

1. Record the deployed image digest and database engine/version. Retain the
   preceding application image for rollback and check its schema compatibility.
2. Pause application writes, assessment generation, provisioning and passback
   workers. Keep workers disabled on the recovery instance, including its first
   application startup. Record pending jobs and uncertain external writes.
3. Back up **before** deploying a schema change. For SQLite use an online backup
   API, `.backup`, or `VACUUM INTO`; copying only a live `.db` file can omit WAL
   transactions. For PostgreSQL use a version-compatible `pg_dump` and retain
   required roles/extensions separately. Protect backups as education records.
4. Store the artifact off-host with encryption, retention and access controls;
   record checksum, backup timestamp, application version and database version.
   Validate transfer and restore into a fresh isolated target. Never restore
   over the only working source or treat a nonzero dump/restore exit as success.
5. Run integrity and logical record checks before enabling the restored app.
   Check identity bindings, instructor review decisions, calibration source
   links, grade export receipts, token budgets and LTI delivery receipts. Compare
   expected row counts and references; counts alone cannot establish equality.
6. Separately recover repository content, notebook files, uploaded artifacts,
   signing keys and application configuration. Database recovery cannot restore
   these. Never put keys or raw student records in a public evidence artifact.
7. Reconcile uncertain external writes against provider/LMS state. Re-enable
   workers only after choosing the authoritative recovery instance and disabling
   the old one. Record data lost since the snapshot and measured recovery time.

A pre-upgrade snapshot supports reverting the application and database together.
Do not run SQL down migrations as a substitute for a verified snapshot; destructive
migration rollback can erase newer coursework. Rehearse on copies, with workers
stopped, and explicitly account for changes after the chosen recovery point.

## Remaining hosted acceptance

A separate-host restore, backup retention/rotation, encrypted off-host storage,
actual artifact recovery, real workload timing, documented recovery objectives
and a second operator's execution remain deployment gates. Passing these local
fixtures is not a promise of zero data loss, hosting readiness or grading quality.
