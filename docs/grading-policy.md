# Instructor-owned grading policy

Cairn loads grading rules from the instructor template at an explicit full Git
commit ID. It never loads them from the learner checkout. Existing unpinned
assignments must select an instructor version before another grade can run;
historical grades are retained unchanged and are not retroactively verified.

## Select a version

In the instructor dashboard, expand an assignment and save its **Instructor
grading version**: a full 40- or 64-character commit ID from that assignment's
template repository and the relative grading-file path (usually grading.json).
A branch/tag such as main is rejected. The Grade button stays disabled until a
commit is saved. The server independently rejects unpinned grade requests.

The same operation is available to authenticated operators:

```http
PATCH /assignments/{id}/grading-policy
Content-Type: application/json

{"template_commit":"FULL_COMMIT_ID","grading_spec":"grading.json"}
```

This deliberately updates the assignment's existing template.ref, which also
selects the template version for future repository provisioning where the host
adapter supports it. Existing learner repositories are not rewritten. Creating
an assignment with template.ref set to a full commit pins it immediately.

The instructor must control the selected repository and review the selected
commit. A Git object ID is immutable content identification, not proof of who
owns a repository or whether its tests are sound. The clone credential must have
read access to both the instructor template and learner repositories. Git fetch
must support the selected historical object; a failed/mismatched fetch fails the
grade, with no fallback to a moving branch or learner manifest.

## Keep test code outside the learner checkout

The container runner mounts the pinned instructor checkout read-only at
/cairn-policy and supplies CAIRN_POLICY_DIR. The student checkout remains the
working directory /work. Reference instructor test files explicitly:

```json
{
  "version": "1",
  "tests": [
    {"name": "answer", "run": "sh \"$CAIRN_POLICY_DIR/check.sh\"", "points": 10}
  ]
}
```

A command such as `sh check.sh` still runs the learner-editable copy in /work.
Move grading harnesses/fixtures to the instructor path, review language import
paths and adversarial behavior, and keep assessment logic independent of learner
self-reports. The mount protects files from writes; it does not make arbitrary
in-process test harnesses immune to malicious code. Instructor files are readable
inside the sandbox: this is not a hidden-answer secrecy mechanism.

The pinned checkout has VCS metadata removed before mounting. Spec paths must
remain inside its root, including when symlinks are present. Manifests over 1 MiB,
empty test commands, invalid point values and unsupported versions fail closed.
Every completed grade/run's result JSON records the instructor repository,
commit, spec path and SHA-256 of the manifest. Historical scores retain the
policy they used even if an operator later selects another version.

## Operator resource ceilings

The container runner limits both setup and test steps, including per-test
resource overrides. Defaults remain 30 seconds, 512 MiB and one CPU; ceilings are:

```dotenv
CAIRN_GRADER_MAX_TIMEOUT=5m
CAIRN_GRADER_MAX_MEMORY_MB=2048
CAIRN_GRADER_MAX_CPUS=2
```

Configure lower or higher positive ceilings for the worker's capacity. Requests
above a ceiling are clamped. Non-finite/invalid operator settings fail startup.
These are per-step ceilings, not a total course/run budget: test count, aggregate
runtime/concurrency, output volume and filesystem quotas need separate controls.
The explicitly unsafe local-exec runner still has no filesystem/CPU/RAM sandbox;
use it only for trusted local material. Real learner deployments use containers
and the appropriate kernel isolation tier.

## Validation (October 7, 2026)

- Full Go race suite; API race suite repeated after adding the grade-request gate.
- Historical Git commit test: advancing template HEAD did not change the pinned file.
- Synthetic forged learner grading.json (999 points) could not change the score.
- Unpinned revisions, parent paths and escaping symlinks wrote no grade.
- Anonymous/student policy updates were denied; the operator pin persisted.
- Setup/per-test resource overrides were bounded by operator ceilings.
- Real Docker acceptance using the local Python/R course image: wrong answer 0/10,
  correct answer 10/10, instructor script overwrite refused in both runs.
- Dashboard typecheck, 25 existing tests and production build passed. Playwright
  exercised the real local API: branch rejected, commit saved, Grade enabled, and
  the saved commit survived browser reload. This used a synthetic local fixture.

This closes the specific learner-manifest and per-step resource-ceiling findings.
It does not establish full assessment integrity against every malicious submission,
validate institutional TLS/OIDC, or retroactively certify existing scores.
