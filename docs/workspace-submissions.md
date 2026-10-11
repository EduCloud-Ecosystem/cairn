# Explicit saved-file submissions

Cairn can accept selected coursework files from the learner's device or an
operator-configured Waypoint workspace. The learner authenticates with Cairn,
selects an assignment, previews exact file paths, byte counts and SHA-256 hashes,
and confirms. Saving or executing a notebook never submits it automatically.

Each confirmation creates a durable receipt and queues one Git commit. The commit
preserves unselected files and has the previewed branch head as its only parent.
A conditional push refuses a concurrent branch change. Retry uses the same
receipt, frozen bytes and commit metadata; it never rebases or silently replaces
newer work. The normal immutable-revision grading queue and signed webhook use
the same deduplication key, so delivery through both paths grades once.

## Enable for a synthetic course

Use the normal authenticated Cairn deployment and prebuild its bounded checkout
image. Back up the database before upgrading: migration 0012 adds receipt and
operation storage. Do not enable unsafe host checkout.

```text
CAIRN_SUBMISSIONS=true
CAIRN_SUBMISSION_CLASSROOMS=<approved classroom ID,comma-separated if needed>
CAIRN_OAUTH_REDIRECT_URL=https://cairn.example.edu/auth/callback
CAIRN_COOKIE_SECURE=1
CAIRN_CHECKOUT_IMAGE=<operator-built checkout image>
CAIRN_WORKSPACE_URLS={"<classroom ID>":"https://work.example.edu"}
```

The existing host Git credentials must permit writes to the approved assignment
repositories. They remain inside Cairn's bounded Git writer and are never sent to
the notebook or browser. No new provider token is requested from the learner.
The writer uses HTTP(S) Git transport and requires a SHA-1 repository. GitHub,
GitLab and Forgejo share the Git protocol, but the combined course pilot exercises
Forgejo; other providers still need their own live synthetic acceptance.

On Waypoint, configure `WORKSPACE_CAIRN_ORIGIN=https://cairn.example.edu` and rebuild
the course image. The JupyterLab **Submit saved files to Cairn** link opens a
saved-file picker. The browser transfers selected bytes to a Cairn popup only
after the learner selects a matching assignment and explicitly receives them.
The Cairn workspace mapping permits its exact origin or one HTTPS per-user DNS
label below that host, with the same port. Both services authenticate separately;
there is no implicit OIDC-to-Git-account mapping or shared bearer token.

For local synthetic tests only, Cairn accepts a loopback HTTP callback. Waypoint
also requires explicit `WORKSPACE_CAIRN_LOCAL_TEST=1` to target HTTP loopback. This
does not relax institutional workspace TLS, roster checks or notebook networking.

## Student behavior and limits

Use **My work → Submit saved files & view receipts**, or enter from Waypoint.
Inspect the signed-in Cairn username and course before confirming. Device uploads
use each file's basename; Waypoint preserves paths relative to the notebook root.
Selection is limited to 32 files, 1 MiB per file and 4 MiB total. Paths are at most
240 ASCII characters, using letters, numbers, underscores, hyphens and extensions.
Hidden files, unsafe paths, links, submodules and automation/configuration files
are rejected. Use the existing Git workflow for unsupported files or larger work.
This is additive file replacement, not repository synchronization or file deletion.

Previews expire after 15 minutes and there can be at most three unresolved
operations per submission. Expiry removes unconfirmed source bytes; successful
or definitively refused operations retain receipt metadata but drop payloads.
Learner erasure removes their receipt rows through the existing deletion path.
Review the deployment's database backup retention separately.

The learner must still be active and the assignment open before confirmation and
worker execution. Confirm before the deadline with time for queued execution.
Do not interpret a queued receipt as a deadline exemption.

| Receipt | Meaning and next action |
| --- | --- |
| Preview | Files reviewed, nothing submitted. Confirm before expiry. |
| Queued / publishing | Request received; repository acceptance is still pending. Refresh receipts. |
| Committed | Exact recorded Git commit verified. Grading and instructor review happen separately. |
| Conflict | Repository changed before a first push. Preview the latest state again. |
| Verification needed | A remote outcome may have occurred. Keep the receipt and ask the instructor to reconcile it before submitting again. |
| Refused / failed / expired | Inspect the explanation, retain the original saved files, then correct access or create a fresh preview when appropriate. |

For an uncertain receipt, an operator should inspect the recorded candidate commit,
base, branch and exact manifest against the Git host's branch and history. Commit
object existence alone does not prove that a branch update happened. Do not erase
the receipt, retry with a new key, force a branch or edit the database to invent
success. The pilot intentionally leaves ambiguous outcomes for instructor/operator
review; automatic ancestry adjudication and an operator reconciliation UI are
follow-up work.

## API contract

Student-scoped routes require the provider-verified session and stable account
ownership. POSTs require JSON and an exact `Origin` matching the configured Cairn
origin; forwarded headers never establish that authority.

```text
POST /me/work/{submissionID}/submission-previews
  {"idempotency_key":"<8–128 ASCII letters/digits/_/->",
   "files":[{"path":"solution.py","content":"<base64 bytes>"}]}
POST /me/work/{submissionID}/submission-previews/{receiptID}/confirm
  {}
GET /me/work/{submissionID}/submission-receipts
GET /me/work/{submissionID}/submission-receipts/{receiptID}
```

Same key and identical bytes recover the same preview; changed bytes conflict.
Confirming the same receipt cannot create a second job. Receipt JSON contains
identifiers, state, hashes and commit provenance, not source bytes, host tokens or
stable provider IDs. Never expose receipt endpoints as a public artifact catalog.

## Verification boundary

Unit/race and database conformance checks exercise identity, deadline/lock changes,
expiry, retention, atomic queueing and restart paths. The bounded writer's Docker
suite exercises authenticated smart HTTP, deterministic commits, concurrent branch
updates and response-loss reconciliation. Waypoint's disposable course rehearsal
uses the real browser preview/confirmation, Forgejo OAuth/push delivery and pinned
grading. These are synthetic acceptance checks, not hosted deployment, institutional
SSO acceptance, Brightspace passback or class-size capacity evidence.
