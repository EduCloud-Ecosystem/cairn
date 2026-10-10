// SPDX-License-Identifier: AGPL-3.0-or-later
package assessment

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/EduCloud-Ecosystem/cairn/internal/store"
	"io"
	"net/http"
	"strings"
	"testing"
)

func referenceFor(d Document) []Judgment {
	a := d.Artifacts[0]
	out := []Judgment{}
	for _, c := range d.Rubric.Criteria {
		out = append(out, Judgment{CriterionID: c.ID, Points: ptr(2), Feedback: "PRIVATE independent instructor reasoning", Uncertainty: "Execution not verified.", Citations: []Citation{{Path: a.Path, SHA256: a.SHA256, Location: a.Segments[0].Location}}})
	}
	return out
}
func TestSectionCalibrationReferencesCoverageAndReuse(t *testing.T) {
	svc, _, _ := fixture(t)
	ctx := context.Background()
	parent := Rubric{Title: "Parent", Paths: []string{"response.md"}, Criteria: []Criterion{{ID: "one", Description: "First contract", MaxPoints: 5}, {ID: "two", Description: "Second contract", MaxPoints: 5}}}
	raw, _ := json.Marshal(parent)
	digest := DigestBytes(raw)
	if err := svc.Store.PutAssessmentRubric(ctx, &store.AssessmentRubric{AssignmentID: "a", Digest: digest, Document: raw}); err != nil {
		t.Fatal(err)
	}
	p, _ := NewOpenAI("fixture", "")
	calls := 0
	p.client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		body, _ := io.ReadAll(req.Body)
		if strings.Contains(string(body), "PRIVATE") {
			t.Fatal("reference leaked to provider")
		}
		var request struct {
			Input string `json:"input"`
		}
		json.Unmarshal(body, &request)
		var input struct {
			Criteria []Criterion        `json:"instructor_rubric"`
			Evidence []providerArtifact `json:"student_evidence"`
		}
		json.Unmarshal([]byte(request.Input), &input)
		js := []providerJudgment{}
		for _, c := range input.Criteria {
			j := providerCriteria(3)[0]
			j.CriterionID = c.ID
			j.Citations[0].Quote = input.Evidence[0].LineBlocks[0].Text
			js = append(js, j)
		}
		return mockResponse(200, responseJSON(t, js)), nil
	})
	g := NewGenerator(svc, p, []string{"c"})
	ready := func(n int) *store.Calibration {
		section := Rubric{Title: "Section", Paths: parent.Paths, Criteria: parent.Criteria[n : n+1]}
		c, err := svc.CreateCalibrationSection(ctx, "a", "teacher", "", &section)
		if err != nil {
			t.Fatal(err)
		}
		c, err = svc.ImportCalibrationBundle(ctx, c.ID, "teacher", c.Revision, CalibrationBundle{Version: CalibrationBundleVersion, Purpose: "calibration", Rubric: section, Examples: []CalibrationBundleExample{{ID: "training-01", Files: map[string]string{"response.md": "Relevant historical answer"}}}})
		if err != nil {
			t.Fatal(err)
		}
		e := calibrationDoc(t, c).Examples[0]
		before := calls
		if _, err = g.GenerateCalibration(ctx, c.ID, "teacher", e.ID, c.Revision, e.Document.InputDigest); err == nil || calls != before {
			t.Fatal("section generated before independent reference")
		}
		if _, err = svc.SaveCalibrationReference(ctx, c.ID, "other", e.ID, c.Revision, referenceFor(e.Document), "Private rationale"); !errors.Is(err, store.ErrNotFound) {
			t.Fatal("reference owner bypass")
		}
		c, err = svc.SaveCalibrationReference(ctx, c.ID, "teacher", e.ID, c.Revision, referenceFor(e.Document), "Independent before model")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = svc.SaveCalibrationReference(ctx, c.ID, "teacher", e.ID, c.Revision, referenceFor(e.Document), "Overwrite"); err == nil {
			t.Fatal("reference overwritten")
		}
		c, err = g.GenerateCalibration(ctx, c.ID, "teacher", e.ID, c.Revision, e.Document.InputDigest)
		if err != nil {
			t.Fatal(err)
		}
		e = calibrationDoc(t, c).Examples[0]
		c, err = svc.ReviewCalibrationExample(ctx, c.ID, "teacher", e.ID, c.Revision, e.Document.Proposal.Criteria, "Post-model review")
		if err != nil {
			t.Fatal(err)
		}
		c, err = svc.ApproveCalibration(ctx, c.ID, "teacher", c.Revision, "Use criterion evidence.")
		if err != nil {
			t.Fatal(err)
		}
		e = calibrationDoc(t, c).Examples[0]
		if *e.Reference.Criteria[0].Points != 2 || *e.Review.Criteria[0].Points != 3 {
			t.Fatal("reference or later review lost")
		}
		return c
	}
	first, second := ready(0), ready(1)
	current, _ := svc.Store.GetAssessmentRubric(ctx, "a")
	if current.Digest != digest {
		t.Fatal("section changed parent rubric")
	}
	if _, err := svc.CaptureCalibrated(ctx, "s", "teacher", first.ID); err == nil {
		t.Fatal("section used for whole assignment")
	}
	coverage, err := svc.CalibrationCoverage(ctx, "a", "teacher", []string{first.ID, second.ID})
	if err != nil || !coverage.Complete || coverage.CoveredPoints != 10 {
		t.Fatalf("coverage: %+v %v", coverage, err)
	}
	coverage, err = svc.CalibrationCoverage(ctx, "a", "teacher", []string{first.ID})
	if err != nil || coverage.Complete || len(coverage.Missing) != 1 {
		t.Fatal("missing criterion hidden")
	}
	overlap := ready(0)
	coverage, err = svc.CalibrationCoverage(ctx, "a", "teacher", []string{first.ID, overlap.ID, second.ID})
	if err != nil || coverage.Complete || len(coverage.Overlapping) != 1 || coverage.CoveredPoints != 10 {
		t.Fatal("overlap counted twice")
	}
	if _, err = svc.CalibrationCoverage(ctx, "a", "other", []string{first.ID}); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("coverage leaked other owner")
	}
	round, err := svc.CreateCalibrationFrom(ctx, "a", "teacher", first.ID)
	if err != nil {
		t.Fatal(err)
	}
	d := calibrationDoc(t, round)
	if !d.Section || d.BasedOn == nil || d.Rubric.Criteria[0].ID != "one" {
		t.Fatal("followup lost section")
	}
	round, err = svc.ImportCalibrationBundle(ctx, round.ID, "teacher", round.Revision, CalibrationBundle{Version: CalibrationBundleVersion, Purpose: "holdout", Rubric: d.Rubric, Examples: []CalibrationBundleExample{{ID: "reserved-01", Files: map[string]string{"response.md": "Fresh reserved answer"}}}})
	if err != nil {
		t.Fatal(err)
	}
	e := calibrationDoc(t, round).Examples[0]
	round, err = svc.SaveCalibrationReference(ctx, round.ID, "teacher", e.ID, round.Revision, referenceFor(e.Document), "Reserved reference")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = g.GenerateCalibration(ctx, round.ID, "teacher", e.ID, round.Revision, e.Document.InputDigest); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Store.LatestGradeForSubmission(ctx, "s"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("section published grade")
	}
	parent.Criteria[0].MaxPoints = 6
	raw, _ = json.Marshal(parent)
	svc.Store.PutAssessmentRubric(ctx, &store.AssessmentRubric{AssignmentID: "a", Digest: DigestBytes(raw), Document: raw})
	if _, err = svc.CreateCalibrationFrom(ctx, "a", "teacher", first.ID); err == nil {
		t.Fatal("changed parent reused section")
	}
}
func TestSectionCannotChangeContract(t *testing.T) {
	parent := Rubric{Title: "Parent", Paths: []string{"answer.py"}, Criteria: []Criterion{{ID: "x", Description: "Original", MaxPoints: 5}}}
	for _, change := range []func(*Rubric){func(r *Rubric) { r.Criteria[0].MaxPoints = 10 }, func(r *Rubric) { r.Criteria[0].Description = "Altered" }, func(r *Rubric) { r.Paths = []string{"other.py"} }} {
		section := Rubric{Title: "Part", Paths: append([]string(nil), parent.Paths...), Criteria: append([]Criterion(nil), parent.Criteria...)}
		change(&section)
		if validateSectionRubric(parent, section) == nil {
			t.Fatal("section changed parent contract")
		}
	}
}
func TestGuidancePreflightAndApprovalBudget(t *testing.T) {
	svc, _, _ := fixture(t)
	ctx := context.Background()
	c, err := svc.CreateCalibration(ctx, "a", "teacher")
	if err != nil {
		t.Fatal(err)
	}
	c, err = svc.AddCalibrationExample(ctx, c.ID, "teacher", c.Revision, map[string]string{"response.md": strings.Repeat("x", 14500)})
	if err != nil {
		t.Fatal(err)
	}
	checks, err := svc.PreflightCalibration(ctx, c.ID, "teacher", c.Revision, "")
	if err != nil || !checks[0].Fits {
		t.Fatal("source alone should fit")
	}
	checks, err = svc.PreflightCalibration(ctx, c.ID, "teacher", c.Revision, strings.Repeat("g", 2000))
	if err != nil || checks[0].Fits || checks[0].InputBytes <= 16000 {
		t.Fatal("guidance not counted")
	}
	if _, err = svc.PreflightCalibration(ctx, c.ID, "other", c.Revision, ""); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("preflight owner bypass")
	}
	d := calibrationDoc(t, c)
	e := &d.Examples[0]
	e.Document.Proposal = &Proposal{Criteria: referenceFor(e.Document)}
	e.Review = &Review{Note: "reviewed", Criteria: referenceFor(e.Document)}
	c, err = svc.saveCalibration(ctx, c, d)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = svc.ApproveCalibration(ctx, c.ID, "teacher", c.Revision, strings.Repeat("g", 2000)); err == nil {
		t.Fatal("oversized future guidance approved")
	}
	if _, err = svc.ApproveCalibration(ctx, c.ID, "teacher", c.Revision, "Brief guidance"); err != nil {
		t.Fatal(err)
	}
}
func TestReferenceRefusedAfterFailedAttempt(t *testing.T) {
	svc, _, _ := fixture(t)
	ctx := context.Background()
	c, _ := svc.CreateCalibration(ctx, "a", "teacher")
	c, _ = svc.AddCalibrationExample(ctx, c.ID, "teacher", c.Revision, map[string]string{"response.md": "attempt"})
	e := calibrationDoc(t, c).Examples[0]
	p, _ := NewOpenAI("fixture", "")
	p.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) { return mockResponse(500, "failed"), nil })
	g := NewGenerator(svc, p, []string{"c"})
	if _, err := g.GenerateCalibration(ctx, c.ID, "teacher", e.ID, c.Revision, e.Document.InputDigest); err == nil {
		t.Fatal("expected failed attempt")
	}
	if _, err := svc.SaveCalibrationReference(ctx, c.ID, "teacher", e.ID, c.Revision, referenceFor(e.Document), "Late reference"); err == nil {
		t.Fatal("post-attempt reference accepted")
	}
}

func TestSectionGenerationDiscardsResponseAfterParentChange(t *testing.T) {
	svc, _, _ := fixture(t)
	ctx := context.Background()
	parent, err := svc.Store.GetAssessmentRubric(ctx, "a")
	if err != nil {
		t.Fatal(err)
	}
	var section Rubric
	if err = json.Unmarshal(parent.Document, &section); err != nil {
		t.Fatal(err)
	}
	c, err := svc.CreateCalibrationSection(ctx, "a", "teacher", "", &section)
	if err != nil {
		t.Fatal(err)
	}
	c, err = svc.AddCalibrationExample(ctx, c.ID, "teacher", c.Revision, map[string]string{"response.md": "Relevant historical answer"})
	if err != nil {
		t.Fatal(err)
	}
	e := calibrationDoc(t, c).Examples[0]
	c, err = svc.SaveCalibrationReference(ctx, c.ID, "teacher", e.ID, c.Revision, referenceFor(e.Document), "Before generation")
	if err != nil {
		t.Fatal(err)
	}
	p, _ := NewOpenAI("fixture", "")
	p.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		changed := section
		changed.Title = "Parent changed during request"
		raw, _ := json.Marshal(changed)
		if err := svc.Store.PutAssessmentRubric(ctx, &store.AssessmentRubric{AssignmentID: "a", Digest: DigestBytes(raw), Document: raw}); err != nil {
			t.Fatal(err)
		}
		js := providerCriteria(3)
		js[0].Citations[0].Quote = "Relevant historical answer"
		return mockResponse(200, responseJSON(t, js)), nil
	})
	g := NewGenerator(svc, p, []string{"c"})
	if _, err = g.GenerateCalibration(ctx, c.ID, "teacher", e.ID, c.Revision, e.Document.InputDigest); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("stale response: %v", err)
	}
	saved, err := svc.Store.GetCalibration(ctx, c.ID, "teacher")
	if err != nil {
		t.Fatal(err)
	}
	if calibrationDoc(t, saved).Examples[0].Document.Proposal != nil {
		t.Fatal("saved proposal after parent change")
	}
}
