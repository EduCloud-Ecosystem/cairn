// SPDX-License-Identifier: AGPL-3.0-or-later
package api

import (
	"encoding/json"
	"errors"
	"github.com/EduCloud-Ecosystem/cairn/internal/handoff"
	"github.com/EduCloud-Ecosystem/cairn/internal/store"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// SubmissionOrigin derives the trusted browser origin from operator configuration.
func SubmissionOrigin(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", handoff.ErrInvalid
	}
	if u.Scheme != "https" && (u.Scheme != "http" || (u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost")) {
		return "", handoff.ErrInvalid
	}
	return u.Scheme + "://" + u.Host, nil
}
func (s *Server) handoffActor(w http.ResponseWriter, r *http.Request) (handoff.Actor, bool) {
	if s.handoff == nil {
		httpError(w, 404, "submission handoff is unavailable")
		return handoff.Actor{}, false
	}
	host, name, identity, ok := s.currentIdentity(r)
	if !ok {
		httpError(w, 401, "sign in to submit your work")
		return handoff.Actor{}, false
	}
	if r.Method == http.MethodPost && (s.handoffOrigin == "" || r.Header.Get("Origin") != s.handoffOrigin) {
		httpError(w, 403, "same-origin confirmation required")
		return handoff.Actor{}, false
	}
	return handoff.Actor{Host: host, Username: name, ID: identity}, true
}
func handoffError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		httpError(w, 404, "not found")
	case errors.Is(err, handoff.ErrInvalid):
		httpError(w, 400, "invalid files or request; check path and size limits")
	case errors.Is(err, handoff.ErrExpired):
		httpError(w, 410, "preview expired; select files and review again")
	case errors.Is(err, store.ErrConflict):
		httpError(w, 409, "request conflicts with an existing preview or pending limit")
	case errors.Is(err, handoff.ErrUnavailable):
		httpError(w, 409, "assignment or repository is not currently available for submission")
	default:
		httpError(w, 503, "submission service unavailable; retain your files and check the receipt before retrying")
	}
}
func handoffJSON(w http.ResponseWriter, r *http.Request, v any, limit int64) bool {
	if strings.Split(r.Header.Get("Content-Type"), ";")[0] != "application/json" {
		httpError(w, 415, "application/json required")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if dec.Decode(v) != nil || dec.Decode(new(any)) != io.EOF {
		httpError(w, 400, "invalid or oversized submission request")
		return false
	}
	return true
}
func (s *Server) handleSubmissionPreview(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.handoffActor(w, r)
	if !ok {
		return
	}
	select {
	case s.handoffRequests <- struct{}{}:
		defer func() { <-s.handoffRequests }()
	default:
		httpError(w, 429, "submission previews are busy; retry after the current request finishes")
		return
	}
	var request struct {
		IdempotencyKey string         `json:"idempotency_key"`
		Files          []handoff.File `json:"files"`
	}
	if !handoffJSON(w, r, &request, 6<<20) {
		return
	}
	receipt, err := s.handoff.Preview(r.Context(), actor, r.PathValue("submissionID"), request.IdempotencyKey, request.Files)
	if err != nil {
		handoffError(w, err)
		return
	}
	writeJSON(w, 201, receipt)
}
func (s *Server) handleSubmissionConfirm(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.handoffActor(w, r)
	if !ok {
		return
	}
	var request struct{}
	if !handoffJSON(w, r, &request, 1024) {
		return
	}
	receipt, err := s.handoff.Confirm(r.Context(), actor, r.PathValue("submissionID"), r.PathValue("receiptID"))
	if err != nil {
		handoffError(w, err)
		return
	}
	writeJSON(w, 202, receipt)
}
func (s *Server) handleSubmissionReceipts(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.handoffActor(w, r)
	if !ok {
		return
	}
	rows, err := s.handoff.List(r.Context(), actor, r.PathValue("submissionID"))
	if err != nil {
		handoffError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"receipts": rows})
}
func (s *Server) handleSubmissionReceipt(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.handoffActor(w, r)
	if !ok {
		return
	}
	receipt, err := s.handoff.Get(r.Context(), actor, r.PathValue("submissionID"), r.PathValue("receiptID"))
	if err != nil {
		handoffError(w, err)
		return
	}
	writeJSON(w, 200, receipt)
}
