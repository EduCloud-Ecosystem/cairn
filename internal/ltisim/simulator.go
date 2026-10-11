// SPDX-License-Identifier: AGPL-3.0-or-later
// Package ltisim is a synthetic loopback LMS, never a production identity provider.
package ltisim

import (
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"html/template"
	"math"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/EduCloud-Ecosystem/cairn/internal/id"
	"github.com/EduCloud-Ecosystem/cairn/internal/lti"
	"github.com/golang-jwt/jwt/v5"
)

const ClientID = "cairn-simulator"
const DeploymentID = "synthetic-deployment"
const Resource = "synthetic-assignment"
const Context = "synthetic-course"
const KeyID = "sim-platform-key"

type Score struct {
	UserID    string  `json:"userId"`
	Score     float64 `json:"scoreGiven"`
	Maximum   float64 `json:"scoreMaximum"`
	Timestamp string  `json:"timestamp"`
	Activity  string  `json:"activityProgress"`
	Grading   string  `json:"gradingProgress"`
}
type Simulator struct {
	Base, Tool string
	key        *rsa.PrivateKey
	toolKey    *rsa.PublicKey
	mu         sync.Mutex
	results    map[string]Score
	assertions map[string]time.Time
	tokens     map[string]time.Time
	mode       string
	posts      int
}

func New(base, tool string, key *rsa.PrivateKey, toolKey *rsa.PublicKey) (*Simulator, error) {
	for _, raw := range []string{base, tool} {
		u, e := url.Parse(raw)
		if e != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
			return nil, fmt.Errorf("simulator requires loopback HTTP origins")
		}
	}
	return &Simulator{Base: base, Tool: tool, key: key, toolKey: toolKey, results: map[string]Score{}, assertions: map[string]time.Time{}, tokens: map[string]time.Time{}}, nil
}
func (s *Simulator) LineItem() string { return s.Base + "/ags/lineitems/1" }
func (s *Simulator) Config(classroom, keyPath string) lti.Config {
	return lti.Config{Issuer: s.Base, ClientID: ClientID, DeploymentID: DeploymentID, AuthURL: s.Base + "/authorize", TokenURL: s.Base + "/token", TokenAudience: s.Base + "/token", JWKSURL: s.Base + "/jwks", ServiceOrigin: s.Base, BaseURL: s.Tool, KeyID: "sim-tool-key", PrivateKeyFile: keyPath, Classrooms: []string{classroom}, Simulation: true}
}
func (s *Simulator) Handler() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("GET /{$}", s.home)
	m.HandleFunc("GET /start", s.start)
	m.HandleFunc("GET /authorize", s.authorize)
	m.HandleFunc("POST /token", s.token)
	m.HandleFunc("GET /jwks", func(w http.ResponseWriter, r *http.Request) {
		write(w, map[string]any{"keys": []lti.JWK{lti.PublicJWK(&s.key.PublicKey, KeyID)}})
	})
	m.HandleFunc("GET /ags/lineitems/1", s.line)
	m.HandleFunc("GET /ags/lineitems/1/results", s.read)
	m.HandleFunc("POST /ags/lineitems/1/scores", s.score)
	m.HandleFunc("POST /simulate", s.control)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		m.ServeHTTP(w, r)
	})
}
func write(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}
func (s *Simulator) home(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, `<!doctype html><html lang="en"><title>Simulated Brightspace</title><meta name="viewport" content="width=device-width"><style>body{font:18px system-ui;max-width:800px;margin:3rem auto;padding:1rem}a,button{margin:.5rem;padding:.7rem;display:inline-block}pre{white-space:pre-wrap}</style><h1>Simulated Brightspace</h1><p>Synthetic local LMS. No university accounts or real grades.</p><p>First launch as instructor and connect an existing Cairn assignment. Then launch each learner and connect their Cairn account. Use separate browser profiles for each identity.</p><a href="/start?user=instructor">Launch instructor</a><a href="/start?user=student-a">Launch learner A</a><a href="/start?user=student-b">Launch learner B</a><h2>Delivery scenarios</h2><form method="post" action="/simulate"><button name="action" value="normal">Normal delivery</button><button name="action" value="drop">Lose the next response after saving</button><button name="action" value="reject">Reject the next score before saving</button><button name="action" value="edit">Instructor edits learner A to 63/100</button></form><h2>Stored synthetic scores</h2><pre>`)
	s.mu.Lock()
	b, _ := json.MarshalIndent(s.results, "", "  ")
	s.mu.Unlock()
	template.HTMLEscape(w, b)
	fmt.Fprint(w, "</pre></html>")
}
func (s *Simulator) start(w http.ResponseWriter, r *http.Request) {
	user := r.URL.Query().Get("user")
	if user != "instructor" && user != "student-a" && user != "student-b" {
		http.Error(w, "unknown synthetic role", 400)
		return
	}
	q := url.Values{"iss": {s.Base}, "client_id": {ClientID}, "lti_deployment_id": {DeploymentID}, "target_link_uri": {s.Tool + "/lti/launch"}, "login_hint": {user}, "lti_message_hint": {Resource}}
	http.Redirect(w, r, s.Tool+"/lti/login?"+q.Encode(), http.StatusFound)
}

// Token permits tests to mutate signed claims without weakening the tool verifier.
func (s *Simulator) Token(nonce, user string, mutate func(*lti.LaunchClaims)) (string, error) {
	now := time.Now()
	c := lti.LaunchClaims{RegisteredClaims: jwt.RegisteredClaims{Issuer: s.Base, Subject: user, Audience: jwt.ClaimStrings{ClientID}, IssuedAt: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(now.Add(5 * time.Minute))}, Nonce: nonce, Version: "1.3.0", MessageType: "LtiResourceLinkRequest", DeploymentID: DeploymentID, Target: s.Tool + "/lti/launch", Roles: []string{lti.Learner}}
	if user == "instructor" {
		c.Roles = []string{lti.Instructor}
	}
	c.Context.ID = Context
	c.Resource.ID = Resource
	c.Endpoint.LineItem = s.LineItem()
	c.Endpoint.Scope = []string{lti.AGS + "scope/score", lti.AGS + "scope/result.readonly", lti.AGS + "scope/lineitem.readonly"}
	if mutate != nil {
		mutate(&c)
	}
	t := jwt.NewWithClaims(jwt.SigningMethodRS256, c)
	t.Header["kid"] = KeyID
	return t.SignedString(s.key)
}
func (s *Simulator) authorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if q.Get("client_id") != ClientID || q.Get("redirect_uri") != s.Tool+"/lti/launch" || q.Get("response_type") != "id_token" || q.Get("response_mode") != "form_post" || q.Get("scope") != "openid" || q.Get("prompt") != "none" || len(q.Get("nonce")) < 16 || len(q.Get("state")) < 16 {
		http.Error(w, "invalid authorization request", 400)
		return
	}
	user := q.Get("login_hint")
	if user != "instructor" && user != "student-a" && user != "student-b" {
		http.Error(w, "unknown synthetic user", 400)
		return
	}
	token, err := s.Token(q.Get("nonce"), user, nil)
	if err != nil {
		http.Error(w, "signing failed", 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	t := template.Must(template.New("launch").Parse(`<!doctype html><title>Launching Cairn</title><form method="post" action="{{.URL}}"><input type="hidden" name="state" value="{{.State}}"><input type="hidden" name="id_token" value="{{.Token}}"><button>Continue to Cairn</button></form><script>document.forms[0].submit()</script>`))
	t.Execute(w, map[string]string{"URL": s.Tool + "/lti/launch", "State": q.Get("state"), "Token": token})
}
func (s *Simulator) token(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 32768)
	if r.ParseForm() != nil || r.PostForm.Get("grant_type") != "client_credentials" || r.PostForm.Get("client_assertion_type") != "urn:ietf:params:oauth:client-assertion-type:jwt-bearer" {
		http.Error(w, "invalid request", 400)
		return
	}
	var c jwt.RegisteredClaims
	t, e := jwt.ParseWithClaims(r.PostForm.Get("client_assertion"), &c, func(t *jwt.Token) (any, error) {
		if t.Header["kid"] != "sim-tool-key" {
			return nil, lti.ErrInvalid
		}
		return s.toolKey, nil
	}, jwt.WithValidMethods([]string{"RS256"}), jwt.WithIssuer(ClientID), jwt.WithSubject(ClientID), jwt.WithAudience(s.Base+"/token"), jwt.WithExpirationRequired(), jwt.WithIssuedAt())
	if e != nil || !t.Valid || c.ID == "" || c.IssuedAt == nil || c.ExpiresAt.Sub(c.IssuedAt.Time) > 5*time.Minute {
		http.Error(w, "invalid client assertion", 401)
		return
	}
	scope := r.PostForm.Get("scope")
	needed := []string{lti.AGS + "scope/score", lti.AGS + "scope/result.readonly", lti.AGS + "scope/lineitem.readonly"}
	if strings.Join(strings.Fields(scope), " ") != strings.Join(needed, " ") {
		http.Error(w, "invalid scope", 400)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for k, exp := range s.assertions {
		if !exp.After(now) {
			delete(s.assertions, k)
		}
	}
	for k, exp := range s.tokens {
		if !exp.After(now) {
			delete(s.tokens, k)
		}
	}
	if _, ok := s.assertions[c.ID]; ok {
		http.Error(w, "replayed assertion", 401)
		return
	}
	s.assertions[c.ID] = c.ExpiresAt.Time
	token := id.New()
	s.tokens[token] = now.Add(time.Minute)
	write(w, map[string]any{"access_token": token, "token_type": "Bearer", "expires_in": 60, "scope": scope})
}
func (s *Simulator) authorized(r *http.Request) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tokens[strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")].After(time.Now())
}
func (s *Simulator) line(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		http.Error(w, "unauthorized", 401)
		return
	}
	w.Header().Set("Content-Type", "application/vnd.ims.lis.v2.lineitem+json")
	json.NewEncoder(w).Encode(map[string]any{"id": s.LineItem(), "resourceLinkId": Resource, "scoreMaximum": 100, "label": "Synthetic assignment"})
}
func (s *Simulator) read(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		http.Error(w, "unauthorized", 401)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []any{}
	user := r.URL.Query().Get("user_id")
	if v, ok := s.results[user]; ok {
		out = append(out, map[string]any{"id": s.LineItem() + "/results/" + user, "userId": user, "scoreOf": s.LineItem(), "resultScore": v.Score / v.Maximum, "resultMaximum": 1})
	}
	w.Header().Set("Content-Type", "application/vnd.ims.lis.v2.resultcontainer+json")
	json.NewEncoder(w).Encode(out)
}
func (s *Simulator) score(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		http.Error(w, "unauthorized", 401)
		return
	}
	if r.Header.Get("Content-Type") != "application/vnd.ims.lis.v1.score+json" {
		http.Error(w, "wrong media type", 415)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	var v Score
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if d.Decode(&v) != nil || v.Maximum <= 0 || v.Score < 0 || v.Score > v.Maximum || math.IsNaN(v.Score) || v.Activity != "Completed" || v.Grading != "FullyGraded" || (v.UserID != "student-a" && v.UserID != "student-b") {
		http.Error(w, "invalid score", 400)
		return
	}
	stamp, e := time.Parse(time.RFC3339Nano, v.Timestamp)
	if e != nil {
		http.Error(w, "invalid timestamp", 400)
		return
	}
	s.mu.Lock()
	mode := s.mode
	s.mode = ""
	s.posts++
	if mode != "reject" {
		previous, exists := s.results[v.UserID]
		old, _ := time.Parse(time.RFC3339Nano, previous.Timestamp)
		if !exists || !stamp.Before(old) {
			s.results[v.UserID] = v
		}
	}
	s.mu.Unlock()
	if mode == "reject" {
		http.Error(w, "synthetic unavailable before save", 503)
		return
	}
	if mode == "drop" {
		if h, ok := w.(http.Hijacker); ok {
			conn, _, err := h.Hijack()
			if err == nil {
				conn.Close()
				return
			}
		}
		http.Error(w, "synthetic response loss after save", 503)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (s *Simulator) control(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1024)
	if r.ParseForm() != nil {
		http.Error(w, "bad form", 400)
		return
	}
	switch action := r.PostForm.Get("action"); action {
	case "normal", "drop", "reject":
		s.SetMode(action)
	case "edit":
		s.Edit("student-a", 63, 100)
	default:
		http.Error(w, "unknown scenario", 400)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
func (s *Simulator) SetMode(mode string) { s.mu.Lock(); defer s.mu.Unlock(); s.mode = mode }
func (s *Simulator) Edit(user string, score, max float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.results[user] = Score{UserID: user, Score: score, Maximum: max, Timestamp: time.Now().UTC().Format(time.RFC3339Nano)}
}
func (s *Simulator) Posts() int { s.mu.Lock(); defer s.mu.Unlock(); return s.posts }
