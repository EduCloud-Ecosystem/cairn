// SPDX-License-Identifier: AGPL-3.0-or-later
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EduCloud-Ecosystem/cairn/internal/assessment"
)

func readinessJSON(t *testing.T, path string, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
	return b
}
func readinessBundle(source, path, id string) assessment.CalibrationBundle {
	return assessment.CalibrationBundle{Version: assessment.CalibrationBundleVersion, Purpose: "calibration", Rubric: assessment.Rubric{Title: "Synthetic reasoning", Paths: []string{path}, Criteria: []assessment.Criterion{{ID: "reason", Description: "Explain the result", MaxPoints: 10}}}, Examples: []assessment.CalibrationBundleExample{{ID: id, Files: map[string]string{path: source}}}}
}
func TestReadinessPrivateFrozenSourceFirstAndOverlap(t *testing.T) {
	root := t.TempDir()
	source := "<script>alert('untrusted')</script>\nThis is source evidence.\n"
	bundle := readinessBundle(source, "answer.md", "fresh-name")
	old := readinessBundle(source, "renamed.md", "old-name")
	original := readinessJSON(t, filepath.Join(root, "bundle.json"), bundle)
	readinessJSON(t, filepath.Join(root, "prior.json"), old)
	m := readinessManifest{Version: "cairn-readiness-manifest-v1", InstructorScope: "synthetic-instructor", PriorBundles: []string{"prior.json"}, Sections: []readinessSectionInput{{ID: "section", Bundle: "bundle.json", DraftGuidance: "Review exact claims."}}}
	readinessJSON(t, filepath.Join(root, "manifest.json"), m)
	out := filepath.Join(root, "packet")
	args := []string{"--manifest", filepath.Join(root, "manifest.json"), "--out", out}
	if err := runCalibrationReadiness(args); err != nil {
		t.Fatal(err)
	}
	var report readinessReport
	raw, err := os.ReadFile(filepath.Join(out, "readiness.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &report); err != nil {
		t.Fatal(err)
	}
	sample := report.Sections[0].Samples[0]
	if !sample.PriorExactOverlap || !sample.PriorWhitespaceOverlap || !sample.Fits || !report.SourceReviewRequired || report.ModelCalls != 0 || report.IndependentReferences != 0 || report.ProductionActivation {
		t.Fatalf("%+v", report)
	}
	if report.PromptSHA256 != assessment.PromptInstructionsDigest() || report.PromptVersion != assessment.PromptVersion || report.Model != assessment.DefaultOpenAIModel || report.Sections[0].RubricSHA256 != assessment.Digest(bundle.Rubric) {
		t.Fatal("configuration not frozen")
	}
	page, _ := os.ReadFile(filepath.Join(out, "REVIEW.html"))
	if strings.Contains(string(page), "<script>alert") || !strings.Contains(string(page), "&lt;script&gt;") {
		t.Fatal("source not escaped")
	}
	for _, name := range []string{"readiness.json", "REVIEW.html", "manifest-frozen.json", "instructor-review.json", "checksums.json"} {
		info, err := os.Stat(filepath.Join(out, name))
		if err != nil || info.Mode().Perm()&0077 != 0 {
			t.Fatalf("privacy %s: %v", name, err)
		}
	}
	var checks map[string]string
	checkData, _ := os.ReadFile(filepath.Join(out, "checksums.json"))
	if err := json.Unmarshal(checkData, &checks); err != nil {
		t.Fatal(err)
	}
	for name, digest := range checks {
		data, err := os.ReadFile(filepath.Join(out, name))
		if err != nil || assessment.DigestBytes(data) != digest {
			t.Fatalf("checksum mismatch %s: %v", name, err)
		}
	}
	if checks["readiness.json"] != assessment.DigestBytes(raw) {
		t.Fatal("configuration digest not covered by checksums")
	}
	after, _ := os.ReadFile(filepath.Join(root, "bundle.json"))
	if string(after) != string(original) {
		t.Fatal("input mutated")
	}
	if err := runCalibrationReadiness(args); err == nil {
		t.Fatal("old packet overwritten")
	}
}
func TestReadinessWhitespaceOverlapAndDuplicateSelection(t *testing.T) {
	root := t.TempDir()
	readinessJSON(t, filepath.Join(root, "prior.json"), readinessBundle("return 5\n", "old.py", "old"))
	readinessJSON(t, filepath.Join(root, "bundle.json"), readinessBundle(" return  5 \n", "answer.py", "candidate"))
	m := readinessManifest{Version: "cairn-readiness-manifest-v1", InstructorScope: "owner", PriorBundles: []string{"prior.json"}, Sections: []readinessSectionInput{{ID: "one", Bundle: "bundle.json"}}}
	manifest := filepath.Join(root, "manifest.json")
	readinessJSON(t, manifest, m)
	out := filepath.Join(root, "one")
	if err := runCalibrationReadiness([]string{"--manifest", manifest, "--out", out}); err != nil {
		t.Fatal(err)
	}
	var report readinessReport
	raw, _ := os.ReadFile(filepath.Join(out, "readiness.json"))
	_ = json.Unmarshal(raw, &report)
	s := report.Sections[0].Samples[0]
	if s.PriorExactOverlap || !s.PriorWhitespaceOverlap {
		t.Fatal(s)
	}
	readinessJSON(t, filepath.Join(root, "renamed.json"), readinessBundle(" return  5 \n", "renamed.py", "other-candidate"))
	m.Sections = append(m.Sections, readinessSectionInput{ID: "two", Bundle: "renamed.json"})
	readinessJSON(t, manifest, m)
	failed := filepath.Join(root, "failed")
	if err := runCalibrationReadiness([]string{"--manifest", manifest, "--out", failed}); err == nil {
		t.Fatal("duplicate selected source accepted")
	}
	if _, err := os.Stat(failed); !os.IsNotExist(err) {
		t.Fatal("invalid selection created output")
	}
}
