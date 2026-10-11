# Brightspace grade passback

Cairn includes an opt-in LTI 1.3 resource-link launch and Assignment and Grade
Services (AGS) integration, plus a local LMS simulator. Simulation validates
Cairn's protocol and delivery behavior; it does not establish Brightspace tenant
acceptance. No production registration or deployment is enabled by default.

## Workflow

1. A Brightspace administrator registers Cairn and deploys a top-level external
   learning tool resource to a test course. The resource must expose an AGS line
   item and the score, result-readonly, and lineitem-readonly scopes (full
   lineitem access is also accepted when supplied by the platform).
2. An instructor launches the resource, signs in separately to their existing
   Cairn operator account, and explicitly connects an assignment they own in an
   operator-allowed classroom. LTI roles never create Cairn operator privileges.
3. Each learner launches the same resource, signs in to their existing Cairn
   learner account, and explicitly connects their roster identity. The mapping
   is one-to-one and cannot be silently moved to another learner or assignment.
4. The instructor reviews and publishes a Cairn assessment. In the expanded
   assignment, **Brightspace grade delivery** shows the eligible current grade
   and its delivery status. **Send to Brightspace** sends the displayed grade
   ID; a newer grade or changed source/rubric invalidates that request.
5. Cairn reads the numeric score back before recording delivery as verified.
   Uncertain results can be checked and retried within the attempt limit;
   retries reuse the stable timestamp and first inspect the LMS. Conflicting
   LMS scores stop delivery for instructor reconciliation.

Unassessable work stays pending. Automatic grading runs and model proposals
alone do not qualify for delivery. Only numeric grades leave Cairn; detailed
feedback stays in Cairn. The initial version does not include deep linking,
roster import/NRPS, bulk passback, automatic publication, or automatic conflict
overrides. Use a top-level resource launch, not an embedded iframe. Pending
launches expire after ten minutes and are invalidated by a restart. Start a
fresh launch after a failed or interrupted connection.

## Administrator setup

Use a publicly accessible HTTPS origin for Cairn. Supply these tool URLs:

- OIDC login initiation: `https://CAIRN_HOST/lti/login`
- Redirect and target link URI: `https://CAIRN_HOST/lti/launch`
- Public key set: `https://CAIRN_HOST/lti/jwks`

Obtain the issuer, client ID, deployment ID, authentication endpoint, token
endpoint and its assertion audience, platform JWKS endpoint, and AGS service
origin from the administrator. Request a sandbox course, a tool-linked numeric
gradebook item, one instructor, and two test learners with grading enabled.
These values depend on the actual registration; do not guess platform URLs.

Create an RSA private signing key of at least 2048 bits in a private regular PEM
file with mode `0600`. Store an operator-owned configuration file outside Git:

```json
{
  "issuer": "https://BRIGHTSPACE_ISSUER",
  "client_id": "ADMIN_PROVIDED_CLIENT_ID",
  "deployment_id": "ADMIN_PROVIDED_DEPLOYMENT_ID",
  "auth_url": "https://ADMIN_PROVIDED_AUTH_ENDPOINT",
  "token_url": "https://ADMIN_PROVIDED_TOKEN_ENDPOINT",
  "token_audience": "ADMIN_PROVIDED_TOKEN_AUDIENCE",
  "jwks_url": "https://ADMIN_PROVIDED_JWKS_ENDPOINT",
  "service_origin": "https://BRIGHTSPACE_AGS_HOST",
  "base_url": "https://CAIRN_HOST",
  "key_id": "cairn-key-1",
  "private_key_file": "/private/cairn-lti.pem",
  "classrooms": ["EXPLICIT_CAIRN_CLASSROOM_ID"],
  "simulation": false
}
```

Set `CAIRN_LTI_CONFIG` to that file, configure existing Cairn operator OAuth and
`CAIRN_ADMIN_USERS`, and set `CAIRN_COOKIE_SECURE=1`. Configuration is loaded at
startup; restart to change it. Production URLs require HTTPS. The configured
base and service origins have no trailing slash or path. Cairn restricts AGS
calls to that configured service origin and rejects redirects.

No credentials or launch tokens belong in a URL, repository, browser storage,
or support report. Opaque LMS subjects are retained only in private identity
bindings and are not returned to the dashboard. Apply the same access and
backup controls to the database as to existing roster data. Roster/assignment
erasure removes the associated mapping or delivery records.

## Local simulation

The standalone synthetic LMS generates private configuration and signing keys
in a new directory:

```sh
go run ./cmd/lti-simulator --dir /PRIVATE/NEW_SIMULATION_DIR \
  --classroom SYNTHETIC_CAIRN_CLASSROOM_ID \
  --cairn-url http://127.0.0.1:8080
```

Use the printed configuration path as `CAIRN_LTI_CONFIG` in a local Cairn
instance with operator authentication and `CAIRN_LISTEN_ADDR=127.0.0.1:8080`.
Simulation explicitly permits HTTP only
on `127.0.0.1`; it must never use real learner accounts or an internet-facing
listener. The simulator offers synthetic instructor and two learner launches,
score inspection and failure controls. Cairn still requires its own separately
authenticated accounts and existing roster entries.

For reproducible browser testing without real Git-host credentials, the opt-in
Go test fixture creates a new private SQLite database, synthetic approved
assessments, ephemeral test sessions, and both loopback servers:

```sh
npm --prefix web run build
CAIRN_LTI_BROWSER_DIR=/PRIVATE/NEW_BROWSER_FIXTURE \
  go test ./internal/api -run '^TestLTIBrowserFixture$' -count=1 -v
```

The directory contains `url`, `platform-url`, and private browser storage-state
files for `instructor`, `student-a`, and `student-b`. Load each state into a
separate browser context. Start with the instructor launch to map the assignment,
then learner launches to bind identities, then send each reviewed grade from
the instructor dashboard. Create a `stop` file in that directory to terminate
the fixture. Test sessions and fixture bypasses are compiled only into tests.

Before a real rollout, repeat in the administrator's sandbox: signed launch
and expired/replayed launch rejection, explicit identity linking, two distinct
learner grades, zero and pending/unassessable work, dropped response recovery,
changed grade rejection, remote instructor-edit conflict, and confirmed
readback. Confirm the displayed gradebook result in Brightspace itself. Passing
local simulation does not substitute for those acceptance checks.
