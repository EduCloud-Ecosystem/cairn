# Cairn operational component verification — October 11, 2026

This release adds bounded repository acquisition, reviewed offline crash cleanup,
populated database recovery drills and narrow operator identity reconciliation.
It uses synthetic fixtures; it does not migrate a real installation, repair a
real learner binding, change provider access or deploy a hosted service.

## Observed local evidence

- Full Go race suite, build and vet passed after review fixes; the final release
  is also gated by CI.
- Real Docker acquisition observed kernel ENOSPC at the 512 MiB boundary, stopped
  a stalled process, redacted its synthetic token and completed pinned policy /
  submission acquisition followed by a real container grading result of 10/10.
- A review found extraction widening the root of private assessment captures.
  Extraction now preserves caller permissions; only grading's children beneath
  a private job parent become readable for container mounts. A regression and
  the live Docker service path passed after the correction.
- Shared-lock/exclusive-recovery tests reject active work and stale plans,
  exclude symlinks and unmarked resources, and stop after a partial failure.
  A live Docker fixture recovered exactly its container, volume and job directory,
  then returned an empty owned inventory. Unrelated resources were not selected.
- SQLite and disposable PostgreSQL drills compare logical records through
  historical-schema upgrades, pre-upgrade backup recovery and current-schema
  fresh restore. Records include coursework, approved grades, calibration,
  immutable identities/reconciliation receipts, usage controls, pending jobs and
  uncertain LMS delivery. Corrupt backups and failed migrations are exercised.
- The initial SQLite schema lacked `grades.export_confirmed_at`; upgrade now adds
  it without marking grades exported, and SQLite schema changes are transactional.
- Real PostgreSQL conformance passed with the reconciliation store contract.
- Reconciliation fixtures exercise current provider-ID verification, configured
  provider instance changes, historical evidence binding, plan expiry/drift,
  competing writes, idempotent receipts, erasure and unchanged coursework.
- Independent review identified a gap between repair duplicate checks and normal
  roster claims; release acceptance includes enforcing the identity invariant
  through both paths, rather than relying on the repair transaction alone.
- The first Linux recovery run exposed a checkout-image entrypoint mismatch
  when that image was reused by the grading-workspace fixture. The image now
  supplies a default command instead; the combined acquisition/workspace/recovery
  Docker suite passed, with an assertion that the crash fixture remains running.
- Local Go vulnerability scanning reported no known reachable vulnerabilities.
  Workflow static analysis passed. CI scans both the application and acquisition
  images and runs the Docker/recovery tests on Linux.

The new checks validate component behavior, not grading quality, production
capacity or absence of every vulnerability. Evidence is synthetic and local/CI;
no OpenAI request or real Brightspace write is part of this release.

## Operational boundaries

Preload the acquisition image before upgrading a worker. Its Git endpoints must
be reachable from the daemon network; symlink submissions are deliberately
rejected. Per-job bounds do not guarantee aggregate host capacity. Crash recovery
requires the same protected work directory/lock identity and all old workers
stopped; legacy unmarked leftovers need manual review. Database drills do not
back up external repositories or notebook homes and are not off-host disaster
recovery evidence.

Identity plans require independently reviewed historical identity evidence, not
just a current username lookup. Existing bound IDs are never replaced. Unknown
repository creation, collaborators and pending invitations remain manual provider
investigation because path-only repository references are not ownership proof.

See `../bounded-checkout.md`, `../runtime-recovery.md`, `../recovery-drill.md` and
`../reconciliation.md` for operator commands and exact supported scope.
