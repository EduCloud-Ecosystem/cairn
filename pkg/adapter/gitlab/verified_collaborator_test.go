// SPDX-License-Identifier: Apache-2.0

package gitlab

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/EduCloud-Ecosystem/cairn/pkg/adapter"
)

func TestVerifiedCollaboratorUsesBoundNumericID(t *testing.T) {
	for _, scenario := range []string{"matching", "stale", "wrong-login", "ambiguous", "unbound"} {
		t.Run(scenario, func(t *testing.T) {
			grants := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.Method {
				case http.MethodGet:
					switch scenario {
					case "stale":
						fmt.Fprint(w, `[{"id":22,"username":"alice"}]`)
					case "wrong-login":
						fmt.Fprint(w, `[{"id":11,"username":"renamed"}]`)
					case "ambiguous":
						fmt.Fprint(w, `[{"id":11,"username":"alice"},{"id":22,"username":"alice"}]`)
					default:
						fmt.Fprint(w, `[{"id":11,"username":"alice"}]`)
					}
				case http.MethodPost:
					grants++
					var body struct {
						UserID int64 `json:"user_id"`
					}
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					if body.UserID != 11 {
						t.Errorf("grant did not target stable ID: %d", body.UserID)
					}
					w.WriteHeader(http.StatusCreated)
				default:
					t.Errorf("unexpected %s", r.Method)
				}
			}))
			defer srv.Close()
			a := newTestAdapter(t, srv)
			id := "11"
			if scenario == "unbound" {
				id = ""
			}
			err := a.SetVerifiedCollaborator(context.Background(), adapter.RepoRef{Namespace: "org", Name: "repo"}, "alice", id, adapter.RoleWrite)
			if scenario == "matching" {
				if err != nil || grants != 1 {
					t.Fatalf("verified grant: %v, count %d", err, grants)
				}
			} else if err == nil || grants != 0 {
				t.Fatalf("unverified grant: %v, count %d", err, grants)
			}
		})
	}
}
