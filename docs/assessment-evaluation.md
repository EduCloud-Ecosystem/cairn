# Synthetic assessment evaluation and human review

`cairn assessment-eval` evaluates the **existing** extraction → bounded OpenAI →
private pending proposal path in a dedicated SQLite database. It never connects
to the serving database, real Git repositories, enrolled learners or grade
publication. The bundled 12-case corpus is hand-authored synthetic material with
**agent-authored provisional references**, not instructor-scored ground truth.
No claim of fairness or course readiness follows from matching these references.

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
provider prompt, frozen references, course rubric or grades. A later rubric
revision and evaluation must implement the selected policy. Legacy worksheets
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
