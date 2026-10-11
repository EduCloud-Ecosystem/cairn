# Bounded acquisition and offline runtime recovery

Cairn's container grader acquires Git sources in a separate network-enabled,
read-only container before running student commands. Build and preload the
operator-owned `Dockerfile.checkout` image; set `CAIRN_CHECKOUT_IMAGE` to its
accepted tag/digest. Acquisition refuses an unavailable image rather than pulling
or falling back to host Git. `local-exec-unsafe` remains a trusted/local mode.

Acquisition has a 512 MiB/65,536-inode tmpfs, bounded CPU/memory/PIDs, bounded
Git diagnostics and a default two-minute deadline. Export rejects symlinks,
hardlinks, special files, traversal and duplicate entries; only bounded regular
files/directories leave the container. Symlink-based coursework needs a separate
reviewed submission path. `CAIRN_CHECKOUT_TIMEOUT` defaults to `2m`; `CAIRN_GRADER_TOTAL_TIMEOUT`
defaults to `15m`. The grading service also has an aggregate deadline;
per-step and writable execution limits still apply. Concurrent acquisitions,
retained assessment captures, images and other host services still consume
aggregate capacity: these per-job bounds are not a host-wide storage guarantee.

## Inspect and recover after a crash

Use the **same canonical work directory / TMPDIR and daemon** as the failed
worker. Stop all servers/assessment workers using it before recovery. For a
containerized Cairn service, run the CLI with the same work-directory bind and
runtime socket as the service. Do not delete or replace `.cairn-runtime.lock`:
it contains the persistent random ownership identity and coordinates active
workers with recovery. Separate hosts with the same path have separate identities.

```sh
cairn recover-runtime --work-dir /var/lib/cairn/work --plan recovery-plan.json
# Inspect the private JSON. This first command performs no deletions.
cairn recover-runtime --work-dir /var/lib/cairn/work --apply recovery-plan.json
```

The default is JSON inventory on stdout; `--plan` creates a new 0600 file and
refuses overwrites. `--runtime podman` selects that executable but requires its
own deployment acceptance; Docker is the tested path. Keep plans private.

The exclusive runtime lock refuses active grading/acquisition. The inventory
includes only resources carrying this directory's ownership scope and expected
resource kind/name: acquisition/grading containers, bounded execution volumes,
and private `cairn-job-*` parents with matching ownership markers. Plans contain
container IDs, volume creation timestamps and directory timestamps. Apply checks
the entire current inventory against the saved plan before deleting anything,
then removes containers, unmounted volumes and job directories in that order.
It never forces removal of a mounted volume. A failure stops the operation;
inspect and generate a new plan for the remaining resources. Reusing a stale
plan fails instead of silently extending its scope.

This is offline crash cleanup, not job resume. Inspect failed/running grading
records and requeue through the normal instructor workflow after recovery;
cleanup does not manufacture successful grades or rewrite database state.
Legacy unlabelled resources, old `cairn-grade-*`/`cairn-policy-*` host directories,
images, assessment captures and unrelated containers are deliberately excluded.
Inspect those manually; a prefix alone is not proof of ownership. Do not run
older worker binaries concurrently with this recovery tool: they do not acquire
the coordination lock. The deployment must restrict Docker/socket and work-dir
write authority to trusted operators.

## Verification

```sh
go test -race ./internal/runtimeguard
CAIRN_RECOVERY_INTEGRATION_IMAGE=<preloaded-shell-image> \
  go test -race ./internal/runtimeguard -run TestRuntimeRecoveryDocker -count=1
```

The Docker test creates an invocation-owned container, volume and job directory,
then verifies offline cleanup leaves no matching resources. Unit tests cover
active-worker exclusion, stale plans, symlink/unmarked rejection and stopping on
partial failure. This is not a host reboot or provider-disaster recovery claim.
