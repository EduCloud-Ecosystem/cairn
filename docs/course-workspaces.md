# Browser Python/R workspaces

Cairn supplies the class, assignments, repositories and student work page. A
separate JupyterHub supplies browser Python/R compute. This keeps coursework
usable when an individual Codespaces allowance runs out, and keeps learner
execution off the Cairn database/identity host.

## Connect a course

1. Deploy the Waypoint `course-workspace/` recipe (one Hub per course), or an
   equivalent institution-operated JupyterHub. Establish its approved roster,
   HTTPS, persistent homes, resource limits and backup/restore procedure first.
2. Copy the classroom ID returned by Cairn's classroom API and set an operator
   configuration map, for example:

   ```dotenv
   CAIRN_WORKSPACE_URLS={"classroom-id":"https://work.example.edu"}
   ```

3. Restart Cairn. Invalid origins fail startup. Only HTTPS origins are accepted;
   HTTP on 127.0.0.1 or ::1 is permitted for a local synthetic rehearsal.
4. An authenticated learner sees **Open Python/R workspace** on their own
   assignment cards at `/me`. Unconfigured classes retain the existing view.

The link is navigation, not a credential exchange or proof of enrollment. It
contains no learner ID, repository URL, token or grades. The destination must
perform its own authentication and course authorization; joining Cairn does not
automatically enroll a learner in the Hub. A separate Hub sign-in may be needed.
Cairn must use its own operator authentication before any public course pilot.

## Continuity acceptance

- Execute a Python notebook and an R notebook using the course image.
- Stop and recreate a learner's workspace; saved files must reappear.
- Verify a second learner cannot read the first learner's files.
- Stop the course, create an off-host recovery archive and restore onto a fresh
  deployment, using the same identity issuer/subject mapping and course image.
- Revoke a learner in the Hub, terminate active sessions/servers, and verify
  access is denied. Removing a navigation link alone never revokes access.

The current Waypoint pilot blocks notebook network egress. Use authenticated
file upload/download to move coursework; Git push/pull inside the workspace
requires a separately reviewed egress and credential design. Saving a notebook
is not submitting it to Cairn: students still upload/commit their deliverables
to the assigned repository. Automatic synchronization is not implemented.

This integration does not resolve the separate instructor-owned grading-manifest
and resource-ceiling findings recorded in the September 2026 audit. Those remain
required before trusting grades from learner-editable submissions.
