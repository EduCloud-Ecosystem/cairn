# Finite classroom enrollment export

Implements the operator handoff portion of Cairn #37 and EduCloud #56. The consumer
is Belay-classroom's enrollment-authority branch, stacked on its roster PR #6.
This is a reviewed-file integration, not automatic institutional account linking.
Waypoint remains the OIDC authority; Cairn remains the roster, assignment,
submission and grading authority. No new service or second roster is introduced.

## What changes

`cmd/enrollment-export` opens the existing Cairn SQLite database read-only, without
migrating it. All source reads share one database snapshot. An operator-reviewed
private configuration binds a Waypoint issuer/subject to an existing Cairn roster
row and its verified immutable Git-host account ID. It also pins the full Cairn
assignment digest and the corresponding installed tutor exercise/version.
Display names, usernames and emails must never be used to infer this mapping.

Only active roster rows whose class, host and verified account ID match may yield
a grant. Missing, inactive, unverified, conflicting or changed assignment records
withhold that binding. Database errors abort the export. This does not authenticate
a learner: Belay must still verify their signed issuer/audience/subject credential.
The operator must verify the cross-system link using trusted institutional/IdP and
Cairn evidence before configuring it. These checks have only synthetic acceptance.

The export contains the existing roster ID unchanged, class/institution labels,
exercise grants, an opaque grant generation and a finite authority envelope. It
contains no Git-host account IDs, usernames, names, emails, credentials, grades,
submission contents or coaching text. Treat even this minimal export as private.

## Durable issuance and freshness

One private SQLite issuance journal belongs to each authority/source pair. It
stores only hashed binding keys/digests, opaque generations, revision numbers and
observation times. It cannot enroll anyone. Initialize it once; never delete or
restore it to reset an error. Missing/corrupt history refuses. A different source
path, issuer, audience or institution requires a separately reviewed migration.

Every successful export increments the revision. Unchanged, continuously valid
grants retain their generation across refreshes. An observed withdrawal, changed
binding, or gap past the previous snapshot's expiry rotates the next generation.
Issuance commits before atomic output publication; a failed write may leave a
revision gap, never a reused revision. Export does not extend prior snapshots.

Validity is 1–300 seconds. Belay must independently enforce its deadline and use a
persistent watermark initialized with this authority ID. Older revisions,
conflicting documents at the same revision, wrong authority, clock rollback,
missing state and legacy-file downgrade refuse. The watermark is separate from
snapshot replacement. A revoked snapshot remains an empty authorization, not an
error fallback to the prior file.

Limitations: a source change just after export remains bounded by the original
expiry. A removal and reactivation between exports is not an observed withdrawal.
Run exports at a bounded interval shorter than the lifetime and explicitly export
revocations. For immediate event-level revocation, integrate the lifecycle event
path later. Privileged rollback of both issuance and consumer history is outside
this mechanism; backup/restore procedures must preserve their monotonic history.
An old or copied source database cannot be established as current by file syntax.
The operator is responsible for selecting the authoritative local database.

## Operator commands

Build from a clean checkout using the pinned `go.mod` toolchain and `go.sum`:

```sh
go build -o /private/operator/bin/enrollment-export ./cmd/enrollment-export
# Pick one random 32-lowercase-hex authority ID for this integration.
enrollment-export -journal /private/operator/issuance.db -init-authority AUTHORITY_ID
enrollment-export -database /private/cairn/cairn.db -inspect-assignment ASSIGNMENT_ID
# Review the full assignment and its mapping; the printed digest alone is not approval.
enrollment-export -database /private/cairn/cairn.db \
  -journal /private/operator/issuance.db -config /private/operator/bindings.json \
  -output /private/operator/authorization.json
```

The output parent and journal must be operator-owned, outside learner write access.
The journal requires mode 0600; output is installed atomically with mode 0600.
Do not point this command at real learner records during synthetic preparation.
The first adapter supports SQLite only; it does not claim PostgreSQL export support.

Configuration shape (fictional values; replace the digest after actual review):

```json
{
  "authority_id": "a45abc18d8ca4ae6a7b32def7048d2ac",
  "issuer": "https://identity.test.invalid",
  "audience": "belay-tests",
  "institution_id": "institution",
  "lifetime_seconds": 120,
  "bindings": [{
    "subject": "verified-subject",
    "class_id": "course",
    "roster_id": "47a8ed4608e14957bf74ac18f93655c1",
    "host": "github",
    "host_user_id": "829174",
    "assignments": [{
      "assignment_id": "assignment",
      "assignment_sha256": "REVIEWED_SHA256_REQUIRED",
      "exercise_id": "echo-1",
      "exercise_version": "v1"
    }]
  }]
}
```

## Verification and ownership

Base: `04a9fe0a80f66ae5fa9da224b4be10aa184f9956`, merged main containing verified
roster identity and explicit submission paths. Isolated branch/worktree:
`codex/classroom-enrollment-export-v1` at
`/Users/gregchism/dev/.educloud-worktrees/cairn-classroom-enrollment-export-v1`.
Primary checkout changes and existing grading worktrees are preserved. Scope was
recorded before implementation in EduCloud `classroom-enrollment-authority-v1-scope.json`.
No existing roster mutation, grading, submission or provider lifecycle code changes.

Native SQLite tests exercise active/removed/unverified/conflicting enrollment,
assignment pins, private metadata, refresh/regrant/expiry, clock rollback,
missing/corrupt history, consistent read-only sources and protected CLI files.
The optional `ENROLLMENT_FIXTURE_DIR` test emits a synthetic sequence consumed by
Belay's executable evaluator. No private database or model/provider call is needed.

Commands: `go build ./...`, `go vet ./...`, `go test ./... -race -count=1`, and the
existing dashboard typecheck/test/build. Raw outputs and initial failed controls
are retained under `output/enrollment-v1`. Initial tests tried to mutate immutable
host IDs; Cairn correctly refused the setup. Corrected fixtures now create an
unverified row or mismatch the operator binding. An initial full-race run timed
out in the existing synthetic-runner FIFO test; its unchanged rerun passed.
See the verification JSON for final results, hashes and remaining skips.

Next: consume the reviewed binding in the Waypoint notebook path, prove current
sign-in/refresh behavior, then exercise explicit saved-file submission and result
verification. No merge, deployment, institutional sign-in or classroom launch is
established by this export.
