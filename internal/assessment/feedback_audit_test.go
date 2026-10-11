// SPDX-License-Identifier: AGPL-3.0-or-later
package assessment

import (
	"os"
	"path/filepath"
	"testing"
)

func auditFixture(t *testing.T) (Document, Judgment) {
	t.Helper()
	source := "def answer(frame):\n    \"\"\"Return a grouped summary.\"\"\"\n    # Explain the grouping operation.\n    return frame.groupby('group').sum()\n"
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "answer.py"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	d := Document{Rubric: Rubric{Title: "Review", Paths: []string{"answer.py"}, Criteria: []Criterion{{ID: "reason", Description: "Explain reasoning", MaxPoints: 10}}, StarterFiles: map[string]string{"answer.py": source}}, Artifacts: Extract(dir, []string{"answer.py"})}
	points := 8.0
	j := Judgment{CriterionID: "reason", Points: &points, Feedback: "The `groupby()` and `reset_index()` operations are appropriate. Good documentation.", Uncertainty: "Review manually.", Citations: []Citation{{Path: "answer.py", SHA256: d.Artifacts[0].SHA256, Location: "line:4", Quote: "return frame.groupby('group').sum()"}, {Path: "answer.py", SHA256: d.Artifacts[0].SHA256, Location: "line:2", Quote: `"""Return a grouped summary."""`}}}
	return d, j
}
func TestFeedbackAuditFindsMissingOperationAndInheritedDocumentation(t *testing.T) {
	d, j := auditFixture(t)
	got, err := AuditFeedbackEvidence(d, []Judgment{j})
	if err != nil {
		t.Fatal(err)
	}
	c := got.Criteria[0]
	if !got.RequiresSemanticReview || len(c.UncitedNamedOperations) != 1 || c.UncitedNamedOperations[0] != "reset_index" || c.DocumentationState != "captured_starter_overlap_review_required" {
		t.Fatalf("%+v", got)
	}
	if len(c.Documentation) != 1 || !c.Documentation[0].MatchesStarter {
		t.Fatal(c.Documentation)
	}
}
func TestFeedbackAuditNeverInfersCorrectnessOrAuthorship(t *testing.T) {
	d, j := auditFixture(t)
	j.Feedback = "The `groupby()` implementation is documented."
	j.Citations = j.Citations[:1]
	got, err := AuditFeedbackEvidence(d, []Judgment{j})
	if err != nil {
		t.Fatal(err)
	}
	if !got.RequiresSemanticReview || len(got.Criteria[0].UncitedNamedOperations) != 0 {
		t.Fatal(got)
	}
	j.Feedback = "Good comments."
	got, err = AuditFeedbackEvidence(d, []Judgment{j})
	if err != nil {
		t.Fatal(err)
	}
	if got.Criteria[0].DocumentationState != "no_cited_comment_or_string_line" {
		t.Fatal(got)
	}
}
func TestFeedbackAuditRejectsFabricatedCitation(t *testing.T) {
	d, j := auditFixture(t)
	j.Citations[0].Quote = "frame.reset_index()"
	if _, err := AuditFeedbackEvidence(d, []Judgment{j}); err == nil {
		t.Fatal("fabricated citation accepted")
	}
}
