// SPDX-License-Identifier: AGPL-3.0-or-later
package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
)

// GradingDispatch is a consumed queue invocation, not compute authority. It is
// deliberately independent of grade/run retention and submission cascades.
type GradingDispatch struct {
	JobID         string `json:"job_id"`
	SubmissionID  string `json:"submission_id"`
	TargetSHA256  string `json:"target_sha256"`
	ContextSHA256 string `json:"context_sha256"`
	ClaimSHA256   string `json:"claim_sha256"`
	RunID         string `json:"run_id"`
	Runner        string `json:"runner"`
	State         string `json:"state"`
	GradeID       string `json:"grade_id"`
	ResultSHA256  string `json:"result_sha256"`
}

const DispatchClaimed = "claimed"
const DispatchHeld = "held"
const DispatchPublished = "published"
const MaxDispatchResultBytes = 8 << 20

// A successful new claim is the only permission to invoke the optional runner.
// There is intentionally no reset, expiry or claim takeover method.
type GradingDispatchStore interface {
	GetGradingJob(context.Context, string) (*ProvisioningJob, error)
	GetGradingDispatch(context.Context, string) (*GradingDispatch, error)
	ClaimGradingDispatch(context.Context, GradingDispatch, *GradingRun) (bool, error)
	FinishGradingDispatch(context.Context, GradingDispatch, *GradingRun, *Grade) error
}

func GradingDigest(b []byte) string { return fmt.Sprintf("%x", sha256.Sum256(b)) }

var dispatchDigest = regexp.MustCompile(`^[0-9a-f]{64}$`)

func dispatchID(s string) bool { return s != "" && len(s) <= 256 }

func ValidateDispatchClaim(d GradingDispatch, r *GradingRun) error {
	if !dispatchID(d.JobID) || !dispatchID(d.SubmissionID) || !dispatchID(d.RunID) || !dispatchID(d.Runner) || !dispatchDigest.MatchString(d.TargetSHA256) || !dispatchDigest.MatchString(d.ContextSHA256) || !dispatchDigest.MatchString(d.ClaimSHA256) || d.State != DispatchClaimed || d.GradeID != "" || d.ResultSHA256 != "" || r == nil || r.ID != d.RunID || r.SubmissionID != d.SubmissionID || r.Runner != d.Runner || r.Status != "running" || r.StartedAt == nil || r.FinishedAt != nil {
		return ErrConflict
	}
	return validateDispatchContext(d, r.Result)
}
func validateDispatchContext(d GradingDispatch, result []byte) error {
	if len(result) == 0 || len(result) > MaxDispatchResultBytes {
		return ErrConflict
	}
	var doc struct {
		Job json.RawMessage `json:"job"`
	}
	if json.Unmarshal(result, &doc) != nil {
		return ErrConflict
	}
	var job struct {
		JobID  string `json:"job_id"`
		Target struct {
			SubmissionID string `json:"submission_id"`
		} `json:"target"`
	}
	var compact bytes.Buffer
	if json.Unmarshal(doc.Job, &job) != nil || json.Compact(&compact, doc.Job) != nil || job.JobID != d.JobID || job.Target.SubmissionID != d.SubmissionID || GradingDigest(compact.Bytes()) != d.ContextSHA256 {
		return ErrConflict
	}
	return nil
}
func SameDispatchClaim(a, b GradingDispatch) bool {
	a.State = ""
	a.GradeID = ""
	a.ResultSHA256 = ""
	b.State = ""
	b.GradeID = ""
	b.ResultSHA256 = ""
	return a == b
}
func DispatchMatchesQueue(d GradingDispatch, j *ProvisioningJob) bool {
	return j != nil && j.ID == d.JobID && (j.Type == "grade" || j.Type == "grade_revision") && j.Status == JobInProgress && GradingDigest([]byte(j.TargetRef)) == d.TargetSHA256
}
func ValidateDispatchFinish(d GradingDispatch, r *GradingRun, g *Grade) error {
	if r == nil || r.ID != d.RunID || r.SubmissionID != d.SubmissionID || r.Runner != d.Runner || r.StartedAt == nil || r.FinishedAt == nil || validateDispatchContext(d, r.Result) != nil || d.ResultSHA256 != GradingDigest(r.Result) {
		return ErrConflict
	}
	if g == nil {
		if d.State != DispatchHeld || d.GradeID != "" || r.Status != "failed" {
			return ErrConflict
		}
	} else {
		if d.State != DispatchPublished || r.Status != "completed" || !dispatchID(g.ID) || d.GradeID != g.ID || g.RunID != r.ID || g.SubmissionID != r.SubmissionID || !bytes.Equal(g.Breakdown, r.Result) || g.GradedAt.IsZero() || g.ExportConfirmedAt != nil || math.IsNaN(g.Score) || math.IsInf(g.Score, 0) || math.IsNaN(g.MaxScore) || math.IsInf(g.MaxScore, 0) || g.Score < 0 || g.MaxScore < 0 || g.Score > g.MaxScore {
			return ErrConflict
		}
	}
	return nil
}
