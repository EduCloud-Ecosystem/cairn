#!/usr/bin/env python3
"""Bounded AI suggestions. Never execute model commands or load branch code."""
import argparse
import json
import os
from pathlib import Path
import re
import subprocess
import selectors
import time
import urllib.request

MAX_PATCH = 100_000
MAX_CONTEXT = 100_000


def bounded_output(command, limit=1_000_000, timeout=60):
    """Drain a subprocess only up to the bound; never buffer an unbounded log/blob."""
    with subprocess.Popen(command, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL) as process:
        try:
            result = bytearray()
            deadline = time.monotonic() + timeout
            with selectors.DefaultSelector() as ready:
                ready.register(process.stdout, selectors.EVENT_READ)
                while True:
                    remaining = deadline - time.monotonic()
                    if remaining <= 0 or not ready.select(remaining):
                        raise TimeoutError("Evidence command timed out")
                    chunk = os.read(process.stdout.fileno(), min(65536, limit + 1 - len(result)))
                    if not chunk:
                        break
                    result.extend(chunk)
                    if len(result) > limit:
                        process.kill()
                        return bytes(result[:limit]), True
            if process.wait(timeout=max(.1, deadline - time.monotonic())):
                raise ValueError("Evidence command failed")
            return bytes(result), False
        finally:
            if process.poll() is None:
                process.kill()
            process.wait()


def git(repo, *args):
    result, truncated = bounded_output(["git", "-C", str(repo), "-c", "core.hooksPath=/dev/null", *args])
    if truncated:
        raise ValueError("Git evidence exceeds bound; narrow the change")
    return result


def read_bounded(path, limit):
    with open(path, "rb") as source:
        data = source.read(limit + 1)
    if len(data) > limit:
        raise ValueError("Evidence file exceeds bound")
    return data.decode()


def api(path):
    req = urllib.request.Request("https://api.github.com/" + path, headers={
        "Authorization": "Bearer " + os.environ["GH_TOKEN"], "Accept": "application/vnd.github+json"})
    with urllib.request.urlopen(req, timeout=30) as response:
        data = response.read(2_000_001)
        if len(data) > 2_000_000:
            raise ValueError("GitHub response exceeds bound")
        return json.loads(data)


def redact(text):
    text = re.sub(r"\x1b\[[0-?]*[ -/]*[@-~]", "", text)
    text = re.sub(r"(?i)(bearer\s+|(?:api[_-]?key|token|password|secret)\s*[:=]\s*)\S+", r"\1[REDACTED]", text)
    text = re.sub(r"(?:sk-[A-Za-z0-9_-]{10,}|gh[pousr]_[A-Za-z0-9_]{10,}|github_pat_[A-Za-z0-9_]+)", "[REDACTED]", text)
    return text[-24_000:]


def eligible(pr, run, repo, default_branch):
    return (pr["state"] == "open" and not pr["draft"]
            and pr["head"]["repo"]["full_name"] == repo
            and pr["base"]["repo"]["full_name"] == repo
            and pr["head"]["ref"] != default_branch
            and pr["head"]["sha"] == run["head_sha"]
            and pr["author_association"] in ("OWNER", "MEMBER", "COLLABORATOR")
            and any(x["name"] == "agentic-ci" for x in pr["labels"]))


def plan(args):
    event = json.loads(Path(os.environ["GITHUB_EVENT_PATH"]).read_text())
    repo = os.environ["GITHUB_REPOSITORY"]
    run_id = event.get("workflow_run", {}).get("id") or int(os.environ["INPUT_RUN_ID"])
    run = api(f"repos/{repo}/actions/runs/{run_id}")
    if run["path"] != ".github/workflows/ci.yml" or run["event"] != "pull_request":
        raise ValueError("Only ordinary CI pull-request runs are eligible")
    mode = os.environ.get("INPUT_MODE") or ("repair" if run["conclusion"] == "failure" else "review")
    if mode not in ("repair", "review", "regression", "performance"):
        raise ValueError("Unknown mode")
    if mode == "repair" and run["conclusion"] != "failure":
        raise ValueError("Repair requires failed CI")
    repository = api(f"repos/{repo}")
    prs = api(f"repos/{repo}/commits/{run['head_sha']}/pulls")
    candidates = [p for p in prs if eligible(p, run, repo, repository["default_branch"])]
    if len(candidates) != 1:
        raise ValueError("Require exactly one current, trusted, same-repository, labelled PR")
    pr = candidates[0]
    commit = api(f"repos/{repo}/commits/{run['head_sha']}")
    match = re.search(r"^EduCloud-Repair-Attempt: ([12])$", commit["commit"]["message"], re.M)
    attempt = int(match[1]) + 1 if match else 1
    if mode == "repair" and attempt > 2:
        raise ValueError("Two repair commits exhausted; maintainer review required")
    if int(os.environ.get("GITHUB_RUN_ATTEMPT", "1")) != 1:
        raise ValueError("Rerunning an agent job is disabled; use a new reviewed source commit")
    data = {"repository": repo, "pr": pr["number"], "head": run["head_sha"],
            "base": pr["base"]["sha"], "branch": pr["head"]["ref"], "run_id": run_id,
            "attempt": attempt, "mode": mode}
    Path(args.output).mkdir(exist_ok=True)
    Path(args.output, "plan.json").write_text(json.dumps(data, indent=2))
    with open(os.environ["GITHUB_OUTPUT"], "a") as out:
        for key in ("head", "base", "branch", "mode", "pr"):
            out.write(f"{key}={data[key]}\n")
    # gh already masks known Actions secrets; apply additional redaction and bounds.
    logs, truncated = bounded_output(["gh", "run", "view", str(run_id), "--repo", repo, "--log-failed"], limit=100_000)
    Path(args.output, "failure.txt").write_text(redact(logs.decode(errors="replace")) + ("\n[log truncated]" if truncated else ""))


def source_path(path):
    return (re.fullmatch(r"(?:internal|pkg|cmd|web/src)/[A-Za-z0-9_./-]+\.(?:go|tsx?|jsx?|css)", path)
            and all(p not in ("", ".", "..") and not p.startswith(".") for p in path.split("/")))


def is_test(path):
    return path.endswith("_test.go") or bool(re.search(r"[./](?:test|spec)\.[tj]sx?$", path))


def validate_patch(repo, patch, tests_only=False):
    if not patch or len(patch) > MAX_PATCH or b"\x00" in patch:
        raise ValueError("Empty, binary, or oversized patch")
    # Git parses paths and enforces traversal protections; do not trust diff headers.
    result = subprocess.run(["git", "-C", str(repo), "apply", "--index", "--whitespace=error", "-"],
                            input=patch, capture_output=True)
    if result.returncode:
        raise ValueError("Patch does not apply cleanly")
    entries = git(repo, "diff", "--cached", "--raw", "-z", "--no-renames").split(b"\0")
    count = 0
    for index in range(0, len(entries) - 1, 2):
        metadata, raw_path = entries[index:index + 2]
        old_mode, new_mode, _, _, status = metadata.decode().split()
        path = raw_path.decode()
        count += 1
        if not source_path(path) or new_mode != "100644" or status not in ("A", "M"):
            raise ValueError("Only regular source additions/modifications are allowed")
        if is_test(path) and status != "A":
            raise ValueError("Existing tests may not be changed by automation")
        if tests_only and (status != "A" or not is_test(path)):
            raise ValueError("Regression mode can only add new tests")
    if not 1 <= count <= 8:
        raise ValueError("Patch must change 1 to 8 files")
    return count


def propose(args):
    directory = Path(args.output)
    plan_data = json.loads((directory / "plan.json").read_text())
    repo = Path(args.repo)
    if git(repo, "rev-parse", "HEAD").decode().strip() != plan_data["head"]:
        raise ValueError("Checkout differs from frozen head")
    paths = git(repo, "diff", "--name-only", "--diff-filter=ACMRT", plan_data["base"], plan_data["head"]).decode().splitlines()
    sources = {}
    for path in paths[:40]:
        if source_path(path):
            # Read Git objects, not files/symlinks or checked-out executable configuration.
            size = int(git(repo, "cat-file", "-s", f"{plan_data['head']}:{path}"))
            if size > 20_000:
                continue
            data = git(repo, "show", f"{plan_data['head']}:{path}").decode(errors="replace")
            if len(data) <= 20_000:
                sources[path] = data
    context = {"mode": plan_data["mode"], "sources": sources,
               "diff": git(repo, "diff", "--no-ext-diff", "--no-textconv", plan_data["base"], plan_data["head"], "--", "internal", "pkg", "cmd", "web/src").decode(errors="replace")[:40_000],
               "failure": read_bounded(directory / "failure.txt", 100_000)}
    # Optional deterministic evidence from the originating CI run, never required to claim completeness.
    for name in ("coverage-summary.json", "performance.json"):
        p = directory / name
        if p.exists():
            context[name] = read_bounded(p, 100_000)[:12_000]
    encoded = json.dumps(context)
    if len(encoded) > MAX_CONTEXT:
        raise ValueError("Context exceeds limit; maintainer must narrow the change")
    schema = {"type": "object", "additionalProperties": False,
              "properties": {key: {"type": "string"} for key in ("summary", "patch", "findings", "test_plan", "limitations")},
              "required": ["summary", "patch", "findings", "test_plan", "limitations"]}
    finding_fields = ("title", "path", "trigger", "impact", "validation", "confidence")
    finding = {"type": "object", "additionalProperties": False,
               "properties": {key: {"type": "string"} for key in finding_fields},
               "required": list(finding_fields) + ["line", "severity"]}
    finding["properties"].update(line={"type": "integer"}, severity={"type": "string", "enum": ["low", "medium", "high", "critical"]})
    schema["properties"]["findings"] = {"type": "array", "items": finding}
    prompt = Path(__file__).with_name("prompt.txt").read_text()
    request = {"model": os.environ["AGENTIC_MODEL"], "store": False, "max_output_tokens": 12_000,
               "instructions": prompt, "input": encoded,
               "text": {"format": {"type": "json_schema", "name": "ci_proposal", "strict": True, "schema": schema}}}
    req = urllib.request.Request("https://api.openai.com/v1/responses", data=json.dumps(request).encode(),
                                 headers={"Authorization": "Bearer " + os.environ["OPENAI_API_KEY"], "Content-Type": "application/json"})
    # One request, no hidden retries; project budget is an additional operator-set cap.
    with urllib.request.urlopen(req, timeout=180) as response:
        body = response.read(1_000_001)
        if len(body) > 1_000_000:
            raise ValueError("Provider response exceeds limit")
        raw = json.loads(body)
    if raw.get("status") != "completed":
        raise ValueError("Provider did not complete")
    outputs = [c["text"] for item in raw.get("output", []) for c in item.get("content", []) if c.get("type") == "output_text"]
    result = json.loads("".join(outputs))
    if (set(result) != set(schema["required"])
            or not all(isinstance(result[k], str) for k in result if k != "findings")
            or not isinstance(result["findings"], list) or len(result["findings"]) > 20):
        raise ValueError("Invalid provider response")
    for item in result["findings"]:
        if (not isinstance(item, dict) or set(item) != set(finding["required"])
                or not all(isinstance(item[k], str) for k in finding_fields)
                or not isinstance(item["line"], int) or item["line"] < 1
                or item["severity"] not in ("low", "medium", "high", "critical")):
            raise ValueError("Invalid actionable finding")
    if plan_data["mode"] in ("review", "performance") and result["patch"]:
        raise ValueError("Read-only review cannot propose a patch")
    if len(result["patch"].encode()) > MAX_PATCH:
        raise ValueError("Oversized patch")
    (directory / "proposal.json").write_text(json.dumps(result, indent=2))
    (directory / "candidate.patch").write_text(result["patch"])
    (directory / "usage.json").write_text(json.dumps(raw.get("usage", {})))


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("command", choices=["plan", "propose", "validate"])
    parser.add_argument("--repo", default="candidate")
    parser.add_argument("--output", default="evidence")
    args = parser.parse_args()
    if args.command == "plan":
        plan(args)
    elif args.command == "propose":
        propose(args)
    else:
        plan_data = json.loads(Path(args.output, "plan.json").read_text())
        if git(args.repo, "rev-parse", "HEAD").decode().strip() != plan_data["head"]:
            raise ValueError("Checkout differs from frozen head")
        validate_patch(args.repo, Path(args.output, "candidate.patch").read_bytes(), plan_data["mode"] == "regression")


if __name__ == "__main__":
    main()
