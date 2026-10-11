# Source-first calibration readiness

`cairn calibration-readiness` prepares a private instructor review packet without
opening the application database, executing coursework, sending model requests,
creating references, approving profiles, or changing grades. It supports review
of complete locally available sources before a new calibration attempt.

Build from a clean accepted checkout and retain a separate build record containing
the source revision, verified clean status, build command, and executed binary
SHA-256. Go builds may omit embedded VCS settings even when requested:

```sh
go build -o /tmp/cairn-readiness ./cmd/cairn
/tmp/cairn-readiness calibration-readiness \
  --manifest /private/calibration/manifest.json \
  --out /private/calibration/new-packet
```

The destination must not exist. Files are private (0600) inside a private (0700)
directory. Sources, rubric text, draft guidance, and local paths in this output
must stay outside public repositories and CI artifacts.

The manifest has this shape; relative paths resolve beside the manifest:

```json
{
  "version": "cairn-readiness-manifest-v1",
  "instructor_scope": "Pseudonymous instructor scope",
  "prior_bundles": ["prior/source-bundle.json"],
  "sections": [
    {
      "id": "course-section",
      "bundle": "inputs/source-bundle.json",
      "draft_guidance": "Previously recorded guidance, still unapproved."
    }
  ]
}
```

Bundles use the existing source-only calibration bundle format. Supply prior
bundles comprehensively: overlap checks cover only those inputs. Exact and
whitespace-normalized per-file source hashes ignore filenames and sample labels.
Identical selected file-content multisets are rejected even if filenames differ;
file counts are retained. These checks do not establish semantic novelty,
independence, unseen-by-model status, or authorship.

The report freezes the current model, prompt version and exact prompt-instructions
SHA-256, policy version, executed binary SHA-256, available embedded binary
revision/dirty state, source and rubric hashes,
draft-guidance hashes, and prior-bundle hashes. `checksums.json` covers the report,
frozen manifest, HTML review, and blank worksheet. Preserve the manifest and its
referenced bundles, then regenerate into a new directory after the final build.
Compare these hashes to detect changed inputs. Never overwrite prior decisions.
`binary_code_provenance_available` is true only when both embedded revision and
valid modified state exist. An absent modified state is JSON null, not false.
Missing provenance is unknown; the binary hash identifies the executable but does
not independently establish its source revision or clean build. Verify the
external build record before accepting a code freeze.

Open `REVIEW.html` locally for the rubric, complete captured source with line
numbers, captured starter, overlap findings and request-size preflight. A failed
preflight remains visible; it is not truncated or treated as model readiness.
Review such work manually or prepare and explicitly review a separately scoped
assessment before any provider request. Do not silently remove relevant source
or increase production limits to make a packet pass.

Complete source-first judgments in the authenticated instructor workflow before
viewing a new model proposal. `instructor-review.json` is only a blank worksheet:
it cannot create independently attributed references or approve a profile. Keep
unassessable scores null until instructor review. Prior assisted judgments and
instructor-authorized localized decisions retain their original attribution;
they are not new independent reference labels. Draft guidance remains inactive.

## Feedback evidence checks

`calibration-review-packet` now adds `feedback-audit.json` when supporting
judgments are supplied. It validates the judgments and their source citations
before creating output. The audit flags explicitly backticked operation calls
that are absent from cited text, plus Python comment/string-only citation lines
whose whitespace-normalized text overlaps the captured starter. Documentation
language without a cited comment/string line is marked for review.

These are bounded lexical checks, not semantic verification. Natural-language
claims, multiline expressions and uncaptured starter material still require
review. String literals need not be docstrings, matching starter text does not
prove authorship, and a valid citation does not prove the feedback claim. The
audit always requires instructor semantic review. For each criterion inspect:

1. Every credited operation and deduction, with the exact supporting source.
2. Whether a suggested change is required by the rubric or merely optional.
3. Which documentation is inherited, changed, newly authored, or uncertain.
4. Whether evidence supports a score at all, preserving null when unassessable.

Passing synthetic audit tests or size preflight is not a grading-quality result.
A fresh, independently reviewed calibration run and explicit instructor profile
approval remain separate gates before classroom use.
