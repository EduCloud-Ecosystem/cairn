// SPDX-License-Identifier: AGPL-3.0-or-later
package api

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/EduCloud-Ecosystem/cairn/internal/assessment"
	"github.com/EduCloud-Ecosystem/cairn/internal/lti"
	"github.com/EduCloud-Ecosystem/cairn/internal/ltisim"
	"github.com/EduCloud-Ecosystem/cairn/internal/store"
	"github.com/EduCloud-Ecosystem/cairn/internal/store/sqlite"
	"github.com/EduCloud-Ecosystem/cairn/pkg/adapter"
)

// Opt-in, synthetic-only loopback fixture. No production login bypass exists.
func TestLTIBrowserFixture(t *testing.T) {
	dir := os.Getenv("CAIRN_LTI_BROWSER_DIR")
	if dir == "" {
		t.Skip("opt-in browser fixture")
	}
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal("use a new private directory", err)
	}
	st, err := sqlite.Open(filepath.Join(dir, "fixture.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	now := time.Now()
	revision := strings.Repeat("a", 40)
	for _, err := range []error{
		st.CreateUser(ctx, &store.User{ID: "owner", Host: adapter.HostGitHub, HostUserID: "fixture-teacher", HostUsername: "synthetic-instructor"}),
		st.CreateClassroom(ctx, &store.Classroom{ID: "c", Name: "Synthetic Brightspace course", Host: adapter.HostGitHub, HostNamespace: "fixture", CreatedBy: "owner"}),
		st.CreateAssignment(ctx, &store.Assignment{ID: "a", ClassroomID: "c", Title: "Synthetic reasoning assignment", Slug: "reasoning", Type: store.AssignmentIndividual}),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, user := range []string{"student-a", "student-b"} {
		if err := st.CreateRosterEntry(ctx, &store.RosterEntry{ID: "r-" + user, ClassroomID: "c", Host: adapter.HostGitHub, HostUsername: user, HostUserID: "fixture-" + user, Status: store.RosterActive}); err != nil {
			t.Fatal(err)
		}
		if err := st.CreateSubmission(ctx, &store.Submission{ID: "s-" + user, AssignmentID: "a", RosterEntryID: "r-" + user, Status: "active", LatestCommit: revision, LastActivityAt: &now}); err != nil {
			t.Fatal(err)
		}
	}
	rubric := assessment.Rubric{Title: "Synthetic reasoning", Paths: []string{"response.md"}, Criteria: []assessment.Criterion{{ID: "reason", Description: "Support reasoning", MaxPoints: 10}}}
	raw, _ := json.Marshal(rubric)
	if err := st.PutAssessmentRubric(ctx, &store.AssessmentRubric{AssignmentID: "a", Digest: assessment.DigestBytes(raw), Document: raw}); err != nil {
		t.Fatal(err)
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	platformKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(nil)
	defer server.Close()
	platform := httptest.NewUnstartedServer(nil)
	defer platform.Close()
	toolURL := "http://" + server.Listener.Addr().String()
	platformURL := "http://" + platform.Listener.Addr().String()
	simulator, err := ltisim.New(platformURL, toolURL, platformKey, &key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	client, err := lti.New(simulator.Config("c", ""), key)
	if err != nil {
		t.Fatal(err)
	}
	webDir, _ := filepath.Abs("../../web/dist")
	srv := New(Options{Store: st, AuthEnabled: true, AdminUsers: []string{"synthetic-instructor"}, WebDir: webDir, AssessmentCheckout: assessmentFixture{}, LTIClient: client})
	for _, user := range []string{"student-a", "student-b"} {
		record, err := srv.assessment.Capture(ctx, "s-"+user, "owner")
		if err != nil {
			t.Fatal(err)
		}
		var doc assessment.Document
		if err := json.Unmarshal(record.Document, &doc); err != nil {
			t.Fatal(err)
		}
		points := 8.0
		judgments := []assessment.Judgment{{CriterionID: "reason", Points: &points, Feedback: "Synthetic reviewed feedback.", Uncertainty: "Synthetic fixture", Citations: []assessment.Citation{{Path: "response.md", SHA256: doc.Artifacts[0].SHA256, Location: "line:1"}}}}
		proposal := assessment.Proposal{SubmissionStatus: "relevant_work", Source: "fixture", Model: "synthetic", PromptVersion: "fixture", InputDigest: doc.InputDigest, Criteria: judgments}
		if _, err := srv.assessment.Propose(ctx, record.ID, proposal); err != nil {
			t.Fatal(err)
		}
		if _, err := srv.assessment.Review(ctx, record.ID, "owner", "approve", "Synthetic instructor review", judgments); err != nil {
			t.Fatal(err)
		}
	}
	parsed, _ := url.Parse(toolURL)
	for _, user := range []string{"instructor", "student-a", "student-b"} {
		token := newSessionToken()
		sess := session{username: user, hostUserID: "fixture-" + user, host: adapter.HostGitHub, created: time.Now()}
		if user == "instructor" {
			sess.userID = "owner"
			sess.username = "synthetic-instructor"
			sess.isOperator = true
		}
		srv.sessions[token] = sess
		raw, _ := json.Marshal(map[string]any{"cookies": []any{map[string]any{"name": sessionCookie, "value": token, "domain": parsed.Hostname(), "path": "/", "httpOnly": true, "secure": false, "sameSite": "Lax", "expires": -1}}, "origins": []any{}})
		if err := os.WriteFile(filepath.Join(dir, user+"-state.json"), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	server.Config.Handler = srv
	platform.Config.Handler = simulator.Handler()
	server.Start()
	platform.Start()
	if err := os.WriteFile(filepath.Join(dir, "url"), []byte(toolURL), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "platform-url"), []byte(platformURL), 0600); err != nil {
		t.Fatal(err)
	}
	t.Log("Synthetic LTI fixture", toolURL, platformURL)
	deadline := time.After(15 * time.Minute)
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
