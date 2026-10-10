// SPDX-License-Identifier: AGPL-3.0-or-later
package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EduCloud-Ecosystem/cairn/internal/assessment"
	"github.com/EduCloud-Ecosystem/cairn/internal/grading"
	"github.com/EduCloud-Ecosystem/cairn/pkg/gradingspec"
)

type evidenceRunner struct {
	t    *testing.T
	fail bool
}

func (r evidenceRunner) RunWithPolicy(_ context.Context, s gradingspec.Spec, dir, policy string) (grading.Result, error) {
	if s.Limits.Network != gradingspec.NetworkNone || s.Limits.MemoryMB != 512 || s.Limits.CPUs != 1 {
		r.t.Fatal("limits not forced")
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "answer.py"))
	if string(raw) != "raise NotImplementedError" {
		r.t.Fatal("captured source changed")
	}
	raw, _ = os.ReadFile(filepath.Join(policy, "check.py"))
	if string(raw) != "instructor check" {
		r.t.Fatal("wrong policy")
	}
	if r.fail {
		return grading.Result{}, errors.New("PRIVATE runtime details")
	}
	exit := 2
	return grading.Result{Tests: []grading.TestResult{{Name: "quality", ExitCode: &exit, Detail: "command exited non-zero"}}}, nil
}
func testCheckPlan() (assessment.CalibrationBundle, calibrationChecks) {
	b := assessment.CalibrationBundle{Version: assessment.CalibrationBundleVersion, Purpose: "calibration", Rubric: assessment.Rubric{Title: "Review", Paths: []string{"answer.py"}, Criteria: []assessment.Criterion{{ID: "quality", Description: "Assess source", MaxPoints: 5}}}, Examples: []assessment.CalibrationBundleExample{{ID: "sample-01", Files: map[string]string{"answer.py": "raise NotImplementedError"}}}}
	c := calibrationChecks{Version: "cairn-calibration-checks-v1", Files: map[string]string{"check.py": "instructor check"}, Spec: gradingspec.Spec{Image: "sha256:" + strings.Repeat("a", 64), Tests: []gradingspec.Test{{Name: "quality", Run: "python3 -I /cairn-policy/check.py", Points: 0, Match: &gradingspec.OutputMatch{Expected: "PASS", Trim: true}}}}}
	return b, c
}
func TestExecutionEvidenceBindsSourcePolicyAndNoGrade(t *testing.T) {
	b, c := testCheckPlan()
	for _, failure := range []bool{false, true} {
		out := t.TempDir()
		err := executeCalibrationChecks(context.Background(), b, c, "bundle-digest", "policy-digest", out, evidenceRunner{t, failure})
		if (err != nil) != failure || (err != nil && strings.Contains(err.Error(), "PRIVATE")) {
			t.Fatalf("bad error %v", err)
		}
		raw, e := os.ReadFile(filepath.Join(out, "execution.json"))
		if e != nil {
			t.Fatal(e)
		}
		var report executionReport
		if json.Unmarshal(raw, &report) != nil {
			t.Fatal("bad report")
		}
		if report.ChecksDigest != "policy-digest" || report.BundleDigest != "bundle-digest" || report.RubricDigest != assessment.Digest(b.Rubric) || report.Samples[0].SourceHashes["answer.py"] != assessment.DigestBytes([]byte("raise NotImplementedError")) {
			t.Fatal("provenance missing")
		}
		if failure && report.Samples[0].Status != "execution_error" {
			t.Fatal("infrastructure failure became test result")
		}
		if !failure && (*report.Samples[0].Tests[0].ExitCode != 2 || report.Samples[0].Tests[0].Status != "unassessable_execution_error") {
			t.Fatal("exit code discarded")
		}
	}
}
func TestExecutionPlanRejectsMutableImagesAndGradePoints(t *testing.T) {
	b, c := testCheckPlan()
	if err := validateCalibrationChecks(b.Rubric, c); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"image", "points", "setup", "overrides", "traversal", "unknown", "output"} {
		_, c := testCheckPlan()
		switch kind {
		case "image":
			c.Spec.Image = "python:latest"
		case "points":
			c.Spec.Tests[0].Points = 1
		case "setup":
			c.Spec.Setup = []string{"echo setup"}
		case "overrides":
			c.Spec.Tests[0].Limits = &gradingspec.Limits{Network: gradingspec.NetworkRestricted}
		case "traversal":
			c.Files = map[string]string{"../escape.py": "bad"}
		case "unknown":
			c.Spec.Tests[0].Name = "other"
		case "output":
			c.Spec.Tests[0].Match = nil
		}
		if validateCalibrationChecks(b.Rubric, c) == nil {
			t.Fatalf("accepted unsafe %s", kind)
		}
	}
}
func TestOfflineExecutionRefusesRemoteDocker(t *testing.T) {
	t.Setenv("DOCKER_HOST", "tcp://example.invalid:2375")
	t.Setenv("DOCKER_CONTEXT", "")
	if localDockerContext() == nil {
		t.Fatal("remote Docker accepted")
	}
}

func TestExecutionObservationsNeverBecomeScores(t *testing.T) {
	zero, one, two := 0, 1, 2
	got := checkObservations([]grading.TestResult{{Name: "ok", Passed: true, ExitCode: &zero}, {Name: "assert", ExitCode: &one}, {Name: "error", ExitCode: &two}, {Name: "timeout", TimedOut: true}, {Name: "unknown"}})
	for i, want := range []string{"passed", "check_failed", "unassessable_execution_error", "unassessable_timeout", "unassessable_execution_error"} {
		if got[i].Status != want {
			t.Fatal("outcome conflation")
		}
	}
	raw, _ := json.Marshal(got)
	if strings.Contains(string(raw), "points") || strings.Contains(string(raw), "score") {
		t.Fatal("evidence report contains grades")
	}
}
