"""Trusted publisher: apply validation happens before token issuance; never run candidate code."""
import base64
import json
import os
from pathlib import Path
import subprocess

from agentic import api, eligible, git

plan = json.loads(Path("evidence/plan.json").read_text())
repo = os.environ["GITHUB_REPOSITORY"]
if plan["repository"] != repo or plan["mode"] != "repair" or plan["attempt"] not in (1, 2):
    raise ValueError("Invalid publication plan")
run = api(f"repos/{repo}/actions/runs/{plan['run_id']}")
if run["head_sha"] != plan["head"] or run["conclusion"] != "failure" or run["path"] != ".github/workflows/ci.yml":
    raise ValueError("Failed CI run changed or is no longer a failure")
pr = api(f"repos/{repo}/pulls/{plan['pr']}")
repository = api(f"repos/{repo}")
if not eligible(pr, {"head_sha": plan["head"]}, repo, repository["default_branch"]) or pr["head"]["ref"] != plan["branch"]:
    raise ValueError("PR moved, lost opt-in, or is no longer eligible")
git("candidate", "config", "user.name", "EduCloud CI")
git("candidate", "config", "user.email", "educloud-ci@users.noreply.github.com")
git("candidate", "commit", "-s", "-m", f"fix: resolve CI failure\n\nEduCloud-Repair-Attempt: {plan['attempt']}\nEduCloud-Failed-Run: {plan['run_id']}")
# A GitHub App push triggers fresh CI. A normal push rejects a concurrently moved
# branch; never force-push or mutate the default branch. Credentials are not stored.
auth = base64.b64encode(("x-access-token:" + os.environ["GH_TOKEN"]).encode()).decode()
env = os.environ.copy()
env.update(GIT_CONFIG_COUNT="1", GIT_CONFIG_KEY_0="http.https://github.com/.extraheader", GIT_CONFIG_VALUE_0="AUTHORIZATION: basic " + auth)
subprocess.run(["git", "-C", "candidate", "-c", "core.hooksPath=/dev/null", "push",
                f"https://github.com/{repo}.git", "HEAD:refs/heads/" + plan["branch"]], env=env, check=True)
