// SPDX-License-Identifier: AGPL-3.0-or-later
package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/EduCloud-Ecosystem/cairn/internal/identity"
	"github.com/EduCloud-Ecosystem/cairn/internal/store"
	"github.com/EduCloud-Ecosystem/cairn/pkg/adapter"
)

func oauthCallbackRequest(target string, started *httptest.ResponseRecorder) *http.Request {
	r := httptest.NewRequest(http.MethodGet, target, nil)
	for _, cookie := range started.Result().Cookies() {
		r.AddCookie(cookie)
	}
	return r
}

type countedOAuthResolver struct {
	fakeResolver
	calls int
}

func (r *countedOAuthResolver) Resolve(ctx context.Context, code string) (string, string, error) {
	r.calls++
	return r.fakeResolver.Resolve(ctx, code)
}

// A callback URL from another browser must neither exchange its code nor consume
// the initiating browser's state. Exercise all three OAuth entry points.
func TestOAuthCallbackRequiresInitiatingBrowser(t *testing.T) {
	for _, starter := range []string{"/auth/login", "/student/login", "/assignments/a1/accept"} {
		for _, secure := range []bool{false, true} {
			t.Run(starter+map[bool]string{false: "/http", true: "/https"}[secure], func(t *testing.T) {
				srv, st := newAuthServer("alice")
				srv.cookieSecure = secure
				resolver := &countedOAuthResolver{fakeResolver: fakeResolver{username: "alice"}}
				srv.resolvers = map[adapter.Host]identity.Resolver{adapter.HostGitHub: resolver}
				ctx := context.Background()
				if err := st.CreateClassroom(ctx, &store.Classroom{ID: "c1", Host: adapter.HostGitHub, HostNamespace: "org"}); err != nil {
					t.Fatal(err)
				}
				if err := st.CreateAssignment(ctx, &store.Assignment{ID: "a1", ClassroomID: "c1", Slug: "hw1"}); err != nil {
					t.Fatal(err)
				}
				started := httptest.NewRecorder()
				srv.ServeHTTP(started, httptest.NewRequest(http.MethodGet, starter, nil))
				if started.Code != http.StatusFound {
					t.Fatalf("start = %d: %s", started.Code, started.Body.String())
				}
				location, err := url.Parse(started.Header().Get("Location"))
				if err != nil {
					t.Fatal(err)
				}
				state := location.Query().Get("state")
				cookies := started.Result().Cookies()
				if len(cookies) != 1 {
					t.Fatalf("start cookies = %d", len(cookies))
				}
				cookie := cookies[0]
				if cookie.Name != oauthBrowserCookie || len(cookie.Value) != 64 || !cookie.HttpOnly || cookie.Secure != secure || cookie.SameSite != http.SameSiteLaxMode || cookie.Path != "/auth/callback" || cookie.MaxAge != int(authFlowTTL.Seconds()) {
					t.Fatalf("incorrect OAuth cookie attributes: %+v", cookie)
				}
				target := "/auth/callback?code=x&state=" + state
				for _, value := range []string{"", "another-browser"} {
					r := httptest.NewRequest(http.MethodGet, target, nil)
					if value != "" {
						r.AddCookie(&http.Cookie{Name: oauthBrowserCookie, Value: value})
					}
					response := httptest.NewRecorder()
					srv.ServeHTTP(response, r)
					if response.Code != http.StatusBadRequest || resolver.calls != 0 {
						t.Fatalf("unbound callback = %d, resolver calls = %d", response.Code, resolver.calls)
					}
					if _, exists := srv.states[state]; !exists {
						t.Fatal("unbound callback consumed valid browser state")
					}
					if len(srv.sessions) != 0 {
						t.Fatal("unbound callback created a session")
					}
				}
				response := httptest.NewRecorder()
				srv.ServeHTTP(response, oauthCallbackRequest(target, started))
				if response.Code != http.StatusFound || resolver.calls != 1 {
					t.Fatalf("bound callback = %d, resolver calls = %d: %s", response.Code, resolver.calls, response.Body.String())
				}
				if _, exists := srv.states[state]; exists {
					t.Fatal("bound callback did not consume state")
				}
				cleared := false
				for _, c := range response.Result().Cookies() {
					if c.Name == oauthBrowserCookie && c.Value == "" && c.MaxAge == -1 {
						cleared = true
					}
				}
				if !cleared {
					t.Fatal("completed callback did not clear browser cookie")
				}
				replay := httptest.NewRecorder()
				srv.ServeHTTP(replay, oauthCallbackRequest(target, started))
				if replay.Code != http.StatusBadRequest || resolver.calls != 1 {
					t.Fatalf("replay = %d, resolver calls = %d", replay.Code, resolver.calls)
				}
			})
		}
	}
}
