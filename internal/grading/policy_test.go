// SPDX-License-Identifier: AGPL-3.0-or-later
package grading

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/EduCloud-Ecosystem/cairn/internal/store/memory"
	"github.com/EduCloud-Ecosystem/cairn/pkg/adapter"
	"github.com/EduCloud-Ecosystem/cairn/pkg/gradingspec"
)

type policyCheckout struct {
	student, teacher map[string]string
	seen             adapter.RepoRef
	revision         string
	escape           bool
}

func (f *policyCheckout) Fetch(ctx context.Context, repo adapter.RepoRef, dir string) error {
	return (fakeCheckout{files: f.student}).Fetch(ctx, repo, dir)
}
func (f *policyCheckout) FetchRevision(ctx context.Context, repo adapter.RepoRef, revision, dir string) error {
	f.seen = repo
	f.revision = revision
	if f.escape {
		return os.Symlink(filepath.Join(dir, "..", "outside.json"), filepath.Join(dir, "grading.json"))
	}
	return (fakeCheckout{files: f.teacher}).Fetch(ctx, repo, dir)
}

func policyFixture() *policyCheckout {
	return &policyCheckout{
		student: map[string]string{"grading.json": `{"tests":[{"run":"true","points":999}]}`, "answer.txt": "wrong", "check.sh": "exit 0"},
		teacher: map[string]string{"grading.json": `{"version":"1","tests":[{"name":"answer","run":"sh \"$CAIRN_POLICY_DIR/check.sh\"","points":10}]}`, "check.sh": "test \"$(cat answer.txt)\" = correct\n"},
	}
}

func TestLearnerManifestCannotAwardOwnScore(t *testing.T) {
	ctx := context.Background()
	st := memory.New()
	seedSubmission(t, st)
	f := policyFixture()
	if err := NewService(st, NewExecRunner(), f).Grade(ctx, "s1"); err != nil {
		t.Fatal(err)
	}
	g, err := st.LatestGradeForSubmission(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if g.Score != 0 || g.MaxScore != 10 {
		t.Fatalf("forged learner score was trusted: %v/%v", g.Score, g.MaxScore)
	}
	var result Result
	if err := json.Unmarshal(g.Breakdown, &result); err != nil {
		t.Fatal(err)
	}
	if result.Policy == nil || result.Policy.Revision != testPolicyRevision || len(result.Policy.SHA256) != 64 || f.seen.Name != "hw1-template" || f.revision != testPolicyRevision {
		t.Fatalf("missing pinned instructor provenance: %+v", result.Policy)
	}
}

func TestUnpinnedOrEscapingPolicyNeverWritesGrade(t *testing.T) {
	for _, mode := range []string{"branch", "path", "symlink"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			st := memory.New()
			seedSubmission(t, st)
			f := policyFixture()
			asg, _ := st.GetAssignment(ctx, "a1")
			switch mode {
			case "branch":
				asg.TemplateRef.Ref = "main"
			case "path":
				asg.GradingSpec = "../grading.json"
			case "symlink":
				f.escape = true
			}
			if err := st.UpdateAssignment(ctx, asg); err != nil {
				t.Fatal(err)
			}
			if err := NewService(st, NewExecRunner(), f).Grade(ctx, "s1"); err == nil {
				t.Fatal("unsafe policy accepted")
			}
			if _, err := st.LatestGradeForSubmission(ctx, "s1"); err == nil {
				t.Fatal("grade written for rejected policy")
			}
		})
	}
}

func TestOperatorCeilingsBoundSetupAndPerTest(t *testing.T) {
	fr := &fakeRunner{}
	r := &ContainerRunner{DefaultImage: "grader:1", exec: fr, MaxTimeout: time.Second, MaxMemoryMB: 128, MaxCPUs: 0.5}
	huge := gradingspec.Limits{Timeout: time.Hour, MemoryMB: 999999, CPUs: 999}
	spec := gradingspec.Spec{Setup: []string{"true"}, Limits: huge, Tests: []gradingspec.Test{{Run: "true", Points: 1, Limits: &huge}}}
	runContainer(t, r, spec)
	for _, call := range fr.calls {
		if call.timeout != time.Second || flagValue(call.args, "--memory") != "128m" || flagValue(call.args, "--cpus") != "0.5" {
			t.Fatalf("ceiling bypass: %+v", call)
		}
	}
	for _, bad := range []float64{math.Inf(1), math.NaN()} {
		r.MaxCPUs = bad
		if r.ValidateIsolation() == nil {
			t.Fatal("non-finite operator cap accepted")
		}
	}
}

func TestInstructorMountDoesNotMutateSharedRunner(t *testing.T) {
	fr := &fakeRunner{}
	r := &ContainerRunner{DefaultImage: "grader:1", exec: fr}
	spec := gradingspec.Spec{Tests: []gradingspec.Test{{Run: "true", Points: 1}}}
	if _, err := r.RunWithPolicy(context.Background(), spec, "/student", "/teacher"); err != nil {
		t.Fatal(err)
	}
	if len(r.ExtraArgs) != 0 {
		t.Fatal("shared runner configuration mutated")
	}
	if got := flagValue(fr.calls[0].args, "--mount"); got != "type=bind,source=/teacher,target=/cairn-policy,readonly" {
		t.Fatalf("policy mount=%q", got)
	}
}

// Opt in with a locally built course image; never fetches a provider repository.
func TestContainerPolicyIntegration(t *testing.T) {
	image := os.Getenv("CAIRN_INTEGRATION_IMAGE")
	if image == "" {
		t.Skip("set CAIRN_INTEGRATION_IMAGE to run the real Docker acceptance")
	}
	ctx := context.Background()
	st := memory.New()
	seedSubmission(t, st)
	f := policyFixture()
	f.teacher["check.sh"] = "if echo changed > \"$CAIRN_POLICY_DIR/check.sh\"; then exit 7; fi\ntest \"$(cat answer.txt)\" = correct\n"
	r := &ContainerRunner{DefaultImage: image, MaxTimeout: 10 * time.Second, MaxMemoryMB: 128, MaxCPUs: 0.5}
	svc := NewService(st, r, f)
	if err := svc.Grade(ctx, "s1"); err != nil {
		t.Fatal(err)
	}
	g, _ := st.LatestGradeForSubmission(ctx, "s1")
	if g.Score != 0 || g.MaxScore != 10 {
		t.Fatalf("wrong answer scored %v/%v", g.Score, g.MaxScore)
	}
	f.student["answer.txt"] = "correct"
	if err := svc.Grade(ctx, "s1"); err != nil {
		t.Fatal(err)
	}
	g, _ = st.LatestGradeForSubmission(ctx, "s1")
	if g.Score != 10 || g.MaxScore != 10 {
		t.Fatalf("correct answer or read-only policy failed: %v/%v", g.Score, g.MaxScore)
	}
}
