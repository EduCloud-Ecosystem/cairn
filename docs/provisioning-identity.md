# Repository access and stable provider identities

Repository provisioning requires both a persisted submission-to-repository binding
and the roster entry's verified `HostUserID`. Every grant, including a retry after
an unrelated webhook failure, uses the adapter's `VerifiedCollaborator` capability.
An adapter without that capability or a roster entry without a bound provider ID
fails closed; the worker never falls back to a username-only grant.

New repository names derive from the immutable submission ID. Fresh creation
rejects existing repositories and creation races. The worker saves the returned
matching repository reference before granting access. A creation whose outcome is
unknown, or whose binding cannot be saved, requires operator reconciliation.
Existing legacy repositories remain addressable through their saved references,
but their old naming is not automatically adopted by new creation jobs.

## Provider behavior

- GitLab verifies that an exact username lookup returns one matching numeric ID,
  then grants membership using that numeric ID. A username reassignment between
  lookup and membership creation cannot redirect the grant to a different ID.
- GitHub checks the username and numeric ID immediately before and after its
  username-based collaborator request. When the response contains an invitation,
  it also checks the invitee identity. A failed post-check triggers an attempt to
  cancel the returned invitation and revoke the collaborator.
- Forgejo/Gitea checks the username and numeric ID immediately before and after
  its username-based collaborator request. A failed post-check triggers an
  attempt to revoke the collaborator.

Cleanup uses a fresh context bounded to ten seconds, independent of cancellation
of the original request. Failed cleanup is included in the provisioning error
with an explicit reconciliation requirement; a failed verification never marks
that attempt successful.

The provider contracts are documented in the [GitHub collaborator API](https://docs.github.com/en/rest/collaborators/collaborators#add-a-repository-collaborator),
[GitHub invitation API](https://docs.github.com/en/rest/collaborators/invitations),
[Gitea collaborator API](https://docs.gitea.com/api/1.24/operations/repo-add-collaborator/),
[Gitea user API](https://docs.gitea.com/api/1.24/operations/user-get/), and
[GitLab project membership API](https://docs.gitlab.com/api/project_members/).

## Remaining race and reconciliation requirements

GitHub and Forgejo/Gitea do not expose an atomic expected-user-ID condition on the
username-based grant used here. Pre/post checks detect stale queued names and many
concurrent renames, but cannot eliminate a rename/reassignment during the grant.
A wrong recipient could obtain temporary access before cleanup; source already
read cannot be recovered by revocation. Repeated renames can also move the affected
account away from the username used for cleanup. A GitHub request with an unknown
outcome may leave an invitation whose ID was not received. Successful cleanup
requests therefore do not prove that every possible raced grant was removed.

After an identity or reconciliation failure, operators must inspect provider
collaborators and pending invitations by immutable account identity, remove any
incorrect access, and verify the roster binding before retrying. Do not clear or
replace a bound ID merely to bypass the check. Legacy/imported roster entries
without verified identities require deliberate reconciliation before provisioning.
These controls have local deterministic tests; no live provider rename race or
production deployment is claimed as verified.
