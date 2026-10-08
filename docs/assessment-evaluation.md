# Synthetic assessment evaluation and human review

`cairn assessment-eval` evaluates the **existing** extraction → bounded OpenAI →
private pending proposal path in a dedicated SQLite database. It never connects
to the serving database, real Git repositories, enrolled learners or grade
publication. The bundled 12-case corpus is hand-authored synthetic material with
**agent-authored provisional references**, not instructor-scored ground truth.
No claim of fairness or course readiness follows from matching these references.

## Selected missing-work policy

On October 7, 2026, the user selected: **“Mark it unassessable until instructor
review.”** The selection applies to a readable submission with no relevant
substantive answer, including prompt-injection-only content. The V2 calibration
rubric now instructs the model to return `points: null` for every criterion in
that situation. Incorrect or partial substantive attempts still follow the
point rules for answered and omitted parts. Missing/unreadable/unsupported files
continue to block extraction; they are never silently scored zero.

The October 7 policy was implemented in `synthetic-eval-v2`'s instructor rubric, which is
captured, hashed and sent through the existing provider path. It does not silently
change any stored course rubric, global provider prompt or published grade.
Normal publication still requires an instructor to resolve null criteria with
finite reviewed scores and citations. The decision does not supply the still-
pending human score/feedback ratings.

New runs use V2. `synthetic-eval-v1` and its original corpus digest are preserved
for old reports and worksheets. The V2 rubric also makes the existing rule clear
that worked calculations cannot be demanded unless the rubric requires them.
The source cases and provisional score references are unchanged. A recheck on
these same examples is a **development regression**, not a held-out benchmark.

## Run and reproduce

Build `go build -o ./bin/cairn ./cmd/cairn`. Create the parent output directory
with `mkdir -p output`. Every run needs a **new** output directory; existing ones
are refused to preserve evidence and uncertain usage. Offline is the default:

```sh
./bin/cairn assessment-eval run --out output/evaluation-offline
```

This captures/extracts all cases without loading a key or making model calls.
Missing, unsupported and malformed files must block extraction. To explicitly
send only the bundled synthetic source through the configured private key:

```sh
./bin/cairn assessment-eval run --live --trials 2 \
  --key-file /absolute/private/path/cairn-openai.env \
  --out output/evaluation-live
```

One or two trials are supported: at most 9 or 18 sequential paid requests.
Provider, prompt and limits are the same as `docs/llm-assessment.md`, including
no automatic retry and one attempt per capture. The isolated database retains
its daily reservations and outcomes. **Separate evaluations have separate
budgets**; repeated manual runs are additional paid work, not bounded by a
serving instance's daily quota. Reported usage is not a billing reconciliation
or dollar cap. No model override or external/private corpus loading is supported
by this first CLI.

Artifacts live in an owner-only directory; individual files are owner-only:

- `report.json`: source/extraction/proposals, rubric, provisional references,
  corpus digest, model/prompt provenance, usage, latency and comparisons.
- `evaluation.db`: durable captures and usage reservations. A crash or interruption
  may leave an uncertain attempt; never assume an interrupted request was free.
- `review.html`: offline evidence/feedback viewer and human-review form.
- `human-review.json`: blank, report-digest-bound human worksheet.

Progress is saved atomically after each trial. A save failure stops further
calls. Provider/structural failures are retained, never counted as successful
proposals, and yield a nonzero exit after writing the packet. Score disagreements
are reported, not silently fixed or converted to a pass/fail course threshold.
A crash does not automatically resume/retry: inspect the database and partial
report before deliberately authorizing another paid run.

To regenerate a packet from a saved/partial report without another API call:

```sh
./bin/cairn assessment-eval packet \
  --report output/evaluation-live/report.json --out output/review-packet
```

## Human review

Open `review.html` directly in a browser. Read the rubric and captured evidence
before opening the collapsed model feedback. Enter independent assessability
and points, whether citations actually support the feedback, whether feedback
is useful, whether uncertainty is appropriate, and a rationale for each criterion.
The missing-work policy field records `zero` or `unassessable` plus a rationale,
reviewer and time. It starts undecided and is separate from the criterion ratings.
It is a calibration decision record only: exporting it does not change the
provider prompt, frozen references, course rubric or grades. An exported worksheet alone cannot implement a choice; V2 implements the user-selected
unassessable policy in its calibration rubric. Legacy worksheets
without these fields remain valid but show policy review as incomplete.
Provisional reference scores are collapsed at the bottom. The page has no external
scripts, analytics or provider calls; evidence/feedback are HTML-escaped.

Export the worksheet to save work; it is **not** stored automatically in browser
storage. Use **Resume a saved worksheet** to continue later. Imports must match
the exact report digest. Do not overwrite a completed worksheet with the blank
one generated for a new packet. Validate the exported file locally:

```sh
./bin/cairn assessment-eval review \
  --report output/evaluation-live/report.json \
  --review /path/to/exported/human-review.json
```

The summary distinguishes completion from score/evidence/feedback disagreement.
The summary reports policy-review completion, score-review completion and provider
failures separately. Blank or partial entries remain incomplete, `null` remains distinct from zero,
and stale/duplicate/out-of-range reviews fail validation. Reviewer identity is
locally asserted, not authenticated. Even a fully completed review is neither
course acceptance nor authorization to publish grades. Failed provider trials
remain recorded failures outside the successful-proposal review denominator.

## Baseline observed October 7, 2026

The two-trial live run used `gpt-5.4-mini-2026-03-17` and `cairn-rubric-v1`.
The [frozen result summary](evaluations/2026-10-07-synthetic-baseline.json) records
the corpus/report digests, per-trial results and measured counters. Raw reports,
private SQLite data and browser-QA worksheets remain outside version control.

| Measure | Observed |
| --- | --- |
| Trials | 24: 18 provider attempts and 6 expected extraction blocks |
| Valid pending proposals | 17/18 provider attempts |
| Structural/provider failure | 1 `invalid_proposal`, Python-source trial 2 |
| Exact provisional criterion matches | 31/34 in accepted proposals |
| Numeric mean absolute point error | 0.067 points over 30 numeric pairs |
| Assessability disagreements | 2 criteria: zero versus provisional `null` |
| Score/assessability stability | 6/8 cases with two successful proposals |
| Reported input/output tokens | 8,224 / 5,575, including the failed attempt |
| Valid citation locations | 37; semantic support still needs human review |
| Actual human ratings | 0/34; not instructor-calibrated |
| Published grades | 0 |

The failed proposal was rejected before storage/publication and not retried.
The baseline adapter exposed only `invalid_proposal`, so this historical run cannot
identify the precise validation rule that rejected it. Subsequent diagnostics now
record bounded categories such as `invalid_proposal_citation_location`,
`invalid_proposal_citation_missing`, `invalid_proposal_points_range`,
`invalid_proposal_criterion_coverage` and `invalid_proposal_feedback_bounds`.
Unknown validator errors remain generic. Raw rejected responses, source, filenames
and credentials are not added to these diagnostics. Usage reservations and the
no-retry behavior are unchanged. The packet displays recorded failure codes;
the older baseline retains its original generic code.

Two repeated cases varied. In the mixed prompt-injection case, one trial awarded
2/4 for a correctly stated original mean; another gave 0/4 because it wanted
worked calculations the rubric did not require. Injection-only content received
zeros in one trial and unassessable/null in the other. The reference's null policy
needs human confirmation: the rubric says missing parts receive zero while the
provider instruction permits unassessable judgments. That ambiguity must be
resolved deliberately, not hidden by choosing whichever reference matches.

Other observations: source-only notebook extraction ignored conflicting correct
outputs; terse Unicode wording received the same points as fuller prose. Some
responses used “Low confidence” while plainly identifying incorrect answers, or
introduced uncertainty about work not required by the rubric. These are findings
from a tiny synthetic sample, not robustness or accessibility guarantees.

## Acceptance and next work

1. An instructor reviews the packet and establishes the missing-evidence versus
   zero policy, expected partial credit and useful feedback standards.
2. Separate development examples from a held-out, permissioned instructor-scored
   set covering varied assignments, formats and ambiguous/partial submissions.
   Do not tune the prompt to this fixed smoke corpus and call it validation.
3. Agree course-specific tolerances for score disagreement, evidence support,
   uncertainty, latency and expense before evaluation. Include failures and
   unassessable work in reporting, with explicit denominators.
4. Resolve destination approval, retention, capture/queue capacity and the other
   `docs/llm-assessment.md` gates before real learner data or production activation.

Automated coverage tests offline extraction, bounded trials, no grade creation,
null/zero distinction, score instability, failures without retries, stopping on
save errors, review binding/completeness/ranges, HTML escaping and private output
files. Browser QA exercises worksheet export/import and incomplete-review
validation using explicitly labeled synthetic ratings; those ratings are not
included in the baseline's human-review count.


## Report integrity checks

Before rendering a saved report or summarizing a worksheet, Cairn re-extracts the
bundled synthetic files and compares the captured artifacts, original bytes,
segments, rubric, revision and input digest. A proposal must bind to that input.
This prevents self-consistent replacement evidence from masquerading as a known
case. It also rejects contradictory extraction states, proposals attached to
non-pending results and completed live runs missing a provider outcome. It does
not cryptographically attest that a provider executed a request or authenticate
a locally asserted reviewer. Current `source-v1` baseline reports remain readable.

Local regression tests exercise categorized provider failures without live API
calls, preserve their usage/no-retry behavior, reject substituted evidence, and
keep policy decisions distinct from human score ratings. No additional paid run
was needed for this diagnostics and calibration-record update.


## V2 development recheck — October 7, 2026

The user-selected unassessable policy was checked with two new trials on the same
synthetic source cases. See the [frozen V2 summary](evaluations/2026-10-07-unassessable-v2.json).
All **18/18** provider attempts yielded valid pending proposals, with **36/36**
provisional criterion matches and **9/9** stable score/assessability pairs. Both
injection-only trials returned null for both criteria. Partial work and mixed
injection-with-answer retained 2/4 calculation and 2/6 interpretation points.
All six extraction checks blocked as expected. Reported usage was 12,148 input
and 5,307 output tokens; the separate ledger recorded 18 attempts and zero grades.

This is a development recheck after clarifying the rubric, using the same source
cases as V1. It does not establish performance on unseen work, fairness, semantic
citation accuracy or feedback usefulness. The old failure remains part of the
V1 record; this run does not identify its cause. The selected policy is recorded
in a private report-bound `policy-decision.json`; all **36 criterion ratings remain
blank**. Policy-review completion and human score-review completion remain separate.


## Transfer evaluation and course policy — October 8, 2026

The current capture workflow now binds the missing-work policy for course
assessments, including imported proposals. See `llm-assessment.md` for its scope,
legacy handling and the distinction between structural checks and human judgment.
V1/V2 corpora and old reports remain frozen; their results used the earlier prompt.

A separate `synthetic-transfer-v1` suite adds nine previously unrun synthetic
cases: blank, off-topic, instruction-only, mixed partial work, correct results
with wrong reasoning, conflicting code/comments, Python evidence across files,
R missing observations, and a multi-cell notebook with misleading saved output.
It changes the numeric task and adds programming rubrics. These agent-authored
cases are transfer checks, not an independent instructor-authored holdout set.
Freeze cases and prompt before the first live run; retain failures without
adjusting references to match the output. Future tuning makes these regression
cases and requires a fresh held-out set for an independent assessment.

```sh
go run ./cmd/cairn assessment-eval run --suite transfer --out output/transfer-offline
# Opt-in synthetic provider check, at most 18 calls in its own evaluation store:
go run ./cmd/cairn assessment-eval run --suite transfer --live --trials 2 \
  --key-file /private/path/cairn-openai.env --out output/transfer-live
```

Both suites retain the same request/usage ceilings. No evaluation publishes
grades. Each live run creates `review.html` with source and rubric, initially
collapsed proposals, and a blank digest-bound `human-review.json` worksheet.

Instructor review procedure:

1. Read each source and rubric before expanding the proposed score. Record your
   own assessability and points, then assess evidence support, usefulness and
   uncertainty. Explain disagreements; do not treat reference agreement as truth.
2. Keep missing-work policy confirmation separate from criterion ratings.
3. Export the worksheet and run `assessment-eval review` against the exact report.
   All criterion ratings remain pending until a human supplies them.
4. Before course activation, add instructor-authored representative assignments,
   accessibility/format variations and agreed acceptance thresholds. Review every
   score/assessability disagreement and unsupported citation. Confirm actual
   review/publication and student isolation in a course rehearsal.

Completing the implementation or automated transfer run does not complete these
human calibration and course-acceptance steps.


### Frozen first transfer run

The October 8 run used `cairn-rubric-v2` with two trials of each new case:
**17/18 valid proposals**, **34/34 provisional criterion matches**, zero
assessability mismatches, and 8/8 stable completed pairs. All 34 accepted
uncertainty fields used the canonical low-uncertainty label with an evidence
reason. This does not establish calibrated uncertainty across harder tasks.

The first multi-file Python trial failed with
`invalid_proposal_citation_location`. It was rejected, its token usage retained,
and no retry performed. The exact invalid location was not retained in safe
failure diagnostics, so its cause remains unresolved. The second trial succeeded;
that does not erase the first failure. Investigate citation reliability before
course acceptance. Raw failed provider output was not logged.

Provider-reported usage was 13,306 input and 5,081 output tokens. The isolated
ledger contains 18 attempts (17 succeeded, one failed), and zero grades. The
[frozen summary](evaluations/2026-10-08-transfer-v1.json) records report/corpus
hashes and every outcome. All **34 human criterion ratings remain blank**.
