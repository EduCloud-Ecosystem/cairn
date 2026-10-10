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

	"github.com/EduCloud-Ecosystem/cairn/internal/store"
)

func calibrationDoc(t *testing.T, c *store.Calibration) CalibrationDocument {
	t.Helper()
	var d CalibrationDocument
	if err := json.Unmarshal(c.Document, &d); err != nil {
		t.Fatal(err)
	}
	return d
}

func TestInstructorCalibrationWorkflow(t *testing.T) {
	svc, _, _ := fixture(t)
	ctx := context.Background()
	c, err := svc.CreateCalibration(ctx, "a", "instructor-a")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Store.GetCalibration(ctx, c.ID, "instructor-b"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("another instructor read private examples")
	}
	if _, err = svc.AddCalibrationExample(ctx, c.ID, "instructor-b", c.Revision, map[string]string{"response.md": "wrong owner"}); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("another instructor edited calibration")
	}
	c, err = svc.AddCalibrationExample(ctx, c.ID, "instructor-a", c.Revision, map[string]string{"response.md": "HISTORICAL_ONLY evidence and reasoning."})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = svc.ApproveCalibration(ctx, c.ID, "instructor-a", c.Revision, "Explain which evidence supports each conclusion."); err == nil {
		t.Fatal("unreviewed calibration approved")
	}
	d := calibrationDoc(t, c)
	e := d.Examples[0]
	p, _ := NewOpenAI("fixture", "")
	p.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		js := providerCriteria(7)
		js[0].Citations[0].Quote = "HISTORICAL_ONLY evidence and reasoning."
		return mockResponse(200, responseJSON(t, js)), nil
	})
	g := NewGenerator(svc, p, []string{"c"})
	c, err = g.GenerateCalibration(ctx, c.ID, "instructor-a", e.ID, c.Revision, e.Document.InputDigest)
	if err != nil {
		t.Fatal(err)
	}
	d = calibrationDoc(t, c)
	e = d.Examples[0]
	if _, err = svc.Store.LatestGradeForSubmission(ctx, "s"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("historical generation published grade")
	}
	js := append([]Judgment(nil), e.Document.Proposal.Criteria...)
	js[0].Points = ptr(8)
	js[0].Feedback = "Instructor correction: credit the explicit reasoning."
	c, err = svc.ReviewCalibrationExample(ctx, c.ID, "instructor-a", e.ID, c.Revision, js, "The answer supports an additional point.")
	if err != nil {
		t.Fatal(err)
	}
	c, err = svc.ApproveCalibration(ctx, c.ID, "instructor-a", c.Revision, "Explain which evidence supports each conclusion.")
	if err != nil {
		t.Fatal(err)
	}
	if c.Status != "ready" {
		t.Fatal("reviewed profile not ready")
	}
	d = calibrationDoc(t, c)
	if *d.Examples[0].Document.Proposal.Criteria[0].Points != 7 || *d.Examples[0].Review.Criteria[0].Points != 8 {
		t.Fatal("calibration erased original comparison")
	}
	if _, err = svc.AddCalibrationExample(ctx, c.ID, "instructor-a", c.Revision, map[string]string{"response.md": "later"}); !errors.Is(err, store.ErrConflict) {
		t.Fatal("ready profile mutable")
	}
	if _, err = svc.CaptureCalibrated(ctx, "s", "instructor-b", c.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("instructor reused someone else's profile")
	}
	capture, err := svc.CaptureCalibrated(ctx, "s", "instructor-a", c.ID)
	if err != nil {
		t.Fatal(err)
	}
	var input Document
	json.Unmarshal(capture.Document, &input)
	if input.Calibration == nil || input.Calibration.Revision != c.Revision || input.InputDigest != InputDigest(input) {
		t.Fatal("profile version not pinned")
	}
	p.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), "HISTORICAL_ONLY") || strings.Contains(string(body), "instructor-a") || !strings.Contains(string(body), "Explain which evidence supports each conclusion.") {
			t.Fatal("wrong calibration data sent with future work")
		}
		return mockResponse(200, responseJSON(t, providerCriteria(7))), nil
	})
	if _, err = g.Generate(ctx, capture.ID, input.InputDigest); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Store.LatestGradeForSubmission(ctx, "s"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("calibrated model published grade")
	}
	followup, err := svc.CreateCalibrationFrom(ctx, "a", "instructor-a", c.ID)
	if err != nil {
		t.Fatal(err)
	}
	followup, err = svc.AddCalibrationExample(ctx, followup.ID, "instructor-a", followup.Revision, map[string]string{"response.md": "Fresh held-out reasoning."})
	if err != nil {
		t.Fatal(err)
	}
	fresh := calibrationDoc(t, followup).Examples[0]
	if fresh.Document.Calibration == nil || fresh.Document.Calibration.ID != c.ID {
		t.Fatal("fresh round omitted prior guidance")
	}
	p.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		js := providerCriteria(7)
		js[0].Citations[0].Quote = "Fresh held-out reasoning."
		return mockResponse(200, responseJSON(t, js)), nil
	})
	if _, err = g.GenerateCalibration(ctx, followup.ID, "instructor-a", fresh.ID, followup.Revision, fresh.Document.InputDigest); err != nil {
		t.Fatal(err)
	}
	rub, _ := svc.Store.GetAssessmentRubric(ctx, "a")
	rub.Digest = "changed"
	svc.Store.PutAssessmentRubric(ctx, rub)
	if _, err = svc.CaptureCalibrated(ctx, "s", "instructor-a", c.ID); err == nil {
		t.Fatal("stale rubric reused calibration")
	}
}

func TestHistoricalCaptureIsPinnedAndErasureRemovesCopies(t *testing.T) {
	svc, _, _ := fixture(t)
	ctx := context.Background()
	c, err := svc.CreateCalibration(ctx, "a", "instructor")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = svc.CaptureCalibrationExample(ctx, c.ID, "instructor", "s", "main", c.Revision); err == nil {
		t.Fatal("unpinned historical ref accepted")
	}
	c, err = svc.CaptureCalibrationExample(ctx, c.ID, "instructor", "s", revision, c.Revision)
	if err != nil {
		t.Fatal(err)
	}
	d := calibrationDoc(t, c)
	if d.Examples[0].SourceSubmissionID != "s" || d.Examples[0].Document.Revision != revision {
		t.Fatal("historical provenance missing")
	}
	if err = svc.Store.DeleteRosterEntry(ctx, "r"); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Store.GetCalibration(ctx, c.ID, "instructor"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("student erasure retained historical calibration copy")
	}
}

func TestCalibrationFailureIsRecordedAndNeverRetried(t *testing.T) {
	svc, _, _ := fixture(t)
	ctx := context.Background()
	c, _ := svc.CreateCalibration(ctx, "a", "teacher")
	c, _ = svc.AddCalibrationExample(ctx, c.ID, "teacher", c.Revision, map[string]string{"response.md": "Historical source"})
	e := calibrationDoc(t, c).Examples[0]
	p, _ := NewOpenAI("SECRET", "")
	calls := 0
	p.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) { calls++; return mockResponse(429, "SECRET"), nil })
	g := NewGenerator(svc, p, []string{"c"})
	if _, err := g.GenerateCalibration(ctx, c.ID, "teacher", e.ID, c.Revision, e.Document.InputDigest); err == nil || strings.Contains(err.Error(), "SECRET") {
		t.Fatal("unsafe failure")
	}
	if _, err := g.GenerateCalibration(ctx, c.ID, "teacher", e.ID, c.Revision, e.Document.InputDigest); !errors.Is(err, store.ErrGenerationLimit) || calls != 1 {
		t.Fatal("failed historical request retried")
	}
	a, err := svc.Store.GetGeneration(ctx, "calibration:"+c.ID+":"+e.ID)
	if err != nil || a.Status != "failed" || a.ErrorCode != "rate_limited" {
		t.Fatal("failure record missing")
	}
}

func TestExcludedExamplesCannotApproveOrRetryCalibration(t *testing.T) {
	svc, _, _ := fixture(t)
	ctx := context.Background()
	c, err := svc.CreateCalibration(ctx, "a", "teacher")
	if err != nil {
		t.Fatal(err)
	}
	c, err = svc.AddCalibrationExample(ctx, c.ID, "teacher", c.Revision, map[string]string{"response.md": "Historical source"})
	if err != nil {
		t.Fatal(err)
	}
	e := calibrationDoc(t, c).Examples[0]
	if _, err = svc.ExcludeCalibrationExample(ctx, c.ID, "teacher", e.ID, c.Revision, " "); err == nil {
		t.Fatal("exclusion accepted without rationale")
	}
	c, err = svc.ExcludeCalibrationExample(ctx, c.ID, "teacher", e.ID, c.Revision, "Not representative of this rubric.")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = svc.ApproveCalibration(ctx, c.ID, "teacher", c.Revision, "Use source evidence."); err == nil {
		t.Fatal("all-excluded profile approved")
	}
	if len(calibrationDoc(t, c).Examples[0].Document.Artifacts) == 0 {
		t.Fatal("exclusion erased evidence")
	}
	p, _ := NewOpenAI("fixture", "")
	p.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("excluded example sent to provider")
		return nil, nil
	})
	g := NewGenerator(svc, p, []string{"c"})
	if _, err = g.GenerateCalibration(ctx, c.ID, "teacher", e.ID, c.Revision, e.Document.InputDigest); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("excluded example generated: %v", err)
	}
}
