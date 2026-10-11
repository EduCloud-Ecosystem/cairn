## What & why

<!-- What does this change and why? Link any related issue. -->

## Checklist

- [ ] `go build ./...`, `go vet ./...`, and `gofmt -l .` are clean
- [ ] No structured student legal name / SIS ID / plaintext email fields are introduced; retained source text and external-provider flows match PRIVACY.md
- [ ] Host-specific code stays behind `adapter.Adapter`
- [ ] New files carry the correct SPDX header (AGPL in `internal/`+`cmd/`, Apache in `pkg/`)
- [ ] Commits are signed off (`git commit -s`, DCO)
