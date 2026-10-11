import json
import os
from pathlib import Path
import subprocess
import tempfile
import sys
import unittest
from unittest.mock import patch

from agentic import eligible, redact, validate_patch, propose, bounded_output, read_bounded
from analysis import compare, coverage, parse_bench


class EligibilityTests(unittest.TestCase):
    def setUp(self):
        self.pr={"state":"open","draft":False,"head":{"repo":{"full_name":"org/repo"},"ref":"feature","sha":"abc"},"base":{"repo":{"full_name":"org/repo"}},"author_association":"MEMBER","labels":[{"name":"agentic-ci"}]}

    def test_only_current_trusted_opted_in_same_repo_pr(self):
        self.assertTrue(eligible(self.pr,{"head_sha":"abc"},"org/repo","main"))
        for edit in (lambda p:p.update(draft=True), lambda p:p.update(state="closed"),lambda p:p.update(labels=[]), lambda p:p.update(author_association="CONTRIBUTOR"),lambda p:p["head"].update(ref="main"),lambda p:p["head"].update(sha="stale"),lambda p:p["head"]["repo"].update(full_name="fork/repo")):
            candidate=json.loads(json.dumps(self.pr))
            edit(candidate)
            self.assertFalse(eligible(candidate,{"head_sha":"abc"},"org/repo","main"))

    def test_redaction_and_size_bound(self):
        text=redact("x"*50_000+"\nAuthorization: Bearer abc123\nTOKEN=hidden\nsk-abcdefghijklmnop\x1b[31m")
        self.assertLessEqual(len(text),24_000)
        for secret in ("abc123","hidden","sk-abcdefghijklmnop","\x1b"):
            self.assertNotIn(secret,text)


class BoundTests(unittest.TestCase):
    def test_subprocess_output_is_bounded(self):
        data, truncated = bounded_output([sys.executable, "-c", "import sys; sys.stdout.write('a'*2000000)"], limit=64)
        self.assertEqual(data, b"a"*64)
        self.assertTrue(truncated)

    def test_no_output_timeout(self):
        with self.assertRaises(TimeoutError):
            bounded_output([sys.executable, "-c", "import time; time.sleep(10)"], timeout=.05)

    def test_evidence_file_bound(self):
        with tempfile.TemporaryDirectory() as directory:
            p = Path(directory, "evidence.json")
            p.write_text("x"*129)
            with self.assertRaises(ValueError):
                read_bounded(p, 128)


class PatchTests(unittest.TestCase):
    def setUp(self):
        self.temp=tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.repo=Path(self.temp.name)
        self.git("init","-q")
        self.git("config","user.email","test@example.invalid")
        self.git("config","user.name","Test")
        for name in ("internal/auth.go","internal/auth_test.go",".github/workflows/ci.yml"):
            p=self.repo/name
            p.parent.mkdir(parents=True,exist_ok=True)
            p.write_text("original\n")
        self.git("add",".")
        self.git("commit","-qm","fixture")

    def git(self,*args):
        return subprocess.check_output(["git","-C",str(self.repo),*args],stderr=subprocess.DEVNULL)

    def candidate(self,name,contents="changed\n",symlink=False):
        p=self.repo/name
        p.parent.mkdir(exist_ok=True,parents=True)
        if symlink:
            p.symlink_to("/etc/passwd")
        else:
            p.write_text(contents)
        self.git("add",".")
        data=self.git("diff","--cached","--binary")
        self.git("reset","--hard","HEAD")
        return data

    def test_small_source_fix(self):
        self.assertEqual(validate_patch(self.repo,self.candidate("internal/auth.go")),1)

    def test_new_regression_allowed(self):
        self.assertEqual(validate_patch(self.repo,self.candidate("internal/new_test.go"),True),1)

    def test_existing_test_cannot_be_weakened(self):
        with self.assertRaises(ValueError):
            validate_patch(self.repo,self.candidate("internal/auth_test.go"))

    def test_workflow_forbidden(self):
        with self.assertRaises(ValueError):
            validate_patch(self.repo,self.candidate(".github/workflows/ci.yml"))

    def test_symlink_forbidden(self):
        with self.assertRaises(ValueError):
            validate_patch(self.repo,self.candidate("internal/link.go",symlink=True))

    def test_delete_forbidden(self):
        (self.repo/"internal/auth.go").unlink()
        self.git("add",".")
        data=self.git("diff","--cached")
        self.git("reset","--hard","HEAD")
        with self.assertRaises(ValueError):
            validate_patch(self.repo,data)

    def test_regression_cannot_change_production(self):
        with self.assertRaises(ValueError):
            validate_patch(self.repo,self.candidate("internal/auth.go"),True)

    def test_empty_oversize_and_traversal(self):
        for value in (b"", b"a"*100001, b"diff --git a/../../evil b/../../evil\n--- /dev/null\n+++ b/../../evil\n@@ -0,0 +1 @@\n+bad\n"):
            with self.assertRaises(ValueError):
                validate_patch(self.repo,value)


class AnalysisTests(unittest.TestCase):
    def test_noise_does_not_become_regression(self):
        base={"x":[80,90,100,110,120]}
        head={"x":[80,100,120,140,160]}
        self.assertFalse(compare(base,head)[0]["regression"])
        self.assertTrue(compare({"x":[100]*6},{"x":[120]*6})[0]["regression"])
        with self.assertRaises(ValueError):
            compare({"x":[1]},{"x":[2]})

    def test_units_and_statement_intersection(self):
        samples=parse_bench("BenchmarkExample-2 100 25.5 ns/op 4 B/op 1 allocs/op\n")
        self.assertEqual(samples["BenchmarkExample ns/op"],[25.5])
        result=coverage("mode: atomic\ngithub.com/EduCloud-Ecosystem/cairn/internal/a.go:2.1,4.2 2 0\ngithub.com/EduCloud-Ecosystem/cairn/internal/a.go:5.1,6.2 1 1\n", "+++ b/internal/a.go\n@@ -2,0 +3,1 @@\n+changed\n")
        self.assertEqual(result["blocks"],[{"file":"internal/a.go","start":2,"end":4,"uncovered_statements":2,"changed_lines":[3]}])


if __name__=="__main__":
    unittest.main()
