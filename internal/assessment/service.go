// SPDX-License-Identifier: AGPL-3.0-or-later
package assessment

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/EduCloud-Ecosystem/cairn/internal/grading"
	"github.com/EduCloud-Ecosystem/cairn/internal/id"
	"github.com/EduCloud-Ecosystem/cairn/internal/store"
	"os"
	"strings"
	"time"
)

type Service struct {
	Store    store.Store
	Checkout grading.RevisionCheckout
}

func (s Service) Capture(ctx context.Context, subID, actor string) (*store.AssessmentRecord, error) {
	if s.Checkout == nil {
		return nil, fmt.Errorf("assessment checkout is not configured")
	}
	sub, err := s.Store.GetSubmission(ctx, subID)
	if err != nil {
		return nil, err
	}
	if sub.Status != "active" || !grading.ValidPolicyRevision(sub.LatestCommit) {
		return nil, fmt.Errorf("capture requires an active submission and a recorded full commit")
	}
	re, err := s.Store.GetRosterEntry(ctx, sub.RosterEntryID)
	if err != nil {
		return nil, err
	}
	if re.Status != store.RosterActive {
		return nil, fmt.Errorf("student is not active")
	}
	rubric, err := s.Store.GetAssessmentRubric(ctx, sub.AssignmentID)
	if err != nil {
		return nil, err
	}
	var r Rubric
	if err = json.Unmarshal(rubric.Document, &r); err != nil {
		return nil, err
	}
	if err = ValidateRubric(r); err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp("", "cairn-assessment-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err = s.Checkout.FetchRevision(ctx, sub.Repo, sub.LatestCommit, dir); err != nil {
		return nil, fmt.Errorf("could not capture pinned submission")
	}
	d := Document{PolicyVersion: PolicyVersion, CreatedAt: time.Now().UTC(), CapturedBy: actor, Revision: sub.LatestCommit, Rubric: r, Artifacts: Extract(dir, r.Paths)}
	d.InputDigest = InputDigest(d)
	status := "collected"
	for _, a := range d.Artifacts {
		if a.Issue != "" {
			status = "blocked"
		}
	}
	body, _ := json.Marshal(d)
	record := &store.AssessmentRecord{ID: id.New(), SubmissionID: subID, SubmissionActivity: store.AssessmentActivity(sub.LastActivityAt), Revision: sub.LatestCommit, RubricDigest: rubric.Digest, Status: status, Document: body}
	if err = s.Store.CreateAssessment(ctx, record); err != nil {
		return nil, err
	}
	return record, nil
}
func (s Service) Propose(ctx context.Context, aid string, p Proposal) (*store.AssessmentRecord, error) {
	r, err := s.Store.GetAssessment(ctx, aid)
	if err != nil {
		return nil, err
	}
	if r.Status != "collected" {
		return nil, store.ErrConflict
	}
	var d Document
	if err = json.Unmarshal(r.Document, &d); err != nil {
		return nil, err
	}
	if err = ValidateProposal(d, p); err != nil {
		return nil, err
	}
	d.Proposal = &p
	r.Document, _ = json.Marshal(d)
	r.Status = "pending"
	if err = s.Store.TransitionAssessment(ctx, r, "collected", nil); err != nil {
		return nil, err
	}
	return r, nil
}
func (s Service) Review(ctx context.Context, aid, actor, action, note string, criteria []Judgment) (*store.AssessmentRecord, error) {
	if strings.TrimSpace(note) == "" || len(note) > 4000 || actor == "" {
		return nil, fmt.Errorf("reviewer and review note are required")
	}
	if action != "approve" && action != "reject" {
		return nil, fmt.Errorf("action must be approve or reject")
	}
	r, err := s.Store.GetAssessment(ctx, aid)
	if err != nil {
		return nil, err
	}
	var d Document
	if err = json.Unmarshal(r.Document, &d); err != nil {
		return nil, err
	}
	from := r.Status
	review := &Review{Reviewer: actor, ReviewedAt: time.Now().UTC(), Note: note}
	var g *store.Grade
	if action == "approve" {
		if r.Status != "pending" || d.Proposal == nil {
			return nil, store.ErrConflict
		}
		score, max, err := ValidateJudgments(d, criteria, true)
		if err != nil {
			return nil, err
		}
		review.Criteria = criteria
		review.GradeID = id.New()
		r.Status = "approved"
		breakdown, _ := json.Marshal(struct {
			Source             string     `json:"source"`
			AssessmentID       string     `json:"assessment_id"`
			SubmissionRevision string     `json:"submission_revision"`
			Rubric             Rubric     `json:"rubric"`
			Criteria           []Judgment `json:"criteria"`
			ReviewNote         string     `json:"review_note"`
		}{"instructor-reviewed", r.ID, r.Revision, d.Rubric, criteria, note})
		g = &store.Grade{ID: review.GradeID, SubmissionID: r.SubmissionID, Score: score, MaxScore: max, Breakdown: breakdown, GradedAt: review.ReviewedAt}
	} else {
		r.Status = "rejected"
	}
	d.Review = review
	r.Document, _ = json.Marshal(d)
	if err = s.Store.TransitionAssessment(ctx, r, from, g); err != nil {
		return nil, err
	}
	return r, nil
}
