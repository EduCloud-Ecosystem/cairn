// SPDX-License-Identifier: AGPL-3.0-or-later
package lti

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"time"

	"github.com/EduCloud-Ecosystem/cairn/internal/store"
	"github.com/EduCloud-Ecosystem/cairn/pkg/adapter"
)

var ErrForbidden = errors.New("LTI action requires the classroom owner or the active learner")
var ErrNotReady = errors.New("LTI mapping, learner binding and a current instructor-approved numeric grade are required")
var ErrBusy = errors.New("grade delivery is already in progress")
var ErrConflict = errors.New("LMS grade differs from the last verified receipt; instructor reconciliation is required")
var ErrAttempts = errors.New("grade delivery retry limit reached; instructor reconciliation is required")

type Service struct {
	Store  store.Store
	Client *Client
}
type mapping struct{ Context, Resource, LineItem, Registration, LineItemScope string }
type binding struct{ Subject string }

// Delivery is a durable receipt, not a claim that the LMS has accepted a POST.
// Sent is persisted before sending so a lost response is recovered by readback.
type Delivery struct {
	GradeID       string     `json:"grade_id"`
	Status        string     `json:"status"`
	Attempts      int        `json:"attempts"`
	Timestamp     string     `json:"timestamp"`
	Score         float64    `json:"score"`
	Maximum       float64    `json:"maximum"`
	VerifiedAt    *time.Time `json:"verified_at,omitempty"`
	LeaseUntil    time.Time  `json:"lease_until"`
	Error         string     `json:"error,omitempty"`
	Sent          bool       `json:"sent"`
	PreviousRatio *float64   `json:"previous_ratio,omitempty"`
}

func key(parts ...string) string {
	raw, _ := json.Marshal(parts)
	h := sha256.Sum256(raw)
	return hex.EncodeToString(h[:])
}
func (s *Service) classroom(ctx context.Context, aid, owner string) (*store.Assignment, error) {
	if s.Client == nil || s.Store == nil {
		return nil, ErrNotReady
	}
	a, e := s.Store.GetAssignment(ctx, aid)
	if e != nil {
		return nil, e
	}
	c, e := s.Store.GetClassroom(ctx, a.ClassroomID)
	if e != nil {
		return nil, e
	}
	if !s.Client.AllowedClassroom(c.ID) || (owner != "" && c.CreatedBy != owner) {
		return nil, ErrForbidden
	}
	if a.Type != store.AssignmentIndividual {
		return nil, ErrNotReady
	}
	return a, nil
}
func (s *Service) validLaunch(l Launch) bool {
	return s.Client != nil && l.Registration == s.Client.Registration && l.Context != "" && l.Resource != "" && l.Subject != "" && s.Client.ServiceURL(l.LineItem)
}
func mapDoc(l Launch) mapping {
	return mapping{l.Context, l.Resource, l.LineItem, l.Registration, l.LineItemScope}
}
func (s *Service) MapAssignment(ctx context.Context, aid, owner string, l Launch) error {
	if owner == "" || !s.validLaunch(l) || l.Role != Instructor {
		return ErrForbidden
	}
	if _, e := s.classroom(ctx, aid, owner); e != nil {
		return e
	}
	raw, _ := json.Marshal(mapDoc(l))
	r := &store.LTIRecord{ID: key("mapping", aid), Kind: "link", UniqueKey: key("mapping", l.Registration, l.LineItem), AssignmentID: aid, Revision: 1, Document: raw}
	if old, e := s.Store.GetLTIRecord(ctx, r.ID); e == nil {
		if string(old.Document) == string(raw) {
			return nil
		}
		return store.ErrConflict
	} else if !errors.Is(e, store.ErrNotFound) {
		return e
	}
	return s.Store.SaveLTIRecord(ctx, r, 0)
}
func (s *Service) AssignmentForLaunch(ctx context.Context, l Launch) (*store.Assignment, error) {
	if !s.validLaunch(l) {
		return nil, ErrForbidden
	}
	r, e := s.Store.FindLTIRecord(ctx, key("mapping", l.Registration, l.LineItem))
	if e != nil {
		return nil, e
	}
	var m mapping
	if json.Unmarshal(r.Document, &m) != nil || m != mapDoc(l) {
		return nil, ErrForbidden
	}
	return s.classroom(ctx, r.AssignmentID, "")
}
func (s *Service) Bind(ctx context.Context, l Launch, host adapter.Host, username string) error {
	if l.Role != Learner || username == "" {
		return ErrForbidden
	}
	a, e := s.AssignmentForLaunch(ctx, l)
	if e != nil {
		return e
	}
	r, e := s.Store.FindRosterEntryByUsername(ctx, a.ClassroomID, username)
	if e != nil {
		return e
	}
	if r.Host != host || r.Status != store.RosterActive {
		return ErrForbidden
	}
	raw, _ := json.Marshal(binding{l.Subject})
	b := &store.LTIRecord{ID: key("binding", a.ID, r.ID), Kind: "binding", UniqueKey: key("binding", a.ID, l.Subject), AssignmentID: a.ID, RosterEntryID: r.ID, Revision: 1, Document: raw}
	if old, e := s.Store.GetLTIRecord(ctx, b.ID); e == nil {
		if string(old.Document) == string(raw) {
			return nil
		}
		return store.ErrConflict
	} else if !errors.Is(e, store.ErrNotFound) {
		return e
	}
	return s.Store.SaveLTIRecord(ctx, b, 0)
}
func (s *Service) target(ctx context.Context, subID, owner string) (*store.Submission, Launch, string, error) {
	if owner == "" {
		return nil, Launch{}, "", ErrForbidden
	}
	sub, e := s.Store.GetSubmission(ctx, subID)
	if e != nil {
		return nil, Launch{}, "", e
	}
	a, e := s.classroom(ctx, sub.AssignmentID, owner)
	if e != nil {
		return nil, Launch{}, "", e
	}
	roster, e := s.Store.GetRosterEntry(ctx, sub.RosterEntryID)
	if e != nil {
		return nil, Launch{}, "", e
	}
	if sub.Status != "active" || roster.Status != store.RosterActive || roster.ClassroomID != a.ClassroomID {
		return nil, Launch{}, "", ErrNotReady
	}
	r, e := s.Store.GetLTIRecord(ctx, key("mapping", a.ID))
	if e != nil {
		return nil, Launch{}, "", ErrNotReady
	}
	var m mapping
	if json.Unmarshal(r.Document, &m) != nil {
		return nil, Launch{}, "", ErrNotReady
	}
	b, e := s.Store.GetLTIRecord(ctx, key("binding", a.ID, roster.ID))
	if e != nil {
		return nil, Launch{}, "", ErrNotReady
	}
	var v binding
	if json.Unmarshal(b.Document, &v) != nil {
		return nil, Launch{}, "", ErrNotReady
	}
	l := Launch{Subject: v.Subject, Context: m.Context, Resource: m.Resource, LineItem: m.LineItem, Registration: m.Registration, Role: Learner, LineItemScope: m.LineItemScope}
	if !s.validLaunch(l) {
		return nil, Launch{}, "", ErrNotReady
	}
	return sub, l, key("delivery", a.ID, roster.ID), nil
}
func (s *Service) approved(ctx context.Context, sub *store.Submission) (*store.Grade, error) {
	g, e := s.Store.LatestGradeForSubmission(ctx, sub.ID)
	if e != nil {
		return nil, ErrNotReady
	}
	if !finite(g.Score) || !finite(g.MaxScore) || g.Score < 0 || g.MaxScore <= 0 || g.Score > g.MaxScore {
		return nil, ErrNotReady
	}
	rows, e := s.Store.ListAssessments(ctx, sub.ID)
	if e != nil {
		return nil, e
	}
	rubric, e := s.Store.GetAssessmentRubric(ctx, sub.AssignmentID)
	if e != nil {
		return nil, ErrNotReady
	}
	for _, r := range rows {
		if r.GradeID == g.ID && r.Status == "approved" && r.Revision == sub.LatestCommit && r.SubmissionActivity == store.AssessmentActivity(sub.LastActivityAt) && r.RubricDigest == rubric.Digest {
			return g, nil
		}
	}
	return nil, ErrNotReady
}
func (s *Service) EligibleGrade(ctx context.Context, subID, owner string) (*store.Grade, error) {
	sub, _, _, e := s.target(ctx, subID, owner)
	if e != nil {
		return nil, e
	}
	return s.approved(ctx, sub)
}
func (s *Service) Status(ctx context.Context, subID, owner string) (*Delivery, error) {
	_, _, rid, e := s.target(ctx, subID, owner)
	if e != nil {
		return nil, e
	}
	r, e := s.Store.GetLTIRecord(ctx, rid)
	if errors.Is(e, store.ErrNotFound) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	var d Delivery
	if json.Unmarshal(r.Document, &d) != nil {
		return nil, ErrInvalid
	}
	return &d, nil
}
func equal(a, b float64) bool { return math.Abs(a-b) <= 1e-8 }
func (s *Service) save(ctx context.Context, r *store.LTIRecord, d *Delivery) error {
	prev := r.Revision
	r.Revision++
	r.GradeID = d.GradeID
	r.Document, _ = json.Marshal(d)
	e := s.Store.SaveLTIRecord(ctx, r, prev)
	if e != nil {
		r.Revision = prev
	}
	return e
}
func (s *Service) Deliver(ctx context.Context, subID, owner, gradeID string) (*Delivery, error) {
	sub, l, rid, e := s.target(ctx, subID, owner)
	if e != nil {
		return nil, e
	}
	g, e := s.approved(ctx, sub)
	if e != nil {
		return nil, e
	}
	if gradeID == "" || g.ID != gradeID {
		return nil, ErrNotReady
	}
	r, e := s.Store.GetLTIRecord(ctx, rid)
	d := &Delivery{}
	if errors.Is(e, store.ErrNotFound) {
		r = &store.LTIRecord{ID: rid, Kind: "delivery", UniqueKey: rid, AssignmentID: sub.AssignmentID, RosterEntryID: sub.RosterEntryID}
	} else if e != nil {
		return nil, e
	} else if json.Unmarshal(r.Document, d) != nil {
		return nil, ErrInvalid
	}
	now := time.Now().UTC()
	if d.LeaseUntil.After(now) {
		return d, ErrBusy
	}
	recoveryOnly := false
	if d.GradeID != "" && d.GradeID != g.ID {
		if d.Status != "verified" {
			recoveryOnly = true
		} else {
			previous := d.Score / d.Maximum
			d = &Delivery{PreviousRatio: &previous}
		}
	}
	if d.GradeID == g.ID && d.Status == "verified" {
		return d, nil
	}
	if d.Status == "conflict" {
		return d, ErrConflict
	}
	if d.GradeID == "" {
		d.GradeID = g.ID
		d.Score = g.Score
		d.Maximum = g.MaxScore
		d.Timestamp = now.Format(time.RFC3339Nano)
	}
	d.LeaseUntil = now.Add(time.Minute)
	d.Status = "sending"
	d.Error = ""
	if e = s.save(ctx, r, d); e != nil {
		return nil, e
	}
	// The deadline is shorter than the durable lease. A second worker may only
	// recover after this operation has stopped issuing HTTP requests.
	op, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	finish := func(status string, cause error) (*Delivery, error) {
		d.Status = status
		d.LeaseUntil = time.Time{}
		if cause != nil {
			d.Error = cause.Error()
		} else {
			d.Error = ""
		}
		persist, done := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer done()
		if err := s.save(persist, r, d); err != nil {
			return d, err
		}
		return d, cause
	}
	token, e := s.Client.accessToken(op, l)
	if e != nil {
		return finish("uncertain", ErrRemote)
	}
	result, e := s.Client.Read(op, l, l.Subject, token)
	if e != nil {
		return finish("uncertain", ErrRemote)
	}
	ratio, present := result.Ratio()
	if d.Sent && present && equal(ratio, d.Score/d.Maximum) {
		t := time.Now().UTC()
		d.VerifiedAt = &t
		return finish("verified", nil)
	}
	if recoveryOnly {
		return finish("uncertain", ErrConflict)
	}
	expected := !present && d.PreviousRatio == nil || present && d.PreviousRatio != nil && equal(ratio, *d.PreviousRatio)
	if !expected {
		return finish("conflict", ErrConflict)
	}
	if d.Attempts >= 3 {
		return finish("uncertain", ErrAttempts)
	}
	// Recheck current approval and enrollment immediately before the remote write.
	fresh, _, _, e := s.target(op, subID, owner)
	if e != nil {
		return finish("uncertain", ErrNotReady)
	}
	current, e := s.approved(op, fresh)
	if e != nil || current.ID != g.ID {
		return finish("uncertain", ErrNotReady)
	}
	d.Sent = true
	d.Attempts++
	if e = s.save(op, r, d); e != nil {
		return d, e
	}
	// Even an error can mean the LMS applied the write and dropped its response.
	_ = s.Client.Send(op, l, l.Subject, token, d.Timestamp, d.Score, d.Maximum)
	result, e = s.Client.Read(op, l, l.Subject, token)
	if e != nil {
		return finish("uncertain", ErrRemote)
	}
	ratio, present = result.Ratio()
	if present && equal(ratio, d.Score/d.Maximum) {
		t := time.Now().UTC()
		d.VerifiedAt = &t
		return finish("verified", nil)
	}
	if present && (d.PreviousRatio == nil || !equal(ratio, *d.PreviousRatio)) {
		return finish("conflict", ErrConflict)
	}
	return finish("uncertain", ErrRemote)
}
