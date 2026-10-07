// SPDX-License-Identifier: AGPL-3.0-or-later
package store

import (
	"context"
	"encoding/json"
	"time"
)

// AssessmentRecord keeps unreviewed proposals out of Grade. Document contains
// immutable evidence/proposal plus an optional terminal instructor review.
func AssessmentActivity(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

type AssessmentRecord struct {
	GradeID            string          `json:"grade_id,omitempty"`
	SubmissionActivity string          `json:"submission_activity"`
	ID                 string          `json:"id"`
	SubmissionID       string          `json:"submission_id"`
	Revision           string          `json:"revision"`
	RubricDigest       string          `json:"rubric_digest"`
	Status             string          `json:"status"`
	Document           json.RawMessage `json:"document"`
}
type AssessmentRubric struct {
	AssignmentID string          `json:"assignment_id"`
	Digest       string          `json:"digest"`
	Document     json.RawMessage `json:"document"`
}

type AssessmentStore interface {
	PutAssessmentRubric(context.Context, *AssessmentRubric) error
	GetAssessmentRubric(context.Context, string) (*AssessmentRubric, error)
	CreateAssessment(context.Context, *AssessmentRecord) error
	GetAssessment(context.Context, string) (*AssessmentRecord, error)
	ListAssessments(context.Context, string) ([]*AssessmentRecord, error)
	// TransitionAssessment atomically checks status and (except rejection)
	// current submission/rubric/roster, updates the document, and inserts a grade.
	// A conflict makes NO changes. A grade is required exactly for approval.
	TransitionAssessment(context.Context, *AssessmentRecord, string, *Grade) error
}

func ValidAssessmentTransition(from, to string, g *Grade, subID string) bool {
	if from == "collected" && to == "pending" {
		return g == nil
	}
	if (from == "pending" || from == "collected" || from == "blocked") && to == "rejected" {
		return g == nil
	}
	return from == "pending" && to == "approved" && g != nil && g.SubmissionID == subID
}
