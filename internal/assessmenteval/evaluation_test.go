// SPDX-License-Identifier: AGPL-3.0-or-later
package assessmenteval

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/EduCloud-Ecosystem/cairn/internal/assessment"
	"github.com/EduCloud-Ecosystem/cairn/internal/store"
	"github.com/EduCloud-Ecosystem/cairn/internal/store/memory"
)

func fixtureGenerator(t *testing.T, st store.Store, calls *int) Generate {
	t.Helper()
	return func(ctx context.Context, id, digest string) (*store.AssessmentRecord, error) {
		*calls++
		r, err := st.GetAssessment(ctx, id)
		if err != nil {
			return nil, err
		}
		var d assessment.Document
		json.Unmarshal(r.Document, &d)
		var ref Case
		for _, c := range Corpus() {
			if c.ID == r.SubmissionID {
				ref = c
			}
		}
		p := assessment.Proposal{SubmissionStatus: "relevant_work", Source: "fixture", Model: "synthetic-test", PromptVersion: "fixture-v1", InputDigest: digest}
		if ref.ID == "injection-only" {
			p.SubmissionStatus = "no_relevant_work"
		}
		a := d.Artifacts[0]
		for _, c := range ref.Rubric.Criteria {
			p.Criteria = append(p.Criteria, assessment.Judgment{CriterionID: c.ID, Points: ref.Expected[c.ID], Feedback: "Synthetic feedback", Uncertainty: "Synthetic uncertainty", Citations: []assessment.Citation{{Path: a.Path, SHA256: a.SHA256, Location: a.Segments[0].Location}}})
		}
		return (assessment.Service{Store: st}).Propose(ctx, id, p)
	}
}
func evaluated(t *testing.T) Report {
	t.Helper()
	st := memory.New()
	calls := 0
	r, err := Run(context.Background(), st, fixtureGenerator(t, st, &calls), 2, func(Report) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if calls != 18 {
		t.Fatalf("calls=%d", calls)
	}
	return r
}
func TestOfflineExtractionAndNoCalls(t *testing.T) {
	r, err := Run(context.Background(), memory.New(), nil, 1, func(Report) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if r.Live || !r.Complete || r.Metrics.ExpectedBlocks != 3 || r.Metrics.Proposals != 0 {
		t.Fatalf("bad offline result %+v", r.Metrics)
	}
	for _, v := range r.Results {
		if v.CaseID == "notebook-output-conflict" {
			segments := v.Document.Artifacts[0].Segments
			if len(segments) != 1 || strings.Contains(segments[0].Text, "new mean 8") {
				t.Fatal("notebook output leaked into extracted source")
			}
		}
	}
	if err = ValidateReport(r); err != nil {
		t.Fatal(err)
	}
}
func TestMetricsKeepNullDistinctAndExposeInstability(t *testing.T) {
	r := evaluated(t)
	if r.Metrics.CriterionComparisons != 36 || r.Metrics.ExactMatches != 36 || r.Metrics.ExpectedBlocks != 6 || r.Metrics.ScoreStableCases != 9 {
		t.Fatalf("bad metrics %+v", r.Metrics)
	}
	for i := range r.Results {
		v := &r.Results[i]
		if v.CaseID == "injection-only" && v.Trial == 2 {
			v.Document.Proposal.Criteria[0].Points = points(0)
		}
		if v.CaseID == "partial" && v.Trial == 2 {
			v.Document.Proposal.Criteria[0].Points = points(3)
		}
	}
	m := Measure(r)
	if m.ExactMatches != 34 || m.AssessabilityMismatches != 1 || m.ScoreStableCases != 7 || m.NumericComparisons != 32 || *m.MeanAbsolutePointError != 1.0/32 {
		t.Fatalf("bad mismatch metrics %+v", m)
	}
}
func TestSaveFailureStopsPaidWork(t *testing.T) {
	st := memory.New()
	calls, saves := 0, 0
	_, err := Run(context.Background(), st, fixtureGenerator(t, st, &calls), 1, func(Report) error {
		saves++
		if saves == 2 {
			return errors.New("disk unavailable")
		}
		return nil
	})
	if err == nil || calls != 1 {
		t.Fatalf("continued after save failure: calls %d err %v", calls, err)
	}
}
func TestProviderFailureDoesNotCreateSuccessOrRetry(t *testing.T) {
	calls := 0
	r, err := Run(context.Background(), memory.New(), func(context.Context, string, string) (*store.AssessmentRecord, error) {
		calls++
		return nil, errors.New("private transport detail")
	}, 1, func(Report) error { return nil })
	if err != nil || calls != 9 || r.Metrics.ProviderFailures != 9 || r.Metrics.Proposals != 0 {
		t.Fatalf("bad failure handling: %+v %v", r.Metrics, err)
	}
	b, _ := json.Marshal(r)
	if strings.Contains(string(b), "private transport detail") {
		t.Fatal("raw provider error leaked")
	}
}
func TestHumanReviewCannotBeInferredOrRebound(t *testing.T) {
	r := evaluated(t)
	raw, _ := json.Marshal(r)
	w := NewWorksheet(r, assessment.DigestBytes(raw))
	s, err := Review(raw, w)
	if err != nil || s.Complete || s.Reviewed != 0 || s.Required != 36 {
		t.Fatalf("blank review accepted: %+v %v", s, err)
	}
	yes, no := true, false
	w.Reviewer = "Synthetic test reviewer"
	w.ReviewedAt = time.Now().UTC().Format(time.RFC3339)
	for i := range w.Judgments {
		j := &w.Judgments[i]
		j.Assessable = &yes
		j.EvidenceSupported = &yes
		j.FeedbackUseful = &yes
		j.UncertaintyAppropriate = &yes
		j.Note = "Unit-test fixture, not a real human review."
		for _, c := range r.Cases {
			if c.ID == j.CaseID {
				j.Points = c.Expected[j.CriterionID]
				if j.Points == nil {
					j.Assessable = &no
				}
			}
		}
	}
	w.Judgments[0].FeedbackUseful = &no
	s, err = Review(raw, w)
	if err != nil || !s.Complete || s.UnhelpfulFeedback != 1 || s.ExactScoreMatches != 36 {
		t.Fatalf("review summary wrong: %+v %v", s, err)
	}
	w.Judgments[0].Points = points(999)
	if _, err = Review(raw, w); err == nil {
		t.Fatal("out-of-range human score accepted")
	}
	w.Judgments[0].Points = points(4)
	w.Judgments = append(w.Judgments, w.Judgments[0])
	if _, err = Review(raw, w); err == nil {
		t.Fatal("duplicate review accepted")
	}
	w.Judgments = w.Judgments[:36]
	w.ReportDigest = "wrong"
	if _, err = Review(raw, w); err == nil {
		t.Fatal("stale review accepted")
	}
}
func TestReportValidationAndEscapedPacket(t *testing.T) {
	r := evaluated(t)
	if err := ValidateReport(r); err != nil {
		t.Fatal(err)
	}
	r.Results[0].Document.Proposal.Criteria[0].Feedback = `<script>alert("injected")</script>`
	var b bytes.Buffer
	if err := RenderPacket(&b, r, "digest"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(b.String(), `<script>alert("injected")`) {
		t.Fatal("feedback rendered as active HTML")
	}
	if !strings.Contains(b.String(), "&lt;script&gt;") {
		t.Fatal("feedback missing or not escaped")
	}
	r.Results = append(r.Results, r.Results[0])
	if err := ValidateReport(r); err == nil {
		t.Fatal("duplicate trial accepted")
	}
}

func TestCorpusBindingRejectsSelfConsistentSubstitution(t *testing.T) {
	for _, mode := range []string{"source", "segments", "revision", "input_digest", "proposal_digest", "unattempted"} {
		t.Run(mode, func(t *testing.T) {
			r := evaluated(t)
			v := &r.Results[0]
			switch mode {
			case "source":
				a := &v.Document.Artifacts[0]
				a.Original = []byte("unrelated source")
				a.SHA256 = assessment.DigestBytes(a.Original)
				a.Size = len(a.Original)
				a.Segments[0].Text = string(a.Original)
				for i := range v.Document.Proposal.Criteria {
					v.Document.Proposal.Criteria[i].Citations[0].SHA256 = a.SHA256
				}
			case "segments":
				v.Document.Artifacts[0].Segments[0].Text = "fabricated extracted evidence"
			case "revision":
				v.Document.Revision = strings.Repeat("b", 40)
			case "input_digest":
				v.Document.InputDigest = "wrong"
			case "proposal_digest":
				v.Document.Proposal.InputDigest = "wrong"
			case "unattempted":
				v.Status = "collected"
				v.Document.Proposal = nil
			}
			if err := ValidateReport(r); err == nil {
				t.Fatal("altered evaluation accepted")
			}
		})
	}
}
func TestPolicyDecisionIsSeparateFromHumanScores(t *testing.T) {
	r := evaluated(t)
	raw, _ := json.Marshal(r)
	w := NewWorksheet(r, assessment.DigestBytes(raw))
	s, err := Review(raw, w)
	if err != nil || s.PolicyReviewComplete || s.Complete {
		t.Fatalf("blank decision accepted: %+v %v", s, err)
	}
	w.MissingWorkPolicy = "zero"
	w.PolicyNote = "Synthetic test only: missing readable work receives zero."
	if _, err = Review(raw, w); err == nil {
		t.Fatal("policy decision without reviewer accepted")
	}
	w.Reviewer = "Synthetic reviewer"
	w.ReviewedAt = time.Now().UTC().Format(time.RFC3339)
	s, err = Review(raw, w)
	if err != nil || !s.PolicyReviewComplete || s.Complete || s.Reviewed != 0 {
		t.Fatalf("policy conflated with human scoring: %+v %v", s, err)
	}
	w.MissingWorkPolicy = "arbitrary"
	if _, err = Review(raw, w); err == nil {
		t.Fatal("unknown policy accepted")
	}
	// Legacy worksheets remain readable, with no inferred policy decision.
	legacy, _ := json.Marshal(map[string]any{"report_digest": w.ReportDigest, "judgments": []HumanJudgment{}})
	var old Worksheet
	json.Unmarshal(legacy, &old)
	s, err = Review(raw, old)
	if err != nil || s.PolicyReviewComplete {
		t.Fatalf("legacy worksheet not preserved: %+v %v", s, err)
	}
}

func TestVersionedMissingWorkPolicyPreservesBaseline(t *testing.T) {
	old, err := CorpusForVersion(LegacyVersion)
	if err != nil {
		t.Fatal(err)
	}
	if assessment.Digest(old) != "580775ce480233836524b8bf0d5df28991ff311690663eb4632e0d40d9946359" {
		t.Fatal("original corpus changed")
	}
	current := Corpus()
	if assessment.Digest(current) == assessment.Digest(old) {
		t.Fatal("policy revision did not change corpus identity")
	}
	for i, c := range current {
		if assessment.Digest(c.Files) != assessment.Digest(old[i].Files) || assessment.Digest(c.Expected) != assessment.Digest(old[i].Expected) {
			t.Fatal("source or score references changed with policy")
		}
		for _, j := range c.Rubric.Criteria {
			if strings.Count(j.Description, "Assessability first:") != 1 || !strings.Contains(j.Description, "return points:null for every criterion") {
				t.Fatal("policy missing or duplicated")
			}
		}
	}
	if _, err := CorpusForVersion("unknown"); err == nil {
		t.Fatal("unknown version accepted")
	}
	// Merely relabeling a V2 report as V1 must not pass corpus binding.
	r := evaluated(t)
	r.Version = LegacyVersion
	if err := ValidateReport(r); err == nil {
		t.Fatal("wrong corpus version accepted")
	}
}
