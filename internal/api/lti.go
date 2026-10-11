// SPDX-License-Identifier: AGPL-3.0-or-later
package api

import (
	"errors"
	"html/template"
	"net/http"
	"sync"
	"time"

	"github.com/EduCloud-Ecosystem/cairn/internal/lti"
	"github.com/EduCloud-Ecosystem/cairn/internal/store"
)

const ltiTTL = 10 * time.Minute
const ltiBrowserCookie = "cairn_lti_browser"
const ltiPendingCookie = "cairn_lti_pending"

type ltiFlow struct {
	nonce, browser string
	created        time.Time
}
type ltiPending struct {
	launch  lti.Launch
	created time.Time
}
type ltiState struct {
	sync.Mutex
	flows   map[string]ltiFlow
	pending map[string]ltiPending
}

func (s *Server) ltiRoutes(protect func(http.HandlerFunc) http.HandlerFunc) {
	s.mux.HandleFunc("GET /lti-capabilities", protect(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]bool{"enabled": s.ltiService != nil})
	}))
	if s.ltiService == nil {
		return
	}
	s.mux.HandleFunc("GET /lti/jwks", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, s.ltiService.Client.JWKS()) })
	s.mux.HandleFunc("GET /lti/login", s.handleLTILogin)
	s.mux.HandleFunc("POST /lti/login", s.handleLTILogin)
	s.mux.HandleFunc("POST /lti/launch", s.handleLTILaunch)
	s.mux.HandleFunc("GET /lti/connect", s.handleLTIConnect)
	s.mux.HandleFunc("POST /lti/connect", s.handleLTIConnectAction)
	s.mux.HandleFunc("GET /submissions/{id}/lti", protect(s.handleLTIStatus))
	s.mux.HandleFunc("POST /submissions/{id}/lti", protect(s.handleLTIDeliver))
}
func (s *Server) pruneLTILocked() {
	for k, v := range s.ltiState.flows {
		if time.Since(v.created) > ltiTTL {
			delete(s.ltiState.flows, k)
		}
	}
	for k, v := range s.ltiState.pending {
		if time.Since(v.created) > ltiTTL {
			delete(s.ltiState.pending, k)
		}
	}
}
func (s *Server) ltiCookie(w http.ResponseWriter, name, value string, launch bool) {
	mode := http.SameSiteLaxMode
	if launch && !s.ltiService.Client.Config.Simulation {
		mode = http.SameSiteNoneMode
	}
	http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: "/lti", HttpOnly: true, Secure: !s.ltiService.Client.Config.Simulation, SameSite: mode, MaxAge: int(ltiTTL.Seconds())})
}
func (s *Server) handleLTILogin(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 16384)
	if r.ParseForm() != nil {
		httpError(w, 400, "invalid login")
		return
	}
	cfg := s.ltiService.Client.Config
	q := r.Form
	if q.Get("iss") != cfg.Issuer || (q.Get("client_id") != "" && q.Get("client_id") != cfg.ClientID) || (q.Get("lti_deployment_id") != "" && q.Get("lti_deployment_id") != cfg.DeploymentID) || q.Get("target_link_uri") != cfg.BaseURL+"/lti/launch" || len(q.Get("login_hint")) == 0 || len(q.Get("login_hint")) > 2048 || len(q.Get("lti_message_hint")) > 2048 {
		httpError(w, 400, "invalid login")
		return
	}
	state, nonce, browser := newSessionToken(), newSessionToken(), newSessionToken()
	s.ltiState.Lock()
	s.pruneLTILocked()
	if len(s.ltiState.flows) >= 1024 {
		s.ltiState.Unlock()
		httpError(w, 429, "too many pending launches")
		return
	}
	s.ltiState.flows[state] = ltiFlow{nonce, browser, time.Now()}
	s.ltiState.Unlock()
	s.ltiCookie(w, ltiBrowserCookie, browser, true)
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, s.ltiService.Client.Authorize(state, nonce, q.Get("login_hint"), q.Get("lti_message_hint")), http.StatusFound)
}
func (s *Server) handleLTILaunch(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 65536)
	if r.ParseForm() != nil {
		httpError(w, 400, "invalid launch")
		return
	}
	state := r.PostForm.Get("state")
	browser, err := r.Cookie(ltiBrowserCookie)
	s.ltiState.Lock()
	s.pruneLTILocked()
	flow, ok := s.ltiState.flows[state]
	delete(s.ltiState.flows, state)
	s.ltiState.Unlock()
	if !ok || err != nil || flow.browser != browser.Value {
		httpError(w, 400, "unknown, expired or unbound launch")
		return
	}
	launch, err := s.ltiService.Client.Verify(r.Context(), r.PostForm.Get("id_token"), flow.nonce)
	if err != nil {
		httpError(w, 400, "launch verification failed")
		return
	}
	token := newSessionToken()
	s.ltiState.Lock()
	s.pruneLTILocked()
	if len(s.ltiState.pending) >= 1024 {
		s.ltiState.Unlock()
		httpError(w, 429, "too many pending connections")
		return
	}
	s.ltiState.pending[token] = ltiPending{*launch, time.Now()}
	s.ltiState.Unlock()
	s.ltiCookie(w, ltiPendingCookie, token, false)
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, "/lti/connect", http.StatusSeeOther)
}
func (s *Server) pendingLaunch(r *http.Request) (lti.Launch, bool) {
	c, err := r.Cookie(ltiPendingCookie)
	if err != nil {
		return lti.Launch{}, false
	}
	s.ltiState.Lock()
	defer s.ltiState.Unlock()
	s.pruneLTILocked()
	p, ok := s.ltiState.pending[c.Value]
	return p.launch, ok
}
func (s *Server) ltiMutation(w http.ResponseWriter, r *http.Request, dst any) bool {
	// Require the configured public origin, not a client-controlled Host header.
	if r.Header.Get("Origin") != s.ltiService.Client.Config.BaseURL {
		httpError(w, 403, "same-origin request required")
		return false
	}
	return assessmentJSON(w, r, dst)
}
func ltiHTTPError(w http.ResponseWriter, err error) {
	status := 409
	if errors.Is(err, store.ErrNotFound) || errors.Is(err, lti.ErrForbidden) {
		status = 404
	}
	httpError(w, status, "Brightspace connection or grade is not ready; refresh and review the connection and published grade")
}
func (s *Server) handleLTIConnectAction(w http.ResponseWriter, r *http.Request) {
	launch, ok := s.pendingLaunch(r)
	if !ok {
		httpError(w, 400, "launch expired; reopen this resource in Brightspace")
		return
	}
	var in struct {
		AssignmentID string `json:"assignment_id"`
	}
	if !s.ltiMutation(w, r, &in) {
		return
	}
	var err error
	if launch.Role == lti.Instructor {
		op, ok := s.operatorFromCookie(r)
		if !ok {
			httpError(w, 401, "Cairn instructor login required")
			return
		}
		err = s.ltiService.MapAssignment(r.Context(), in.AssignmentID, op.ID, launch)
	} else {
		sess, ok := s.sessionFromCookie(r)
		if !ok || sess.isOperator {
			httpError(w, 401, "Cairn learner login required")
			return
		}
		if in.AssignmentID != "" {
			httpError(w, 400, "learners cannot select assignment mappings")
			return
		}
		err = s.ltiService.Bind(r.Context(), launch, sess.host, sess.username)
	}
	if err != nil {
		ltiHTTPError(w, err)
		return
	}
	c, _ := r.Cookie(ltiPendingCookie)
	s.ltiState.Lock()
	delete(s.ltiState.pending, c.Value)
	s.ltiState.Unlock()
	writeJSON(w, 200, map[string]string{"status": "connected"})
}
func (s *Server) handleLTIStatus(w http.ResponseWriter, r *http.Request) {
	op, _ := operatorFrom(r.Context())
	delivery, err := s.ltiService.Status(r.Context(), r.PathValue("id"), op.ID)
	if err != nil {
		ltiHTTPError(w, err)
		return
	}
	grade, readyErr := s.ltiService.EligibleGrade(r.Context(), r.PathValue("id"), op.ID)
	var latest any
	if grade != nil {
		latest = map[string]any{"id": grade.ID, "score": grade.Score, "maximum": grade.MaxScore}
	}
	writeJSON(w, 200, map[string]any{"delivery": delivery, "ready": readyErr == nil, "grade": latest})
}
func (s *Server) handleLTIDeliver(w http.ResponseWriter, r *http.Request) {
	var in struct {
		GradeID string `json:"grade_id"`
	}
	if !s.ltiMutation(w, r, &in) {
		return
	}
	op, _ := operatorFrom(r.Context())
	delivery, err := s.ltiService.Deliver(r.Context(), r.PathValue("id"), op.ID, in.GradeID)
	if err != nil {
		ltiHTTPError(w, err)
		return
	}
	writeJSON(w, 200, delivery)
}
func (s *Server) handleLTIConnect(w http.ResponseWriter, r *http.Request) {
	launch, ok := s.pendingLaunch(r)
	if !ok {
		httpError(w, 400, "launch expired; reopen this resource in Brightspace")
		return
	}
	data := struct {
		Instructor, Authenticated bool
		Username                  string
		Assignments               []*store.Assignment
		Assignment                *store.Assignment
		Nonce                     string
	}{Instructor: launch.Role == lti.Instructor, Nonce: newSessionToken()}
	if data.Instructor {
		if op, ok := s.operatorFromCookie(r); ok {
			data.Authenticated = true
			data.Username = op.HostUsername
			classes, err := s.store.ListClassrooms(r.Context())
			if err != nil {
				httpError(w, 500, "could not load classrooms")
				return
			}
			for _, c := range classes {
				if c.CreatedBy == op.ID && s.ltiService.Client.AllowedClassroom(c.ID) {
					as, err := s.store.ListAssignmentsByClassroom(r.Context(), c.ID)
					if err != nil {
						httpError(w, 500, "could not load assignments")
						return
					}
					data.Assignments = append(data.Assignments, as...)
				}
			}
		}
	} else if sess, ok := s.sessionFromCookie(r); ok && !sess.isOperator {
		data.Authenticated = true
		data.Username = sess.username
		data.Assignment, _ = s.ltiService.AssignmentForLaunch(r.Context(), launch)
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'nonce-"+data.Nonce+"'; style-src 'unsafe-inline'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = ltiConnectTemplate.Execute(w, data)
}

var ltiConnectTemplate = template.Must(template.New("lti").Parse(`<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width"><title>Connect Brightspace · Cairn</title><style>body{font:18px system-ui;max-width:42rem;margin:3rem auto;padding:1rem}button,select{font:inherit;margin:.5rem 0;padding:.5rem}</style><main><h1>Connect Brightspace</h1>{{if not .Authenticated}}<p>Sign in to your existing Cairn {{if .Instructor}}instructor{{else}}learner{{end}} account in another tab, then reload this page. This Brightspace launch does not create a Cairn account.</p><a target="_blank" rel="noopener" href="{{if .Instructor}}/auth/login{{else}}/student/login{{end}}">Sign in to Cairn</a> <a href="/lti/connect">Reload connection</a>{{else}}<p>Signed in as <strong>{{.Username}}</strong>.</p>{{if .Instructor}}<p>Choose the assignment owned by you that this Brightspace gradebook item represents. The connection cannot be reassigned.</p><label for="assignment">Cairn assignment</label><select id="assignment"><option value="">Choose an assignment</option>{{range .Assignments}}<option value="{{.ID}}">{{.Title}} ({{.Slug}})</option>{{end}}</select><br><button id="connect">Connect assignment</button>{{else}}{{if .Assignment}}<p>Connect your Cairn account to <strong>{{.Assignment.Title}}</strong> for instructor-approved numeric grade delivery. Feedback stays in Cairn.</p><button id="connect">Connect my account</button>{{else}}<p>Your instructor must connect this Brightspace resource to a Cairn assignment first.</p>{{end}}{{end}}{{end}}<p id="status" role="status"></p><a href="/">Cairn</a></main><script nonce="{{.Nonce}}">const button=document.getElementById('connect');if(button)button.onclick=async()=>{button.disabled=true;const select=document.getElementById('assignment');try{const response=await fetch('/lti/connect',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({assignment_id:select?select.value:''})});const data=await response.json();if(!response.ok)throw Error(data.error||'Connection failed');document.getElementById('status').textContent='Connected. You can close this page.';}catch(e){document.getElementById('status').textContent=e.message;button.disabled=false;}};</script></html>`))
