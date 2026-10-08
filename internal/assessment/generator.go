// SPDX-License-Identifier: AGPL-3.0-or-later
package assessment

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/EduCloud-Ecosystem/cairn/internal/store"
)

var ErrProviderBusy = errors.New("another model request is in progress; retry later")

type Generator struct {
	Service    Service
	Provider   *OpenAI
	Classrooms map[string]bool
	Limits     store.GenerationLimits
	slots      chan struct{}
}

func NewGenerator(service Service, provider *OpenAI, classrooms []string) *Generator {
	allowed := map[string]bool{}
	for _, c := range classrooms {
		if c != "" {
			allowed[c] = true
		}
	}
	return &Generator{Service: service, Provider: provider, Classrooms: allowed, Limits: store.GenerationLimits{DailyRequests: 20, LearnerDailyRequests: 3, DailyUnits: 200000}, slots: make(chan struct{}, 1)}
}
func (g *Generator) Generate(ctx context.Context, aid, inputDigest string) (*store.AssessmentRecord, error) {
	select {
	case g.slots <- struct{}{}:
		defer func() { <-g.slots }()
	default:
		return nil, ErrProviderBusy
	}
	r, err := g.Service.Store.GetAssessment(ctx, aid)
	if err != nil {
		return nil, err
	}
	if r.Status != "collected" {
		return nil, store.ErrConflict
	}
	sub, err := g.Service.Store.GetSubmission(ctx, r.SubmissionID)
	if err != nil {
		return nil, err
	}
	assignment, err := g.Service.Store.GetAssignment(ctx, sub.AssignmentID)
	if err != nil {
		return nil, err
	}
	if !g.Classrooms[assignment.ClassroomID] {
		return nil, errors.New("OpenAI has not been enabled for this classroom")
	}
	var d Document
	if json.Unmarshal(r.Document, &d) != nil || inputDigest == "" || d.InputDigest != inputDigest {
		return nil, store.ErrConflict
	}
	if err := g.validateCalibration(ctx, d, sub.AssignmentID, r.RubricDigest); err != nil {
		return nil, err
	}
	body, err := g.Provider.request(d)
	if err != nil {
		return nil, err
	}
	attempt := &store.Generation{AssessmentID: r.ID, Day: time.Now().UTC().Format("2006-01-02"), ReservedUnits: len(body) + MaxProviderOutputTokens, Status: "running", Model: g.Provider.model}
	if err = g.Service.Store.ReserveGeneration(ctx, r, attempt, g.Limits); err != nil {
		return nil, err
	}
	// A reservation is durable before network I/O. Errors, cancellation and
	// crashes never trigger an automatic retry or refund uncertain usage.
	p, callErr := g.Provider.respond(ctx, body, d)
	attempt.Status = "failed"
	attempt.ErrorCode = "provider_failure"
	if p.Usage != nil {
		attempt.InputTokens = p.Usage.InputTokens
		attempt.OutputTokens = p.Usage.OutputTokens
		attempt.ResponseID = p.Usage.ResponseID
	}
	if callErr == nil {
		callErr = g.validateCalibration(ctx, d, sub.AssignmentID, r.RubricDigest)
	}
	if callErr == nil {
		d.Proposal = &p
		r.Document, _ = json.Marshal(d)
		r.Status = "pending"
		callErr = g.Service.Store.TransitionAssessment(ctx, r, "collected", nil)
		if callErr == nil {
			attempt.Status = "succeeded"
			attempt.ErrorCode = ""
		} else {
			attempt.ErrorCode = "stale_or_unsaved"
		}
	} else {
		var failure providerFailure
		if errors.As(callErr, &failure) {
			attempt.ErrorCode = string(failure)
		}
	}
	auditCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err = g.Service.Store.FinishGeneration(auditCtx, attempt); err != nil {
		return nil, errors.New("generation outcome could not be recorded; refresh before any further action")
	}
	if callErr != nil {
		return nil, callErr
	}
	r.Generation = attempt
	return r, nil
}

func (g *Generator) validateCalibration(ctx context.Context, d Document, assignment, rubricDigest string) error {
	if d.Calibration == nil {
		return nil
	}
	if g.Provider.model != DefaultOpenAIModel {
		return errors.New("calibration model does not match provider")
	}
	binding, err := g.Service.CalibrationBinding(ctx, d.Calibration.ID, d.Calibration.OwnerID, assignment, rubricDigest)
	if err != nil {
		return err
	}
	if Digest(binding) != Digest(d.Calibration) {
		return store.ErrConflict
	}
	return nil
}
