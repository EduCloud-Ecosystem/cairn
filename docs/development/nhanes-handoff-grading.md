# Saved-file handoff grading context

The 2026-11-14 synthetic NHANES desktop run produced a verified repository
receipt, but its queued grading job failed before execution. The handoff producer
still emitted only submission ID and revision; the grading consumer now requires
the version-1 submission, assignment, repository and instructor-policy pins.

Reuse `gradingjob.Capture` and `Target.Encode`, as the authenticated webhook and
manual grading paths already do. Queue the exact verified commit and current
pinned policy atomically with the committed receipt, preserving the shared
webhook idempotency key. An unavailable/unpinned policy preserves the verified
receipt, reports that instructor action is required, and creates no malformed
grading job. Existing historical failed jobs are not silently upgraded or replayed.

New memory/SQLite controls decode the produced job with the real consumer
contract, verify every pin, and prove an unpinned policy creates no grading job.
Existing receipt/idempotency, handoff, provisioning and targeted API controls pass.
Build/vet and the locked dashboard build pass. Initial new SQLite test used an
immediately scheduled whole-second timestamp and found no due job; the test now
places its clock one second in the past to test queue contents independently of
the scheduler. An existing API queue-claim test also returned not-found once in
the first broad run; its targeted follow-up passed. Retain that first failure;
its exact timing cause was not established here. No scheduler/deadline changed.

Starting source: main `d28f7e48cd905cf18e08331572a6d467347ebd58` in isolated
`codex/cairn-nhanes-handoff`. The failed native journey is retained in Waypoint's
`output/nhanes-qualification-v1/attempt-001`; its cleanup passed. The second,
hypothesis-specific native journey will test this exact committed repair.
This is a source fix, not a deployed service or student-release qualification.
