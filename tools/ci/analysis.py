#!/usr/bin/env python3
"""Changed-statement coverage and same-host repeated microbenchmark evidence."""
import argparse
import json
from pathlib import Path
import re
import statistics
import subprocess


def compare(base, head, threshold=0.15):
    output = []
    for name in sorted(base.keys() & head.keys()):
        before, after = base[name], head[name]
        if len(before) < 5 or len(after) < 5:
            raise ValueError("At least five repeated samples are required")
        b, h = statistics.median(before), statistics.median(after)
        noise = 3 * (statistics.median(abs(v-b) for v in before) + statistics.median(abs(v-h) for v in after))
        delta = h-b
        regression = delta > b*threshold and delta > noise
        output.append({"benchmark":name, "baseline_median":b, "head_median":h, "relative_change":delta/b if b else None,
                       "noise_allowance":noise, "regression":regression, "samples":[len(before),len(after)]})
    return output


def parse_bench(text):
    samples = {}
    for line in text.splitlines():
        parts=line.split()
        if not parts or not parts[0].startswith("Benchmark"):
            continue
        for unit in ("ns/op", "B/op", "allocs/op"):
            if unit in parts:
                samples.setdefault(re.sub(r"-\d+$", "", parts[0])+" "+unit,[]).append(float(parts[parts.index(unit)-1]))
    return samples


def changed_lines(diff):
    result={}
    name=None
    line=0
    for entry in diff.splitlines():
        if entry.startswith("+++ b/"):
            name=entry[6:]
        elif entry.startswith("@@"):
            line=int(re.search(r"\+(\d+)",entry)[1])
        elif name and entry.startswith("+"):
            result.setdefault(name,set()).add(line)
            line+=1
        elif entry.startswith(" "):
            line+=1
    return result


def coverage(profile, diff):
    changed=changed_lines(diff)
    rows=[]
    for line in profile.splitlines()[1:]:
        match=re.match(r"(.+):(\d+)\.\d+,(\d+)\.\d+ (\d+) (\d+)$",line)
        if not match:
            continue
        filename,start,end,statements,count=match.groups()
        filename=filename.removeprefix("github.com/EduCloud-Ecosystem/cairn/")
        touched=changed.get(filename,set()) & set(range(int(start),int(end)+1))
        if touched and int(count)==0:
            rows.append({"file":filename,"start":int(start),"end":int(end),"uncovered_statements":int(statements),"changed_lines":sorted(touched)})
    return {"metric":"uncovered Go statement blocks intersecting changed lines; not branch/path coverage", "blocks":rows}


def main():
    parser=argparse.ArgumentParser()
    parser.add_argument("mode", choices=["coverage","benchmark"])
    parser.add_argument("--base", required=True)
    parser.add_argument("--head", default="HEAD")
    parser.add_argument("--output", default="ci-analysis")
    args=parser.parse_args()
    out=Path(args.output)
    out.mkdir(exist_ok=True)
    if args.mode=="coverage":
        diff=subprocess.check_output(["git","diff","--unified=0",args.base,args.head,"--","*.go"],text=True)
        result=coverage(Path("coverage.out").read_text(),diff)
        (out/"coverage-summary.json").write_text(json.dumps(result,indent=2))
    else:
        base=parse_bench(Path("baseline-bench.txt").read_text())
        head=parse_bench(Path("head-bench.txt").read_text())
        if not base or base.keys()!=head.keys():
            raise ValueError("Missing or incomparable benchmark results")
        result={"threshold":0.15,"method":"same runner/toolchain/harness, six samples, median with three-MAD noise guard", "comparisons":compare(base,head),
                "limitations":"Microbenchmarks only; not DB query counts, load tests, or production latency."}
        (out/"performance.json").write_text(json.dumps(result,indent=2))
        if any(row["regression"] for row in result["comparisons"]):
            print("::warning::Measured benchmark regression; inspect performance.json and CPU profile")


if __name__=="__main__":
    main()
