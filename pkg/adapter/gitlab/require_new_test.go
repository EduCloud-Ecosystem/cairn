// SPDX-License-Identifier: Apache-2.0

package gitlab

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/EduCloud-Ecosystem/cairn/pkg/adapter"
)

func TestRequireNewDoesNotAdoptExistingOrRacingRepository(t *testing.T) {
	for _, scenario := range []string{"existing", "race", "fresh"} {
		t.Run(scenario, func(t *testing.T) {
			posts := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.Method {
				case http.MethodGet:
					if scenario == "existing" || posts > 0 {
						w.WriteHeader(http.StatusOK)
					} else {
						w.WriteHeader(http.StatusNotFound)
					}
				case http.MethodPost:
					posts++
					if scenario == "race" {
						w.WriteHeader(http.StatusUnprocessableEntity)
					} else {
						w.WriteHeader(http.StatusCreated)
						_, _ = w.Write([]byte(`{"id":1}`))
					}
				case http.MethodDelete:
					w.WriteHeader(http.StatusNoContent)
				default:
					t.Errorf("unexpected request %s", r.Method)
					w.WriteHeader(http.StatusInternalServerError)
				}
			}))
			defer srv.Close()
			a := newTestAdapter(t, srv)
			ref, err := a.CreateRepoFromTemplate(context.Background(), adapter.TemplateRef{Namespace: "org", Name: "template"}, adapter.NamespaceRef{Slug: "org"}, "submission", adapter.CreateRepoOptions{Private: true, RequireNew: true})
			if scenario == "fresh" {
				if err != nil || ref.Name != "submission" {
					t.Fatalf("fresh create: %+v %v", ref, err)
				}
			} else {
				if err == nil || ref != (adapter.RepoRef{}) {
					t.Fatalf("adopted existing repository: %+v %v", ref, err)
				}
			}
			if scenario == "existing" && posts != 0 {
				t.Fatal("attempted to create over existing repository")
			}
		})
	}
}
