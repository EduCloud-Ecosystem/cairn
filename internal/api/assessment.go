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
		writeJSON(w, 200, map[string]bool{"review": s.assessment != nil, "model_provider": false})
	}))
	if s.assessment == nil {
		return
	}
	s.mux.HandleFunc("GET /assignments/{id}/assessment-rubric", protect(s.handleGetAssessmentRubric))
	s.mux.HandleFunc("PUT /assignments/{id}/assessment-rubric", protect(s.handlePutAssessmentRubric))
	s.mux.HandleFunc("GET /submissions/{id}/assessments", protect(s.handleListAssessments))
	s.mux.HandleFunc("POST /submissions/{id}/assessments", protect(s.handleCaptureAssessment))
	s.mux.HandleFunc("POST /assessments/{id}/proposal", protect(s.handleAssessmentProposal))
	s.mux.HandleFunc("POST /assessments/{id}/review", protect(s.handleAssessmentReview))
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
	writeJSON(w, 200, v)
}
func (s *Server) handleCaptureAssessment(w http.ResponseWriter, r *http.Request) {
	var empty struct{}
	if !assessmentJSON(w, r, &empty) {
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
	v, err := s.assessment.Capture(r.Context(), r.PathValue("id"), assessmentActor(actor))
	if err != nil {
		assessmentError(w, err)
		return
	}
	writeJSON(w, 201, v)
}
func (s *Server) handleAssessmentProposal(w http.ResponseWriter, r *http.Request) {
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
