# Cairn testing readiness

Testing package `educloud-testing-v1`, 2026-10-11. This is an operator checklist
for the next candidate, not an assertion that production testing is complete.
The accepted starting code is PR #28 (`52ad665`), following the operational
recovery/reconciliation work in PR #27. Record the exact new candidate and build
hashes when testing subsequent changes.

## Choose the first test

Start with synthetic coursework in a disposable local deployment. Use one
instructor and two learners, an isolated Git organization and one assignment.
The repeatable cross-component runner lives in Waypoint:
[RUNBOOK](https://github.com/EduCloud-Ecosystem/waypoint/blob/main/course-workspace/rehearsal/RUNBOOK.md),
[fillable acceptance worksheet](https://github.com/EduCloud-Ecosystem/waypoint/blob/main/course-workspace/rehearsal/TESTING-CHECKLIST.md).
It builds from an explicitly selected Cairn checkout and accepts
`--cairn-binary`, `--cairn-source`, `--lti-binary` and a fresh `--output` directory.
Its local subset exercises private Forgejo OAuth/checkout, signed pushes,
grading, review, simulated LMS delivery and restart persistence. The Linux
runner additionally executes notebook Python/R, isolation, revocation, saved-file
stop/resume and bounded admission.

No OpenAI API key is needed for those deterministic fixtures. Their reviewed
proposals and score deliveries are transport/workflow evidence, not evidence
that model feedback is correct. The [source-first calibration packet](calibration-readiness.md) freezes review inputs.
Instructor-specific historical calibration, adjudication and permission to use those submissions stay separate. Keep work
unassessable until instructor review when assessment evidence is insufficient;
never infer a zero or approve a proposal merely to unblock a test.

## Candidate checks

Run from the Cairn repository root with the Go version in `go.mod` and Node 22.
These are commands to execute for the candidate, not recorded results:

```sh
go build ./...
go vet ./...
go test -race ./... -count=1
npm --prefix web ci
npm --prefix web run typecheck
npm --prefix web test
npm --prefix web run build
go build -o /tmp/cairn-pilot ./cmd/cairn
go build -o /tmp/cairn-lti-pilot ./cmd/lti-simulator
docker build -f Dockerfile.checkout -t cairn-checkout:local .
```

Use `.github/workflows/ci.yml` for the separate PostgreSQL conformance, security,
coverage and performance checks applicable to the candidate; use its disposable
service configuration, never a production `CAIRN_TEST_DATABASE_URL`. Check every
required job and exact head revision. A green build alone is not a security audit
or hosted acceptance. Generated review/test suggestions do not replace the
required tests or reviewer decision.

Run populated upgrade/backup/restore tests before accepting migrations:

```sh
go test -race ./tools/recovery -count=1 -v
CAIRN_RECOVERY_POSTGRES=1 go test -race ./tools/recovery -count=1 -v
```

The second command creates its own disposable PostgreSQL container. See
[recovery-drill.md](recovery-drill.md) for preserved logical records, corruption
controls and interrupted-run inventory. Do not use live coursework for the drill.

Before starting a hosted candidate, take a verified pre-upgrade database backup,
record binary/image/configuration versions, and run `cairn doctor` with the
intended deployment environment while the server is stopped so its listen
address can be checked. Doctor opens the configured store (which can apply
normal migrations) and may start a short-lived grading container; it is not a
backup or migration dry run. `cairn doctor --verify-hosts` adds read-only provider
credential checks. Keep the checked-out acquisition image preloaded; there is
no permission to fall back to unbounded host checkout.

## Minimal instructor test procedure

Record each outcome and evidence reference in the private acceptance worksheet.

1. Sign in as instructor, create/select the synthetic classroom and assignment,
   and enroll two independently authenticated learners. Verify immutable provider
   IDs. Inventory legacy records with [reconcile](reconciliation.md) rather than
   treating a current username lookup as historical identity proof.
2. Each learner submits different work. Confirm only their own repository,
   submission, grade and feedback are accessible. Submit a changed revision and
   confirm replayed webhooks do not create duplicate work or alter the wrong grade.
3. Check a passing, failing and unassessable submission. Review rubric evidence,
   proposed score and feedback as instructor, approve only the intended current
   version, then check the learner view. Stale source/rubric/grade versions must
   not silently replace the current result.
4. Restart Cairn and verify the approved grade, feedback, identities and delivery
   state survive. Inspect uncertain/failed job state before any retry.
5. Complete the local LTI simulation in [brightspace.md](brightspace.md). Verify
   approved grade and numeric zero, pending/unassessable refusal, wrong-user
   isolation, uncertain-delivery reconciliation and remote-conflict refusal.
6. Review automated accessibility scans and keyboard behavior for the actual
   built dashboard. Separately complete human screen-reader, 200%/400% zoom and
   narrow/mobile checks through sign-in, review, approval and learner feedback.
7. Review distinct-user load and available sampled memory/backlog metrics on the
   tested hardware. The synthetic 5/15-user HTTP test is not a concurrent grading
   capacity result. SQL query counts remain uninstrumented; collect DB-side
   diagnostics separately if latency/backlog suggests an issue.

Wrong-user access, incorrect grade writes, lost submissions, failed recovery,
essential accessibility barriers or missing required evidence mean **NO-GO** for
the affected scope. A clean synthetic result permits the next staged test. It
does not enable a real course, promote a model/calibration version or establish
production Brightspace support.

## Hosted prerequisites and rollback

Supply a dedicated worker and separate replacement worker, an off-host backup
repository, independent recovery-key custody, TLS/DNS, approved identity setup,
operator/instructor owners and explicit recovery objectives. Follow Waypoint's
[hosted/recovery checklist](https://github.com/EduCloud-Ecosystem/waypoint/blob/main/course-workspace/rehearsal/TESTING-CHECKLIST.md).
Actual institution-account access and target-host recovery/load tests cannot be
substituted by CI or simulated credentials. Real grade passback additionally
requires the Brightspace administrator worksheet below.

If a candidate fails, stop admission/grade sends and preserve the database,
receipt IDs and private logs. For stopped-worker orphan cleanup, follow the
reviewed plan/apply procedure in [runtime-recovery.md](runtime-recovery.md);
do not remove lock files or use daemon-wide prune. Never recover resources while
workers are running or force-retry an uncertain remote score.

Restore a verified pre-upgrade backup to a fresh database using its matching
application version; reconcile any writes after that backup before switching
traffic. Do not downgrade a migrated live database in place. Disable the LTI
configuration and restart Cairn to suspend that integration if required, keeping
binding/delivery records intact for reconciliation. Disabling LTI does not undo a
grade already written to Brightspace.

For triage record candidate SHA/hash, scope, failed step, UTC time, expected versus
actual behavior, affected synthetic records, cleanup status, containment and
owner. Attach only minimal redacted evidence to public issues; raw fixture
folders, private coursework, OAuth state, tokens and signing keys stay private.
