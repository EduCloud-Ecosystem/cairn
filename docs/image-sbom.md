# Cairn Linux/amd64 image SBOM

The container CI job builds the main control-plane Dockerfile, including the
dashboard, for Linux/amd64 and saves the image once. It scans that archive with
the existing Trivy check. The SPDX verifier reads the archive's OCI index,
selects the runnable Linux/amd64 image manifest, and checks the manifest,
config, and layer descriptor sizes and SHA-256 digests. It checks the source
revision label against the build checkout. The SPDX image package embeds the
platform manifest digest and a link to that Git revision.

The verifier copies evidence from a stopped container created by the verified
config digest. It checks the Alpine installed-package database and the Go
binary's embedded module list against the Trivy report for that same config
digest. The Go toolchain version identifies the embedded standard library.
The dashboard build records npm packages that contributed rendered Rollup
modules to the shipped chunks. It compares the installed package identities
against the lockfile and records hashes for the generated dashboard files.
The image verifier checks those hashes and package identities again against
the actual files in the image and the checkout's lockfile. The evidence JSON
lives under `/usr/local/share/cairn/`, outside the served dashboard.

The CI artifact `cairn-linux-amd64-sbom` contains the validated SPDX 2.3 JSON,
the exact platform manifest bytes, and their SHA-256 checksums. Run
`sha256sum --check cairn-linux-amd64.SHA256SUMS` in the artifact directory. The
manifest checksum should also match the SHA-256 on `SPDXRef-Cairn-Image`.
CI uses `pyspdxtools` from SPDX tools-python 0.8.5 for format and relationship
validation. Existing Trivy vulnerability checks still run for both images.

The inventory covers installed Alpine packages, the compiled Go executable,
and npm packages contributing rendered JavaScript to the built dashboard.
It does not inventory build-only tools, separately built checkout or grading
images, mounted services, or another architecture. Package licenses are
unasserted. The image uses floating base tags and Alpine repositories, so a
second build at the same Git commit can produce a different image digest.
The CI artifact is a verification output, with no registry publication step.

## Change record

The isolated `codex/cairn-image-spdx` branch started at Cairn main
`1b6bf0d04a63f546414aed98ed08f8d93734c45c`. This extends the existing
container build and scan job with a single digest-bound inventory and an
independent reconciliation step. Focused tests reject a changed OCI layer,
incorrect platform or revision, a foreign scanner report, package mismatches,
and changed dashboard bytes. The opt-in local dashboard build identified
React 18.3.1, React DOM 18.3.1, and Scheduler 0.23.2. The full CI build verifies
the inventory against the shipped image. Review that artifact before any
registry or release decision.
