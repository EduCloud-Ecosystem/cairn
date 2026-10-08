// SPDX-License-Identifier: AGPL-3.0-or-later
package api

import (
	"encoding/json"
	"errors"
	"github.com/EduCloud-Ecosystem/cairn/internal/assessment"
	"github.com/EduCloud-Ecosystem/cairn/internal/store"
	"io"
	"net/http"
)

func (s *Server) assessmentRoutes(protect func(http.HandlerFunc) http.HandlerFunc) {
	s.mux.HandleFunc("GET /assessment-capabilities", protect(func(w http.ResponseWriter, r *http.Request) {
		paused, err := s.store.GenerationPaused(r.Context())
		if err != nil {
			httpError(w, 500, "could not read provider state")
			return
		}
		classrooms := []string{}
		if s.assessmentGenerator != nil {
			for id := range s.assessmentGenerator.Classrooms {
				classrooms = append(classrooms, id)
			}
		}
		writeJSON(w, 200, map[string]any{"review": s.assessment != nil, "calibration": s.authEnabled && s.assessment != nil, "model_provider": s.assessmentGenerator != nil, "paused": paused, "classrooms": classrooms})
	}))
	if s.assessment == nil {
		return
	}
	s.calibrationRoutes(protect)
	s.mux.HandleFunc("GET /assignments/{id}/assessment-rubric", protect(s.handleGetAssessmentRubric))
	s.mux.HandleFunc("PUT /assignments/{id}/assessment-rubric", protect(s.handlePutAssessmentRubric))
	s.mux.HandleFunc("GET /submissions/{id}/assessments", protect(s.handleListAssessments))
	s.mux.HandleFunc("POST /submissions/{id}/assessments", protect(s.handleCaptureAssessment))
	s.mux.HandleFunc("POST /assessments/{id}/proposal", protect(s.handleAssessmentProposal))
	s.mux.HandleFunc("POST /assessments/{id}/review", protect(s.handleAssessmentReview))
	if s.assessmentGenerator != nil {
		s.mux.HandleFunc("POST /assessments/{id}/generate", protect(s.handleGenerateAssessment))
		s.mux.HandleFunc("POST /assessment-provider/control", protect(s.handleAssessmentProviderControl))
	}
}

// Require JSON and reject unknown/trailing fields; this also prevents cross-site
// simple form POSTs from reaching authenticated state-changing handlers.
func assessmentJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	if r.Header.Get("Content-Type") != "application/json" {
		httpError(w, 415, "application/json required")
		return false
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRequestBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		httpError(w, 400, "invalid assessment JSON")
		return false
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		httpError(w, 400, "expected one JSON document")
		return false
	}
	return true
}
func assessmentError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		httpError(w, 404, "assessment, rubric or submission not found")
	case errors.Is(err, store.ErrGenerationLimit) || errors.Is(err, assessment.ErrProviderBusy):
		httpError(w, 429, err.Error())
	case errors.Is(err, store.ErrConflict):
		httpError(w, 409, "assessment changed, is stale, or has already been reviewed; capture current work again")
	default:
		httpError(w, 400, err.Error())
	}
}
func (s *Server) handleGetAssessmentRubric(w http.ResponseWriter, r *http.Request) {
	v, err := s.store.GetAssessmentRubric(r.Context(), r.PathValue("id"))
	if err != nil {
		assessmentError(w, err)
		return
	}
	writeJSON(w, 200, v)
}
func (s *Server) handlePutAssessmentRubric(w http.ResponseWriter, r *http.Request) {
	var rubric assessment.Rubric
	if !assessmentJSON(w, r, &rubric) {
		return
	}
	if err := assessment.ValidateRubric(rubric); err != nil {
		assessmentError(w, err)
		return
	}
	aid := r.PathValue("id")
	if _, err := s.store.GetAssignment(r.Context(), aid); err != nil {
		assessmentError(w, err)
		return
	}
	b, _ := json.Marshal(rubric)
	v := &store.AssessmentRubric{AssignmentID: aid, Digest: assessment.DigestBytes(b), Document: b}
	if err := s.store.PutAssessmentRubric(r.Context(), v); err != nil {
		httpError(w, 500, "could not save rubric")
		return
	}
	writeJSON(w, 200, v)
}
func (s *Server) handleListAssessments(w http.ResponseWriter, r *http.Request) {
	v, err := s.store.ListAssessments(r.Context(), r.PathValue("id"))
	if err != nil {
		httpError(w, 500, "could not list assessments")
		return
	}
	actor, _ := operatorFrom(r.Context())
	visible := []*store.AssessmentRecord{}
	for _, record := range v {
		var doc assessment.Document
		if json.Unmarshal(record.Document, &doc) != nil {
			httpError(w, 500, "could not read assessment")
			return
		}
		if doc.Calibration != nil && (actor == nil || actor.ID != doc.Calibration.OwnerID) {
			continue
		}
		visible = append(visible, record)
		attempt, e := s.store.GetGeneration(r.Context(), record.ID)
		if e == nil {
			record.Generation = attempt
		} else if !errors.Is(e, store.ErrNotFound) {
			httpError(w, 500, "could not read generation outcome")
			return
		}
	}
	writeJSON(w, 200, visible)
}
func (s *Server) handleCaptureAssessment(w http.ResponseWriter, r *http.Request) {
	var body struct {
		CalibrationID string `json:"calibration_id"`
	}
	if !assessmentJSON(w, r, &body) {
		return
	}
	select {
	case s.assessmentCapture <- struct{}{}:
		defer func() { <-s.assessmentCapture }()
	default:
		httpError(w, 429, "another capture is in progress; retry later")
		return
	}
	actor, _ := operatorFrom(r.Context())
	v, err := s.assessment.CaptureCalibrated(r.Context(), r.PathValue("id"), assessmentActor(actor), body.CalibrationID)
	if err != nil {
		assessmentError(w, err)
		return
	}
	writeJSON(w, 201, v)
}
func (s *Server) handleAssessmentProposal(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeCalibratedAssessment(w, r) {
		return
	}
	var p assessment.Proposal
	if !assessmentJSON(w, r, &p) {
		return
	}
	v, err := s.assessment.Propose(r.Context(), r.PathValue("id"), p)
	if err != nil {
		assessmentError(w, err)
		return
	}
	writeJSON(w, 200, v)
}
func (s *Server) handleAssessmentReview(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeCalibratedAssessment(w, r) {
		return
	}
	var body struct {
		Action   string                `json:"action"`
		Note     string                `json:"note"`
		Criteria []assessment.Judgment `json:"criteria"`
	}
	if !assessmentJSON(w, r, &body) {
		return
	}
	actor, _ := operatorFrom(r.Context())
	v, err := s.assessment.Review(r.Context(), r.PathValue("id"), assessmentActor(actor), body.Action, body.Note, body.Criteria)
	if err != nil {
		assessmentError(w, err)
		return
	}
	writeJSON(w, 200, v)
}

func assessmentActor(actor *store.User) string {
	if actor.ID != "" {
		return actor.ID
	}
	return "local-development"
}

func (s *Server) handleGenerateAssessment(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeCalibratedAssessment(w, r) {
		return
	}
	var body struct {
		InputDigest string `json:"input_digest"`
	}
	if !assessmentJSON(w, r, &body) {
		return
	}
	v, err := s.assessmentGenerator.Generate(r.Context(), r.PathValue("id"), body.InputDigest)
	if err != nil {
		assessmentError(w, err)
		return
	}
	writeJSON(w, 200, v)
}
func (s *Server) handleAssessmentProviderControl(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Paused *bool `json:"paused"`
	}
	if !assessmentJSON(w, r, &body) {
		return
	}
	if body.Paused == nil {
		httpError(w, 400, "paused is required")
		return
	}
	if err := s.store.SetGenerationPaused(r.Context(), *body.Paused); err != nil {
		httpError(w, 500, "could not change provider state")
		return
	}
	writeJSON(w, 200, map[string]bool{"paused": *body.Paused})
}
