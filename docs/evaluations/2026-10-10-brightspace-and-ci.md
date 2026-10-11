# Cairn local completion and security verification — October 10, 2026

## Delivered scope

Opt-in LTI 1.3 resource-link launches and AGS numeric grade delivery are implemented
for Brightspace registration, with a loopback synthetic LMS. Instructor mapping,
learner connection, current instructor-approved grade selection, durable delivery
leases, explicit retries and score readback are exercised locally. No real LMS,
student grades, OpenAI calls or hosted deployment were used for this verification.

The Cairn CI reference adds deterministic vulnerability, workflow, property,
changed-statement coverage and matched performance checks. Model review, bounded
repair and regression drafting are implemented behind disabled repository flags.
They have not been exercised with a hosted model or GitHub App. Other EduCloud
repositories were inspected but remain unchanged. See `../agentic-ci.md`.

## Security findings and fixes

A frozen source review at `e5ab96ab90fe7e2d18fd5893201925c6b0ac6991`
identified three medium-severity findings. The reviewed production source included
authentication, provisioning, grading, assessment, persistence, LTI and adapters;
auxiliary fixtures/history were not exhaustively audited. This is a bounded
source review, not a claim that the entire system is free of vulnerabilities.

| Finding | Remediation and regression evidence |
| --- | --- |
| OAuth login state was not tied to its initiating browser | All three starters bind a private browser cookie; callback checks precede code exchange. Missing/wrong browser, expiration and replay tests pass. |
| Assignment slug plus username could select another submission's repository | Names derive from immutable submission IDs; provider adapters refuse existing/racing resources; persisted matching ownership precedes grants. Collision, orphan, binding failure and provider race tests pass. |
| Recycled usernames could inherit coursework | OAuth stable IDs persist in roster bindings; atomic updates refuse replacement or removal. Student work and LTI connection require a matching ID. Legacy claimed/imported work fails closed; recycled operator usernames are also rejected. |

Writable execution now uses a 512 MiB / 65,536-inode tmpfs volume, reused across
steps with a bounded keeper container. The host source is read-only during copy
and absent from grading containers. Real Docker testing verified ENOSPC at the
bound, setup persistence, unchanged source and removal of keeper/volume. The
existing live instructor-policy isolation test also passed. Clone acquisition
still happens before this boundary: hosted workers require quota-backed checkout
storage and recovery procedures for host/daemon crashes. See `../deploy.md`.

The CI privilege split was separately reviewed. Candidate code runs only in a
job without model credentials or repository write authority. The publisher
reapplies the original bounded patch, checks the live PR/head and uses a normal
push. Input/output reads are bounded; current-branch, fork, path, existing-test,
symlink, size, timeout and publication constraints have deterministic tests.

## Validation results

- Full Go race suite with statement coverage, `go vet ./...`, `go build ./...`,
  formatting and diff whitespace checks passed.
- Memory/SQLite conformance and real disposable PostgreSQL 17 conformance passed,
  including concurrent immutable identity claims and durable LTI records.
- Dashboard typecheck, 25 tests and production build passed.
- Curriculum contract check and six publication tests passed.
- Fifteen CI controller boundary/analysis tests and Actionlint 1.7.12 passed.
- Bounded fuzzing passed for assessment path confinement, grade bounds and AGS
  service URLs. An empty-fragment URL discovered during fuzzing was fixed and
  its reproducer retained.
- `govulncheck` reported no known vulnerabilities after Go 1.26.9 and x/text,
  x/sys dependency updates. npm audit reported zero vulnerabilities. Gitleaks
  found no secrets in the complete proposed diff. These scanners have limited
  detection scope and are not substitutes for source review.
- Six same-toolchain samples on base and proposed source produced no regression
  warning for one- and 32-criterion assessment validation. Allocation medians
  were unchanged. Timing was noisy; no speedup claim is made. CPU/memory profiles
  were generated. These are microbenchmarks, not classroom load/DB-query tests.
- Final Docker image built and served its health endpoint and dashboard locally.

## Browser rehearsal

The secured API was run with a new SQLite fixture and three separate browser
contexts: instructor, learner A and learner B. Each completed a signed LMS
launch and explicit connection. Instructor-approved 8/10 was delivered for A
normally. The simulator then saved B's 8/10 and dropped its response; Cairn
recovered through readback and displayed verified after one attempt. Both
separate score records were visible in the simulator. This validates the local
protocol path and UI, not Brightspace tenant acceptance or grading quality.

Private local evidence is under `output/`, including browser screenshots,
Go/PostgreSQL/scanner logs and profiles. No browser session files, private keys
or course data were committed.

## Remaining activation requirements

1. Brightspace administrator registration, HTTPS deployment, sandbox course and
   real-tenant acceptance with an instructor and two test learners.
2. A separate budgeted CI OpenAI project and narrowly scoped GitHub App, followed
   by a synthetic repair rehearsal before enabling automated publication.
3. Hosted notebook/grading worker, bounded clone storage, off-host backups and
   restore/load acceptance. This turn completed local implementation; it did not
   deploy or certify that infrastructure.
4. Existing installations need deliberate stable-ID reconciliation for legacy
   claimed/imported roster rows. Repository creation with an uncertain outcome
   now leaves an orphan for operator reconciliation instead of silently adopting
   it. Preserve existing records and backups; do not clear identities to bypass
   these checks.

NRPS roster sync, deep linking, bulk passback, automatic conflict overrides and
unreviewed grade publication are outside this initial release.
