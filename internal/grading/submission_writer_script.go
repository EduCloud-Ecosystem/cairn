// SPDX-License-Identifier: AGPL-3.0-or-later
package grading

// The payload tar is generated exclusively by submissionPayload after path and
// size validation. Git trees remain objects: no checkout, read-tree -u, filters,
// hooks, submodule traversal or repository-provided commands are executed.
const submissionObjectScript = `set -efu
IFS= read -r CAIRN_GIT_TOKEN
export CAIRN_GIT_TOKEN GIT_TERMINAL_PROMPT=0 GIT_CONFIG_NOSYSTEM=1 GIT_CONFIG_GLOBAL=/dev/null HOME=/scratch
export GIT_LITERAL_PATHSPECS=1 GIT_CONFIG_COUNT=7
export GIT_CONFIG_KEY_0=credential.helper
export GIT_CONFIG_VALUE_0='!f() { if [ "$1" = get ]; then printf "password=%s\n" "$CAIRN_GIT_TOKEN"; fi; }; f'
export GIT_CONFIG_KEY_1=core.hooksPath GIT_CONFIG_VALUE_1=/dev/null
export GIT_CONFIG_KEY_2=protocol.allow GIT_CONFIG_VALUE_2=never
export GIT_CONFIG_KEY_3=protocol.http.allow GIT_CONFIG_VALUE_3=always
export GIT_CONFIG_KEY_4=protocol.https.allow GIT_CONFIG_VALUE_4=always
export GIT_CONFIG_KEY_5=http.followRedirects GIT_CONFIG_VALUE_5=false
export GIT_CONFIG_KEY_6=credential.useHttpPath GIT_CONFIG_VALUE_6=true
operation=$1
endpoint=$2
intended=$3
mkdir /scratch/input
# Credentials never enter the payload directory or repository config.
tar -xf - -C /scratch/input
branch=$(cat /scratch/input/branch)
base=$(cat /scratch/input/base)
retry=$(cat /scratch/input/retry)
remote_state() {
 git ls-remote --symref -- "$endpoint" HEAD "$branch" > /scratch/remote || return 1
 remote_branch=$(awk '$1 == "ref:" && $3 == "HEAD" {print $2}' /scratch/remote)
 remote_sha=$(awk -v branch="$branch" '$2 == branch && $1 != "ref:" {print $1}' /scratch/remote)
}
if [ "$operation" = inspect ]; then
 git ls-remote --symref -- "$endpoint" HEAD > /scratch/remote
 branch=$(awk '$1 == "ref:" && $3 == "HEAD" {print $2}' /scratch/remote)
 base=$(awk '$2 == "HEAD" && $1 != "ref:" {print $1}' /scratch/remote)
fi
case "$branch" in refs/heads/*) ;; *) exit 20;; esac
git check-ref-format "$branch"
test "$(printf '%s' "$base" | wc -c | tr -d ' ')" = 40
case "$base" in *[!a-f0-9]*|'') exit 21;; esac
git init --quiet --bare /scratch/repo
export GIT_DIR=/scratch/repo
# A bare fetch reads objects only. Disk/inode/memory/PID/time limits apply even
# when a remote supplies an oversized pack or pathological object graph.
git fetch --quiet --no-tags --recurse-submodules=no --depth=1 -- "$endpoint" "$base"
test "$(git rev-parse --verify FETCH_HEAD)" = "$base"
test "$(git cat-file -t "$base")" = commit
git read-tree "$base"
if [ "$operation" = inspect ]; then printf '%s\n%s\n' "$branch" "$base"; fi
while IFS= read -r file; do
 # Verify every existing ancestor before changing the index. Never replace a
 # symlink, submodule or directory, including an ancestor of a selected file.
 parent=${file%/*}
 if [ "$parent" != "$file" ]; then
  while [ "$parent" != . ]; do
   entry=$(git ls-tree "$base" -- "$parent")
   if [ -n "$entry" ]; then
    set -- $entry
    if [ "$1" != 040000 ] || [ "$2" != tree ]; then exit 22; fi
   fi
   next=${parent%/*}
   if [ "$next" = "$parent" ]; then break; fi
   parent=$next
  done
 fi
 entry=$(git ls-tree "$base" -- "$file")
 mode=100644
 action=create
 if [ -n "$entry" ]; then
  set -- $entry
  case "$1:$2" in 100644:blob|100755:blob) mode=$1;; *) exit 22;; esac
  action=replace
 fi
 if [ "$operation" = inspect ]; then
  printf '%s\n' "$action"
 else
  blob=$(git hash-object -w --no-filters -- "/scratch/input/files/$file")
  git update-index --add --cacheinfo "$mode,$blob,$file"
 fi
done < /scratch/input/paths
if [ "$operation" = inspect ]; then exit 0; fi
tree=$(git write-tree)
export GIT_AUTHOR_NAME='Cairn learner submission' GIT_COMMITTER_NAME='Cairn learner submission'
export GIT_AUTHOR_EMAIL='submission@cairn.invalid' GIT_COMMITTER_EMAIL='submission@cairn.invalid'
export GIT_AUTHOR_DATE="@$(cat /scratch/input/timestamp) +0000" GIT_COMMITTER_DATE="@$(cat /scratch/input/timestamp) +0000"
commit=$(printf 'Cairn workspace submission %s\n' "$(cat /scratch/input/receipt)" | git commit-tree "$tree" -p "$base")
parents=$(git cat-file commit "$commit" | sed -n '/^$/q; /^parent /p')
test "$parents" = "parent $base"
if [ "$operation" = prepare ]; then printf '%s\n' "$commit"; exit 0; fi
test "$operation" = publish
test "$commit" = "$intended"
# A lost response can be reconciled without sending a second push. Any other
# tip/default-branch change is a conflict, including a reset or deleted branch.
if ! remote_state; then printf 'verification_needed\n'; exit 0; fi
if [ "$remote_branch" != "$branch" ]; then
 if [ "$retry" = true ]; then printf 'verification_needed\n'; else printf 'conflict\n'; fi
 exit 0
fi
if [ "$remote_sha" = "$commit" ]; then printf 'committed\n'; exit 0; fi
if [ "$remote_sha" != "$base" ]; then
 if [ "$retry" = true ]; then printf 'verification_needed\n'; else printf 'conflict\n'; fi
 exit 0
fi
# A previous attempt may have succeeded before an instructor reset the branch
# to this base. The same tip cannot prove that no write occurred. Retried
# publication is read-only reconciliation; never undo an intervening reset.
if [ "$retry" = true ]; then printf 'verification_needed\n'; exit 0; fi
# The lease is an exact expected-old-value CAS, not permission to rewrite
# history: the just-rebuilt candidate has exactly this base as its sole parent.
# Ignore the transport exit status; only an independent remote read proves success.
git push --quiet --force-with-lease="$branch:$base" -- "$endpoint" "$commit:$branch" >&2 || true
if ! remote_state; then printf 'verification_needed\n'; exit 0; fi
if [ "$remote_branch" != "$branch" ]; then printf 'verification_needed\n'
elif [ "$remote_sha" = "$commit" ]; then printf 'committed\n'
elif [ "$remote_sha" = "$base" ]; then printf 'verification_needed\n'
else printf 'verification_needed\n'
fi
`
