// SPDX-License-Identifier: AGPL-3.0-or-later
package api

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/EduCloud-Ecosystem/cairn/internal/lti"
	"github.com/EduCloud-Ecosystem/cairn/internal/store"
	"github.com/EduCloud-Ecosystem/cairn/internal/store/memory"
	"github.com/EduCloud-Ecosystem/cairn/pkg/adapter"
	"github.com/golang-jwt/jwt/v5"
)

func ltiTestServer(t *testing.T) (*Server, *rsa.PrivateKey) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	platform := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"keys": []lti.JWK{lti.PublicJWK(&key.PublicKey, "test")}})
	}))
	t.Cleanup(platform.Close)
	c, err := lti.New(lti.Config{Issuer: platform.URL, ClientID: "client", DeploymentID: "deploy", AuthURL: platform.URL + "/authorize", TokenURL: platform.URL + "/token", TokenAudience: platform.URL + "/token", JWKSURL: platform.URL + "/jwks", ServiceOrigin: platform.URL, BaseURL: "http://127.0.0.1:8090", KeyID: "test", Classrooms: []string{"c"}, Simulation: true}, key)
	if err != nil {
		t.Fatal(err)
	}
	st := memory.New()
	s := New(Options{Store: st, AuthEnabled: true, LTIClient: c})
	ctx := context.Background()
	if err := st.CreateClassroom(ctx, &store.Classroom{ID: "c", CreatedBy: "owner", Host: adapter.HostGitHub}); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateAssignment(ctx, &store.Assignment{ID: "a", ClassroomID: "c", Title: "Assignment", Type: store.AssignmentIndividual}); err != nil {
		t.Fatal(err)
	}
	s.sessions["owner"] = session{userID: "owner", username: "teacher", isOperator: true, created: time.Now()}
	s.sessions["other"] = session{userID: "other", username: "other", isOperator: true, created: time.Now()}
	s.sessions["learner"] = session{username: "student", host: adapter.HostGitHub, created: time.Now()}
	return s, key
}
func ltiRequest(s *Server, method, path, body, sessionID, pending, origin, content string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if sessionID != "" {
		r.AddCookie(&http.Cookie{Name: sessionCookie, Value: sessionID})
	}
	if pending != "" {
		r.AddCookie(&http.Cookie{Name: ltiPendingCookie, Value: pending})
	}
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	if content != "" {
		r.Header.Set("Content-Type", content)
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}
func TestLTILoginLaunchBrowserBindingAndReplay(t *testing.T) {
	s, key := ltiTestServer(t)
	cfg := s.ltiService.Client.Config
	params := url.Values{"iss": {cfg.Issuer}, "client_id": {cfg.ClientID}, "target_link_uri": {cfg.BaseURL + "/lti/launch"}, "login_hint": {"learner"}}
	w := ltiRequest(s, "GET", "/lti/login?"+params.Encode(), "", "", "", "", "")
	if w.Code != 302 {
		t.Fatal(w.Code, w.Body.String())
	}
	redirect, _ := url.Parse(w.Header().Get("Location"))
	q := redirect.Query()
	cookie := w.Result().Cookies()[0]
	if !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("unsafe simulation cookie: %+v", cookie)
	}
	now := time.Now()
	claims := lti.LaunchClaims{RegisteredClaims: jwt.RegisteredClaims{Issuer: cfg.Issuer, Subject: "opaque-learner", Audience: jwt.ClaimStrings{cfg.ClientID}, IssuedAt: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(now.Add(time.Minute))}, Nonce: q.Get("nonce"), Version: "1.3.0", MessageType: "LtiResourceLinkRequest", DeploymentID: cfg.DeploymentID, Target: cfg.BaseURL + "/lti/launch", Roles: []string{lti.Learner}}
	claims.Context.ID = "course"
	claims.Resource.ID = "resource"
	claims.Endpoint.LineItem = cfg.ServiceOrigin + "/lineitem"
	claims.Endpoint.Scope = []string{lti.AGS + "scope/score", lti.AGS + "scope/result.readonly", lti.AGS + "scope/lineitem.readonly"}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = "test"
	raw, err := token.SignedString(key)
	if err != nil {
		t.Fatal(err)
	}
	form := url.Values{"state": {q.Get("state")}, "id_token": {raw}}
	req := httptest.NewRequest("POST", "/lti/launch", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	w = httptest.NewRecorder()
	s.ServeHTTP(w, req)
	if w.Code != 303 || w.Header().Get("Location") != "/lti/connect" {
		t.Fatal(w.Code, w.Body.String())
	}
	if len(s.sessions) != 3 {
		t.Fatal("launch created Cairn session")
	}
	serialized, _ := json.Marshal(s.ltiState.pending)
	if strings.Contains(string(serialized), "opaque-learner") {
		t.Fatal("subject serialized")
	}
	replay := httptest.NewRequest("POST", "/lti/launch", strings.NewReader(form.Encode()))
	replay.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	replay.AddCookie(cookie)
	w = httptest.NewRecorder()
	s.ServeHTTP(w, replay)
	if w.Code != 400 {
		t.Fatal("replay accepted")
	}
	s.ltiState.flows["unbound"] = ltiFlow{"nonce", "correct", time.Now()}
	w = ltiRequest(s, "POST", "/lti/launch", "state=unbound&id_token=x", "", "", "", "application/x-www-form-urlencoded")
	if w.Code != 400 {
		t.Fatal("unbound browser accepted")
	}
}
func TestLTIConnectRequiresCairnAuthorizationAndOrigin(t *testing.T) {
	s, _ := ltiTestServer(t)
	launch := lti.Launch{Subject: "subject", Context: "context", Resource: "resource", LineItem: s.ltiService.Client.Config.ServiceOrigin + "/lineitem", Role: lti.Instructor, Registration: s.ltiService.Client.Registration}
	s.ltiState.pending["pending"] = ltiPending{launch, time.Now()}
	for _, tc := range []struct {
		name, session, origin, content string
		want                           int
	}{
		{"no session", "", s.ltiService.Client.Config.BaseURL, "application/json", 401},
		{"student cannot map", "learner", s.ltiService.Client.Config.BaseURL, "application/json", 401},
		{"cross site", "owner", "https://evil.example", "application/json", 403},
		{"missing origin", "owner", "", "application/json", 403},
		{"simple form", "owner", s.ltiService.Client.Config.BaseURL, "text/plain", 415},
		{"other owner", "other", s.ltiService.Client.Config.BaseURL, "application/json", 404},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := ltiRequest(s, "POST", "/lti/connect", `{"assignment_id":"a"}`, tc.session, "pending", tc.origin, tc.content)
			if w.Code != tc.want {
				t.Fatalf("got %d: %s", w.Code, w.Body)
			}
		})
	}
	w := ltiRequest(s, "GET", "/lti/connect", "", "other", "pending", "", "")
	if strings.Contains(w.Body.String(), `value="a"`) {
		t.Fatal("other owner's assignment exposed")
	}
	w = ltiRequest(s, "POST", "/lti/connect", `{"assignment_id":"a"}`, "owner", "pending", s.ltiService.Client.Config.BaseURL, "application/json")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = ltiRequest(s, "POST", "/lti/connect", `{"assignment_id":"a"}`, "owner", "pending", s.ltiService.Client.Config.BaseURL, "application/json")
	if w.Code != 400 {
		t.Fatal("pending action replay accepted")
	}
}
func TestLTIDisabledWithoutOperatorAuth(t *testing.T) {
	s, _ := ltiTestServer(t)
	insecure := New(Options{Store: s.store, LTIClient: s.ltiService.Client})
	w := ltiRequest(insecure, "GET", "/lti/login", "", "", "", "", "")
	if w.Code != 404 {
		t.Fatal(w.Code)
	}
	for _, path := range []string{"/submissions/missing/lti", "/lti-capabilities"} {
		w = ltiRequest(s, "GET", path, "", "learner", "", "", "")
		if w.Code != 401 {
			t.Fatal(path, w.Code)
		}
	}
}
func TestLTILoginRejectsDestinationAndExpiredState(t *testing.T) {
	s, _ := ltiTestServer(t)
	cfg := s.ltiService.Client.Config
	q := url.Values{"iss": {cfg.Issuer}, "target_link_uri": {"https://evil.example"}, "login_hint": {"student"}}
	w := ltiRequest(s, "GET", "/lti/login?"+q.Encode(), "", "", "", "", "")
	if w.Code != 400 {
		t.Fatal("open redirect accepted")
	}
	s.ltiState.pending["old"] = ltiPending{created: time.Now().Add(-2 * ltiTTL)}
	w = ltiRequest(s, "GET", "/lti/connect", "", "owner", "old", "", "")
	if w.Code != 400 {
		t.Fatal("expired pending accepted")
	}
}

func TestLTIProductionCookieAndFlowCapacity(t *testing.T) {
	s, key := ltiTestServer(t)
	cfg := s.ltiService.Client.Config
	cfg.Simulation = false
	cfg.Issuer = "https://platform.example"
	cfg.AuthURL = cfg.Issuer + "/auth"
	cfg.TokenURL = cfg.Issuer + "/token"
	cfg.JWKSURL = cfg.Issuer + "/jwks"
	cfg.ServiceOrigin = cfg.Issuer
	cfg.BaseURL = "https://cairn.example"
	c, err := lti.New(cfg, key)
	if err != nil {
		t.Fatal(err)
	}
	s = New(Options{Store: s.store, LTIClient: c, AuthEnabled: true, CookieSecure: true})
	q := url.Values{"iss": {cfg.Issuer}, "target_link_uri": {cfg.BaseURL + "/lti/launch"}, "login_hint": {"student"}}
	w := ltiRequest(s, "GET", "/lti/login?"+q.Encode(), "", "", "", "", "")
	if w.Code != 302 {
		t.Fatal(w.Code)
	}
	cookie := w.Result().Cookies()[0]
	if !cookie.Secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteNoneMode {
		t.Fatalf("unsafe cross-site form_post cookie: %+v", cookie)
	}
	for i := 0; i < 1024; i++ {
		s.ltiState.flows[string(rune(i+1))] = ltiFlow{created: time.Now()}
	}
	w = ltiRequest(s, "GET", "/lti/login?"+q.Encode(), "", "", "", "", "")
	if w.Code != 429 {
		t.Fatal("flow capacity not enforced", w.Code)
	}
}

func TestLTILearnerCannotBindInstructorSessionOrSelectAssignment(t *testing.T) {
	s, _ := ltiTestServer(t)
	s.ltiState.pending["learner-launch"] = ltiPending{launch: lti.Launch{Role: lti.Learner}, created: time.Now()}
	w := ltiRequest(s, "POST", "/lti/connect", `{}`, "owner", "learner-launch", s.ltiService.Client.Config.BaseURL, "application/json")
	if w.Code != 401 {
		t.Fatal("operator session bound to learner", w.Code)
	}
	w = ltiRequest(s, "POST", "/lti/connect", `{"assignment_id":"a"}`, "learner", "learner-launch", s.ltiService.Client.Config.BaseURL, "application/json")
	if w.Code != 400 {
		t.Fatal("learner selected mapping", w.Code)
	}
}
