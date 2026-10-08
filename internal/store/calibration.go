// SPDX-License-Identifier: AGPL-3.0-or-later
package store

import (
	"context"
	"encoding/json"
)

// Calibration is instructor-owned historical work. It has no grade/submission
// foreign key or publication transition. Ready records are immutable.
type Calibration struct {
	SourceSubmissionID string          `json:"-"` // link a newly captured example atomically
	ID                 string          `json:"id"`
	OwnerID            string          `json:"owner_id"`
	AssignmentID       string          `json:"assignment_id"`
	RubricDigest       string          `json:"rubric_digest"`
	Revision           int             `json:"revision"`
	Status             string          `json:"status"`
	Document           json.RawMessage `json:"document,omitempty"`
}

type CalibrationStore interface {
	CreateCalibration(context.Context, *Calibration) error
	GetCalibration(context.Context, string, string) (*Calibration, error)
	ListCalibrations(context.Context, string, string) ([]*Calibration, error)
	UpdateCalibration(context.Context, *Calibration, int) error
	DeleteCalibration(context.Context, string, string) error
	ReserveCalibrationGeneration(context.Context, *Calibration, *Generation, GenerationLimits) error
}
