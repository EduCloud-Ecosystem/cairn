# LLM-assisted feedback and proposed scores

Product decision (October 7, 2026): support rubric-based feedback **plus proposed
scores**, with flexible student deliverables. The first local review workflow is implemented behind `CAIRN_ASSESSMENT_REVIEW=1`.
It captures source artifacts, generates bounded OpenAI proposals or imports
fixture/instructor proposals, and supports instructor review/edit/reject/publish.
The OpenAI adapter is separately opt-in and has passed local synthetic acceptance.
It is not enabled in the production pilot or accepted for real course grading.

## Assessability and uncertainty policy (October 8, 2026)

New captures record `policy_version: unassessable-until-review-v1`, included in
`input_digest`. The course provider prompt (`cairn-rubric-v10`) applies this policy
before rubric scoring: entirely blank, off-topic, or instruction-only readable
work is unassessable, with null points on every criterion. Relevant wrong or
partial attempts receive the rubric's normal points, including zero for missing
parts. Unsupported or unreadable files still block assessment.

New generated and imported proposals must declare `submission_status` as
`relevant_work` or `no_relevant_work`. Validation rejects numeric scores paired
with `no_relevant_work`, and independently rejects a relevant-work classification
for entirely whitespace-only extracted source. Relevance for nonblank text is
still a model/reviewer judgment, not deterministically verified. An instructor
must inspect all classifications. Approval still requires resolved numeric points
and an explicit review note, preserving the original proposal when corrected.

Old saved proposals and reports remain readable and reviewable. New generation
from a legacy capture is refused before any provider reservation or transmission;
capture current work again to bind the policy. Existing course rubric text and
previous grades are not silently rewritten.

Provider output uses `uncertainty_level` (`low`, `medium`, `high`) and
`uncertainty_reason`. Cairn constructs the stored label, such as "Low uncertainty:
The values are explicit in the source." Reasons must describe evidence without
confidence/uncertainty labels; contradictory label language is rejected with
`invalid_uncertainty`, retaining usage and the no-retry reservation. This enforces
consistent vocabulary, not calibrated confidence or semantic correctness.
Instructor-edited uncertainty and legacy feedback retain their existing format.

## Per-instructor calibration

Calibration belongs to an authenticated instructor and an assignment rubric,
not to the installation or a shared synthetic benchmark. In the assignment's
**Your instructor calibration** panel, start a private draft after saving the
rubric. Each instructor sees only their own profiles.

1. Upload past files at the rubric's required paths, or select an existing Cairn
   classroom, assignment and submission. Optionally specify a full historical
   commit; otherwise Cairn pins the recorded latest commit. These are private
   calibration copies, not new student submissions or grades.
2. Inspect the extracted source. Remove identifying details from uploads and use
   only work you are authorized to reuse. Each model request requires a separate
   explicit source-review/send action. Nothing is sent by uploading or capturing.
3. Generate feedback using the shared classroom allowlist, pause and request/unit
   budget. Review criterion scores, feedback, limitations and citations, then save
   your corrections and rationale. Past recorded grades are not automatically
   adopted as reference scores: the current rubric may differ.
4. Write reusable guidance describing the implications of your corrections for
   future feedback. Approve the reviewed profile. All included examples must have
   a generated proposal and an instructor review; failed/unhelpful examples may be
   explicitly excluded with a retained rationale. At least one reviewed example
   is required. Completion does not certify quality or representative coverage.
5. Select that profile for new assessment captures. Its ID, revision, document
   digest and approved guidance are bound into the input digest. Only guidance
   accompanies new student evidence; historical examples, reviewer identity and
   calibration scores are not sent as few-shot examples. This is instructor-owned
   prompt guidance, not model-weight training or automatic learning from grades.
6. Use **Test selected profile on another historical sample** for a fresh round
   using its guidance. Compare proposed scores with your own judgments and revise
   guidance in the new profile. Use fresh, representative examples for an honest
   transfer check. Every profile remains scoped to its owner, assignment, exact
   rubric digest, model, prompt and policy version. Changes require recalibration.

Existing uncalibrated capture/import paths remain available and explicitly show
that no instructor profile was used. Calibration never bypasses live instructor
review or creates a published grade. A reviewed profile is a configuration
approved by its instructor, not a system-wide acceptance certificate. Do not
activate a course based solely on one sample or a perfect synthetic match.

Current limits: nine examples per profile, 4 MiB stored profile document, existing
extraction/provider limits and the shared instance's 20 daily requests/200,000
reservation units. Calibration uses the same admission lock and usage ledger as
normal assessments. Failed and uncertain attempts are retained without automatic
retry or refund, including after a profile is deleted. Repeated new profiles are
additional paid requests under the same serving-instance cap.

Profile ownership is enforced in the API and store using the authenticated
operator's stable user ID; the auth-disabled development identity cannot create
profiles. Existing course/submission management permissions remain instance-wide.
Calibrated assessment records containing private guidance are also owner-scoped.

Delete a profile to erase its historical source and private reviews. For examples
copied from Cairn, permanent roster erasure removes every profile containing that
student's stored submission; the instructor must recalibrate. Independent uploads
are not linked to roster identities and must be erased through profile deletion.
Usage-only audit records remain. Existing live assessment captures retain their
approved guidance and review history, but deletion prevents new generation using
the deleted profile. No historical examples are copied into those live captures.

The UI and bounded OpenAI path remain a local pilot. Permission to reuse actual
course work and institutional destination/retention acceptance are course-specific.

### Preparing historical samples from GitHub or local archives

`cairn calibration-prepare` builds a private, source-only bundle and an offline
inspection page from local files. Fetch repository files at full commit IDs
first; preserve the repository, commit, original SHA-256 and any redactions or
scope extraction in a separate private provenance log. Never commit real course
samples or that identity mapping into Cairn. The command has no network access,
provider credentials or serving database, and never executes student source.

A manifest contains `purpose` (`calibration` or `holdout`), the intended `rubric`,
and 1–9 `examples`, each with a neutral `id` and a relative `directory`. Each
sample directory contains all files named by `rubric.paths`. For example:

```json
{
  "purpose": "calibration",
  "rubric": {
    "title": "Design explanation",
    "paths": ["answer.qmd"],
    "criteria": [{"id": "reasoning", "description": "Explain the design choice using the assignment requirements.", "max_points": 5}]
  },
  "examples": [{"id": "sample-01", "directory": "sample-01"}]
}
```

```sh
cairn calibration-prepare --manifest /private/manifest.json \
  --source-root /private/reviewed-source --out /private/new-packet
```

Inspect `inspect.html` and `rubric.json`, save the intended rubric in Cairn, then
start an empty calibration draft and use **Import a prepared historical sample**
to load `bundle.json`. Importing sends nothing to OpenAI; every generation and
instructor review remains explicit. Source inspection and rubric interpretation
remain human responsibilities. The command does not redact identifying text.
Files are created with owner-only permissions in a new directory. Bundles are
limited to 900 KiB including JSON encoding to fit the bounded import API.

The server requires an exact rubric match and commits the entire validated batch
in one update. Bundle membership is frozen after import. Source-only imports
cannot inject reviews, scores, owner identities or approvals. The recorded bundle
digest and sample aliases preserve which prepared set was used. A holdout bundle
requires a new round based on an approved profile and rejects exact source reuse
from its calibration lineage, including renamed samples. This does not detect
paraphrases, near-duplicates, or related work from the same learner; select splits
before reviewing feedback and maintain that provenance outside the bundle.

`.qmd` and `.Rmd` are extracted as inert text with line citations. Cairn does not
run code, render documents, follow includes, open links or inspect plotted pixels.
Source can support narrative/code feedback; visual appearance and live deployment
criteria require additional evidence or remain unassessable. If only one exercise
is sampled, use a rubric explicitly limited to that exercise and retain the
original line range and transformation hash in the private provenance log.

An October 9 authenticated browser check imported two synthetic examples into an
empty draft, recorded the bundle identity/purpose, froze membership, and left
both instructor reviews pending. The isolated database had zero provider requests
and zero grades. Private real-course packets were prepared separately; no real
course sources are included in this repository or its tests.

### Synthetic calibration workflow verification — October 8, 2026

An isolated authenticated browser fixture exercised both file upload and capture
of a Cairn submission at a full commit ID. Two synthetic historical examples
received live proposals, simulated review corrections and an approved profile.
A third live request used that profile on a new assessment and saved a pending
proposal with the captured profile revision. All three requests succeeded; the
fixture database contained zero grades. The learner page showed no published
feedback or private calibration data. Automated tests separately check owner
isolation, guidance-only transmission, shared limits, erasure and stale profiles.

These were hand-authored synthetic examples and explicitly labeled simulated
reviews. They verify the workflow, not any instructor's calibration or grading
quality. Production remains unchanged.

## Assessment pipeline

1. The instructor pins an assignment rubric, accepted formats and weighting.
   Tests and rubric criteria may coexist. Use deterministic checks for executable
   answers and mechanically verifiable properties; use model assistance for
   explanation, argument, design, interpretation and other suitable criteria.
2. Capture a submission commit and an artifact manifest with paths, media types,
   sizes and SHA-256 digests. Start with text/Markdown, Python/R source and notebook
   source (including passive Quarto/R Markdown); add PDF, office documents, images and other formats only with tested
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
  "submission_status": "relevant_work",
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
- Rejected judgments record fixed categories for score, citation, coverage and
  feedback validation failures. Raw rejected responses and credentials are not
  logged; older `invalid_proposal` entries cannot be diagnosed retrospectively.
- One model attempt per captured assessment. Failed, cancelled, stale and uncertain
  calls retain their reservation; there is no automatic retry/refund. A crash can
  leave an attempt marked running. Inspect it, use an instructor import, or
  deliberately recapture for another attempt subject to the same daily limits.
- The durable pause control stops **new** requests; in-flight requests may finish.
  Busy/limit responses use HTTP 429. Pause state and usage reservations survive
  restart in SQLite/PostgreSQL.

### Data sent and retained

Criterion IDs/descriptions/maxima, extracted source segments and any selected
profile's approved instructor guidance go to OpenAI.
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

A repeatable synthetic evaluation and offline human-review packet are now
implemented: see [assessment evaluation](assessment-evaluation.md). The 18-call
baseline recorded one rejected proposal and score/assessability instability; it
is not course acceptance. The user selected unassessable-until-review for
readable submissions with no relevant answer. V2 calibration rubrics encode that
choice and passed an 18-call development recheck; no stored course rubric changed.
Next: instructor score/feedback ratings, a held-out permissioned benchmark, and
the retention/course-destination gates before real course use.

### Complete source with compact line citations

Prompt v4 sends consecutive text lines in blocks of at most 20 lines, with a
starting line number and a newline-delimited text string. All source text, blank lines and trailing
empty lines are retained. Notebook cells and nonconsecutive locations retain
explicit segments. Stored evidence, source hashes and returned `line:N`/`cell:N`
citation validation are unchanged. This reduces repeated JSON labels; it does
not truncate source or increase the 16,000-byte provider-input limit. Changing
the prompt version requires a new calibration profile before reusing guidance.

`calibration-prepare` also writes private `preflight.json` results and shows them
in `inspect.html`, using the exact live-request serializer without credentials
or network access. The results include each sample's complete input size and
whether it fits without instructor guidance. Oversized samples remain intact in
the bundle for inspection, but are visibly blocked for generation; preparation
success does not mean every example can be sent. Bound guidance adds to input
size, and live generation always checks the complete input again. Source review,
destination authorization and instructor approval remain separate requirements.

### Compact-input synthetic check — October 9, 2026

Five opt-in live synthetic calls passed with prompt v4: correct and incorrect
arithmetic, instruction-only work, a 421-line correct submission with exact
answer citations at `line:21` and `line:401`, and a 421-line instruction-only
submission whose criteria remained unassessable. The two long inputs measured
13,678 and 13,756 bytes. These validate a bounded compatibility example, not
instructor calibration, course accuracy, or general citation reliability. No
real coursework was transmitted and no grades were created.

Repeat the two long-source checks with `TestOpenAILiveCompactSource`, the same
opt-in `CAIRN_OPENAI_LIVE_KEY_FILE`, and optionally `CAIRN_COMPACT_EVAL_OUTPUT`
pointing to a private output file. Normal test runs skip provider calls.

## Section review foundations — October 10, 2026

Instructors can create a section calibration on the same assignment by providing
`section` (a rubric) to the existing create route. A section may select parent
paths and criteria but cannot change criterion descriptions or maxima. The row
remains bound to the complete parent rubric digest. Changing that parent makes
old profiles stale. A follow-up based on an approved section inherits its scope;
whole-assignment captures explicitly reject section-only profiles.

The dashboard accepts section rubric files from prepared packets. Its combined
coverage check requires an explicit selection of approved profiles and reports
missing and overlapping criteria without double-counting their weights. Coverage
is not a student score, a combined approved profile, or support for publishing a
whole assignment from multiple generated sections. That orchestration remains a
separate build step.

Before section generation, instructors must save an independent reference via
`POST /calibrations/{id}/examples/{example}/reference`. The reference is immutable,
owner-scoped and never sent to OpenAI. It cannot be created after any model
attempt, including a failed one. Post-generation corrections remain a separate
review. Legacy whole-rubric profiles retain their previous generation path but
can also save an independent reference first.

`POST /calibrations/{id}/preflight` accepts revision and draft guidance. It uses
the actual request serializer on each included example, with that guidance, and
makes no provider call. Approval repeats this check server-side. Passing checks
for calibration examples does not promise that reserved or future work fits;
run the check in the corresponding round and retain the generation-time limit.
Excluded examples do not block approval. An empty result never suffices to
approve a profile without reviewed examples.

Prompt v5 explicitly treats supplied starter scaffolds and unimplemented stubs
as no relevant student work, including for code-quality criteria. These remain
unassessable until instructor review. Model classification is not proof of source
authorship; retain pinned instructor templates and distinguish authored changes.
Existing profiles require recalibration for the new prompt version.

Remaining acceptance work: adjudicate ambiguous historical criteria without
retroactive new deductions, record instructor scoring anchors, attach verified
results from isolated execution, and test held-out real-course feedback. No
synthetic check or coverage display establishes those outcomes.

Offline preparation also accepts `--guidance-file /private/guidance.txt` so a
reserved packet can be checked without importing it or contacting a provider.
The report retains the size without guidance, the complete size with guidance,
and the digest of the trimmed guidance. It does not copy guidance into the
source bundle. Source remains intact even when the check fails.

### Synthetic acceptance results — October 10, 2026

The browser fixture verified section creation, source-bundle import, a frozen
pre-model reference, and a guidance-aware preflight through the dashboard/API
and SQLite. No provider was connected and no grades or generation attempts were
created. Automated tests cover section scope, owner isolation, immutable
references, held-out reuse, overlap detection and guidance budget enforcement.

Live synthetic checks classified the starter-only control as unassessable and
passed the long-source controls, but exposed a citation limitation. One short
student implementation returned an invalid location and was rejected. A later
response passed structural validation yet cited surrounding instructor scaffold
instead of the operation at line 4. More explicit prompt instructions still
failed the tightened semantic check. `TestOpenAILiveStarterControl` retains that
assertion and failed under v5. The later v6 quote-resolution run below passed
this bounded control. Source location existence still is not proof that a
citation supports feedback; do not interpret offline CI as establishing
real-course quality. No historical student source was sent in these checks.

## Exact-quote citations and offline execution — October 10, 2026

### Server-selected excerpt choices (v10)

The provider now selects `artifact_id` and `excerpt_id` from an artifact-scoped
schema catalog. Cairn constructs each choice from exact captured source and
expands repeated lines with adjacent context until the excerpt is unique.
Notebook cells never merge. Long lines use bounded UTF-8-safe excerpts. The
model sees the complete original source as before; the catalog is an additional
selection aid, not a source truncation or an authorship/quality classifier.

The response schema pairs each artifact with only its own excerpt IDs. Cairn
independently rejects unknown IDs and mixed ID/quote payloads, resolves the ID
locally, and applies the existing exact-source checks. Legacy verbatim-quote
responses still undergo those same checks; stored proposal citations keep their
original representation. Source text embedded in the catalog remains untrusted
data. No filenames, roster data, previous judgments, or approvals enter it.

This uses the enum and nested `anyOf` structure documented in the
[OpenAI Structured Outputs guide](https://developers.openai.com/api/docs/guides/structured-outputs?api-mode=responses).
Live testing rejected multiline enum literals, so v10 uses short IDs and puts
the excerpt catalog in the schema description. Limits are 900 combined citation
choice/artifact enum values, 60,000 bytes of catalog excerpts, and a 96 KiB
serialized request. Existing limits of 16,000 input bytes, 1,000 bytes per quote,
32 resolved citations per criterion and 4,096 output tokens remain unchanged.
Preflight checks the additional bounds, without silently dropping choices.
The complete request, including the catalog, contributes to the existing durable
usage reservation. Catalogs increase input tokens; they do not provide free
retries or a higher classroom quota.

Prompt v10 also requires concrete rubric support for deductions. Fixed settings
are judged under each instructor's rubric: the same setting may be acceptable
in one course and prohibited by an explicit parameterization requirement in
another. Historical course interpretations belong in that instructor's versioned
rubric and guidance, not a global exception in Cairn.

The seven synthetic controls include both interpretations, genuinely hardcoded
answers, notebook evidence, repeated lines, and starter-only work. These remain
development controls. Selecting real source does not establish that a citation
supports the feedback or that the proposed deduction is justified. Recalibrate
approved profiles for v10 before course use. See the
[v10 evaluation record](evaluations/2026-10-10-citation-v10.json) for failed
intermediate approaches and the bounded historical checks.

All seven v10 synthetic requests and three v10 historical development requests
produced valid citations. The final historical policy check gave the original
quality case 5/5. A second reviewed example still received an unjustified 4/5
deduction for previously waived constant-column handling. Its original proposal
was retained and a separately attributed assistant adjudication recorded 5/5.
That correction is calibration evidence, not independent instructor ground
truth. That development round did not use fresh reserved work, publish grades,
approve profiles or activate production. The larger catalog request cost is
recorded in the report.

The subsequent [reserved-source score review](evaluations/2026-10-10-reserved-score-review-v10.json)
carried the previously adjudicated constant-column waiver into a historical
quality v5 rubric before reviewing two reserved sources. Four bounded calls
matched assistant references recorded before provider responses: both quality
scores were 5/5; a reversed membership comparison received 2/10 for the outlier
criterion, while a coherent log-density rule received 10/10 under the historical
ambiguity exception. The functional criterion was unchanged. The policy was
supplied in the rubric, not through an approved calibration guidance binding.

All four proposals passed exact citation checks, but one omitted the model call
needed to support its explanation. A separately attributed assistant review
added that citation and clarified log-density threshold units; the original
proposal and score were preserved. The two-source result is not general grading
accuracy or independent instructor validation. Both diagnostic profiles remain
drafts with zero grades; neither is a formal approved-profile holdout. These
sources are now consumed evaluation data. The next stage is broader instructor
course rehearsal with fixed policy and explicit instructor review, not automatic
grade publication.

### Broader course rehearsal (v10)

The [broader rehearsal record](evaluations/2026-10-10-broader-rehearsal-v10.json)
covers two archived examples from each of six additional assignment components
across INFO-511, INFO-523 and INFO-557. Scoring anchors were frozen before source
inspection and assistant references before provider responses. All twelve
requests produced accepted proposals: 10/12 scored criteria matched the
references, and all eight expected unassessable criteria stayed null. These are
component scores from one instructor's coursework, not full-assignment grades,
cross-instructor validation, or fresh holdout results.

The evidence review failed more often than the score comparison. All six scored
quality judgments cited signatures or scaffold rather than the implementation
claims; two functional judgments omitted credited operations. One quality
deduction also crossed into an explicitly excluded functional component. A
malformed authored attempt correctly stayed null but was inaccurately described
as untouched scaffold. Separately attributed assistant reviews repair the
supporting evidence and explanations while preserving all original proposals.
They do not establish independent instructor ground truth.

Before transmission, a prior privacy redaction that broke four model-name
string literals was repaired after checking the original source hash and AST.
Only neutral quoted names changed; the student algorithm was not repaired.
Genuine invalid student syntax remained intact. The preparation transformations
are recorded privately, and the scoring rubrics did not change after inspection.

All six profiles remain drafts with zero grades and no production activation.
Unreviewed feedback is not ready: the next build must strengthen authored-source
evidence review using pinned starter provenance, flag scaffold-only support,
preserve functional/quality component boundaries, and validate privacy
transformations before sending. Do not treat exact quotation checks or score
agreement as proof of semantic feedback quality.

### Contiguous multiline excerpts (v7)

Prompt v7 permits exact contiguous multiline excerpts in line-based artifacts.
The resolver first checks ordinary single-segment quotes, then reconstructs only
consecutive `line:1` through `line:N` segments and requires a unique byte-exact
match. It expands that match into the existing per-line citations. Whitespace
and blank lines participate in matching; blank-only pieces are not citations.
The 1000-byte quote and 32-citations-per-criterion limits remain enforced.
Notebook cells remain atomic. The resolver never normalizes CRLF, repairs
indentation, joins skipped lines, bridges files/cells, or selects an arbitrary
occurrence of repeated text. Existing stored citations remain unchanged.

This fixes a reproducible mismatch between the provider's compact code blocks
and the old one-line resolver. It does not establish the cause of old failures
whose rejected response text was not retained. Approved profiles must be
recalibrated for v7; old proposals retain their original provenance.

Four opt-in synthetic controls passed. Two produced multiline excerpts that
the old resolver would reject. A separate, authorized four-request diagnostic
on previously rejected historical examples accepted three proposals, with five
exact multiline excerpts and all five accepted criterion scores matching the
assistant-authored references. One quality proposal was still rejected for a
repeated parameter-line quote. Its suggested style deduction also remains
unadjudicated. No grade or profile approval resulted; the original trial and
fresh reserved samples were preserved. See the
[evaluation record](evaluations/2026-10-10-citation-v7.json).

Run the current seven synthetic controls with an explicit key file and a **new,
absolute** private report path whose parent already exists:

```sh
CAIRN_OPENAI_LIVE_KEY_FILE=/private/cairn-openai.env \
CAIRN_CITATION_EVAL_OUTPUT=/private/new-citation-report.json \
go test ./internal/assessment -run '^TestOpenAILiveCitationControls$' -count=1 -v
```

The test reserves its report before any call, refuses overwrites, and makes one
attempt per case. The report includes synthetic response text for diagnostics;
production error messages continue to contain fixed codes only.

### Original v6 evaluation

Prompt v6 asks the provider for `artifact_id` and a verbatim `quote` instead of
model-counted line numbers. Cairn resolves each quote to exactly one captured
segment, adds the path/hash/location, and retains the quote for instructor
inspection. Empty, oversized, fabricated and ambiguous excerpts fail closed;
usage remains recorded and failed requests are not retried automatically.
Notebook quotes resolve to cells. Imported or edited citations with a quote must
match their source location. Older citations without quotes remain reviewable.
A correct match establishes source correspondence, not semantic support,
student authorship, or grading correctness. Existing approved profiles need a
new calibration round for prompt v6.

The final seven-call synthetic run passed correct/incorrect arithmetic,
instruction-only work, two 421-line controls, starter-only work, and a student
implementation requiring the operation at line 4. An initial v6 trial rejected
a fabricated starter citation; the final prompt tells `no_relevant_work` cases
to return no citations. This resolves the observed v5 line-4 acceptance failure
in the bounded synthetic checks, not general real-course acceptance.

`cairn calibration-execute --bundle /private/bundle.json --checks
/private/checks.json --out /private/new-run` produces `execution.json` locally.
It never opens the serving database, contacts a model, publishes a grade or
imports results as trusted provider evidence. Its check plan is reviewed
instructor code with this shape:

```json
{
  "version": "cairn-calibration-checks-v1",
  "files": {"check.py": "print('PASS')\n"},
  "spec": {
    "version": "1",
    "image": "sha256:<64 hex digits from docker image inspect>",
    "tests": [{
      "name": "an_existing_rubric_criterion_id",
      "run": "python3 -I -B /cairn-policy/check.py",
      "points": 0,
      "match": {"expected": "PASS", "trim": true}
    }]
  }
}
```

The example only illustrates the format; it is not a functional test. Check
names must be distinct rubric criterion IDs. Setup commands and per-test limit
overrides are refused. Use an already available image containing the assignment
dependencies. Only local Unix-socket Docker contexts and immutable image IDs are
accepted; execution never pulls an image. Instructor files and captured source
are copied into private temporary directories and mounted read-only. Each check
runs as UID 65534 with no egress, a read-only root filesystem, no capabilities,
no new privileges, 512 MiB memory, one CPU, 64 PIDs, and a 15-second timeout.
This uses the existing shared-kernel container tier, not gVisor or a dedicated
remote worker. Captured process output is capped at 64 KiB per stream. Matching
stdout cannot pass when the command exits nonzero; cancellation kills the named
container as well as its client process.

Reports bind the original bundle, rubric, check plan, image and individual source
hashes. They list unchecked criteria and preserve partial progress. `completed`
means all examples were attempted, not that checks passed. Observations contain
no numeric scores. Check scripts should exit 0 with their expected marker for
success, 1 for an assertion failure, and 2 or higher for unimplemented work or
other execution errors. Timeouts and errors remain unassessable. Distinguish
missing dependencies from student failures. A check completing successfully is
not proof against an adversarial submission manipulating its Python process;
review the harness and observations before making a grading judgment.

Local synthetic Docker verification distinguished correct, incorrect and stub
implementations and exercised read-only source/policy/rootfs, denied egress,
non-root identity, no-new-privileges, memory and PID caps. Historical execution
is exploratory evidence, not independent held-out grading validation. Results
can be explicitly attached to calibration examples as supporting evidence using
the workflow below. They are never sent to a model.

### Attributed supporting evidence

Build a private packet for one exact source-bundle sample:

```sh
cairn calibration-review-packet \
  --bundle /private/bundle.json --sample sample-01 \
  --execution /private/run/execution.json \
  --judgments /private/judgments.json \
  --authorship assistant --author "Local adjudication assistant" \
  --note "Reviewed interpretation; execution has bounded coverage." \
  --out /private/new-review-packet
```

`--judgments` accepts a JSON array of Cairn judgments (`criterion_id`, `points`,
`feedback`, `uncertainty`, and `citations`). Citations use `path`, `sha256`,
`location`, and an exact `quote` from the captured source. Scored judgments need
supporting citations. Null remains unassessable; partial adjudications may omit
criteria without assigning or renormalizing their points. Existing offline
review formats are not interchangeable with this array.

Either execution or judgments may be omitted. Judgments require authorship
`assistant`, `instructor-assisted`, or `external-reviewer`. For checks alone,
omit `--judgments` and select `--authorship execution-only`. The builder performs
passive source extraction and validation only. It refuses an existing output
directory and writes the new directory/file with permissions 0700/0600.

In the instructor console, open a calibration example and choose **Supporting
review evidence → Attach a review packet**. Upload or paste `review-packet.json`.
The owner-scoped endpoint checks the current revision, exact rubric digest,
source paths/hashes, score bounds, and quoted locations. Reports must be complete
and account for checked and unchecked criteria. A full-source report cannot be
attached to a compact excerpt or a different section rubric. Prepare evidence
against that exact source and rubric instead of changing its hashes.

The console distinguishes claimed authorship from the authenticated importing
instructor and server timestamp. Hash matches establish source correspondence;
they do not verify who wrote a judgment, ran a check, or whether its conclusion
is correct. Packet/report digests identify canonical decoded JSON, not original
file bytes or signed execution attestations. Only the selected execution sample
is retained in the example, alongside report, bundle, check-plan and image
identifiers. Keep the private original report for a full audit.

Up to four attachments can be added before final instructor review or exclusion.
They are immutable supporting records and do not modify model input, proposals,
references, grades or profile approval. After generating feedback, **Use
adjudication in instructor review form** copies only the included criteria into
the editable form. Saving the instructor review remains a separate action.

Attaching assisted judgments before a reference closes that example's independent
reference path. Section feedback may then proceed explicitly as assisted
calibration, subject to the usual source-send authorization and provider controls.
A reference saved earlier stays unchanged; execution-only material does not
block independent references. Assisted agreement is not blind validation.
Ready profiles retain attachments but still require actual instructor reviews
and explicit approval. Deleting the calibration deletes its attachments through
the existing private-data lifecycle.

The October 10 synthetic browser check verified upload, attributed display,
source-quote inspection, explicit copying and review, and persistence after
reload. Both profiles remained drafts with no independent references; the
fixture recorded zero grades and zero provider requests. Real-course instructor
acceptance and authorized source transmission remain separate steps.

## Python implementation evidence requirements (v11)

In **Assessment rubric**, set a criterion's **Evidence requirement** to
**Python implementation operation** to reject scores supported only by function
signatures, comments, docstrings, imports or unimplemented stubs. For assignments
with supplied code, choose **Python operation changed from captured starter**
and upload the starter for each `.py` path under **Starter files for Python
evidence checks**. Save the rubric before capturing new work or calibrating.
An empty starter file is an explicit empty baseline; a missing upload is not.

The JSON equivalents are criterion `evidence: "python_implementation"` or
`evidence: "python_authored"`, plus rubric `starter_files`, a path-to-source map.
Starter files must match `.py` rubric paths, contain UTF-8 text without NULs,
and fit 4096 lines each and 256 KiB combined. The starter is instructor-supplied;
Cairn does not attest that it matches a remote template. Keep its repository and
commit provenance in the private calibration record.

These optional requirements apply to model proposals, imports, calibration
judgments, supporting reviews and final approval. An eligible citation must
include the full detected operation text on its source line. Empty or partial
quotes cannot use an operation elsewhere on that line. Null remains
unassessable and cannot be published as a grade. A rejected provider response is
recorded as a failed attempt; it is not silently scored or automatically retried.

The check is deliberately narrow: it recognizes operation-like Python source
lines lexically, without executing student code or parsing a full Python AST.
It does not prove syntax validity, correctness, independent authorship, or that
every feedback claim is supported. Multiline expressions still need additional
citations for the claimed behavior. Unsupported lexical forms may require
manual review. For changed-from-starter mode, a line must be absent from the
starter after whitespace/comment normalization; matching is independent of
position and scope, and whitespace inside strings is also normalized. Unchanged
shared lines therefore cannot establish changed implementation. This is a
minimum evidence check, not a plagiarism detector or semantic grading test.

Starter snapshots remain in the private rubric and captured records. They are
not added to OpenAI requests; submitted source may naturally contain overlapping
starter text. The provider receives the criterion's requirement and eligible
excerpt IDs computed by Cairn. Existing source, request and citation size limits
still apply. The optional fields preserve legacy rubric digests when absent.
Changing an evidence rule or starter changes the rubric/input digest, and section
rubrics must preserve the parent's requirement and corresponding starter.
Prompt version `cairn-rubric-v11` requires fresh calibration for subsequent
provider use. Existing historical proposals remain unchanged. R, notebooks and
prose retain the default source-citation mode in this first version.

The October 10 offline replay rejected all six previously identified
signature/scaffold-only quality judgments in both modes, accepted their six
corrected supporting reviews, and preserved two null quality judgments. Four
starter snapshots were fetched at their recorded template commits. Three
single-attempt synthetic OpenAI controls passed: changed implementation scored;
inherited implementation and an unimplemented stub remained null. This is
bounded implementation evidence, not independent instructor acceptance or
production validation. See `evaluations/2026-10-10-implementation-evidence-v11.json`.

To repeat only the synthetic live checks, choose a new private output filename:

```sh
CAIRN_OPENAI_LIVE_KEY_FILE=/private/cairn-openai.env \
CAIRN_IMPLEMENTATION_EVAL_OUTPUT=/private/new-implementation-controls.json \
go test ./internal/assessment -run '^TestOpenAILiveImplementationControls$' -v -count=1
```
