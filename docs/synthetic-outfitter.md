# Disposable synthetic Outfitter runner

`internal/syntheticoutfitter` implements Cairn's `Runner` and `PolicyRunner`
interfaces for a deliberately narrow fixture. Only the separate
`cmd/cairn-outfitter-synthetic` command instantiates it. Cairn's service,
provisioning defaults, server configuration, migrations, and pilot deployment
are unchanged. It creates a proposed `grading.Result`, never a stored `Grade`.

This is local synthetic integration with accepted Outfitter
`273bb0148fead33c91fcb92e0cc9e0bacdb929c2` (PR #9). Docker remains the baseline.
Live Jetstream2 testing remains **BLOCKED pending Greg's separate approval**.
No live provider, allocation, real course/student data, production deployment,
or additional dependency purchase is authorized by this harness. Ben's #5
broader live lifecycle and second-operator acceptance remain open.

## Supported fixture

The caller supplies an explicit synthetic opt-in, opaque project/job key,
absolute paths, the broker binary's SHA-256, original authority ID, full
submission and policy commits, policy repository/path/hash, and separate
Git-free snapshots. Policy bytes must parse to exactly the supplied grading
spec and match the manifest entry and declared SHA-256. Hashes bind bytes;
they do not authenticate Git provenance or instructor authorization. The
acceptance harness creates actual local Git fixture commits.

Only version 1, one `python /input/submission.py` check, no setup or per-test
overrides, a digest-pinned installed image, no network, at most 1 CPU,
256 MiB RAM, 64 PIDs, 30 seconds and 65,536 output bytes are supported.
Input and policy manifests together have at most 128 files and 16 MiB.
Each snapshot is limited to 512 total entries; special files and symlinks are
refused before inspection, with nonblocking descriptor checks for replacement.
Arbitrary shell commands, multi-step policies, writable workspaces, remote
checkouts and general course execution are outside this profile.

## Durable identity and recovery

The private binding directory contains a process lock and one checksummed
intent per `(consumer, project, job, attempt=1)`. It records the request,
broker binding hash, policy/scoring semantics, manifests, immutable image and
limits, binary pin and original authority ID. The intent file and directory
are synchronized before the sole `consumer-run`. This is a binding record,
not another accounting ledger: Outfitter owns reservations, charges and cleanup.

After intent persistence every repeated call is `consumer-status` only.
Cancellation, process loss, lost response, absent receipt and budget refusal
never create another attempt. Outfitter does not durably record budget refusal,
so a refused job remains unassessable on later lookup. A crash between intent
and admission can leave a permanent hold with no execution; it is safer to
retain that uncertainty than to guess that dispatch did not occur. Partial or
corrupt intents fail closed. Preserve binding records with broker state; never
delete them to retry. Changing a job key is a new authorized job, not recovery.

`--mode status` reads the saved intent without either original checkout.
`--mode run` also verifies the supplied current snapshots/spec against that
intent; use status after the snapshots disappear. A different binary or
authority is refused. To read an explicitly restored authority, set its state
path and `recovery_checkpoint_sha256`; its source authority and checkpoint must
match the original pin. Recovery does not activate the authority or dispatch
on a held restore. `--mode cancel` operates only on the original active
authority and produces no score.
It reports success only after a matching terminal receipt verifies cleanup and
settled accounting; unknown or failed cleanup remains an explicit error. The
harness retains per-job cleanup outcomes and fails on outstanding liabilities.

Cairn scores only a complete matching receipt with execution started,
verified cleanup, no unresolved liability/reconciliation/output error, a
recognized terminal status, and the exact reserved/charged upper-bound
accounting. Exit zero is mandatory even when stdout matches the expectation.
Wrong output or a recognized nonzero workload exit is a failed test;
missing output, timeout, cancellation and infrastructure failure return an
error with no result. Unknown work is never a zero grade or a refund.

The local operator and pinned broker binary are trusted. This is not a
multi-tenant filesystem boundary against an operator replacing files or
rolling back both stores. Same-host persistence/watchdog evidence does not
prove host-loss cleanup or provider self-expiry.

## Reproduce

Build the pinned broker from the accepted commit and record its revision and
binary hash. Have the exact image already installed locally; the harness
never pulls, prunes or restarts Docker. CI explicitly prepares that public
pinned image on its disposable runner.

```sh
mkdir -p .runtime/bin
go build -o .runtime/bin/cairn-outfitter-synthetic ./cmd/cairn-outfitter-synthetic
python3 tools/synthetic-outfitter/verify.py \
  --cairn-binary .runtime/bin/cairn-outfitter-synthetic \
  --outfitter-binary /absolute/path/to/pinned/outfitter \
  --image python@sha256:229a2c5bfa27522db7815ea81f9bed70af17ccb9de9fc7ad142b1877b5830d36 \
  --output .runtime/cairn-outfitter-acceptance.json
```

The cooperative `/tmp/educloud-docker-acceptance.lock` excludes overlapping
participating rehearsals. The harness admits five sequential workloads under
a total 150 CPU-second ceiling and separately refuses a 1 CPU-second account.
It checks correct/wrong/nonzero results, changed input/policy/submission,
repeated delivery, cancellation, client loss/deadline, missing source paths,
budget refusal, held recovery and exact fixture-container absence. Unit tests
cover ambiguous/malformed/missing receipts, corruption, concurrency, and the
existing generic worker retry loop. Evidence records source/file/binary/image
and fixture pins and retains the wrong-answer control.

The [local acceptance record](evidence/2026-10-17-synthetic-outfitter.json)
contains 19 passing controls and explicit cleanup outcomes. The
[independent review record](evidence/2026-10-17-synthetic-outfitter-review.json)
retains the reproduced findings, fixes and rechecks. These are pinned local
records; the PR separately records final-head CI and merge status.

## Production handoff still required

The Service now carries [pinned queue context](pinned-grading-jobs.md) through an
optional job-aware policy-runner interface. It still creates a separate
`GradingRun` delivery record on infrastructure retry. This synthetic adapter
does not implement that new interface and remains a separately configured
single-job fixture. **Do not install it in the Service.** A separate reviewed
integration must bind each job to its adapter intent, qualify authenticated
pinned checkouts, define operator reconciliation and attempt authorization,
and publish/replace grades atomically
through Cairn's existing assessment path. Real course admission, live provider
accounting, host-loss recovery and independent human reproduction are separate
acceptance gates.
