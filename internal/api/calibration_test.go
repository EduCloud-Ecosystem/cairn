// SPDX-License-Identifier: AGPL-3.0-or-later
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/EduCloud-Ecosystem/cairn/internal/assessment"
	"github.com/EduCloud-Ecosystem/cairn/internal/store"
	"github.com/EduCloud-Ecosystem/cairn/pkg/adapter"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCalibrationOwnerRoutesAndBothHistoricalSources(t *testing.T) {
	srv, st := newAuthServer("alice")
	ctx := context.Background()
	srv.assessment = &assessment.Service{Store: st, Checkout: assessmentFixture{}}
	srv.assessmentCapture = make(chan struct{}, 1)
	srv.mux = http.NewServeMux()
	srv.routes()
	st.CreateClassroom(ctx, &store.Classroom{ID: "c"})
	st.CreateAssignment(ctx, &store.Assignment{ID: "a", ClassroomID: "c"})
	st.CreateRosterEntry(ctx, &store.RosterEntry{ID: "r", ClassroomID: "c", Host: adapter.HostGitHub, HostUsername: "learner", Status: store.RosterActive})
	st.CreateSubmission(ctx, &store.Submission{ID: "s", AssignmentID: "a", RosterEntryID: "r", LatestCommit: strings.Repeat("a", 40), Status: "active"})
	owner := login(t, srv)
	token := newSessionToken()
	srv.sessions[token] = session{userID: "other-instructor", username: "other", isOperator: true, created: time.Now()}
	other := &http.Cookie{Name: sessionCookie, Value: token}
	student := studentCookie(srv, adapter.HostGitHub, "learner")
	request := func(method, path string, body any, cookie *http.Cookie) *httptest.ResponseRecorder {
		b, _ := json.Marshal(body)
		req := httptest.NewRequest(method, path, strings.NewReader(string(b)))
		req.Header.Set("Content-Type", "application/json")
		if cookie != nil {
			req.AddCookie(cookie)
		}
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		return rec
	}
	rubric := assessment.Rubric{Title: "Reasoning", Paths: []string{"response.md"}, Criteria: []assessment.Criterion{{ID: "reason", Description: "Support claims", MaxPoints: 10}}}
	if rec := request("PUT", "/assignments/a/assessment-rubric", rubric, owner); rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	for _, cookie := range []*http.Cookie{nil, student} {
		if rec := request("POST", "/assignments/a/calibrations", map[string]any{}, cookie); rec.Code != 401 {
			t.Fatal("non-instructor created calibration")
		}
	}
	rec := request("POST", "/assignments/a/calibrations", map[string]any{}, owner)
	if rec.Code != 201 {
		t.Fatal(rec.Body.String())
	}
	var c store.Calibration
	json.Unmarshal(rec.Body.Bytes(), &c)
	for _, route := range []struct {
		method, path string
		body         any
	}{
		{"GET", "/calibrations/" + c.ID, nil},
		{"POST", "/calibrations/" + c.ID + "/examples", map[string]any{"revision": 1, "files": map[string]string{"response.md": "private work"}}},
		{"POST", "/calibrations/" + c.ID + "/capture", map[string]any{"revision": 1, "submission_id": "s"}},
		{"POST", "/calibrations/" + c.ID + "/preflight", map[string]any{"revision": 1, "guidance": "private"}},
		{"POST", "/calibrations/" + c.ID + "/examples/example/evidence", map[string]any{"revision": 1}},
		{"POST", "/calibrations/" + c.ID + "/examples/example/reference", map[string]any{"revision": 1, "note": "private"}},
		{"POST", "/assignments/a/calibrations/coverage", map[string]any{"profile_ids": []string{c.ID}}},
		{"POST", "/calibrations/" + c.ID + "/approve", map[string]any{"revision": 1, "guidance": "private"}},
		{"DELETE", "/calibrations/" + c.ID, map[string]any{}},
	} {
		if rec = request(route.method, route.path, route.body, other); rec.Code != 404 {
			t.Fatalf("owner boundary %s %d", route.path, rec.Code)
		}
	}
	if rec = request("GET", "/assignments/a/calibrations", nil, other); rec.Code != 200 || strings.Contains(rec.Body.String(), c.ID) {
		t.Fatal("another instructor listed private calibration")
	}
	rec = request("POST", "/calibrations/"+c.ID+"/examples", map[string]any{"revision": c.Revision, "files": map[string]string{"response.md": "Past uploaded work"}}, owner)
	if rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	json.Unmarshal(rec.Body.Bytes(), &c)
	rec = request("POST", "/calibrations/"+c.ID+"/capture", map[string]any{"revision": c.Revision, "submission_id": "s"}, owner)
	if rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	json.Unmarshal(rec.Body.Bytes(), &c)
	var d assessment.CalibrationDocument
	json.Unmarshal(c.Document, &d)
	if len(d.Examples) != 2 || d.Examples[1].SourceSubmissionID != "s" {
		t.Fatal("historical inputs not captured")
	}

	packet, err := assessment.PrepareCalibrationReviewPacket(assessment.CalibrationBundle{Version: assessment.CalibrationBundleVersion, Purpose: "calibration", Rubric: rubric, Examples: []assessment.CalibrationBundleExample{{ID: "sample-one", Files: map[string]string{"response.md": "Past uploaded work"}}}}, assessment.CalibrationReviewPacket{SampleID: "sample-one", Authorship: "assistant", Author: "Synthetic assistant", Note: "Private delegated adjudication", Judgments: []assessment.Judgment{{CriterionID: "reason", Points: nil, Feedback: "Unassessable", Uncertainty: "No execution", Citations: []assessment.Citation{}}}})
	if err != nil {
		t.Fatal(err)
	}
	evidencePath := "/calibrations/" + c.ID + "/examples/" + d.Examples[0].ID + "/evidence"
	evidenceBody := map[string]any{"revision": c.Revision, "packet": packet}
	for _, cookie := range []*http.Cookie{nil, student, other} {
		rec = request("POST", evidencePath, evidenceBody, cookie)
		if rec.Code != 401 && rec.Code != 404 {
			t.Fatal("evidence owner boundary")
		}
	}
	injectedEvidence := map[string]any{"revision": c.Revision, "packet": packet, "imported_by": "fake-instructor"}
	if rec = request("POST", evidencePath, injectedEvidence, owner); rec.Code != 400 {
		t.Fatal("accepted forged importing identity")
	}
	if rec = request("POST", evidencePath, evidenceBody, owner); rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	json.Unmarshal(rec.Body.Bytes(), &c)
	if !strings.Contains(rec.Body.String(), "review_evidence") || strings.Contains(rec.Body.String(), "\"reference\"") {
		t.Fatal("evidence was not kept separate")
	}
	if rec = request("POST", evidencePath, evidenceBody, owner); rec.Code != 409 {
		t.Fatal("stale evidence mutation")
	}
	if rec = request("GET", "/assignments/a/calibrations", nil, owner); strings.Contains(rec.Body.String(), "Private delegated") {
		t.Fatal("list leaked review content")
	}

	// Bundle imports preserve authentication and reject attempted review injection.
	rec = request("POST", "/assignments/a/calibrations", map[string]any{}, owner)
	var batch store.Calibration
	json.Unmarshal(rec.Body.Bytes(), &batch)
	bundle := assessment.CalibrationBundle{Version: assessment.CalibrationBundleVersion, Purpose: "calibration", Rubric: rubric, Examples: []assessment.CalibrationBundleExample{{ID: "sample-one", Files: map[string]string{"response.md": "Historical batch source"}}}}
	path := "/calibrations/" + batch.ID + "/bundle"
	body := map[string]any{"revision": batch.Revision, "bundle": bundle}
	for _, cookie := range []*http.Cookie{nil, student, other} {
		rec = request("POST", path, body, cookie)
		if rec.Code != 401 && rec.Code != 404 {
			t.Fatal("bundle owner boundary bypassed")
		}
	}
	raw, _ := json.Marshal(body)
	var injected map[string]any
	json.Unmarshal(raw, &injected)
	injected["bundle"].(map[string]any)["review"] = map[string]any{"reviewer": "teacher", "points": 10}
	if rec = request("POST", path, injected, owner); rec.Code != 400 {
		t.Fatal("bundle accepted imported judgment")
	}
	if rec = request("POST", path, body, owner); rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	if rec = request("POST", path, body, owner); rec.Code != 409 {
		t.Fatal("bundle replay duplicated source")
	}
	// Calibrated course captures must not expose private guidance via otherwise
	// instance-wide operator assessment endpoints.
	pub := &store.AssessmentRecord{ID: "bound", SubmissionID: "s", Revision: strings.Repeat("a", 40), RubricDigest: "d", Status: "collected"}
	pub.Document, _ = json.Marshal(assessment.Document{Calibration: &assessment.CalibrationBinding{ID: c.ID, OwnerID: c.OwnerID, Guidance: "PRIVATE_GUIDANCE"}})
	if err := st.CreateAssessment(ctx, pub); err != nil {
		t.Fatal(err)
	}
	if rec = request("GET", "/submissions/s/assessments", nil, other); strings.Contains(rec.Body.String(), "PRIVATE_GUIDANCE") {
		t.Fatal("private guidance leaked")
	}
	if rec = request("POST", "/assessments/bound/review", map[string]any{}, other); rec.Code != 404 {
		t.Fatal("other instructor edited calibrated assessment")
	}
	if _, err := st.LatestGradeForSubmission(ctx, "s"); err == nil {
		t.Fatal("calibration changed live grades")
	}
	if rec = request("GET", "/me/work/s", nil, student); strings.Contains(rec.Body.String(), "Past uploaded work") {
		t.Fatal("historical examples leaked to learner")
	}
	if rec = request("POST", "/calibrations/"+c.ID+"/approve", map[string]any{"revision": c.Revision, "guidance": "Approve without review"}, owner); rec.Code != 400 {
		t.Fatal(fmt.Sprintf("unfinished calibration accepted: %d", rec.Code))
	}
}
