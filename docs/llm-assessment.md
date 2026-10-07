# LLM-assisted feedback and proposed scores

Product decision (October 7, 2026): support rubric-based feedback **plus proposed
scores**, with flexible student deliverables. This document defines the next
assessment feature; it does not claim that an LLM provider, review UI or automated
model grading is currently implemented. The push-grading release establishes
immutable submission provenance needed for that feature.

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
