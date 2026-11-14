# Go 1.26.11 security CI repair

Cairn main CI run43551385844 failed its unchanged vulnerability gate after
PR38 merged asfc038eb93cba16fe8265718e8472aca6f9ecf2d9. The scanner reported
GO-2026-6859 in the pinned Go1.26.10 standard library. The official Go database
lists1.26.11 as the fixed release on this supported release line:
https://vuln.go.dev/ID/GO-2026-6859.json

Reuse the existing exact-version build policy: update go.mod and the container
build stage together to1.26.11. No application behavior, module dependency,
scanner exclusion, deadline, or runtime deployment is changed. The official
Go download index and golang:1.26.11-alpine manifest were checked before editing.

Local Go1.26.11 build and vet pass; govulncheck1.1.4 reports no vulnerabilities.
The first broad test run failed the existing two-second named-pipe CLI check
while the new toolchain compiled in parallel with other local verification.
That is retained as a failure, not proof of its cause; no test deadline is changed.
The focused test passed three consecutive runs (config0.22–0.23seconds,
policy0.01seconds), and a fresh `go test ./... -count=1` passed. The initial
delay remains unexplained; the FIFO refusal and original deadline are unchanged.
PostgreSQL/race and image checks remain CI gates. Merging this source patch does
not rebuild or replace the desktop pilot's previously pinned local images or
claim that an existing deployment has been patched.

Checkpoint: /Users/gregchism/.codex/worktrees/cairn-go-security-ci/cairn,
branch codex/cairn-go-security-ci, basefc038eb. Scope is the two version pins
plus this record. Local scanner and follow-up test logs are in ignored
output/go-security-ci/. Next: require successful exact-candidate CI before merge.
