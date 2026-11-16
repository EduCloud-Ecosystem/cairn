## What & why

<!-- What does this change and why? Link any related issue. -->

## Checklist

- [ ] `go build ./...`, `go vet ./...`, and `gofmt -l .` are clean
- [ ] No structured student legal name / SIS ID / plaintext email fields are introduced; retained source text and external-provider flows match PRIVACY.md
- [ ] Host-specific code stays behind `adapter.Adapter`
- [ ] New files carry the correct SPDX header (AGPL in `internal/`+`cmd/`, Apache in `pkg/`)
- [ ] Commits are signed off (`git commit -s`, DCO)

## Shared pilot verification

## Outcome and scope

What concrete behavior changes, and which issue/spec or owner-authorized change record covers it?

## Interfaces and authority

Affected components and pinned producer/consumer revisions; compatibility, privacy, budget and lifecycle implications.

## Verification

Exact source/environment, commands and results. Include failed/skipped/unavailable controls and retained evidence. UI changes need actual interaction; workflow changes need the relevant complete journey.

## Release and recovery

State implemented/tested/merged/deployed/live-verified separately. Describe rollback, unresolved liabilities and the next gate. A badge or automated review is not student admission or spending approval.

- [ ] Followed the shared pilot protocol and component guidance.
- [ ] Signed commits (DCO); preserved path licenses and unrelated work.
- [ ] No secrets, learner data or unsupported readiness claims in this PR.
