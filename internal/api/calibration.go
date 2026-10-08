// SPDX-License-Identifier: AGPL-3.0-or-later
package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/EduCloud-Ecosystem/cairn/internal/assessment"
	"github.com/EduCloud-Ecosystem/cairn/internal/store"
)

func (s *Server) calibrationRoutes(protect func(http.HandlerFunc) http.HandlerFunc) {
	ownerOnly := func(next func(http.ResponseWriter, *http.Request, string)) http.HandlerFunc {
		return protect(func(w http.ResponseWriter, r *http.Request) {
			actor, _ := operatorFrom(r.Context())
			if !s.authEnabled || actor == nil || actor.ID == "" {
				httpError(w, 403, "sign in as an instructor to calibrate")
				return
			}
			next(w, r, actor.ID)
		})
	}
	s.mux.HandleFunc("GET /assignments/{id}/calibrations", ownerOnly(func(w http.ResponseWriter, r *http.Request, owner string) {
		v, err := s.store.ListCalibrations(r.Context(), r.PathValue("id"), owner)
		if err != nil {
			assessmentError(w, err)
			return
		}
		writeJSON(w, 200, v)
	}))
	s.mux.HandleFunc("POST /assignments/{id}/calibrations", ownerOnly(func(w http.ResponseWriter, r *http.Request, owner string) {
		var body struct {
			BasedOn string `json:"based_on"`
		}
		if !assessmentJSON(w, r, &body) {
			return
		}
		v, err := s.assessment.CreateCalibrationFrom(r.Context(), r.PathValue("id"), owner, body.BasedOn)
		if err != nil {
			assessmentError(w, err)
			return
		}
		writeJSON(w, 201, v)
	}))
	s.mux.HandleFunc("GET /calibrations/{id}", ownerOnly(func(w http.ResponseWriter, r *http.Request, owner string) {
		s.writeCalibration(w, r, r.PathValue("id"), owner)
	}))
	s.mux.HandleFunc("DELETE /calibrations/{id}", ownerOnly(func(w http.ResponseWriter, r *http.Request, owner string) {
		var body struct{}
		if !assessmentJSON(w, r, &body) {
			return
		}
		if err := s.store.DeleteCalibration(r.Context(), r.PathValue("id"), owner); err != nil {
			assessmentError(w, err)
			return
		}
		writeJSON(w, 200, map[string]bool{"deleted": true})
	}))
	s.mux.HandleFunc("POST /calibrations/{id}/examples", ownerOnly(func(w http.ResponseWriter, r *http.Request, owner string) {
		var body struct {
			Revision int               `json:"revision"`
			Files    map[string]string `json:"files"`
		}
		if !assessmentJSON(w, r, &body) {
			return
		}
		_, err := s.assessment.AddCalibrationExample(r.Context(), r.PathValue("id"), owner, body.Revision, body.Files)
		if err != nil {
			assessmentError(w, err)
			return
		}
		s.writeCalibration(w, r, r.PathValue("id"), owner)
	}))
	s.mux.HandleFunc("POST /calibrations/{id}/capture", ownerOnly(func(w http.ResponseWriter, r *http.Request, owner string) {
		var body struct {
			Revision     int    `json:"revision"`
			SubmissionID string `json:"submission_id"`
			Commit       string `json:"commit"`
		}
		if !assessmentJSON(w, r, &body) {
			return
		}
		select {
		case s.assessmentCapture <- struct{}{}:
			defer func() { <-s.assessmentCapture }()
		default:
			httpError(w, 429, "another capture is in progress")
			return
		}
		_, err := s.assessment.CaptureCalibrationExample(r.Context(), r.PathValue("id"), owner, body.SubmissionID, body.Commit, body.Revision)
		if err != nil {
			assessmentError(w, err)
			return
		}
		s.writeCalibration(w, r, r.PathValue("id"), owner)
	}))
	s.mux.HandleFunc("POST /calibrations/{id}/examples/{example}/review", ownerOnly(func(w http.ResponseWriter, r *http.Request, owner string) {
		var body struct {
			Revision int                   `json:"revision"`
			Criteria []assessment.Judgment `json:"criteria"`
			Note     string                `json:"note"`
		}
		if !assessmentJSON(w, r, &body) {
			return
		}
		_, err := s.assessment.ReviewCalibrationExample(r.Context(), r.PathValue("id"), owner, r.PathValue("example"), body.Revision, body.Criteria, body.Note)
		if err != nil {
			assessmentError(w, err)
			return
		}
		s.writeCalibration(w, r, r.PathValue("id"), owner)
	}))
	s.mux.HandleFunc("POST /calibrations/{id}/examples/{example}/exclude", ownerOnly(func(w http.ResponseWriter, r *http.Request, owner string) {
		var body struct {
			Revision int    `json:"revision"`
			Note     string `json:"note"`
		}
		if !assessmentJSON(w, r, &body) {
			return
		}
		_, err := s.assessment.ExcludeCalibrationExample(r.Context(), r.PathValue("id"), owner, r.PathValue("example"), body.Revision, body.Note)
		if err != nil {
			assessmentError(w, err)
			return
		}
		s.writeCalibration(w, r, r.PathValue("id"), owner)
	}))
	s.mux.HandleFunc("POST /calibrations/{id}/approve", ownerOnly(func(w http.ResponseWriter, r *http.Request, owner string) {
		var body struct {
			Revision int    `json:"revision"`
			Guidance string `json:"guidance"`
		}
		if !assessmentJSON(w, r, &body) {
			return
		}
		_, err := s.assessment.ApproveCalibration(r.Context(), r.PathValue("id"), owner, body.Revision, body.Guidance)
		if err != nil {
			assessmentError(w, err)
			return
		}
		s.writeCalibration(w, r, r.PathValue("id"), owner)
	}))
	if s.assessmentGenerator != nil {
		s.mux.HandleFunc("POST /calibrations/{id}/examples/{example}/generate", ownerOnly(func(w http.ResponseWriter, r *http.Request, owner string) {
			var body struct {
				Revision       int    `json:"revision"`
				InputDigest    string `json:"input_digest"`
				SourceReviewed bool   `json:"source_reviewed"`
			}
			if !assessmentJSON(w, r, &body) {
				return
			}
			if !body.SourceReviewed {
				httpError(w, 400, "review historical source before sending it to OpenAI")
				return
			}
			_, err := s.assessmentGenerator.GenerateCalibration(r.Context(), r.PathValue("id"), owner, r.PathValue("example"), body.Revision, body.InputDigest)
			if err != nil {
				assessmentError(w, err)
				return
			}
			s.writeCalibration(w, r, r.PathValue("id"), owner)
		}))
	}
}

func (s *Server) writeCalibration(w http.ResponseWriter, r *http.Request, cid, owner string) {
	c, err := s.store.GetCalibration(r.Context(), cid, owner)
	if err != nil {
		assessmentError(w, err)
		return
	}
	var d assessment.CalibrationDocument
	if json.Unmarshal(c.Document, &d) != nil {
		httpError(w, 500, "could not read calibration")
		return
	}
	for i, e := range d.Examples {
		g, err := s.store.GetGeneration(r.Context(), "calibration:"+cid+":"+e.ID)
		if err == nil {
			d.Examples[i].Generation = g
		} else if !errors.Is(err, store.ErrNotFound) {
			httpError(w, 500, "could not read calibration usage")
			return
		}
	}
	c.Document, _ = json.Marshal(d)
	writeJSON(w, 200, c)
}

// Existing assessment management is instance-wide; calibrated records contain
// an instructor's private guidance and therefore require that owner's session.
func (s *Server) authorizeCalibratedAssessment(w http.ResponseWriter, r *http.Request) bool {
	c, err := s.store.GetAssessment(r.Context(), r.PathValue("id"))
	if err != nil {
		assessmentError(w, err)
		return false
	}
	var d assessment.Document
	if json.Unmarshal(c.Document, &d) != nil {
		httpError(w, 500, "could not read assessment")
		return false
	}
	actor, _ := operatorFrom(r.Context())
	if d.Calibration != nil && (actor == nil || actor.ID != d.Calibration.OwnerID) {
		assessmentError(w, store.ErrNotFound)
		return false
	}
	return true
}
