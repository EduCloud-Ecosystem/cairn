// SPDX-License-Identifier: Apache-2.0

package github

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/EduCloud-Ecosystem/cairn/pkg/adapter"
)

func TestVerifiedCollaboratorIdentityBoundary(t *testing.T) {
	for _, scenario := range []string{"matching", "stale", "wrong-login", "lookup-failure", "post-mismatch", "post-failure", "cancelled", "cleanup-failure"} {
		t.Run(scenario, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			gets, grants, revocations := 0, 0, 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.Method {
				case http.MethodGet:
					gets++
					if scenario == "lookup-failure" || (gets > 1 && scenario == "post-failure") {
						w.WriteHeader(http.StatusServiceUnavailable)
						return
					}
					if gets > 1 && scenario == "cancelled" {
						cancel()
						return
					}
					id, login := 11, "alice"
					if scenario == "stale" || (gets > 1 && (scenario == "post-mismatch" || scenario == "cleanup-failure")) {
						id = 22
					}
					if scenario == "wrong-login" {
						login = "renamed"
					}
					fmt.Fprintf(w, `{"id":%d,"login":%q}`, id, login)
				case http.MethodPut:
					grants++
					w.WriteHeader(http.StatusNoContent)
				case http.MethodDelete:
					revocations++
					if scenario == "cleanup-failure" {
						w.WriteHeader(http.StatusServiceUnavailable)
					} else {
						w.WriteHeader(http.StatusNoContent)
					}
				default:
					t.Errorf("unexpected %s", r.Method)
					w.WriteHeader(http.StatusInternalServerError)
				}
			}))
			defer srv.Close()
			a := &Adapter{httpc: srv.Client(), baseURL: srv.URL}
			err := a.SetVerifiedCollaborator(ctx, adapter.RepoRef{Namespace: "org", Name: "repo"}, "alice", "11", adapter.RoleWrite)
			if scenario == "matching" {
				if err != nil || grants != 1 || revocations != 0 {
					t.Fatalf("matching grant: %v, grants %d revocations %d", err, grants, revocations)
				}
				return
			}
			if err == nil {
				t.Fatal("unverified grant succeeded")
			}
			if scenario == "stale" || scenario == "wrong-login" || scenario == "lookup-failure" {
				if grants != 0 {
					t.Fatal("grant occurred before identity was verified")
				}
			} else if grants != 1 || revocations != 1 {
				t.Fatalf("missing cleanup: grants %d revocations %d, %v", grants, revocations, err)
			}
			if scenario == "cleanup-failure" && !strings.Contains(err.Error(), "reconciliation required") {
				t.Fatalf("cleanup failure hidden: %v", err)
			}
			if scenario == "stale" && !errors.Is(err, adapter.ErrIdentityMismatch) {
				t.Fatal(err)
			}
		})
	}
}

func TestVerifiedCollaboratorCancelsWrongInvitation(t *testing.T) {
	invitationDeleted, collaboratorDeleted := false, false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			fmt.Fprint(w, `{"id":11,"login":"alice"}`)
		case http.MethodPut:
			w.WriteHeader(http.StatusCreated)
			fmt.Fprint(w, `{"id":99,"invitee":{"id":22,"login":"alice"}}`)
		case http.MethodDelete:
			if strings.HasSuffix(r.URL.Path, "/invitations/99") {
				invitationDeleted = true
			}
			if strings.HasSuffix(r.URL.Path, "/collaborators/alice") {
				collaboratorDeleted = true
			}
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer srv.Close()
	a := &Adapter{httpc: srv.Client(), baseURL: srv.URL}
	err := a.SetVerifiedCollaborator(context.Background(), adapter.RepoRef{Namespace: "org", Name: "repo"}, "alice", "11", adapter.RoleWrite)
	if !errors.Is(err, adapter.ErrIdentityMismatch) || !invitationDeleted || !collaboratorDeleted {
		t.Fatalf("wrong invitation not reconciled: %v %v %v", err, invitationDeleted, collaboratorDeleted)
	}
}
