// SPDX-License-Identifier: AGPL-3.0-or-later
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/EduCloud-Ecosystem/cairn/internal/lti"
	"github.com/EduCloud-Ecosystem/cairn/internal/store"
	"github.com/EduCloud-Ecosystem/cairn/pkg/adapter"
)

func TestStudentClaimRequiresStableAccountBinding(t *testing.T) {
	for _, tc := range []struct {
		name, boundID string
		claimed       bool
		want          int
	}{
		{"unclaimed invite", "", false, http.StatusFound},
		{"same account", "100", true, http.StatusFound},
		{"reassigned username", "previous-account", true, http.StatusForbidden},
		{"legacy claimed row", "", true, http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, st, queue := newTestServer("alice")
			ctx := context.Background()
			st.CreateClassroom(ctx, &store.Classroom{ID: "c", Host: adapter.HostGitHub, JoinPolicy: store.ClassroomJoinPolicyRoster})
			st.CreateAssignment(ctx, &store.Assignment{ID: "a", ClassroomID: "c"})
			roster := &store.RosterEntry{ID: "r", ClassroomID: "c", Host: adapter.HostGitHub, HostUsername: "alice", HostUserID: tc.boundID, Status: store.RosterInvited}
			if tc.claimed {
				now := time.Now().Add(-time.Hour)
				roster.ClaimedAt = &now
			}
			if err := st.CreateRosterEntry(ctx, roster); err != nil {
				t.Fatal(err)
			}
			response := doAcceptCallback(srv, "a")
			if response.Code != tc.want {
				t.Fatalf("claim = %d: %s", response.Code, response.Body.String())
			}
			saved, err := st.GetRosterEntry(ctx, "r")
			if err != nil {
				t.Fatal(err)
			}
			if tc.want == http.StatusFound {
				if saved.HostUserID != "100" || saved.ClaimedAt == nil {
					t.Fatalf("identity not bound: %+v", saved)
				}
				for _, sess := range srv.sessions {
					if sess.hostUserID != "100" {
						t.Fatal("OAuth stable identity was not retained in learner session")
					}
				}
			} else if saved.HostUserID != tc.boundID || len(queue.jobs) != 0 || len(srv.sessions) != 0 {
				t.Fatal("rejected claimant changed identity, provisioned work or received a session")
			}
		})
	}
}

func TestStudentWorkDoesNotAdoptUnboundRoster(t *testing.T) {
	srv, st, _ := newTestServer("operator")
	ctx := context.Background()
	st.CreateClassroom(ctx, &store.Classroom{ID: "c", Host: adapter.HostGitHub})
	st.CreateAssignment(ctx, &store.Assignment{ID: "a", ClassroomID: "c"})
	st.CreateRosterEntry(ctx, &store.RosterEntry{ID: "r", ClassroomID: "c", Host: adapter.HostGitHub, HostUsername: "alice", Status: store.RosterActive})
	st.CreateSubmission(ctx, &store.Submission{ID: "s", AssignmentID: "a", RosterEntryID: "r", Status: "active"})
	cookie := studentCookie(srv, adapter.HostGitHub, "alice")
	for _, path := range []string{"/me/work", "/me/work/s"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.AddCookie(cookie)
		res := httptest.NewRecorder()
		srv.ServeHTTP(res, req)
		if path == "/me/work" {
			var body struct {
				Work []workItem `json:"work"`
			}
			if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil || res.Code != http.StatusOK || len(body.Work) != 0 {
				t.Fatal("unbound roster exposed list", res.Code, res.Body.String())
			}
		} else if res.Code != http.StatusNotFound {
			t.Fatal("unbound roster exposed detail", res.Code)
		}
	}
}

func TestStudentWorkRejectsReassignedUsernameAndLegacyIdentity(t *testing.T) {
	for _, stableID := range []string{"replacement-account", ""} {
		t.Run("session-"+stableID, func(t *testing.T) {
			srv, st, _ := newTestServer("operator")
			seedStudentData(t, st)
			cookie := studentCookie(srv, adapter.HostGitHub, "alice")
			sess := srv.sessions[cookie.Value]
			sess.hostUserID = stableID
			srv.sessions[cookie.Value] = sess
			for _, path := range []string{"/me/work", "/me/work/s1"} {
				req := httptest.NewRequest(http.MethodGet, path, nil)
				req.AddCookie(cookie)
				res := httptest.NewRecorder()
				srv.ServeHTTP(res, req)
				if stableID == "" {
					if res.Code != http.StatusUnauthorized {
						t.Fatal("session without stable identity accepted", res.Code)
					}
				} else if path == "/me/work" {
					var body struct {
						Work []workItem `json:"work"`
					}
					if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil || res.Code != http.StatusOK || len(body.Work) != 0 {
						t.Fatal("reassigned username exposed list", res.Code, res.Body.String())
					}
				} else if res.Code != http.StatusNotFound {
					t.Fatal("reassigned username exposed detail", res.Code)
				}
			}
		})
	}
}

func TestLTILearnerConnectRequiresStableRosterIdentity(t *testing.T) {
	for _, tc := range []struct {
		name, rosterID string
		want           int
	}{
		{"same account", "fixture-student", http.StatusOK},
		{"reassigned username", "previous-account", http.StatusNotFound},
		{"legacy roster", "", http.StatusNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, _ := ltiTestServer(t)
			ctx := context.Background()
			launch := lti.Launch{Subject: "lms-student", Context: "context", Resource: "resource", LineItem: srv.ltiService.Client.Config.ServiceOrigin + "/lineitem", Role: lti.Instructor, Registration: srv.ltiService.Client.Registration}
			if err := srv.ltiService.MapAssignment(ctx, "a", "owner", launch); err != nil {
				t.Fatal(err)
			}
			if err := srv.store.CreateRosterEntry(ctx, &store.RosterEntry{ID: "r", ClassroomID: "c", Host: adapter.HostGitHub, HostUsername: "student", HostUserID: tc.rosterID, Status: store.RosterActive}); err != nil {
				t.Fatal(err)
			}
			launch.Role = lti.Learner
			srv.ltiState.pending["pending"] = ltiPending{launch, time.Now()}
			res := ltiRequest(srv, "POST", "/lti/connect", `{}`, "learner", "pending", srv.ltiService.Client.Config.BaseURL, "application/json")
			if res.Code != tc.want {
				t.Fatalf("connect = %d: %s", res.Code, res.Body.String())
			}
		})
	}
}

func TestUnclaimedImportedWorkNeedsIdentityReconciliation(t *testing.T) {
	srv, st, queue := newTestServer("alice")
	ctx := context.Background()
	st.CreateClassroom(ctx, &store.Classroom{ID: "c", Host: adapter.HostGitHub})
	st.CreateAssignment(ctx, &store.Assignment{ID: "a", ClassroomID: "c"})
	st.CreateRosterEntry(ctx, &store.RosterEntry{ID: "r", ClassroomID: "c", Host: adapter.HostGitHub, HostUsername: "alice", Status: store.RosterActive})
	st.CreateSubmission(ctx, &store.Submission{ID: "s", AssignmentID: "a", RosterEntryID: "r", Status: "active"})
	response := doAcceptCallback(srv, "a")
	if response.Code != http.StatusForbidden || len(srv.sessions) != 0 || len(queue.jobs) != 0 {
		t.Fatal("legacy imported work adopted", response.Code)
	}
}
