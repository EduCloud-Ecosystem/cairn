// SPDX-License-Identifier: AGPL-3.0-or-later
package assessment

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/EduCloud-Ecosystem/cairn/internal/store"
)

func evidencePacket(d Document) CalibrationReviewPacket {
	hashes := map[string]string{}
	for _, a := range d.Artifacts {
		hashes[a.Path] = a.SHA256
	}
	js := referenceFor(d)
	for i := range js {
		js[i].Feedback = "PRIVATE_ASSISTED judgment"
		js[i].Citations[0].Quote = d.Artifacts[0].Segments[0].Text
	}
	zero := 0
	unchecked := []string{}
	for _, c := range d.Rubric.Criteria[1:] {
		unchecked = append(unchecked, c.ID)
	}
	return CalibrationReviewPacket{Version: CalibrationReviewPacketVersion, SampleID: "sample-one", RubricDigest: Digest(d.Rubric), SourceHashes: hashes, Authorship: "assistant", Author: "PRIVATE_ASSISTED assistant", Note: "PRIVATE_ASSISTED rules", Judgments: js, Execution: &CalibrationExecutionReport{Version: "cairn-calibration-execution-v1", Completed: true, ExpectedSamples: 1, CreatedAt: time.Now().UTC(), RubricDigest: Digest(d.Rubric), BundleDigest: strings.Repeat("b", 64), ChecksDigest: strings.Repeat("c", 64), Image: "sha256:" + strings.Repeat("d", 64), UncheckedCriteria: unchecked, Samples: []CalibrationExecutionSample{{SampleID: "sample-one", SourceHashes: hashes, Status: "completed", Tests: []CalibrationExecutionObservation{{CriterionID: d.Rubric.Criteria[0].ID, Status: "passed", ExitCode: &zero}}}}}}
}
func TestCalibrationEvidenceValidation(t *testing.T) {
	svc, _, _ := fixture(t)
	ctx := context.Background()
	c, _ := svc.CreateCalibration(ctx, "a", "teacher")
	c, _ = svc.AddCalibrationExample(ctx, c.ID, "teacher", c.Revision, map[string]string{"response.md": "Reasoned answer"})
	d := calibrationDoc(t, c).Examples[0].Document
	p := evidencePacket(d)
	if _, err := ValidateCalibrationReviewPacket(d, p); err != nil {
		t.Fatal(err)
	}
	partialDoc := d
	partialDoc.Rubric.Criteria = append([]Criterion{}, d.Rubric.Criteria...)
	partialDoc.Rubric.Criteria = append(partialDoc.Rubric.Criteria, Criterion{ID: "limits", Description: "Explain limitations", MaxPoints: 5})
	partial := evidencePacket(partialDoc)
	partial.Judgments = partial.Judgments[:1]
	checked, err := ValidateCalibrationReviewPacket(partialDoc, partial)
	if err != nil || len(checked.Judgments) != 1 || checked.Judgments[0].Points == nil || *checked.Judgments[0].Points != *p.Judgments[0].Points {
		t.Fatal("partial adjudication changed or filled omitted criteria", err)
	}
	for name, change := range map[string]func(*CalibrationReviewPacket){
		"wrong rubric":        func(p *CalibrationReviewPacket) { p.RubricDigest = strings.Repeat("a", 64) },
		"wrong source":        func(p *CalibrationReviewPacket) { p.SourceHashes["response.md"] = strings.Repeat("a", 64) },
		"extra source":        func(p *CalibrationReviewPacket) { p.SourceHashes["other.md"] = strings.Repeat("a", 64) },
		"fake independent":    func(p *CalibrationReviewPacket) { p.Authorship = "independent-instructor" },
		"hidden adjudication": func(p *CalibrationReviewPacket) { p.Authorship = "execution-only" },
		"missing author":      func(p *CalibrationReviewPacket) { p.Author = "" },
		"empty assisted":      func(p *CalibrationReviewPacket) { p.Judgments = nil },
		"forged quote":        func(p *CalibrationReviewPacket) { p.Judgments[0].Citations[0].Quote = "not in source" },
		"no quote":            func(p *CalibrationReviewPacket) { p.Judgments[0].Citations[0].Quote = "" },
		"over score":          func(p *CalibrationReviewPacket) { p.Judgments[0].Points = ptr(10001) },
		"unknown criterion":   func(p *CalibrationReviewPacket) { p.Judgments[0].CriterionID = "absent" },
		"duplicate judgment":  func(p *CalibrationReviewPacket) { p.Judgments = append(p.Judgments, p.Judgments[0]) },
		"partial report":      func(p *CalibrationReviewPacket) { p.Execution.Completed = false },
		"missing sample":      func(p *CalibrationReviewPacket) { p.Execution.Samples[0].SampleID = "other" },
		"report source": func(p *CalibrationReviewPacket) {
			p.Execution.Samples[0].SourceHashes["response.md"] = strings.Repeat("e", 64)
		},
		"count":           func(p *CalibrationReviewPacket) { p.Execution.ExpectedSamples = 2 },
		"report rubric":   func(p *CalibrationReviewPacket) { p.Execution.RubricDigest = "bad" },
		"unbound checks":  func(p *CalibrationReviewPacket) { p.Execution.ChecksDigest = "" },
		"floating image":  func(p *CalibrationReviewPacket) { p.Execution.Image = "python:latest" },
		"invalid outcome": func(p *CalibrationReviewPacket) { p.Execution.Samples[0].Tests[0].Status = "verified" },
		"false pass":      func(p *CalibrationReviewPacket) { n := 1; p.Execution.Samples[0].Tests[0].ExitCode = &n },
		"duplicate checks": func(p *CalibrationReviewPacket) {
			p.Execution.Samples[0].Tests = append(p.Execution.Samples[0].Tests, p.Execution.Samples[0].Tests[0])
		},
		"overlapping unchecked": func(p *CalibrationReviewPacket) { p.Execution.UncheckedCriteria = []string{d.Rubric.Criteria[0].ID} },
		"oversized":             func(p *CalibrationReviewPacket) { p.Note = strings.Repeat("x", MaxCalibrationReviewPacketBytes) },
	} {
		t.Run(name, func(t *testing.T) {
			raw, _ := json.Marshal(p)
			var bad CalibrationReviewPacket
			json.Unmarshal(raw, &bad)
			change(&bad)
			if _, err := ValidateCalibrationReviewPacket(d, bad); err == nil {
				t.Fatal("accepted invalid packet")
			}
		})
	}
	p.Judgments[0].Points = nil
	p.Judgments[0].Citations = nil
	if out, err := ValidateCalibrationReviewPacket(d, p); err != nil || out.Judgments[0].Points != nil || out.Judgments[0].Citations == nil {
		t.Fatal("null judgment rejected or citations not normalized", err)
	}
	p.Authorship = "execution-only"
	p.Judgments = nil
	if _, err := ValidateCalibrationReviewPacket(d, p); err != nil {
		t.Fatal(err)
	}
}

func TestAssistedEvidenceWorkflowAndProviderSeparation(t *testing.T) {
	svc, _, _ := fixture(t)
	ctx := context.Background()
	r, _ := svc.Store.GetAssessmentRubric(ctx, "a")
	var rubric Rubric
	json.Unmarshal(r.Document, &rubric)
	c, err := svc.CreateCalibrationSection(ctx, "a", "teacher", "", &rubric)
	if err != nil {
		t.Fatal(err)
	}
	c, err = svc.AddCalibrationExample(ctx, c.ID, "teacher", c.Revision, map[string]string{"response.md": "Reasoned historical answer"})
	if err != nil {
		t.Fatal(err)
	}
	e := calibrationDoc(t, c).Examples[0]
	packet := evidencePacket(e.Document)
	if _, err = svc.ImportCalibrationReviewEvidence(ctx, c.ID, "other", e.ID, c.Revision, packet); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("owner boundary", err)
	}
	if _, err = svc.ImportCalibrationReviewEvidence(ctx, c.ID, "teacher", e.ID, c.Revision-1, packet); !errors.Is(err, store.ErrConflict) {
		t.Fatal("revision bypass", err)
	}
	c, err = svc.ImportCalibrationReviewEvidence(ctx, c.ID, "teacher", e.ID, c.Revision, packet)
	if err != nil {
		t.Fatal(err)
	}
	saved := calibrationDoc(t, c).Examples[0]
	if saved.Reference != nil || saved.Review != nil || saved.Document.Proposal != nil || c.Status != "draft" || saved.Document.InputDigest != e.Document.InputDigest {
		t.Fatal("attachment promoted judgment or changed provider input")
	}
	rec := saved.ReviewEvidence[0]
	if rec.ImportedBy != "teacher" || rec.ImportedAt.IsZero() || rec.Execution.ReportDigest != Digest(packet.Execution) {
		t.Fatal("missing audit trail")
	}
	if _, err = svc.ImportCalibrationReviewEvidence(ctx, c.ID, "teacher", e.ID, c.Revision, packet); !errors.Is(err, store.ErrConflict) {
		t.Fatal("duplicate attachment")
	}
	if _, err = svc.SaveCalibrationReference(ctx, c.ID, "teacher", e.ID, c.Revision, referenceFor(e.Document), "Not blind"); err == nil {
		t.Fatal("assisted judgment laundered as independent")
	}
	if _, err = svc.ApproveCalibration(ctx, c.ID, "teacher", c.Revision, "Guidance"); err == nil {
		t.Fatal("attachment approved profile")
	}
	provider, _ := NewOpenAI("fixture", "")
	calls := 0
	provider.client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		raw, _ := io.ReadAll(req.Body)
		if strings.Contains(string(raw), "PRIVATE_ASSISTED") {
			t.Fatal("supporting material sent to model")
		}
		js := providerCriteria(3)
		js[0].Citations[0].Quote = "Reasoned historical answer"
		return mockResponse(200, responseJSON(t, js)), nil
	})
	g := NewGenerator(svc, provider, []string{"c"})
	c, err = g.GenerateCalibration(ctx, c.ID, "teacher", e.ID, c.Revision, e.Document.InputDigest)
	if err != nil || calls != 1 {
		t.Fatal("assisted section blocked", err)
	}
	e = calibrationDoc(t, c).Examples[0]
	c, err = svc.ReviewCalibrationExample(ctx, c.ID, "teacher", e.ID, c.Revision, e.Document.Proposal.Criteria, "Instructor reviewed with attributed supporting material")
	if err != nil {
		t.Fatal(err)
	}
	packet.Note = "another check"
	if _, err = svc.ImportCalibrationReviewEvidence(ctx, c.ID, "teacher", e.ID, c.Revision, packet); err == nil {
		t.Fatal("evidence changed after final instructor review")
	}
	c, err = svc.ApproveCalibration(ctx, c.ID, "teacher", c.Revision, "Cite source evidence")
	if err != nil {
		t.Fatal(err)
	}
	if calibrationDoc(t, c).Examples[0].Reference != nil {
		t.Fatal("approval created independent reference")
	}
	if _, err = svc.ImportCalibrationReviewEvidence(ctx, c.ID, "teacher", e.ID, c.Revision, packet); !errors.Is(err, store.ErrConflict) {
		t.Fatal("ready profile mutable")
	}
	if _, err = svc.Store.LatestGradeForSubmission(ctx, "s"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("historical evidence published a grade")
	}
}

func TestExecutionEvidenceDoesNotBlockIndependentReference(t *testing.T) {
	svc, _, _ := fixture(t)
	ctx := context.Background()
	c, _ := svc.CreateCalibration(ctx, "a", "teacher")
	c, _ = svc.AddCalibrationExample(ctx, c.ID, "teacher", c.Revision, map[string]string{"response.md": "Reasoned answer"})
	e := calibrationDoc(t, c).Examples[0]
	p := evidencePacket(e.Document)
	p.Judgments = nil
	p.Authorship = "execution-only"
	c, err := svc.ImportCalibrationReviewEvidence(ctx, c.ID, "teacher", e.ID, c.Revision, p)
	if err != nil {
		t.Fatal(err)
	}
	c, err = svc.SaveCalibrationReference(ctx, c.ID, "teacher", e.ID, c.Revision, referenceFor(e.Document), "Independent judgment using execution evidence")
	if err != nil {
		t.Fatal(err)
	}
	p.Authorship = "assistant"
	p.Judgments = referenceFor(e.Document)
	p.Judgments[0].Citations[0].Quote = e.Document.Artifacts[0].Segments[0].Text
	c, err = svc.ImportCalibrationReviewEvidence(ctx, c.ID, "teacher", e.ID, c.Revision, p)
	if err != nil {
		t.Fatal(err)
	}
	if calibrationDoc(t, c).Examples[0].Reference.Note != "Independent judgment using execution evidence" {
		t.Fatal("earlier independent reference changed")
	}
}
