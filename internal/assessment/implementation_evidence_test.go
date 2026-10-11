// SPDX-License-Identifier: AGPL-3.0-or-later
package assessment

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EduCloud-Ecosystem/cairn/internal/store"
	"github.com/EduCloud-Ecosystem/cairn/pkg/adapter"
)

func implementationJudgment(d Document, line int, quote string) Judgment {
	a := d.Artifacts[0]
	return Judgment{CriterionID: "quality", Points: ptr(5), Feedback: "Visible operation", Uncertainty: "Source review only", Citations: []Citation{{Path: a.Path, SHA256: a.SHA256, Location: fmtLocation(line), Quote: quote}}}
}

func TestImplementationEvidenceRejectsScaffoldAndPartialQuotes(t *testing.T) {
	for _, tc := range []struct {
		name, source, quote string
		line                int
		allowed             bool
	}{
		{"signature", "def mean(values):\n    return sum(values) / len(values)\n", "def mean(values):", 1, false},
		{"operation", "def mean(values):\n    return sum(values) / len(values)\n", "return sum(values) / len(values)", 2, true},
		{"empty", "result = compute(values)\n", "", 1, false},
		{"partial", "result = compute(values)\n", "compute", 1, false},
		{"comment", "# result = compute(values)\n", "# result = compute(values)", 1, false},
		{"docstring", "def f():\n    \"\"\"\n    result = compute(values)\n    \"\"\"\n    pass\n", "result = compute(values)", 3, false},
		{"raw string", "r'''\nreturn useful(value)\n'''\n", "return useful(value)", 2, false},
		{"escaped quote", "text = \"a \\\" # not a comment\"\n", "text = \"a \\\" # not a comment\"", 1, true},
		{"multiline signature", "def f(\n    value = expensive(),\n):\n    pass\n", "value = expensive(),", 2, false},
		{"inline signature fragment", "def f(x): return x + 1\n", "def f(x):", 1, false},
		{"inline body", "def f(x): return x + 1\n", "return x + 1", 1, true},
		{"decorator", "@wrap(\n    value=compute(),\n)\ndef f():\n    pass\n", "value=compute(),", 2, false},
		{"import", "import numpy as np\n", "import numpy as np", 1, false},
		{"stub", "raise NotImplementedError('todo')\n", "raise NotImplementedError('todo')", 1, false},
		{"CRLF", "def f(x):\r\n    return x + 1\r\n", "    return x + 1\r", 2, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := inputDocument(t, tc.source)
			d.Rubric.Criteria[0].Evidence = PythonImplementation
			j := implementationJudgment(d, tc.line, tc.quote)
			for _, approval := range []bool{false, true} {
				_, _, err := ValidateJudgments(d, []Judgment{j}, approval)
				if (err == nil) != tc.allowed {
					t.Fatalf("approval %v: %v", approval, err)
				}
				if err != nil && judgmentFailureCode(err) != "invalid_proposal_citation_implementation" {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestAuthoredEvidenceBindsStarterAndKeepsNull(t *testing.T) {
	d := inputDocument(t, "def f(x):\n    return x + 1 # new comment\n")
	d.Rubric.Criteria[0].Evidence = PythonAuthored
	if ValidateRubric(d.Rubric) == nil {
		t.Fatal("missing starter accepted")
	}
	d.Rubric.StarterFiles = map[string]string{"answer.py": "def f(x):\n  return x+1\n"}
	j := implementationJudgment(d, 2, "return x + 1 # new comment")
	if _, _, err := ValidateJudgments(d, []Judgment{j}, false); err == nil {
		t.Fatal("format/comment change counted as authored logic")
	}
	d.Rubric.StarterFiles["answer.py"] = "def f(x):\n    raise NotImplementedError\n"
	if _, _, err := ValidateJudgments(d, []Judgment{j}, true); err != nil {
		t.Fatal(err)
	}
	d.Rubric.StarterFiles["answer.py"] = string(d.Artifacts[0].Original)
	j.Points, j.Citations = nil, nil
	if _, _, err := ValidateJudgments(d, []Judgment{j}, false); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ValidateJudgments(d, []Judgment{j}, true); err == nil {
		t.Fatal("null approved")
	}
}

func TestEvidenceRuleDigestAndSectionIntegrity(t *testing.T) {
	d := inputDocument(t, "result = 1\n")
	legacy := `{"title":"Synthetic review","paths":["answer.py"],"criteria":[{"id":"quality","description":"Explain the code","max_points":10}]}`
	if Digest(d.Rubric) != DigestBytes([]byte(legacy)) {
		t.Fatal("legacy rubric digest changed")
	}
	d.Rubric.Criteria[0].Evidence = PythonAuthored
	d.Rubric.StarterFiles = map[string]string{"answer.py": "pass\n"}
	before := InputDigest(d)
	d.Rubric.StarterFiles["answer.py"] = "result = 1\n"
	if before == InputDigest(d) {
		t.Fatal("starter is not bound to input digest")
	}
	var section Rubric
	raw, _ := json.Marshal(d.Rubric)
	json.Unmarshal(raw, &section)
	if err := validateSectionRubric(d.Rubric, section); err != nil {
		t.Fatal(err)
	}
	section.Criteria[0].Evidence = ""
	if validateSectionRubric(d.Rubric, section) == nil {
		t.Fatal("section removed evidence requirement")
	}
	json.Unmarshal(raw, &section)
	section.StarterFiles["answer.py"] = "pass\n"
	if validateSectionRubric(d.Rubric, section) == nil {
		t.Fatal("section replaced starter")
	}
	for _, mutate := range []func(*Rubric){
		func(r *Rubric) { r.Criteria[0].Evidence = "guess" },
		func(r *Rubric) { r.StarterFiles["private.md"] = "text" },
		func(r *Rubric) { r.StarterFiles["answer.py"] = strings.Repeat("x", MaxStarterBytes+1) },
		func(r *Rubric) { r.StarterFiles["answer.py"] = string([]byte{255}) },
	} {
		var bad Rubric
		json.Unmarshal(raw, &bad)
		mutate(&bad)
		if ValidateRubric(bad) == nil {
			t.Fatal("invalid evidence configuration accepted")
		}
	}
}

func TestEvidenceCatalogAndProviderKeepStarterPrivate(t *testing.T) {
	d := inputDocument(t, "def compute(x):\n    return x + 1\n")
	d.Rubric.Criteria[0].Evidence = PythonAuthored
	d.Rubric.StarterFiles = map[string]string{"answer.py": "# PRIVATE_STARTER_CANARY\ndef compute(x):\n    pass\n"}
	p, _ := NewOpenAI("unused", "")
	body, err := p.request(d)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "PRIVATE_STARTER_CANARY") {
		t.Fatal("starter leaked to provider")
	}
	if !strings.Contains(string(body), "Server-checked operation anchor IDs") {
		t.Fatal("missing catalog guidance")
	}
	for _, quote := range []string{"def compute(x):", "    return x + 1"} {
		js := []providerJudgment{{CriterionID: "quality", Points: ptr(5), Feedback: "Review", UncertaintyLevel: "low", UncertaintyReason: "Source visible", Citations: []providerCitation{{ArtifactID: "artifact_1", Quote: quote}}}}
		p.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) { return mockResponse(200, responseJSON(t, js)), nil })
		_, err := p.respond(context.Background(), body, d)
		if (err == nil) != strings.Contains(quote, "return") {
			t.Fatalf("%q: %v", quote, err)
		}
	}
}

func TestCalibrationRejectsScaffoldProposalWithoutRetry(t *testing.T) {
	svc, _, _ := fixture(t)
	ctx := context.Background()
	rubric := Rubric{Title: "Python quality", Paths: []string{"answer.py"}, Criteria: []Criterion{{ID: "quality", Description: "Implementation", MaxPoints: 5, Evidence: PythonAuthored}}, StarterFiles: map[string]string{"answer.py": "def f(x):\n    pass\n"}}
	raw, _ := json.Marshal(rubric)
	if err := svc.Store.PutAssessmentRubric(ctx, &store.AssessmentRubric{AssignmentID: "a", Digest: Digest(rubric), Document: raw}); err != nil {
		t.Fatal(err)
	}
	c, err := svc.CreateCalibration(ctx, "a", "teacher")
	if err != nil {
		t.Fatal(err)
	}
	c, err = svc.AddCalibrationExample(ctx, c.ID, "teacher", c.Revision, map[string]string{"answer.py": "def f(x):\n    return x + 1\n"})
	if err != nil {
		t.Fatal(err)
	}
	ex := calibrationDoc(t, c).Examples[0]
	p, _ := NewOpenAI("unused", "")
	calls := 0
	p.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return mockResponse(200, responseJSON(t, []providerJudgment{{CriterionID: "quality", Points: ptr(5), Feedback: "Impl", UncertaintyLevel: "low", UncertaintyReason: "Visible source", Citations: []providerCitation{{ArtifactID: "artifact_1", Quote: "def f(x):"}}}})), nil
	})
	g := NewGenerator(svc, p, []string{"c"})
	for i := 0; i < 2; i++ {
		if _, err = g.GenerateCalibration(ctx, c.ID, "teacher", ex.ID, c.Revision, ex.Document.InputDigest); err == nil {
			t.Fatal("scaffold accepted")
		}
	}
	if calls != 1 {
		t.Fatal("retry occurred", calls)
	}
	attempt, err := svc.Store.GetGeneration(ctx, "calibration:"+c.ID+":"+ex.ID)
	if err != nil || attempt.Status != "failed" || attempt.ErrorCode != "invalid_proposal_citation_implementation" {
		t.Fatal(attempt, err)
	}
	c, _ = svc.Store.GetCalibration(ctx, c.ID, "teacher")
	if calibrationDoc(t, c).Examples[0].Document.Proposal != nil {
		t.Fatal("bad proposal saved")
	}
}

type implementationCheckout struct{}

func (implementationCheckout) FetchRevision(_ context.Context, _ adapter.RepoRef, _, dir string) error {
	return os.WriteFile(filepath.Join(dir, "answer.py"), []byte("def f(x):\n    return x + 1\n"), 0600)
}

func TestImplementationApprovalCannotReplaceOperationWithSignature(t *testing.T) {
	svc, _, _ := fixture(t)
	ctx := context.Background()
	rubric := Rubric{Title: "Python", Paths: []string{"answer.py"}, Criteria: []Criterion{{ID: "quality", Description: "Implementation", MaxPoints: 5, Evidence: PythonAuthored}}, StarterFiles: map[string]string{"answer.py": "def f(x):\n    pass\n"}}
	raw, _ := json.Marshal(rubric)
	if err := svc.Store.PutAssessmentRubric(ctx, &store.AssessmentRubric{AssignmentID: "a", Digest: Digest(rubric), Document: raw}); err != nil {
		t.Fatal(err)
	}
	svc.Checkout = implementationCheckout{}
	record, err := svc.Capture(ctx, "s", "teacher")
	if err != nil {
		t.Fatal(err)
	}
	var d Document
	json.Unmarshal(record.Document, &d)
	bad := implementationJudgment(d, 1, "def f(x):")
	good := implementationJudgment(d, 2, "return x + 1")
	p := Proposal{SubmissionStatus: "relevant_work", Source: "fixture", Model: "synthetic", PromptVersion: PromptVersion, InputDigest: d.InputDigest, Criteria: []Judgment{bad}}
	if _, err := svc.Propose(ctx, record.ID, p); err == nil {
		t.Fatal("signature-only import accepted")
	}
	p.Criteria = []Judgment{good}
	if _, err := svc.Propose(ctx, record.ID, p); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Review(ctx, record.ID, "teacher", "approve", "replace evidence", []Judgment{bad}); err == nil {
		t.Fatal("signature-only approval accepted")
	}
	if _, err := svc.Store.LatestGradeForSubmission(ctx, "s"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("rejected review published grade", err)
	}
	if _, err := svc.Review(ctx, record.ID, "teacher", "approve", "synthetic valid evidence", []Judgment{good}); err != nil {
		t.Fatal(err)
	}
	if g, err := svc.Store.LatestGradeForSubmission(ctx, "s"); err != nil || g.Score != 5 {
		t.Fatal("valid synthetic approval failed", err)
	}
}

func TestChangedSignatureDoesNotMakeInlineBodyAuthored(t *testing.T) {
	d := inputDocument(t, "def renamed(x): return x + 1\n")
	d.Rubric.Criteria[0].Evidence = PythonAuthored
	d.Rubric.StarterFiles = map[string]string{"answer.py": "def original(x): return x + 1\n"}
	j := implementationJudgment(d, 1, "return x + 1")
	if _, _, err := ValidateJudgments(d, []Judgment{j}, false); err == nil {
		t.Fatal("signature change made inherited inline body authored")
	}
	d.Rubric.StarterFiles["answer.py"] = "def original(x):\n    return x + 1\n"
	if _, _, err := ValidateJudgments(d, []Judgment{j}, false); err == nil {
		t.Fatal("moving inherited body inline made it authored")
	}
}
