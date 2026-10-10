// SPDX-License-Identifier: AGPL-3.0-or-later
package api

import (
	"context"
	"encoding/json"
	"github.com/EduCloud-Ecosystem/cairn/internal/assessment"
	"github.com/EduCloud-Ecosystem/cairn/internal/grading"
	"github.com/EduCloud-Ecosystem/cairn/internal/store"
	"github.com/EduCloud-Ecosystem/cairn/internal/store/sqlite"
	"github.com/EduCloud-Ecosystem/cairn/pkg/adapter"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Explicit, loopback-only synthetic browser fixture. The production executable
// has no fixture routes or login bypass. Touch output/stop to shut this down.
func TestAssessmentBrowserFixture(t *testing.T) {
	dir := os.Getenv("CAIRN_ASSESSMENT_BROWSER_DIR")
	if dir == "" {
		t.Skip("opt-in browser fixture")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	st, err := sqlite.Open(filepath.Join(dir, "fixture.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	for _, err := range []error{st.CreateClassroom(ctx, &store.Classroom{ID: "c", Name: "Synthetic assessment course", Host: adapter.HostGitHub, HostNamespace: "fixture"}), st.CreateAssignment(ctx, &store.Assignment{ID: "a", ClassroomID: "c", Title: "Explain your reasoning", Slug: "reasoning"}), st.CreateRosterEntry(ctx, &store.RosterEntry{ID: "r", ClassroomID: "c", Host: adapter.HostGitHub, HostUsername: "synthetic-student", Status: store.RosterActive}), st.CreateSubmission(ctx, &store.Submission{ID: "s", AssignmentID: "a", RosterEntryID: "r", Status: "active", LatestCommit: strings.Repeat("a", 40)})} {
		if err != nil {
			t.Fatal(err)
		}
	}
	rubric := assessment.Rubric{Title: "Reasoning", Paths: []string{"response.md"}, Criteria: []assessment.Criterion{{ID: "reason", Description: "Support reasoning with evidence", MaxPoints: 10}}}
	var provider *assessment.OpenAI
	var checkout grading.RevisionCheckout = assessmentFixture{}
	if keyFile := os.Getenv("CAIRN_ASSESSMENT_BROWSER_OPENAI_KEY_FILE"); keyFile != "" {
		key, err := assessment.LoadOpenAIKeyFile(keyFile)
		if err != nil {
			t.Fatal(err)
		}
		provider, err = assessment.NewOpenAI(key, "")
		if err != nil {
			t.Fatal(err)
		}
		checkout = openAIBrowserCheckout{}
		rubric.Criteria[0].Description = "Award 4 points for correctly stating that the mean of 2,4,6 is 4, 4 points for correctly stating that adding 20 changes the mean to 8, and 2 points for explaining the effect of the high value. Wrong or absent parts earn no points."
	}
	b, _ := json.Marshal(rubric)
	if err = st.PutAssessmentRubric(ctx, &store.AssessmentRubric{AssignmentID: "a", Digest: assessment.DigestBytes(b), Document: b}); err != nil {
		t.Fatal(err)
	}
	webDir, _ := filepath.Abs("../../web/dist")
	srv := New(Options{Store: st, AuthEnabled: true, AdminUsers: []string{"synthetic-instructor"}, WebDir: webDir, AssessmentCheckout: checkout, OpenAIProvider: provider, OpenAIClassrooms: []string{"c"}})
	// Session cookies are random, synthetic, and expire when this fixture ends.
	teacherToken, studentToken := newSessionToken(), newSessionToken()
	srv.sessions[teacherToken] = session{userID: "fixture-instructor", username: "synthetic-instructor", host: adapter.HostGitHub, isOperator: true, created: time.Now()}
	srv.sessions[studentToken] = session{username: "synthetic-student", host: adapter.HostGitHub, created: time.Now()}
	server := httptest.NewServer(srv)
	defer server.Close()
	u, _ := url.Parse(server.URL)
	for name, token := range map[string]string{"instructor": teacherToken, "student": studentToken} {
		state := map[string]any{"cookies": []any{map[string]any{"name": sessionCookie, "value": token, "domain": u.Hostname(), "path": "/", "httpOnly": true, "secure": false, "sameSite": "Lax", "expires": -1}}, "origins": []any{}}
		raw, _ := json.Marshal(state)
		os.WriteFile(filepath.Join(dir, name+"-state.json"), raw, 0600)
	}
	r, err := srv.assessment.Capture(ctx, "s", "fixture-instructor")
	if err != nil {
		t.Fatal(err)
	}
	var d assessment.Document
	json.Unmarshal(r.Document, &d)
	points := 6.0
	proposal := assessment.Proposal{SubmissionStatus: "relevant_work", Source: "fixture", Model: "synthetic-no-model", PromptVersion: "fixture-v1", InputDigest: d.InputDigest, Criteria: []assessment.Judgment{{CriterionID: "reason", Points: &points, Feedback: "Add detail linking your evidence to the conclusion.", Uncertainty: "Synthetic proposal only", Citations: []assessment.Citation{{Path: "response.md", SHA256: d.Artifacts[0].SHA256, Location: "line:1"}}}}}
	b, _ = json.Marshal(proposal)
	os.WriteFile(filepath.Join(dir, "proposal.json"), b, 0600)

	if os.Getenv("CAIRN_ASSESSMENT_BROWSER_REVIEW_EVIDENCE") == "1" {
		ids := map[string]string{}
		for _, mode := range []string{"before-generation", "with-proposal"} {
			cal, e := srv.assessment.CreateCalibrationSection(ctx, "a", "fixture-instructor", "", &rubric)
			if e != nil {
				t.Fatal(e)
			}
			source := "Synthetic evidence supports a bounded conclusion."
			cal, e = srv.assessment.AddCalibrationExample(ctx, cal.ID, "fixture-instructor", cal.Revision, map[string]string{"response.md": source})
			if e != nil {
				t.Fatal(e)
			}
			var doc assessment.CalibrationDocument
			json.Unmarshal(cal.Document, &doc)
			example := doc.Examples[0]
			points := 4.0
			judgments := []assessment.Judgment{{CriterionID: "reason", Points: &points, Feedback: "Credit the stated evidence; the limitation needs elaboration.", Uncertainty: "Assistant-authored synthetic adjudication; not a blind instructor reference.", Citations: []assessment.Citation{{Path: "response.md", SHA256: assessment.DigestBytes([]byte(source)), Location: "line:1", Quote: source}}}}
			zero := 0
			report := &assessment.CalibrationExecutionReport{Version: "cairn-calibration-execution-v1", Completed: true, ExpectedSamples: 1, CreatedAt: time.Now().UTC(), RubricDigest: assessment.Digest(rubric), BundleDigest: strings.Repeat("b", 64), ChecksDigest: strings.Repeat("c", 64), Image: "sha256:" + strings.Repeat("d", 64), UncheckedCriteria: []string{}, Samples: []assessment.CalibrationExecutionSample{{SampleID: "synthetic-one", SourceHashes: map[string]string{"response.md": assessment.DigestBytes([]byte(source))}, Status: "completed", Tests: []assessment.CalibrationExecutionObservation{{CriterionID: "reason", Status: "passed", ExitCode: &zero, Detail: "Synthetic imported observation; not a live execution attestation."}}}}}
			packet, e := assessment.PrepareCalibrationReviewPacket(assessment.CalibrationBundle{Version: assessment.CalibrationBundleVersion, Purpose: "calibration", Rubric: rubric, Examples: []assessment.CalibrationBundleExample{{ID: "synthetic-one", Files: map[string]string{"response.md": source}}}}, assessment.CalibrationReviewPacket{SampleID: "synthetic-one", Authorship: "assistant", Author: "Synthetic assistant", Note: "Delegated local review, not an independent instructor rating.", Judgments: judgments, Execution: report})
			if e != nil {
				t.Fatal(e)
			}
			raw, _ := json.MarshalIndent(packet, "", "  ")
			os.WriteFile(filepath.Join(dir, "review-packet.json"), raw, 0600)
			if mode == "with-proposal" {
				doc.Examples[0].Document.Proposal = &assessment.Proposal{Source: "fixture", Model: "synthetic-no-model", PromptVersion: "fixture-v1", InputDigest: example.Document.InputDigest, Criteria: judgments, SubmissionStatus: "relevant_work"}
				previous := cal.Revision
				cal.Revision++
				cal.Document, _ = json.Marshal(doc)
				if e = st.UpdateCalibration(ctx, cal, previous); e != nil {
					t.Fatal(e)
				}
			}
			ids[mode] = cal.ID
		}
		raw, _ := json.Marshal(ids)
		os.WriteFile(filepath.Join(dir, "calibration-ids.json"), raw, 0600)
	}
	os.WriteFile(filepath.Join(dir, "url"), []byte(server.URL), 0600)
	t.Log("Synthetic fixture listening", server.URL)
	deadline := time.After(10 * time.Minute)
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-deadline:
			t.Fatal("browser fixture timed out")
		case <-tick.C:
			if _, err := os.Stat(filepath.Join(dir, "stop")); err == nil {
				return
			}
		}
	}
}

type openAIBrowserCheckout struct{}

func (openAIBrowserCheckout) FetchRevision(_ context.Context, _ adapter.RepoRef, _, dir string) error {
	return os.WriteFile(filepath.Join(dir, "response.md"), []byte("The mean of 2,4,6 is 4. Adding 20 changes the mean to 8. The high value pulls the mean upward, showing its sensitivity to outliers."), 0600)
}
