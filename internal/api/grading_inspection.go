// SPDX-License-Identifier: AGPL-3.0-or-later
package api

import (
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"strconv"

	"github.com/EduCloud-Ecosystem/cairn/internal/gradingjob"
	"github.com/EduCloud-Ecosystem/cairn/internal/store"
)

type gradingInspectionItem struct {
	JobID         string `json:"job_id"`
	QueueState    string `json:"queue_state"`
	DispatchState string `json:"dispatch_state"`
	SubmissionID  string `json:"submission_id,omitempty"`
	RunID         string `json:"run_id,omitempty"`
	GradeID       string `json:"grade_id,omitempty"`
	TargetSHA256  string `json:"target_sha256,omitempty"`
	ContextSHA256 string `json:"context_sha256,omitempty"`
	ClaimSHA256   string `json:"claim_sha256,omitempty"`
	ResultSHA256  string `json:"result_sha256,omitempty"`
	BindingStatus string `json:"binding_status"`
	RunPresence   string `json:"run_presence"`
	GradePresence string `json:"grade_presence"`
	Observation   string `json:"observation"`
}

var inspectionLimit = regexp.MustCompile(`^[1-9][0-9]{0,2}$`)
var inspectionDigest = regexp.MustCompile(`^[0-9a-f]{64}$`)

// This route intentionally refuses the synthetic auth-disabled operator. It
// uses the existing current session and allowlist without touching the store.
func (s *Server) handleGradingInspection(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	op, ok := s.operatorFromCookie(r)
	if !s.authEnabled || !ok || op.ID == "" || !s.admins[op.HostUsername] {
		httpError(w, http.StatusUnauthorized, "operator_authentication_required")
		return
	}
	if len(r.URL.RawQuery) > 1024 {
		httpError(w, http.StatusBadRequest, "invalid_inspection_query")
		return
	}
	// Query() silently discards malformed escapes; ParseQuery does not.
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		httpError(w, http.StatusBadRequest, "invalid_inspection_query")
		return
	}
	q := store.GradingInspectionQuery{JobID: query.Get("job_id"), After: query.Get("after"), Limit: 25}
	for name, values := range query {
		if len(values) != 1 || values[0] == "" || (name != "job_id" && name != "after" && name != "limit") {
			httpError(w, 400, "invalid_inspection_query")
			return
		}
	}
	if q.JobID != "" && (query.Has("after") || query.Has("limit")) {
		httpError(w, 400, "invalid_inspection_query")
		return
	}
	if query.Has("limit") {
		value := query.Get("limit")
		if !inspectionLimit.MatchString(value) {
			httpError(w, 400, "invalid_inspection_query")
			return
		}
		q.Limit, err = strconv.Atoi(value)
	}
	if err != nil || q.Limit > 100 || q.Validate() != nil {
		httpError(w, 400, "invalid_inspection_query")
		return
	}
	limit := q.Limit
	if q.JobID != "" {
		q.Limit = 1
	} else {
		q.Limit++
	}
	rows, err := s.store.ReadGradingInspection(r.Context(), q)
	if err != nil {
		httpError(w, 503, "grading_inspection_unavailable")
		return
	}
	if q.JobID != "" && len(rows) == 0 {
		httpError(w, 404, "grading_job_not_found")
		return
	}
	items := make([]gradingInspectionItem, 0, len(rows))
	next := ""
	if len(rows) > limit {
		rows = rows[:limit]
		next = rows[len(rows)-1].Job.ID
	}
	for _, row := range rows {
		item, valid := projectGradingInspection(row)
		if !valid {
			httpError(w, 503, "grading_inspection_unavailable")
			return
		}
		items = append(items, item)
	}
	writeJSON(w, 200, struct {
		Version          int                     `json:"version"`
		Items            []gradingInspectionItem `json:"items"`
		NextAfter        string                  `json:"next_after,omitempty"`
		ReplayAuthorized bool                    `json:"replay_authorized"`
		Limits           []string                `json:"limits"`
	}{1, items, next, false, []string{"per_request_snapshot_not_frozen_history", "worker_liveness_and_external_execution_unknown", "record_removal_cause_unknown", "result_body_integrity_not_reverified", "no_replay_recovery_or_compute_authority"}})
}

func projectGradingInspection(r store.GradingInspectionRecord) (gradingInspectionItem, bool) {
	i := gradingInspectionItem{JobID: r.Job.ID, QueueState: string(r.Job.Status), DispatchState: "absent", BindingStatus: "not_recorded", RunPresence: "not_linked", GradePresence: "not_linked", Observation: "no_dispatch_recorded"}
	if !store.ValidInspectionID(i.JobID) {
		return i, false
	}
	valid := true
	switch r.Job.Status {
	case store.JobPending, store.JobInProgress, store.JobSucceeded, store.JobFailed:
	default:
		i.QueueState = "unknown"
		valid = false
	}
	target, err := gradingjob.Decode(r.Job.TargetRef)
	if err == nil {
		if store.ValidInspectionID(target.SubmissionID) {
			i.SubmissionID = target.SubmissionID
		} else {
			valid = false
		}
	}
	if err != nil || (r.Job.Type != "grade" && r.Job.Type != "grade_revision") {
		valid = false
	}
	if !r.HasDispatch {
		if r.Job.Status == store.JobInProgress {
			i.Observation = "in_progress_without_dispatch"
		}
		if !valid {
			i.BindingStatus = "invalid"
			i.Observation = "invalid_binding"
		}
		return i, true
	}
	d := r.Dispatch
	i.BindingStatus = "consistent"
	for _, field := range []struct {
		raw      string
		out      *string
		required bool
	}{{d.SubmissionID, &i.SubmissionID, true}, {d.RunID, &i.RunID, true}, {d.GradeID, &i.GradeID, d.State == store.DispatchPublished}} {
		if field.raw != "" && store.ValidInspectionID(field.raw) {
			*field.out = field.raw
		} else if field.raw != "" || field.required {
			valid = false
		}
	}
	for _, field := range []struct {
		raw      string
		out      *string
		required bool
	}{{d.TargetSHA256, &i.TargetSHA256, true}, {d.ContextSHA256, &i.ContextSHA256, true}, {d.ClaimSHA256, &i.ClaimSHA256, true}, {d.ResultSHA256, &i.ResultSHA256, d.State != store.DispatchClaimed}} {
		if inspectionDigest.MatchString(field.raw) {
			*field.out = field.raw
		} else if field.raw != "" || field.required {
			valid = false
		}
	}
	if d.JobID != r.Job.ID || d.Runner == "" || len(d.Runner) > 256 {
		valid = false
	}
	if err == nil {
		ctxBody, _ := json.Marshal(gradingjob.Context{JobID: r.Job.ID, Target: target})
		if d.SubmissionID != target.SubmissionID || d.TargetSHA256 != store.GradingDigest([]byte(r.Job.TargetRef)) || d.ContextSHA256 != store.GradingDigest(ctxBody) {
			valid = false
		}
	}
	i.RunPresence = "absent"
	if r.RunPresent {
		i.RunPresence = "present"
		if r.RunSubmissionID != d.SubmissionID || r.RunRunner != d.Runner {
			valid = false
		}
	}
	if d.GradeID != "" {
		i.GradePresence = "absent"
	}
	if r.GradePresent {
		if d.GradeID == "" {
			valid = false
		}
		i.GradePresence = "present"
		if r.GradeSubmissionID != d.SubmissionID || r.GradeRunID != d.RunID {
			valid = false
		}
	}
	switch d.State {
	case store.DispatchClaimed:
		i.DispatchState = "claimed"
		i.Observation = "claimed_liveness_unknown"
		if r.Job.Status != store.JobInProgress || d.GradeID != "" || d.ResultSHA256 != "" || (r.RunPresent && r.RunStatus != "running") {
			valid = false
		}
	case store.DispatchHeld:
		i.DispatchState = "held"
		i.Observation = "held_reconciliation_required"
		if r.Job.Status != store.JobFailed || d.GradeID != "" || (r.RunPresent && r.RunStatus != "failed") {
			valid = false
		}
	case store.DispatchPublished:
		i.DispatchState = "published"
		i.Observation = "published_records_present"
		if !r.RunPresent || !r.GradePresent {
			i.Observation = "published_records_absent"
		}
		if r.Job.Status != store.JobSucceeded || (r.RunPresent && r.RunStatus != "completed") {
			valid = false
		}
	default:
		i.DispatchState = "unknown"
		valid = false
	}
	if !valid {
		i.BindingStatus = "invalid"
		i.Observation = "invalid_binding"
	}
	return i, true
}
