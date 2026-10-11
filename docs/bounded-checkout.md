# Bounded repository acquisition

Container grading and assessment review acquire repositories in a disposable
container before student execution. Build the operator-owned image on the worker
daemon first:

```sh
docker build -f Dockerfile.checkout -t cairn-checkout:local .
```

`CAIRN_CHECKOUT_IMAGE` selects that preloaded image; jobs use `--pull=never` and
fail if it is missing. The image needs `/bin/sh`, Git and tar. Prefer a reviewed
immutable digest in a hosted deployment. `cairn doctor` checks runtime and image
availability even if assessment is enabled without the grading worker. It does
not claim a successful clone or verify kernel enforcement.

Each acquisition has a 512 MiB, 65,536-inode tmpfs, a read-only container root,
768 MiB RAM with no swap allowance, one CPU and 64 PIDs. It receives no host bind
mount. Git's object database and working tree share that filesystem quota; even
a highly compressed oversized repository must fit before it can be exported.
The Git endpoint must be reachable from the daemon's default bridge network.
Container-local service DNS and a host's loopback address are not inherited;
private certificate authorities/proxy settings need an operator-reviewed acquisition
image/network recipe. Do not disable TLS checks to bypass this requirement.
The shared host kernel
is still a boundary limitation; acquisition does not claim a microVM boundary.

The token enters stdin and is used by an askpass helper on tmpfs, not stored in
Docker environment metadata or process arguments. Git diagnostics drain into a
64 KiB buffer and redact the token before returning an error. Container logging
is disabled. The returned archive excludes `.git`; extraction rejects traversal,
duplicate entries, links and special files and caps regular file bytes and tar
entries at the same limits. Symlink-containing coursework therefore fails closed
and needs instructor review or a regular-file version. This is a compatibility
limit, not a low score. Directory traversal is not followed during extraction.

The host receives at most 512 MiB per acquisition, inside a private grading job
directory; a grading job can hold one policy and one submission. Plan host free
space and concurrency for these copies and the daemon's temporary memory. Per-job
bounds are not an aggregate service quota. Grading cleanup removes the parent
job directory; the offline recovery command handles verified abandoned jobs.

`CAIRN_CHECKOUT_TIMEOUT` defaults to `2m` for the full clone/fetch/export sequence.
`CAIRN_GRADER_TOTAL_TIMEOUT` defaults to `15m` for both acquisitions plus all setup
and grading steps. They must be positive durations. Whole-run cancellation marks
the grading run failed and writes no grade; failure persistence has a separate
10-second deadline. Runtime cleanup has a separate bounded deadline so it can
still remove the acquisition container after cancellation. Cleanup failures are
reported for operator recovery.

The explicitly unsafe local runner keeps host Git acquisition for trusted local
fixtures and has no filesystem quota. Its Git process output and acquisition
deadline are still bounded. Do not use it for untrusted student work.

## Verification

```sh
go test ./internal/grading ./internal/doctor
CAIRN_CHECKOUT_DOCKER_TEST=1 go test ./internal/grading -run TestCheckoutDockerAcquisition -v
```

The opt-in Docker test builds a synthetic local fixture image, observes kernel
`ENOSPC` for an oversized acquisition, checks stalled-process cleanup and token
redaction, and completes a real source/policy acquisition and container grading
run. It needs no real Git credentials or external repository. Fixture images and
containers are removed at the end. This is local component evidence; hosted
storage, aggregate load and off-host recovery still need deployment rehearsal.
