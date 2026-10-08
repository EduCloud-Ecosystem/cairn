// SPDX-License-Identifier: AGPL-3.0-or-later
package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
)

var ErrGenerationLimit = errors.New("assessment generation is paused, already attempted, or over its usage limit")

// Generation contains no prompt, source, key or model output. Reservations are
// never refunded: uncertain provider outcomes cannot silently reset budgets.
type Generation struct {
	AssessmentID  string `json:"assessment_id"`
	Day           string `json:"day"`
	ReservedUnits int    `json:"reserved_units"`
	Status        string `json:"status"`
	ErrorCode     string `json:"error_code,omitempty"`
	InputTokens   int    `json:"input_tokens"`
	OutputTokens  int    `json:"output_tokens"`
	ResponseID    string `json:"response_id,omitempty"`
	Model         string `json:"model"`
}
type GenerationLimits struct{ DailyRequests, LearnerDailyRequests, DailyUnits int }
type GenerationStore interface {
	ReserveGeneration(context.Context, *AssessmentRecord, *Generation, GenerationLimits) error
	FinishGeneration(context.Context, *Generation) error
	GetGeneration(context.Context, string) (*Generation, error)
	GenerationPaused(context.Context) (bool, error)
	SetGenerationPaused(context.Context, bool) error
}

func GenerationLearnerKey(day, id string) string {
	h := sha256.Sum256([]byte(day + "\x00" + id))
	return hex.EncodeToString(h[:])
}
