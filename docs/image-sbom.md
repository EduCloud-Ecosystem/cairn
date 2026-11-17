# Cairn Linux/amd64 image SBOM

The container CI job builds the main control-plane Dockerfile, including the
dashboard, for Linux/amd64 and saves the image once. It scans that archive with
the existing Trivy check. The SPDX verifier reads the archive's OCI index,
selects the runnable Linux/amd64 image manifest, and checks the manifest,
config, and layer descriptor sizes and SHA-256 digests. It matches each
uncompressed layer hash to the config's root filesystem list and checks the source
revision label against the build checkout. The SPDX image package embeds the
platform manifest digest and a link to that Git revision.

The verifier creates a stopped container from the local CI image and confirms
its selected platform manifest digest before copying any bytes. It checks the Alpine installed-package database and the Go
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
validation. Existing Trivy vulnerability checks run for both images.

The inventory covers installed Alpine packages, the compiled Go executable,
and npm packages contributing rendered JavaScript to the built dashboard.
This control-plane inventory excludes build-only tools, the separate checkout
and grading images, mounted services, and other architectures. Package licenses are
unasserted. The image uses floating base tags and Alpine repositories, so a
second build at the same Git commit can produce a different image digest.
The CI artifact is a verification output, with no registry publication step.

## Checkout image

The container job also builds and saves `Dockerfile.checkout` for Linux/amd64
with the source revision label. The verifier runs in `--checkout` mode against
that archive and its Trivy report. It verifies the manifest, config, and layers,
matches the manifest Docker selected for a stopped container, and reconciles
every installed Alpine package name and version against the report for that
config digest. The checkout image SBOM covers the APK inventory of this
repository acquisition helper. Student grading images have separate inventories.

The CI artifact `cairn-checkout-linux-amd64-sbom` includes the validated SPDX 2.3
JSON, raw OCI manifest, and their checksums. Run
`sha256sum --check cairn-checkout-linux-amd64.SHA256SUMS` in the artifact directory.
The manifest SHA-256 must match the checksum on `SPDXRef-Cairn-Checkout-Image`.
The checkout recipe also uses a floating Alpine tag and package repositories,
so this evidence applies to the specific saved image. The artifact stays in CI.

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
