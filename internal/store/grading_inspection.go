// SPDX-License-Identifier: AGPL-3.0-or-later
package store

import (
	"context"
	"regexp"
)

// GradingInspectionQuery has no mutation or execution authority.
type GradingInspectionQuery struct {
	JobID, After string
	Limit        int
}

var inspectionID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,256}$`)

func ValidInspectionID(id string) bool { return inspectionID.MatchString(id) }
func (q GradingInspectionQuery) Validate() error {
	if q.Limit < 1 || q.Limit > 101 || (q.JobID != "" && (!ValidInspectionID(q.JobID) || q.After != "")) || (q.After != "" && !ValidInspectionID(q.After)) {
		return ErrConflict
	}
	return nil
}

// GradingInspectionRecord is private metadata for the API projection. TargetRef
// is bounded and used only for pin/digest checks. No educational body is read.
// Prevent accidental direct JSON serialization of these internal fields.
type GradingInspectionRecord struct {
	Job                                   ProvisioningJob `json:"-"`
	Dispatch                              GradingDispatch `json:"-"`
	HasDispatch                           bool            `json:"-"`
	RunPresent                            bool            `json:"-"`
	RunSubmissionID, RunRunner, RunStatus string          `json:"-"`
	GradePresent                          bool            `json:"-"`
	GradeSubmissionID, GradeRunID         string          `json:"-"`
}

type GradingInspectionStore interface {
	ReadGradingInspection(context.Context, GradingInspectionQuery) ([]GradingInspectionRecord, error)
}
