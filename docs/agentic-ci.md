# EduCloud CI automation

Cairn is the reference implementation. Deterministic CI checks run on normal
pull requests without an AI credential. Model review, repair, and test drafting
are implemented but **disabled until configured and rehearsed**. This document
does not claim a hosted AI run, successful automated repair, or ecosystem-wide
activation.

## What was missing and what is now built

The October 10, 2026 repository inspection found ordinary test/build CI in Cairn
and Belay, a milestone review workflow in Portage, and workspace recovery tests
in Waypoint. Outfitter and the EduCloud umbrella had no workflow. None had the
four proposed automation loops.

| Capability | Cairn reference | Recommendation |
| --- | --- | --- |
| Bounded CI repair | Failed CI can create a structured patch; separate jobs validate, test, and optionally publish it | Enable proposals first, then publishing after a synthetic failure rehearsal |
| Adversarial review | Successful CI triggers structured findings for opted-in PRs; manual review is also available | Keep deterministic security checks required; treat model findings as evidence to investigate |
| Regression drafting | Changed Go statement coverage is captured; manual regression mode drafts new test files and runs the candidate suite | Review behavioral value and prove bug-before/fix-after where relevant; do not chase 100% path coverage |
| Performance | Base and candidate use identical harness/toolchain/runner, six samples, timing and allocation metrics, CPU/memory profiles | Start with warnings; establish representative load/query benchmarks before hard latency gates |

The existing Go race, vet, build, PostgreSQL conformance, dashboard, and
curriculum checks remain. CI adds reachable Go vulnerability scanning,
high-or-critical npm dependency auditing, a separately built container scan for
high/critical OS and embedded-binary vulnerabilities, workflow linting, automation boundary
tests, and bounded fuzzing of evidence path confinement, score bounds, and LTI
service URL restrictions. These controls do not prove that a component has no
security vulnerabilities.

## AI workflow

`.github/workflows/agentic-ci.yml` uses `workflow_run` from the ordinary `CI`
workflow and `workflow_dispatch` with a CI run ID. Every execution checks the
live PR before proceeding:

- It must be open, ready for review, from the same repository, and authored by
  an owner/member/collaborator. Forks and default-branch changes are ineligible.
- A maintainer must have applied the `agentic-ci` label.
- The failed/completed run must be the ordinary pull-request CI workflow at the
  exact current PR head. Stale runs fail closed.
- Repair requires a failed run. At most two consecutive automated repair
  commits are permitted; the commit trailer records the attempt. Rerunning an
  agent job is rejected. A deliberate new manual dispatch can make another
  proposal on the same head, so this is **not a global API-spend quota**.

The trusted controller is loaded from the default branch. It reads bounded
source snapshots and the diff as Git objects, plus failed logs with additional
redaction and size limits. It makes one Responses API request with structured
output, no tools, no shell, no retries, a 12,000-output-token limit, a 100,000
character context limit, and a 180-second request timeout. It does not execute
candidate code in the credential-bearing job. Review findings must include a
location, reachable trigger, impact, confidence, severity, and validation plan.
We do not ask the model to invent a CVSS score. Structured output constrains the
format; it does not establish correctness. [OpenAI structured outputs](https://developers.openai.com/api/docs/guides/structured-outputs)

Repair and regression patches are limited to eight regular source files under
`internal/`, `pkg/`, `cmd/`, and `web/src/`, at most 100 KB. Deletions, renames,
symlinks, workflow/policy/configuration/dependency/migration edits, and changes
to existing tests are rejected. Regression mode can only add new test files.
A separate runner has no OpenAI key, App token, write permission, or persisted
Git credentials; it applies the patch and runs the Go/PostgreSQL/dashboard/
curriculum suites. A malformed patch or failed suite stops automatically and
requires inspection; the system does not repeatedly guess against a broken
candidate.

The optional publisher downloads the original proposal, revalidates it, checks
that the PR and failed run still match, using a narrowly scoped
GitHub App token issued after independent patch validation. It runs no candidate code. A normal signed-off commit and
non-force push reject concurrent branch movement. There is no automatic merge
or default-branch push. App-authenticated pushes are used so new checks run;
`GITHUB_TOKEN` pushes are not used for this loop. Two repair commits without a
successful CI outcome exhaust the automatic loop. Existing GitHub notification
preferences still apply: this workflow cannot promise to suppress the original
CI failure notification.

A successful CI run requests an advisory review. `review` and `performance`
modes cannot emit a patch. `regression` produces a verified artifact for human
review, without publishing a supplementary commit automatically. The artifact
includes the proposal, frozen head/run metadata, usage, and collected evidence,
with a seven-day retention period. No automated PR comments are necessary to
use it.

## Performance and coverage interpretation

`tools/ci/bench/assessment_test.go` currently measures assessment validation at
one and 32 criteria. CI copies the same harness onto the base revision and runs
both on the same host/toolchain. Timing, bytes, and allocations warn when their
median increases more than 15% **and** the difference exceeds three times the
sum of each sample's median absolute deviation. This is a noise guard, not a
statistical significance test. The report includes all measurements; noisy
results need another controlled run. CPU and memory profiles and a textual CPU
summary are saved. No benchmark threshold currently blocks merge.

Coverage guidance lists uncovered Go statement blocks that intersect changed
lines. This is not branch coverage, path coverage, semantic completeness, or a
coverage gate. Frontend coverage and database query-count/load baselines still
need workload-specific additions. Generated tests must demonstrate an invariant
or real regression; a passing generated test alone is not bug-before/fix-after
proof. `regression` mode records that proof plan for reviewer follow-through.

## Activation and rehearsal

No repository secrets were present when inspected. Do not reuse the course
assessment API key: CI source analysis should have a separate OpenAI project
with an explicit budget/rate limit and no student data or production secrets.
Set:

1. Secret `EDUCLOUD_CI_OPENAI_API_KEY` for that project.
2. Variable `AGENTIC_MODEL` to an explicitly selected model that supports the
   Responses API structured-output request used by the controller. There is no
   hard-coded legacy or guessed model identifier.
3. Variable `AGENTIC_CI_ENABLED=true`, leaving `AGENTIC_CI_PUBLISH` unset/false.
4. Create the maintainer-controlled `agentic-ci` label. First use manual
   `review`, then `regression`, on a small synthetic feature PR. Inspect the
   source/data sent, structured output, token usage, and candidate tests.
5. For publishing, install an EduCloud CI GitHub App on **Cairn only**, with
   Contents write, Actions read and Pull requests read, no administration/workflow/secrets
   access and no protected-branch bypass. Set variable `EDUCLOUD_CI_APP_ID` and
   secret `EDUCLOUD_CI_APP_PRIVATE_KEY`.
6. Rehearse a deliberately broken synthetic PR: verify exact-head rejection,
   policy rejection, a passing repair candidate, fresh CI on the App commit,
   two-attempt exhaustion, and a concurrent-push rejection. Only then set
   `AGENTIC_CI_PUBLISH=true`. Do not make the model's advisory review a required
   approval or allow it to merge.

For manual runs choose `review`, `regression`, `performance`, or `repair`, and
supply the current ordinary CI run ID. Disable new AI work by setting
`AGENTIC_CI_ENABLED=false`; disable only publication with
`AGENTIC_CI_PUBLISH=false`. Cancel any already-running job when shutting down.
The controller intentionally uses a bounded structured request instead of
executing agent-generated commands with secrets. The Codex Action is another
supported runner option, but would require additional sandbox/privilege
verification before substitution. [OpenAI Codex Action guidance](https://learn.chatgpt.com/docs/github-action)

## Ecosystem rollout

Other repositories are unchanged in this build. Carry over the trust boundaries
and separate publisher, then adapt deterministic checks and allowlists to each
component. Start Belay with pytest/PostgreSQL, coverage.py and bounded Hypothesis
invariants; Portage with current routing/usage tests and a replacement for its
stale milestone-review trigger; Waypoint with identity/recovery invariants and
host lifecycle benchmarks. Establish baseline test/security CI in Outfitter
before any autonomous repairs. The umbrella needs documentation/link/contract
checks rather than artificial application coverage targets. Each repository
needs its own enabled configuration, scoped App installation, meaningful
benchmark workload, and synthetic failure rehearsal.

## Local verification

```sh
python3 -m unittest discover -s tools/ci -p 'test_*.py' -v
actionlint .github/workflows/ci.yml .github/workflows/agentic-ci.yml
go test ./tools/ci/bench -count=1
go test ./tools/ci/bench -run '^$' -fuzz '^FuzzArtifactPathConfinement$' -fuzztime=15s -parallel=2
go test ./tools/ci/bench -run '^$' -fuzz '^FuzzGradeBounds$' -fuzztime=15s -parallel=2
```

The local boundary suite and bounded fuzz runs validate deterministic behavior.
They are not a hosted provider, GitHub App, or automatic-repair acceptance test.
