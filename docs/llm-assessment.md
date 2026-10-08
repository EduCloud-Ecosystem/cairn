# LLM-assisted feedback and proposed scores

Product decision (October 7, 2026): support rubric-based feedback **plus proposed
scores**, with flexible student deliverables. The first local review workflow is implemented behind `CAIRN_ASSESSMENT_REVIEW=1`.
It captures source artifacts, generates bounded OpenAI proposals or imports
fixture/instructor proposals, and supports instructor review/edit/reject/publish.
The OpenAI adapter is separately opt-in and has passed local synthetic acceptance.
It is not enabled in the production pilot or accepted for real course grading.

## Assessment pipeline

1. The instructor pins an assignment rubric, accepted formats and weighting.
   Tests and rubric criteria may coexist. Use deterministic checks for executable
   answers and mechanically verifiable properties; use model assistance for
   explanation, argument, design, interpretation and other suitable criteria.
2. Capture a submission commit and an artifact manifest with paths, media types,
   sizes and SHA-256 digests. Start with text/Markdown, Python/R source and notebook
   source; add PDF, office documents, images and other formats only with tested
   extractors. Preserve originals and extraction provenance. Unsupported,
   unreadable or truncated material must be surfaced for review, never silently
   treated as absent or scored zero.
3. Process student content as untrusted evidence. Instructions embedded in a
   submission cannot change the rubric, tools, provider destination or output
   schema. Extractors run with bounded resources and no network; preserve file,
   cell, page or section references for citations. Do not execute notebook outputs
   or active document content as part of extraction.
4. Route only approved artifact content to an institution-approved model endpoint
   under the course's data-destination policy. Keep roster PII and secrets out of
   prompts. The local OpenAI pilot below adds input/output limits, durable usage
   reservations and an operator pause control. A production queue, billing-aware
   cost controls and course-specific destination approval remain acceptance work.
5. Return a **proposal**, distinct from `Grade`: submission ID/commit, rubric
   revision/digest, artifact/extraction manifest, model/provider/configuration,
   prompt version, per-criterion suggested points, evidence citations, actionable
   feedback, uncertainty and unassessable items. Compute totals in Cairn from
   instructor-owned weights; the model cannot set its own maximum score.
6. Validate schema, finite/ranged points, known criterion IDs, complete coverage,
   artifact hashes, citation locations and revision freshness deterministically.
   Those checks establish structural integrity, not the correctness or fairness
   of the model's judgment. Failed or incomplete proposals remain unpublishable.
7. An instructor reviews/edits/rejects the proposal. Approval records the reviewer,
   timestamp, changes, final score and evidence. Publish approved feedback and
   grades through normal Cairn access controls. A newer submission or rubric makes
   an older proposal stale and requires reassessment or explicit reviewed handling.
   Never overwrite a recorded grade just because a model run completed.

The model can suggest nuanced scores without deciding whether a student's
submission meets an objective test. Keep test results and model judgments
separately visible; define how they combine in the instructor's rubric. Provide
learners with explanations and a correction/appeal path.

## Acceptance before enabling a course

Use an instructor-scored, permissioned benchmark covering supported formats,
strong/weak/partial submissions, accessibility needs, missing evidence and prompt
injection attempts. Measure criterion agreement, score differences, evidence
accuracy, actionable-feedback quality, repeatability, latency and cost. Compare
against the existing human/deterministic workflow; model marketing claims are
not acceptance evidence. Include instructor review/edit/publish, stale proposal
rejection, student isolation, provider failure/budget exhaustion, and retention
or deletion of proposal artifacts. Begin with synthetic/approved examples;
provider credentials and real student uploads require course-specific setup.


## Local review pilot

Enable `CAIRN_ASSESSMENT_REVIEW=1` on a local Cairn instance using the existing
Git clone host configuration. Assessment capture does not require a container
grader. Keep normal operator authentication enabled outside a disposable local
fixture. Operator authorization remains instance-wide, as in the existing
management API; this release adds no per-class instructor roles.

In the expanded assignment card:

1. Save an assessment rubric: title, required repository paths, criterion IDs,
   descriptions and maximum points. Cairn hashes the normalized rubric. This
   rubric is separate from the executable test policy.
2. Select a submission with a recorded push commit and capture it. The checkout
   uses that exact commit. Inspect the captured source and issues.
3. Generate an OpenAI proposal when the separately configured classroom is allowed,
   or import a proposal JSON whose `input_digest` matches the captured document.
   Only `fixture` and `instructor-import` sources are accepted. Provider/model
   labels on imported proposals are instructor-supplied provenance, not attested
   model execution. No fixture automatically invents scores in the product.
4. Review the original proposal and evidence. Edit points, feedback, uncertainty
   and citations; add an instructor review note. **Approve and publish grade**
   appends a grade atomically with the review. Rejection publishes nothing.
5. The student sees approved feedback, evidence locations, the assessed commit,
   and an instructor contact route for corrections. Original files, unapproved
   proposals, model labels and private reviewer identity are not student API
   fields. Existing own-submission authorization still applies.

The sum of criterion maxima is the assessment maximum (no separate weighting
layer). Deterministic test grades and reviewed grades are distinct history
entries; they are not automatically combined. The existing latest-grade view
and CSV export use the most recently completed/published grade. Instructors must
choose a grading policy before mixing these workflows in a real course.

Capture permits at most 16 exact paths, 256 KiB per file and 1 MiB of source
bytes total. Text supports at most 4096 lines; notebooks at most 512 cells.
Supported extensions: `.txt`, `.md`, `.py`, `.r` (case-insensitive), `.ipynb`.
Notebook **source** is assessed: outputs, attachments and metadata are excluded
from extracted segments, though the private original remains in the evidence
record. Files are never executed. Links, traversal, hidden paths, binary text,
missing files and unsupported formats are blocked, not silently scored zero.
The UI lists all paths as required; alternative filenames, optional artifacts,
PDF/Office/image extraction and direct upload are future work.

Capture has a 30-second checkout deadline and one concurrent capture per server
process. Extraction limits do not bound the Git repository download size; a
quota-enforced capture worker, durable admission controls and pagination are
still needed before broad production use. Capture freshness is relative to
Cairn's recorded webhook activity, not a live remote-HEAD attestation: any later
recorded activity, even a same-commit replay, invalidates approval. Changed
rubrics, inactive submissions or removed roster entries also fail closed.
Webhook completeness/out-of-order delivery still needs production acceptance.

The original proposal, rubric, source bytes/digests, extraction version,
reviewer ID, time, final edits and review note are durable in SQLite/PostgreSQL.
Concurrent approval or a retried request cannot create a second grade for that
assessment. Grade insertion failure rolls back publication. Purging an exported
grade cascades to its linked approved assessment evidence/review; deleting a
roster entry erases every dependent assessment. Unpublished/rejected captures
remain until roster erasure; configurable draft retention and targeted erasure
are required before use with real learners.

### API shape

All these routes require the existing operator session and JSON mutations use
`Content-Type: application/json`. Routes are disabled unless the feature is on.

- `GET/PUT /assignments/{id}/assessment-rubric`
- `GET/POST /submissions/{id}/assessments` (POST body `{}` captures)
- `POST /assessments/{id}/proposal`
- `POST /assessments/{id}/review`
- `GET /assessment-capabilities`
- `POST /assessments/{id}/generate` with `{ "input_digest": "CAPTURE_INPUT_DIGEST" }`
- `POST /assessment-provider/control` with `{ "paused": true }` (false resumes)

Example proposal (replace digest values with those returned by capture):

```json
{
  "source": "fixture",
  "model": "synthetic-no-model",
  "prompt_version": "fixture-v1",
  "input_digest": "CAPTURE_INPUT_DIGEST",
  "criteria": [{
    "criterion_id": "reasoning",
    "points": 6,
    "feedback": "Connect the evidence to your conclusion.",
    "uncertainty": "Synthetic fixture; no model judgment.",
    "citations": [{"path": "response.md", "sha256": "ARTIFACT_SHA256", "location": "line:1"}]
  }]
}
```

An unassessable criterion uses `"points": null` and explanatory feedback and
uncertainty. Approval requires the instructor to resolve it with a finite score
and valid citation. Review JSON has `action` (`approve` or `reject`), `note`, and
`criteria` (the complete reviewed judgments for approval).

### Reproduce the synthetic browser fixture

Build the dashboard, choose a **new** private output directory, then run:

```sh
npm --prefix web ci
npm --prefix web run build
CAIRN_ASSESSMENT_BROWSER_DIR="$PWD/output/playwright/review-run" \
  go test ./internal/api -run '^TestAssessmentBrowserFixture$' -v -count=1
```

The opt-in test starts a loopback-only server and writes its URL, a proposal
fixture, and instructor/student browser storage state in that directory. Load
those states with Playwright to exercise the actual dashboard and `/me`. Touch
`output/playwright/review-run/stop` to close the test; it times out after ten
minutes. The production binary contains no synthetic login bypass. Do not
publish the SQLite fixture or browser state files.

Local acceptance covers capture/import/edit/publish/student display, private
pending proposals, invalid points/citations, stale captures, concurrent approval,
SQL rollback, restart persistence, roster erasure and exported-grade retention.
This is workflow acceptance with synthetic content, not an LLM quality evaluation.

## OpenAI local pilot

The adapter uses the Responses API with strict structured output and defaults to
`gpt-5.4-mini-2026-03-17`. A model override must name a compatible pinned snapshot;
the response model must match exactly. The endpoint is fixed to OpenAI, redirects
are refused, and no tools or automatic retries are enabled. Official contract:
[structured outputs](https://developers.openai.com/api/docs/guides/structured-outputs),
[model documentation](https://developers.openai.com/api/docs/models/gpt-5.4-mini).
The live API confirmed access and returned valid proposals in the synthetic checks
below; schema validation does not establish grading accuracy.

Keep the key on the server. Put one literal `OPENAI_API_KEY=your-key` line in an
owner-only regular file outside the repository (`chmod 600`). The loader reads
it as data, never executes it as shell code, and rejects symlinks and broadly
readable files. Alternatively supply `OPENAI_API_KEY` in the server environment.
Do not place credentials in browser configuration, Git, logs or proposal files.
With the existing operator OAuth configuration enabled, add:

```sh
CAIRN_ASSESSMENT_REVIEW=1
CAIRN_OPENAI_ENABLED=1
CAIRN_OPENAI_KEY_FILE=/absolute/private/path/cairn-openai.env
CAIRN_OPENAI_CLASSROOMS=synthetic-classroom-id
CAIRN_OPENAI_MODEL=gpt-5.4-mini-2026-03-17
```

The classroom setting is a comma-separated allowlist of Cairn classroom IDs.
Startup refuses provider activation without operator authentication, review mode
and an explicit classroom allowlist. Container deployments must mount the private
file read-only at the configured path; this change does not modify deployment
secrets or activate a provider automatically. Apply migration 0007 on PostgreSQL
using the existing migration procedure; SQLite initializes the added tables on
open. Use a durable store for local testing that must survive restarts.

### Admission, failure and usage behavior

- One active provider call per server process; additional calls receive a busy
  response. There is no durable provider queue or cross-instance concurrency cap.
- At most 20 admitted requests per UTC day per shared store, 3 per learner roster
  enrollment per UTC day, and 200,000 daily reservation units. Re-enrolling with a
  new roster ID starts a new enrollment; the instance cap still applies.
- Each reservation charges JSON request bytes plus 4,096 output units. This is a
  conservative admission measure, **not exact token counting or a dollar cap**.
  Provider-reported input/output tokens are recorded separately when available;
  network failures can leave usage unknown. No provider billing reconciliation is
  implemented. Set any account-side billing controls separately.
- Extracted rubric/source JSON is limited to 16,000 bytes, output to 4,096 tokens,
  provider response to 256 KiB and the request to 60 seconds. Oversized input fails
  before transmission; it is never silently truncated.
- Admission is transactional before the HTTP call. Captured commit, rubric,
  recorded submission activity and active enrollment must still match. Freshness
  is checked again before saving a pending proposal. Neither generation nor a
  provider failure can create a grade.
- One model attempt per captured assessment. Failed, cancelled, stale and uncertain
  calls retain their reservation; there is no automatic retry/refund. A crash can
  leave an attempt marked running. Inspect it, use an instructor import, or
  deliberately recapture for another attempt subject to the same daily limits.
- The durable pause control stops **new** requests; in-flight requests may finish.
  Busy/limit responses use HTTP 429. Pause state and usage reservations survive
  restart in SQLite/PostgreSQL.

### Data sent and retained

Only criterion IDs/descriptions/maxima and extracted source segments go to OpenAI.
Cairn replaces filenames with artifact aliases and binds returned citations back
to its stored paths, hashes and exact locations. It omits roster/actor metadata,
repository URLs, revision IDs, original bytes, notebook outputs and prior proposals.
**Source or rubric text can itself contain personal information or secrets; this
adapter does not redact them.** Review content and obtain destination approval
before any real learner data is used.

Requests use `store: false`. This disables response application storage, but is
not a zero-retention guarantee: OpenAI documents separate abuse-monitoring logs,
which by default may retain customer content for up to 30 days. See
[OpenAI data controls](https://developers.openai.com/api/docs/guides/your-data).

Cairn keeps provider model/prompt provenance, request digest, response ID and known
usage with the pending proposal. The separate admission ledger deliberately
survives evidence or roster erasure so deletion cannot reset paid usage limits.
It retains opaque assessment IDs, a day-scoped hash of the roster ID, date,
reservation, outcome/error code, model, response ID and known usage. It contains
no prompts, artifacts, feedback or scores. These hashes are pseudonymous, not an
anonymity guarantee. Ledger retention has no automatic expiry yet; a policy and
bounded cleanup are required before real course use, alongside draft retention.

### Synthetic acceptance — October 7, 2026

Four live calls used the configured private key and pinned model, exclusively
with hand-authored synthetic content. No actual student material was sent.

| Case | Observed proposal | Reported input/output tokens |
| --- | --- | --- |
| Correct arithmetic and explanation | 10/10, pending; no grade | 431 / 259 |
| Incorrect arithmetic | 0/10, pending; no grade | 410 / 346 |
| Prompt-injection-only submission | Both criteria unassessable (`null`), no total or grade | 414 / 335 |
| Browser correct-answer workflow | 10/10; published only after explicit instructor approval | 380 / 140 |

The first three responses took approximately 4.4, 2.9 and 3.2 seconds. The browser
check exercised generation, provenance/usage display, pause persistence after
refresh, explicit approval and the student's approved feedback/citation view.
One incorrect-answer uncertainty statement said “Low confidence” despite correctly
identifying the error; wording/calibration needs evaluation. These are connection
and workflow smoke checks, not an instructor-scored benchmark, a robustness rate
or evidence of fairness/accuracy across courses.

Automated coverage includes provider errors/refusals/incomplete output, invalid
scores/citations, secret-safe errors, no automatic retry, preflight authorization
and freshness gates, changes during the HTTP call, concurrent durable admission,
restart persistence, budgets, erasure without usage refunds and instructor-only
publication. Store conformance runs against memory, SQLite and PostgreSQL 17.

To repeat the three paid synthetic calls explicitly:

```sh
CAIRN_OPENAI_LIVE_KEY_FILE=/absolute/private/path/cairn-openai.env \
CAIRN_EVAL_OUTPUT="$PWD/output/assessment-openai/live-eval.json" \
  go test ./internal/assessment -run '^TestOpenAILiveSynthetic$' -v -count=1
```

For the synthetic browser fixture above, additionally set
`CAIRN_ASSESSMENT_BROWSER_OPENAI_KEY_FILE` to the private file. The fixture only
calls the provider when its operator presses the generate button. Normal test/CI
runs skip both opt-in live paths and require no key.

Next: build an instructor-scored evaluation corpus using synthetic/permissioned
examples, measure score/evidence/feedback quality and costs, and resolve the
retention and course-destination gates before enabling a real course.
