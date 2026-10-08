package api

import (
	"context"
	"encoding/json"
	"github.com/EduCloud-Ecosystem/cairn/internal/assessment"
	"github.com/EduCloud-Ecosystem/cairn/internal/store"
	"github.com/EduCloud-Ecosystem/cairn/pkg/adapter"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type assessmentFixture struct{}

func (assessmentFixture) FetchRevision(_ context.Context, _ adapter.RepoRef, _, dir string) error {
	return os.WriteFile(filepath.Join(dir, "response.md"), []byte("Supported reasoning."), 0600)
}
func TestAssessmentOperatorReviewAndStudentPrivacy(t *testing.T) {
	srv, st := newAuthServer("alice")
	srv.assessment = &assessment.Service{Store: st, Checkout: assessmentFixture{}}
	srv.assessmentCapture = make(chan struct{}, 1)
	srv.mux = http.NewServeMux()
	srv.routes()
	ctx := context.Background()
	st.CreateClassroom(ctx, &store.Classroom{ID: "c"})
	st.CreateAssignment(ctx, &store.Assignment{ID: "a", ClassroomID: "c"})
	st.CreateRosterEntry(ctx, &store.RosterEntry{ID: "r", ClassroomID: "c", Host: adapter.HostGitHub, HostUsername: "bob", Status: store.RosterActive})
	st.CreateSubmission(ctx, &store.Submission{ID: "s", AssignmentID: "a", RosterEntryID: "r", Status: "active", LatestCommit: strings.Repeat("a", 40)})
	operator := login(t, srv)
	student := studentCookie(srv, adapter.HostGitHub, "bob")
	other := studentCookie(srv, adapter.HostGitHub, "eve")
	request := func(method, path, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if cookie != nil {
			req.AddCookie(cookie)
		}
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		return rec
	}
	for _, route := range []struct{ method, path string }{{"PUT", "/assignments/a/assessment-rubric"}, {"GET", "/assignments/a/assessment-rubric"}, {"POST", "/submissions/s/assessments"}, {"GET", "/submissions/s/assessments"}, {"POST", "/assessments/fake/proposal"}, {"POST", "/assessments/fake/review"}} {
		for _, cookie := range []*http.Cookie{nil, student} {
			if rec := request(route.method, route.path, `{}`, cookie); rec.Code != 401 {
				t.Fatalf("unauthorized %s %d", route.path, rec.Code)
			}
		}
	}
	rubric := `{"title":"Reasoning","paths":["response.md"],"criteria":[{"id":"reason","description":"Use evidence","max_points":10}]}`
	if rec := request("PUT", "/assignments/a/assessment-rubric", rubric, operator); rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	rec := request("POST", "/submissions/s/assessments", `{}`, operator)
	if rec.Code != 201 {
		t.Fatal(rec.Body.String())
	}
	var r store.AssessmentRecord
	json.Unmarshal(rec.Body.Bytes(), &r)
	var d assessment.Document
	json.Unmarshal(r.Document, &d)
	points := 6.0
	p := assessment.Proposal{Source: "fixture", Model: "synthetic", PromptVersion: "v1", InputDigest: d.InputDigest, Criteria: []assessment.Judgment{{CriterionID: "reason", Points: &points, Feedback: "Improve the explanation.", Uncertainty: "Synthetic example", Citations: []assessment.Citation{{Path: "response.md", SHA256: d.Artifacts[0].SHA256, Location: "line:1"}}}}}
	b, _ := json.Marshal(p)
	if rec = request("POST", "/assessments/"+r.ID+"/proposal", string(b), operator); rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	rec = request("GET", "/me/work/s", "", student)
	if strings.Contains(rec.Body.String(), "Improve the explanation") {
		t.Fatal("unapproved feedback leaked")
	}
	review, _ := json.Marshal(map[string]any{"action": "approve", "note": "Checked evidence", "criteria": p.Criteria})
	rec = request("POST", "/assessments/"+r.ID+"/review", string(review), operator)
	if rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	rec = request("GET", "/me/work/s", "", student)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Improve the explanation") || strings.Contains(rec.Body.String(), "original") || strings.Contains(rec.Body.String(), "synthetic") {
		t.Fatalf("published student view: %s", rec.Body.String())
	}
	if rec = request("GET", "/me/work/s", "", other); rec.Code != 404 {
		t.Fatal("peer assessment leaked")
	}
	if rec = request("POST", "/assessments/"+r.ID+"/review", string(review), operator); rec.Code != 409 {
		t.Fatal("duplicate approval accepted")
	}
}

func TestOpenAIRoutesRequireOperator(t *testing.T) {
	srv, st := newAuthServer("alice")
	provider, err := assessment.NewOpenAI("synthetic-never-sent", "")
	if err != nil {
		t.Fatal(err)
	}
	srv.assessment = &assessment.Service{Store: st, Checkout: assessmentFixture{}}
	srv.assessmentGenerator = assessment.NewGenerator(*srv.assessment, provider, []string{"c"})
	srv.mux = http.NewServeMux()
	srv.routes()
	for _, path := range []string{"/assessments/p/generate", "/assessment-provider/control"} {
		for _, cookie := range []*http.Cookie{nil, studentCookie(srv, adapter.HostGitHub, "bob")} {
			req := httptest.NewRequest("POST", path, strings.NewReader(`{"paused":true}`))
			req.Header.Set("Content-Type", "application/json")
			if cookie != nil {
				req.AddCookie(cookie)
			}
			rec := httptest.NewRecorder()
			srv.ServeHTTP(rec, req)
			if rec.Code != 401 {
				t.Fatalf("non-operator access %s: %d", path, rec.Code)
			}
		}
	}
	req := httptest.NewRequest("POST", "/assessment-provider/control", strings.NewReader(`{"paused":true}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(login(t, srv))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	paused, err := st.GenerationPaused(context.Background())
	if err != nil || !paused {
		t.Fatal("pause not persisted")
	}
}
