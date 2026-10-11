# Roadmap

Timeline context: GitHub Classroom sign-ups are already disabled; full shutdown
is **August 28, 2026**, with final data deletion September 4. The differentiator
(host-agnosticism + privacy) should land early enough to matter to educators
migrating now.

## Phase 1 — MVP (GitHub only)
The full vertical slice on one host, with the adapter interface and data model in
their final shape:
- [x] GitHub App auth (installation tokens minted on demand)
- [x] Implement `pkg/adapter/github` methods (DispatchGrading intentionally deferred to the runner path)
- [x] Classrooms; assignments from a template repo (individual; group is additive)
- [x] Student self-claim join flow (host OAuth → bind username only)
- [x] Repo provisioning via the idempotent, rate-limited queue (worker drains jobs against the adapter)
- [x] Deadlines (scheduler auto-locks repos at the deadline; manual lock/unlock endpoints for early close / extensions)
- [x] Autograding + score capture (pluggable Runner + Checkout; persists GradingRun & Grade). Sandboxed **container runner** enforces gradingspec.Limits (network/memory/cpu/pids, dropped caps, read-only rootfs); host-exec runner remains as an explicit unsafe/local option
- [x] CSV export of scores keyed by username
- [x] Web dashboard — instructor console (React/TS + Vite in web/); student-facing views are follow-up
- [x] Durable persistence — PostgreSQL store (`internal/store/postgres`, database/sql), selected by `CAIRN_DATABASE_URL` / `CAIRN_STORE=postgres`; SQLite is the zero-config default and in-memory is available for tests. (No build tag: the pgx driver is always compiled in.)
- [x] Operator authentication — host OAuth + username allowlist, cookie sessions, `created_by` attribution; opt-in via `CAIRN_ADMIN_USERS` (open mode otherwise, with a warning)

## Phase 2 — Host-agnostic *(the differentiator)*
- [x] Forgejo/Gitea adapter — working end to end (README; live-validated)
- [x] GitLab adapter — adapter + OAuth resolver + webhook wiring (commit 5388baf)
- [ ] Generalized auth across hosts — remaining polish

*(Checkboxes updated 2026-07-21 to match commits and README; the file had drifted.)*

## Pilot sprint — July–August 2026 (the 1.0-beta gap; see KICKOFF-PROMPT.md)
Deadline context: pilot-ready before the August 28 shutdown; the pilot course
integrates for the full semester from the first assignment.
- [x] GitHub Classroom import (`cairn import ghc`) — the migration path. Reads the
  Classroom REST API directly (no export script), captures a snapshot that outlives
  the September 4 deletion, and binds Cairn to the existing org repos without
  touching GitHub. Export surface verified against a real classroom first; findings
  and the rejected `/grades` endpoint are documented in `docs/ghc-import.md`
- [x] `cairn doctor` self-diagnostics — store reachability (distinguishing
  unmigrated from unreachable), unrecognized/ignored `CAIRN_*` variables, listen
  address, host credentials (`--verify-hosts` proves them against the host),
  webhook config, operator auth, dashboard, container runtime, and a real
  round-trip proving grading can see its work directory. Every failure prints the
  fix; exit status 1 on failure
- [x] One-command deploy verified on a clean VM (docs/deploy.md) — `Dockerfile` +
  root `compose.yaml` + `deploy/docker-compose.yml` (cairn + PostgreSQL, Caddy
  under `--profile tls`). Verified end to end on a bare Ubuntu 24.04 droplet
  2026-08-06: Docker install → build → `docker compose up -d` (2m06s) → `cairn
  doctor` green → classroom created in the dashboard; grading's socket + shared
  work directory confirmed on native Linux; survives reboot with data intact
- [x] Student-facing views completed *(already built — `internal/api/student.go`
  + `studentpage.go`, 7 tests backing isolation/401/404/privilege refusal; the
  checkbox was never ticked. Marked during CC-CA2 after an end-to-end check
  against a real classroom.)*
- [x] docs/migrate-from-github-classroom.md — the afternoon migration guide
  *(CC-CA2, executed against a real classroom before writing)*

**Known, expected behavior, surfaced by CC-CA2's real-classroom run —
not a defect and not unique to that classroom:** a course that graded
outside GitHub Classroom's own Autograder imports with **zero grades**,
because `accepted_assignments[].grade` is simply null for every submission —
there was nothing in Classroom to bring over (202/202 in the tested
classroom, confirmed by Greg as a course that never had grades in Classroom
at all). This will be the common case across migrating courses, not the
exception, and the guide states it plainly rather than treating it as a bug.
**The backfill path has now been tested for real (CC-CA4, 2026-08-09), and
the answer is narrower than the docs first suggested.** Both docs above are
corrected to match:

- **A passed deadline is not what blocks grades.** A repo whose assignment
  closed 2026-02-21 was autograded successfully on 2026-02-24 and carries a
  `90/90` grade — post-deadline runs execute and are recorded. Past-deadline
  repos are not locked, archived, or refused.
- **What blocks it is that Classroom only records a score for autograding
  tests registered in its own UI when the assignment was set up.** Tested
  live on a real past-deadline repo with no autograding configured
  (`INFO-526-SU26/final-project-Gchism94`, Greg's own, not a student's): a
  minimal `classroom.yml` was pushed, `workflow_dispatch` ran, every step
  including the Autograding Reporter succeeded — and
  `accepted_assignments[].grade` stayed **null**. Re-import produced 0
  grades. The test artifact was reverted. So the backfill works only for an
  assignment that already had autograding configured, and is impossible for
  one that never did. Neither case is something Cairn can change; the field
  is Classroom's to populate.

**Grade import fidelity is now verified, closing a gap CC-CA2 left open.**
Its classroom had zero grades, so the happy path was only ever asserted.
Checked against `INFO-523-S26` (11 real non-null grades): ground truth read
from the raw API before import, then compared field by field — **all 11 rows
matched exactly** on `score`, `max_score`, `breakdown.raw`, and
`breakdown.source`.

## UX polish — queued 2026-08-09, from an instructor/student review
Not pilot-blocking, but cheap and worth doing before the pilot term starts.
See `docs/prompts/CC-CA3-invite-link-and-student-landing.md`.
- [x] Copy-invite-link action on each assignment card (the join URL is a real
  route already — `/assignments/{id}/accept` — but nothing in the UI surfaces
  it, so instructors hand-build it) *(CC-CA3, `internal/api/server.go`'s
  `studentRootRedirect` + `AssignmentCard.tsx`'s copy-invite button)*
- [x] Stop serving the instructor login screen at `/` to a student who is
  already signed in. Confirmed in code: a valid student session 401s against
  `/auth/me` (operator-only), so the SPA shows "sign in" to someone already
  authenticated, with no link to `/me` or `/student/login`. *(CC-CA3, scoped
  to exactly `r.URL.Path == "/"` so asset/SPA sub-routes are untouched)*

## Phase 3 — Ephemeral LMS roster agent
Split 2026-08-09 per Greg: the agent is the goal, but it must not be the only
path — not every LMS will be reachable (GitHub Classroom itself never
supported Brightspace), so the manual fallback ships first and independently.
- [x] **CC-CA6 — bulk manual roster entry** (the guaranteed fallback). Single-add
  already exists (`POST /classrooms/{id}/roster`, `RosterPanel.tsx`) but only
  takes one student at a time — not usable for a real class roster. Ships
  first; has no dependency on CC-CA7. *(`POST /classrooms/{id}/roster/bulk`,
  idempotent, per-row results, always-200; client-side email hashing in
  `web/src/roster-parse.ts`)*
- [x] **CC-CA7 — the LMS-roster agent itself.** Open, auditable local agent
  (CLI first; browser-extension DOM-scrape reserved for LMSs with no
  self-serve API token — verify each LMS's actual token-access model against
  current docs before assuming one, per CLAUDE.md's platform invariant).
  Local-only name↔username matching; server receives username (+ email hash)
  only via CC-CA6's bulk endpoint. Depends on CC-CA6 landing first.
  *(`internal/rosteragent`, `cairn roster pull`; Brightspace CSV export
  primary, Valence API secondary — live-verified against a real course that
  the admin-gated API path is correctly gated, per §1 of
  `docs/lms-roster-agent.md`. A same-name collision in `MatchRoster` now has
  a real resolution path — `Match.Resolve`, an interactive numbered prompt
  in `cairn roster pull`, and a middle-initial tiebreak that narrows
  (never promotes to exact) — fixed in CC-CA10/CC-CA11. Known follow-ups,
  not yet fixed: initials are compared positionally, so a tie with
  reordered initials (`"Jane A. B. Doe"` vs `"Jane B. A. Doe"`) still falls
  back to ambiguous instead of narrowing. Matching was ASCII/case-folding
  only, so an accented name (e.g. "José") wouldn't match at all — fixed in
  CC-CA13/CC-CA14 (2026-08-10): `normalizeName` now folds diacritics via a
  Unicode NFD-decompose/strip-marks/NFC-recompose pass
  (`golang.org/x/text`), verified against both precomposed and decomposed
  input forms and against the initial-narrowing tier.)*

## Hardening and CI — 2026-08-09
- [x] **CC-CA1 — gVisor isolation tier for grading.** `CAIRN_GRADER_ISOLATION`
  (`shared` default / `gvisor`), refused rather than silently downgraded when
  unavailable; `cairn doctor` checks the daemon's registered runtimes.
  `ExtraArgs` smuggling a `--runtime` override, and the doctor check
  misreading a podman deployment, are both fixed (CC-CA10/CC-CA11): the
  runtime escape is now a full deny-list covering every hardening flag
  `ExtraArgs`'s append-last position could otherwise override
  (`--privileged`, `--cap-add`, `--user`, `--network`, `--memory`,
  `--memory-swap`, `--pids-limit`, `--cpus`, `--security-opt`,
  `--read-only`, `--runtime`), and podman gets its own gVisor-availability
  message (honest "not verified" rather than a false "not registered") plus
  its own `runtimeFix`/mount-round-trip advice instead of Docker's. Known
  follow-ups, not yet fixed: the deny-list is a static map with no shared
  source of truth against `buildRunArgs`, so a newly-added hardening flag
  there won't automatically get denied; and `GradingConfig.Runtime` is
  free-form, so a typo like `"Podman"` silently falls through to Docker's
  advice instead of podman's.
- [x] **CC-CA8 — frontend test runner.** Vitest stood up; backfilled the
  `roster-parse.ts` and `api.inviteUrl` tests that CC-CA3 and CC-CA6 could
  only verify by hand. No CI existed yet to run them automatically — see
  CC-CA9.
- [x] **CC-CA9 — CI.** `.github/workflows/ci.yml`: Go (`build`, `vet`,
  `gofmt`, `test -race`) and frontend (`typecheck`, `test`, `build`) on
  every push/PR. Three consecutive green runs on real commits as of
  2026-08-09 (CC-CA12); `main` branch protection deliberately not yet
  enabled — the repo's current direct-to-main workflow (no PR process) means
  requiring status checks would gate every push, not just merges, so this
  waits on a workflow decision rather than a technical bar.
- [x] **CC-CA10/CC-CA11 — close the flagged follow-ups.** Two rounds
  systematically closing gaps each prompt's own execution report named but
  didn't fix: the `ExtraArgs` escape hatch and podman detection (CC-CA1,
  above), and the same-name-collision resolution UI (CC-CA7, above).
- [x] **CC-CA13/CC-CA14 — Unicode/accented-name matching (CC-CA7 follow-up).**
  `normalizeName` now folds diacritics (NFD-decompose, strip `unicode.Mn`
  combining marks, NFC-recompose) so an accented LMS name matches an
  unaccented Git-host profile name at every tier, including the CC-CA11
  initial-narrowing. Verified against both precomposed and decomposed
  Unicode forms of the same visible name, since an LMS export and a Git
  host have no reason to agree on which form they use.

## Phase 4 — Hosted + LMS integration *(stretch)*
- [ ] **Multiple GitHub App installations (multi-org support).** The App
  registration itself (App ID + private key) is one-time and reusable across
  orgs, but each org needs its own installation, and `cmd/cairn/main.go`
  currently reads exactly one `CAIRN_GITHUB_INSTALLATION_ID` env var — Cairn
  as built can only run against one installation at a time. Fine for the
  single-org pilot; becomes a real gap the moment one Cairn instance needs to
  serve more than one org/department. Needs a per-org installation-ID lookup
  (keyed by org or classroom) instead of a single env var — a likely
  prerequisite for the multi-tenant item below, not just adjacent to it.
  Surfaced 2026-08-10 while wiring up the pilot droplet's GitHub App.
- [ ] Multi-tenant hosted offering (scoped data processor)
- [ ] LTI 1.3 Names-and-Roles (NRPS) roster sync
- [x] LTI Assignment-and-Grade Services (AGS) grade passback — opt-in resource-link launches, explicit learner binding, instructor-approved numeric delivery and readback; local simulator tested. Brightspace administrator registration and real-tenant acceptance remain pending (`docs/brightspace.md`).

## Computing continuity (2026-10-06)

- [x] Optional operator-configured per-class browser workspace origins in the
  authenticated student work view. No credential or learner data in the link;
  destination owns authentication and enrollment. See `docs/course-workspaces.md`.
- [ ] Accept a real course against the Waypoint Python/R workspace pilot:
  institutional OIDC, wildcard HTTPS, resource capacity, off-host restore, and
  roster revocation. A local synthetic acceptance is not a production launch.
- [ ] Close the audit's instructor-owned grading manifest and operator resource
  ceiling findings before using learner submissions for authoritative grades.

## Instructor grading authority (October 7, 2026)

- [x] Read the grading manifest from a pinned instructor template commit.
- [x] Provide a read-only instructor test tree, policy provenance, and operator
  per-step resource ceilings; exercise forged-manifest and Docker regressions.
- [x] Add operator-only policy selection and its dashboard controls.
- [ ] Review course harnesses for learner-controlled imports/self-reporting,
  aggregate budgets, filesystem/output bounds and kernel isolation before use.

### October 7 follow-up

- [x] Pin automatic push grading to the delivered submission commit and expose
  commit provenance in student history; fail/retry configured webhook setup.
- [x] Local assessment review foundation: pinned source capture, fixture/imported
  proposals, editable rubric scores/feedback, atomic instructor publication,
  student feedback, freshness checks and assessment retention linkage (October 7).
- [x] Bounded OpenAI adapter for rubric feedback **plus proposed scores**: explicit
  classroom allowlist, durable admission/usage accounting and pause control,
  citation validation, pending proposals and instructor-only publication. Four
  live synthetic calls and the browser approval/student path passed (October 7).
- [x] Repeatable synthetic evaluation CLI, two-trial score/assessability metrics,
  failure retention and offline human-review packet with export/import. Baseline:
  17/18 valid proposals, 31/34 provisional criterion matches; human review pending.
  See `docs/assessment-evaluation.md` (October 7).
- [x] Safe proposal failure categories, corpus-bound report verification and
  a separate missing-work policy decision in the offline review packet. Human
  scoring remains pending (October 7).
- [x] User-selected no-relevant-work policy: unassessable until instructor review.
  V2 calibration rubrics encode the policy; V1 reports and references remain
  readable. This does not modify any production course rubric.
- [x] Course capture now binds the unassessable policy; new proposals declare
  submission relevance and provider uncertainty uses a fixed level plus evidence
  reason. Legacy reports remain reviewable (October 8).
- [x] Frozen transfer suite across new tasks and Python/R formats: 17/18 valid
  proposals, 34/34 provisional matches, one rejected citation-location failure.
  Human ratings remain 0/34; see `docs/evaluations/2026-10-08-transfer-v1.json`.
- [ ] Investigate the rejected multi-file citation before course acceptance;
  the successful second trial does not resolve the first failure.
- [x] Per-instructor calibration projects: uploads and pinned existing Cairn
  submissions, explicit model transmission, reviewed scores/feedback, frozen
  owner/rubric/model profiles and fresh-sample follow-up rounds. Only approved
  guidance is applied to future captures; historical work never publishes grades.
- [ ] Each instructor's representative calibration and course destination/retention acceptance;
  broader format extraction, production queuing and billing-aware budgets remain
  follow-ups. See `docs/llm-assessment.md`; production provider activation is off.

### Historical calibration intake (October 9, 2026)

- [x] Private offline sample preparation, source inspection packet and atomic
  owner-scoped bundle import into an empty draft; calibration/holdout membership
  and exact-source overlap checks against prior profiles.
- [x] Passive Quarto/R Markdown extraction for narrative and code feedback.
- [ ] Instructor review of real historical sources/rubric interpretation, followed
  by generated proposals, calibration corrections and fresh-sample evaluation.
  Prepared source alone does not establish grading quality or course acceptance.

### Prospective clustering assignment contract (October 10, 2026)

- [x] New v2 student template: consistent 95 functional + 5 quality points,
  specified cleaning/default/constant-column behavior, fixed density cutoff
  calibrated separately from evaluation, optional membership uncertainty,
  synthetic notebook data and regression checks. CI guards generated materials
  and release digests. See `templates/clustering-v2/README.md`.
- [x] Connect private execution evidence and delegated adjudications to the
  instructor review UI with explicit authorship; they must not silently become
  independent instructor reference judgments or approved calibration profiles.
  Local synthetic browser and service checks verify explicit attachment/review,
  owner isolation, persistence, and no automatic model request or grade.
- [ ] Verify a real instructor/course adoption of the new assignment version.
  Local synthetic validation does not publish or accept it for a live class.
