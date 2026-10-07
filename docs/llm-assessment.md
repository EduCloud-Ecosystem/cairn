# LLM-assisted feedback and proposed scores

Product decision (October 7, 2026): support rubric-based feedback **plus proposed
scores**, with flexible student deliverables. The first local review workflow is implemented behind `CAIRN_ASSESSMENT_REVIEW=1`.
It captures source artifacts, imports fixture/instructor proposals, and supports
instructor review/edit/reject/publish. No LLM provider or automatic model scoring
is connected. The pipeline below describes both this foundation and remaining
provider/evaluation work.

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
   prompts. Enforce per-submission token/output/cost ceilings, bounded retries,
   a queue and an operator stop control. No provider is selected by this decision.
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
3. Import a proposal JSON whose `input_digest` matches the captured document.
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

Next: a bounded provider adapter and instructor-scored evaluation corpus using
synthetic/permissioned examples, followed by destination approval and measured
quality/cost gates. No real student content has been sent to a model provider.
