// SPDX-License-Identifier: AGPL-3.0-or-later
package lti

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/EduCloud-Ecosystem/cairn/internal/store"
	"github.com/EduCloud-Ecosystem/cairn/internal/store/memory"
	"github.com/EduCloud-Ecosystem/cairn/internal/store/sqlite"
	"github.com/EduCloud-Ecosystem/cairn/pkg/adapter"
)

type gradePlatform struct {
	mu         sync.Mutex
	url        string
	ratio      *float64
	posts      int
	timestamps []string
	failRead   bool
	drop       bool
	authFail   bool
	reject     bool
	ignore     bool
	pause      chan struct{}
	entered    chan struct{}
}

func (p *gradePlatform) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	defer p.mu.Unlock()
	switch r.URL.Path {
	case "/token":
		if p.authFail {
			http.Error(w, "private auth detail", 401)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"access_token": "test-token", "token_type": "Bearer"})
	case "/line":
		json.NewEncoder(w).Encode(map[string]any{"id": p.url + "/line", "resourceLinkId": "resource", "scoreMaximum": 100})
	case "/line/results":
		if p.failRead {
			http.Error(w, "private server detail", 503)
			return
		}
		if r.URL.Query().Get("user_id") != "opaque-subject" {
			http.Error(w, "wrong subject", 400)
			return
		}
		results := []Result{}
		if p.ratio != nil {
			max := 100.0
			score := *p.ratio * 100
			results = append(results, Result{UserID: "opaque-subject", Score: &score, Maximum: &max})
		}
		json.NewEncoder(w).Encode(results)
	case "/line/scores":
		var body struct {
			Score     float64 `json:"scoreGiven"`
			Maximum   float64 `json:"scoreMaximum"`
			Timestamp string  `json:"timestamp"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		p.posts++
		p.timestamps = append(p.timestamps, body.Timestamp)
		if p.pause != nil {
			close(p.entered)
			<-p.pause
			p.pause = nil
		}
		if p.reject {
			http.Error(w, "private score detail", 503)
			return
		}
		if !p.ignore {
			ratio := body.Score / body.Maximum
			p.ratio = &ratio
		}
		if p.drop {
			p.failRead = true
			c, _, _ := w.(http.Hijacker).Hijack()
			c.Close()
			return
		}
		w.WriteHeader(204)
	default:
		http.NotFound(w, r)
	}
}
func fixture(t *testing.T, st store.Store) (*Service, *gradePlatform, Launch) {
	t.Helper()
	ctx := context.Background()
	p := &gradePlatform{}
	server := httptest.NewServer(p)
	p.url = server.URL
	t.Cleanup(server.Close)
	k, e := rsa.GenerateKey(rand.Reader, 2048)
	if e != nil {
		t.Fatal(e)
	}
	c, e := New(Config{Issuer: server.URL, ClientID: "client", DeploymentID: "deployment", AuthURL: server.URL + "/auth", TokenURL: server.URL + "/token", TokenAudience: server.URL + "/token", JWKSURL: server.URL + "/keys", ServiceOrigin: server.URL, BaseURL: server.URL, KeyID: "key", Classrooms: []string{"c"}, Simulation: true}, k)
	if e != nil {
		t.Fatal(e)
	}
	for _, e := range []error{st.CreateUser(ctx, &store.User{ID: "owner", Host: adapter.HostGitHub, HostUserID: "1", HostUsername: "owner"}), st.CreateClassroom(ctx, &store.Classroom{ID: "c", CreatedBy: "owner", Host: adapter.HostGitHub}), st.CreateAssignment(ctx, &store.Assignment{ID: "a", ClassroomID: "c", Type: store.AssignmentIndividual}), st.CreateRosterEntry(ctx, &store.RosterEntry{ID: "r", ClassroomID: "c", Host: adapter.HostGitHub, HostUsername: "student", Status: store.RosterActive}), st.CreateSubmission(ctx, &store.Submission{ID: "s", AssignmentID: "a", RosterEntryID: "r", LatestCommit: "commit", Status: "active"}), st.PutAssessmentRubric(ctx, &store.AssessmentRubric{AssignmentID: "a", Digest: "rubric", Document: []byte(`{}`)})} {
		if e != nil {
			t.Fatal(e)
		}
	}
	approve(t, st, "g", 8)
	s := &Service{Store: st, Client: c}
	l := Launch{Subject: "instructor", Context: "context", Resource: "resource", LineItem: server.URL + "/line", Role: Instructor, Registration: c.Registration, LineItemScope: AGS + "scope/lineitem.readonly"}
	if e = s.MapAssignment(ctx, "a", "owner", l); e != nil {
		t.Fatal(e)
	}
	l.Role = Learner
	l.Subject = "opaque-subject"
	if e = s.Bind(ctx, l, adapter.HostGitHub, "student"); e != nil {
		t.Fatal(e)
	}
	return s, p, l
}
func approve(t *testing.T, st store.Store, gid string, score float64) {
	t.Helper()
	ctx := context.Background()
	a := &store.AssessmentRecord{ID: "assessment-" + gid, SubmissionID: "s", Status: "collected", Revision: "commit", RubricDigest: "rubric", Document: []byte(`{}`)}
	if e := st.CreateAssessment(ctx, a); e != nil {
		t.Fatal(e)
	}
	a.Status = "pending"
	if e := st.TransitionAssessment(ctx, a, "collected", nil); e != nil {
		t.Fatal(e)
	}
	a.Status = "approved"
	if e := st.TransitionAssessment(ctx, a, "pending", &store.Grade{ID: gid, SubmissionID: "s", Score: score, MaxScore: 10, GradedAt: time.Now().UTC()}); e != nil {
		t.Fatal(e)
	}
}
func TestDeliveryVerifiedAndIdempotent(t *testing.T) {
	s, p, _ := fixture(t, memory.New())
	ctx := context.Background()
	d, e := s.Deliver(ctx, "s", "owner", "g")
	if e != nil || d.Status != "verified" || d.Attempts != 1 {
		t.Fatalf("%+v %v", d, e)
	}
	d, e = s.Deliver(ctx, "s", "owner", "g")
	if e != nil || p.posts != 1 {
		t.Fatalf("duplicate sent: %+v %v %d", d, e, p.posts)
	}
	approve(t, s.Store, "g2", 9)
	d, e = s.Deliver(ctx, "s", "owner", "g2")
	if e != nil || d.Status != "verified" || p.posts != 2 {
		t.Fatalf("replacement: %+v %v", d, e)
	}
}
func TestDeliveryDroppedResponseSurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trial.db")
	st, e := sqlite.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	s, p, _ := fixture(t, st)
	ctx := context.Background()
	p.drop = true
	d, e := s.Deliver(ctx, "s", "owner", "g")
	if !errors.Is(e, ErrRemote) || d.Status != "uncertain" {
		t.Fatalf("%+v %v", d, e)
	}
	timestamp := d.Timestamp
	st.Close()
	next, e := sqlite.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer next.Close()
	s = &Service{Store: next, Client: s.Client}
	p.mu.Lock()
	p.failRead = false
	p.drop = false
	p.mu.Unlock()
	d, e = s.Deliver(ctx, "s", "owner", "g")
	if e != nil || d.Status != "verified" || p.posts != 1 || d.Timestamp != timestamp {
		t.Fatalf("restart resent or unverified: %+v %v posts=%d", d, e, p.posts)
	}
}
func TestDeliveryProtectsLMSGrades(t *testing.T) {
	for _, prior := range []bool{false, true} {
		t.Run(map[bool]string{false: "existing", true: "edited"}[prior], func(t *testing.T) {
			s, p, _ := fixture(t, memory.New())
			ctx := context.Background()
			if prior {
				if _, e := s.Deliver(ctx, "s", "owner", "g"); e != nil {
					t.Fatal(e)
				}
				approve(t, s.Store, "g2", 9)
			}
			p.mu.Lock()
			v := 0.7
			p.ratio = &v
			before := p.posts
			p.mu.Unlock()
			gid := "g"
			if prior {
				gid = "g2"
			}
			d, e := s.Deliver(ctx, "s", "owner", gid)
			if !errors.Is(e, ErrConflict) || d.Status != "conflict" || p.posts != before {
				t.Fatalf("%+v %v posts=%d", d, e, p.posts)
			}
		})
	}
}
func TestDeliveryGuards(t *testing.T) {
	for _, which := range []string{"other-owner", "removed", "stale", "activity", "unapproved", "rubric", "grade-id", "allowlist"} {
		t.Run(which, func(t *testing.T) {
			s, p, _ := fixture(t, memory.New())
			ctx := context.Background()
			owner, gid := "owner", "g"
			switch which {
			case "other-owner":
				owner = "another"
			case "removed":
				r, _ := s.Store.GetRosterEntry(ctx, "r")
				r.Status = store.RosterRemoved
				s.Store.UpdateRosterEntry(ctx, r)
			case "stale":
				sub, _ := s.Store.GetSubmission(ctx, "s")
				sub.LatestCommit = "new"
				s.Store.UpdateSubmission(ctx, sub)
			case "activity":
				sub, _ := s.Store.GetSubmission(ctx, "s")
				now := time.Now().UTC()
				sub.LastActivityAt = &now
				s.Store.UpdateSubmission(ctx, sub)
			case "unapproved":
				s.Store.CreateGrade(ctx, &store.Grade{ID: "unreviewed", SubmissionID: "s", Score: 0, MaxScore: 10, GradedAt: time.Now().Add(time.Second)})
				gid = "unreviewed"
			case "rubric":
				s.Store.PutAssessmentRubric(ctx, &store.AssessmentRubric{AssignmentID: "a", Digest: "new", Document: []byte(`{}`)})
			case "grade-id":
				gid = "obsolete"
			case "allowlist":
				s.Client.Config.Classrooms = nil
			}
			if _, e := s.Deliver(ctx, "s", owner, gid); e == nil || p.posts != 0 {
				t.Fatalf("guard %s err=%v posts=%d", which, e, p.posts)
			}
		})
	}
}
func TestMappingBindingImmutability(t *testing.T) {
	s, _, l := fixture(t, memory.New())
	ctx := context.Background()
	if e := s.Bind(ctx, l, adapter.HostGitHub, "student"); e != nil {
		t.Fatal(e)
	}
	l.Subject = "other"
	if e := s.Bind(ctx, l, adapter.HostGitHub, "student"); !errors.Is(e, store.ErrConflict) {
		t.Fatal(e)
	}
	l.Subject = "opaque-subject"
	s.Store.CreateRosterEntry(ctx, &store.RosterEntry{ID: "r2", ClassroomID: "c", Host: adapter.HostGitHub, HostUsername: "other", Status: store.RosterActive})
	if e := s.Bind(ctx, l, adapter.HostGitHub, "other"); !errors.Is(e, store.ErrConflict) {
		t.Fatal(e)
	}
	l.Resource = "changed"
	if _, e := s.AssignmentForLaunch(ctx, l); e == nil {
		t.Fatal("changed resource accepted")
	}
	l.Role = Instructor
	if e := s.MapAssignment(ctx, "a", "another", l); !errors.Is(e, ErrForbidden) {
		t.Fatal(e)
	}
	if e := s.MapAssignment(ctx, "a", "owner", l); !errors.Is(e, store.ErrConflict) {
		t.Fatal(e)
	}
}
func TestDeliveryRetryLimitAndStableTimestamp(t *testing.T) {
	s, p, _ := fixture(t, memory.New())
	p.ignore = true
	ctx := context.Background()
	for i := 0; i < 4; i++ {
		d, e := s.Deliver(ctx, "s", "owner", "g")
		if e == nil || d.Status != "uncertain" {
			t.Fatalf("%+v %v", d, e)
		}
	}
	if p.posts != 3 {
		t.Fatalf("posts %d", p.posts)
	}
	for _, ts := range p.timestamps {
		if ts != p.timestamps[0] {
			t.Fatal("retry timestamp changed")
		}
	}
}
func TestDeliveryLeasePreventsConcurrentSend(t *testing.T) {
	path := filepath.Join(t.TempDir(), "concurrent.db")
	st, e := sqlite.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer st.Close()
	otherStore, e := sqlite.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer otherStore.Close()
	s, p, _ := fixture(t, st)
	p.pause = make(chan struct{})
	p.entered = make(chan struct{})
	done := make(chan error)
	go func() { _, e := s.Deliver(context.Background(), "s", "owner", "g"); done <- e }()
	<-p.entered
	other := &Service{Store: otherStore, Client: s.Client}
	if _, e := other.Deliver(context.Background(), "s", "owner", "g"); !errors.Is(e, ErrBusy) {
		t.Fatal(e)
	}
	close(p.pause)
	if e := <-done; e != nil {
		t.Fatal(e)
	}
}
func TestDeliveryAuthFailureThenExplicitRetry(t *testing.T) {
	s, p, _ := fixture(t, memory.New())
	p.authFail = true
	d, e := s.Deliver(context.Background(), "s", "owner", "g")
	if !errors.Is(e, ErrRemote) || d.Status != "uncertain" || p.posts != 0 || d.Attempts != 0 || d.Error != ErrRemote.Error() {
		t.Fatalf("%+v %v", d, e)
	}
	p.mu.Lock()
	p.authFail = false
	p.mu.Unlock()
	d, e = s.Deliver(context.Background(), "s", "owner", "g")
	if e != nil || d.Status != "verified" || p.posts != 1 {
		t.Fatalf("%+v %v", d, e)
	}
}
func TestDeliveryRejectedWriteThenExplicitRetry(t *testing.T) {
	s, p, _ := fixture(t, memory.New())
	p.reject = true
	d, e := s.Deliver(context.Background(), "s", "owner", "g")
	if !errors.Is(e, ErrRemote) || d.Status != "uncertain" || p.posts != 1 {
		t.Fatalf("%+v %v", d, e)
	}
	p.mu.Lock()
	p.reject = false
	p.mu.Unlock()
	d, e = s.Deliver(context.Background(), "s", "owner", "g")
	if e != nil || d.Status != "verified" || p.posts != 2 || p.timestamps[0] != p.timestamps[1] {
		t.Fatalf("%+v %v", d, e)
	}
}
func TestDeliveryRecoversOldOutcomeBeforeNewGrade(t *testing.T) {
	s, p, _ := fixture(t, memory.New())
	p.drop = true
	d, e := s.Deliver(context.Background(), "s", "owner", "g")
	if !errors.Is(e, ErrRemote) {
		t.Fatal(e)
	}
	approve(t, s.Store, "g2", 9)
	p.mu.Lock()
	p.failRead = false
	p.drop = false
	p.mu.Unlock()
	d, e = s.Deliver(context.Background(), "s", "owner", "g2")
	if e != nil || d.GradeID != "g" || d.Status != "verified" || p.posts != 1 {
		t.Fatalf("recovery: %+v %v", d, e)
	}
	d, e = s.Deliver(context.Background(), "s", "owner", "g2")
	if e != nil || d.GradeID != "g2" || d.Status != "verified" || p.posts != 2 {
		t.Fatalf("new: %+v %v", d, e)
	}
}
func TestDeliveryApprovedZero(t *testing.T) {
	s, p, _ := fixture(t, memory.New())
	approve(t, s.Store, "zero", 0)
	d, e := s.Deliver(context.Background(), "s", "owner", "zero")
	if e != nil || d.Status != "verified" || d.Score != 0 || p.posts != 1 {
		t.Fatalf("%+v %v", d, e)
	}
}
func TestDeliveryNullPendingCannotBecomeGrade(t *testing.T) {
	s, p, _ := fixture(t, memory.New())
	ctx := context.Background()
	pending := &store.AssessmentRecord{ID: "null", SubmissionID: "s", Status: "collected", Revision: "commit", RubricDigest: "rubric", Document: []byte(`{"proposal":{"criteria":[{"criterion_id":"a","points":null}]}}`)}
	if e := s.Store.CreateAssessment(ctx, pending); e != nil {
		t.Fatal(e)
	}
	pending.Status = "pending"
	if e := s.Store.TransitionAssessment(ctx, pending, "collected", nil); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Deliver(ctx, "s", "owner", "null"); !errors.Is(e, ErrNotReady) || p.posts != 0 {
		t.Fatalf("null sent: %v", e)
	}
}
func TestDeliveryExpiredCrashLeaseRecoveredByReadback(t *testing.T) {
	s, p, _ := fixture(t, memory.New())
	ctx := context.Background()
	d := &Delivery{GradeID: "g", Status: "sending", Attempts: 1, Timestamp: time.Now().Add(-2 * time.Minute).UTC().Format(time.RFC3339Nano), Score: 8, Maximum: 10, Sent: true, LeaseUntil: time.Now().Add(-time.Minute)}
	r := &store.LTIRecord{ID: key("delivery", "a", "r"), Kind: "delivery", UniqueKey: key("delivery", "a", "r"), AssignmentID: "a", RosterEntryID: "r"}
	if e := s.save(ctx, r, d); e != nil {
		t.Fatal(e)
	}
	ratio := 0.8
	p.ratio = &ratio
	got, e := s.Deliver(ctx, "s", "owner", "g")
	if e != nil || got.Status != "verified" || got.Timestamp != d.Timestamp || p.posts != 0 {
		t.Fatalf("%+v %v posts=%d", got, e, p.posts)
	}
}
