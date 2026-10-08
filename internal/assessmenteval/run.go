// SPDX-License-Identifier: AGPL-3.0-or-later
package assessmenteval

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/EduCloud-Ecosystem/cairn/internal/assessment"
	"github.com/EduCloud-Ecosystem/cairn/internal/store"
	"github.com/EduCloud-Ecosystem/cairn/pkg/adapter"
)

const Version = "synthetic-eval-v1"

type Result struct {
	CaseID     string              `json:"case_id"`
	Trial      int                 `json:"trial"`
	Status     string              `json:"status"`
	Error      string              `json:"error,omitempty"`
	Document   assessment.Document `json:"document"`
	Generation *store.Generation   `json:"generation,omitempty"`
	LatencyMS  int64               `json:"latency_ms"`
}
type Report struct {
	Version         string    `json:"version"`
	CorpusDigest    string    `json:"corpus_digest"`
	ReferenceStatus string    `json:"reference_status"`
	CreatedAt       time.Time `json:"created_at"`
	Live            bool      `json:"live"`
	Trials          int       `json:"trials"`
	Complete        bool      `json:"complete"`
	Cases           []Case    `json:"cases"`
	Results         []Result  `json:"results"`
	Metrics         Metrics   `json:"metrics"`
}
type Generate func(context.Context, string, string) (*store.AssessmentRecord, error)
type checkout map[string]string

func (c checkout) FetchRevision(_ context.Context, _ adapter.RepoRef, _ string, dir string) error {
	for p, s := range c {
		if !assessment.ValidPath(p) {
			return errors.New("unsafe synthetic path")
		}
		dest := filepath.Join(dir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(dest), 0700); err != nil {
			return err
		}
		if err := os.WriteFile(dest, []byte(s), 0600); err != nil {
			return err
		}
	}
	return nil
}

// Run uses the real capture, extraction and proposal persistence boundaries in a
// dedicated evaluation store. A save failure stops further paid requests.
func Run(ctx context.Context, st store.Store, generate Generate, trials int, save func(Report) error) (Report, error) {
	cases := Corpus()
	r := Report{Version: Version, CorpusDigest: assessment.Digest(cases), ReferenceStatus: "agent-authored; not instructor-calibrated", CreatedAt: time.Now().UTC(), Live: generate != nil, Trials: trials, Cases: cases, Results: []Result{}}
	if trials < 1 || trials > 2 {
		return r, errors.New("trials must be 1 or 2 (at most 18 model requests)")
	}
	persist := func() error { r.Metrics = Measure(r); return save(r) }
	if err := st.CreateClassroom(ctx, &store.Classroom{ID: "eval", Name: "Synthetic evaluation"}); err != nil {
		return r, err
	}
	if err := persist(); err != nil {
		return r, err
	}
	for _, c := range cases {
		if err := ctx.Err(); err != nil {
			return r, err
		}
		rubric, _ := json.Marshal(c.Rubric)
		for _, err := range []error{
			st.CreateAssignment(ctx, &store.Assignment{ID: c.ID, ClassroomID: "eval", Slug: c.ID}),
			st.CreateRosterEntry(ctx, &store.RosterEntry{ID: c.ID, ClassroomID: "eval", HostUsername: c.ID, Status: store.RosterActive}),
			st.CreateSubmission(ctx, &store.Submission{ID: c.ID, AssignmentID: c.ID, RosterEntryID: c.ID, Status: "active", LatestCommit: strings.Repeat("a", 40)}),
			st.PutAssessmentRubric(ctx, &store.AssessmentRubric{AssignmentID: c.ID, Digest: assessment.DigestBytes(rubric), Document: rubric}),
		} {
			if err != nil {
				return r, err
			}
		}
		svc := assessment.Service{Store: st, Checkout: checkout(c.Files)}
		for trial := 1; trial <= trials; trial++ {
			rec, err := svc.Capture(ctx, c.ID, "synthetic-evaluator")
			if err != nil {
				return r, err
			}
			result := Result{CaseID: c.ID, Trial: trial, Status: rec.Status}
			if err = json.Unmarshal(rec.Document, &result.Document); err != nil {
				return r, err
			}
			if rec.Status == "collected" && !c.Blocked && generate != nil {
				start := time.Now()
				got, e := generate(ctx, rec.ID, result.Document.InputDigest)
				result.LatencyMS = time.Since(start).Milliseconds()
				result.Generation, _ = st.GetGeneration(ctx, rec.ID)
				if e != nil {
					result.Status = "provider_failed"
					result.Error = "Generation failed; inspect the recorded fixed error code."
				} else {
					if got.Status != "pending" {
						return r, errors.New("evaluation unexpectedly left pending proposal boundary")
					}
					if e = json.Unmarshal(got.Document, &result.Document); e != nil {
						return r, e
					}
					result.Status = "pending"
				}
			}
			if _, err = st.LatestGradeForSubmission(ctx, c.ID); !errors.Is(err, store.ErrNotFound) {
				return r, fmt.Errorf("evaluation grade isolation failed for %s", c.ID)
			}
			r.Results = append(r.Results, result)
			if err = persist(); err != nil {
				return r, err
			}
		}
	}
	r.Complete = true
	return r, persist()
}

type Metrics struct {
	Trials                  int      `json:"completed_trials"`
	Proposals               int      `json:"proposals"`
	ProviderFailures        int      `json:"provider_failures"`
	ExpectedBlocks          int      `json:"expected_blocks"`
	UnexpectedOutcomes      int      `json:"unexpected_outcomes"`
	CriterionComparisons    int      `json:"criterion_comparisons"`
	ExactMatches            int      `json:"exact_matches"`
	NumericComparisons      int      `json:"numeric_comparisons"`
	MeanAbsolutePointError  *float64 `json:"mean_absolute_point_error"`
	AssessabilityMismatches int      `json:"assessability_mismatches"`
	CitationLocations       int      `json:"validated_citation_locations"`
	RepeatedCases           int      `json:"repeated_cases"`
	ScoreStableCases        int      `json:"score_stable_cases"`
	InputTokens             int      `json:"reported_input_tokens"`
	OutputTokens            int      `json:"reported_output_tokens"`
	UnknownUsageAttempts    int      `json:"unknown_usage_attempts"`
}

func Measure(r Report) Metrics {
	m := Metrics{Trials: len(r.Results)}
	refs := map[string]Case{}
	scores := map[string][]string{}
	sum := 0.0
	for _, c := range r.Cases {
		refs[c.ID] = c
	}
	for _, v := range r.Results {
		c := refs[v.CaseID]
		if v.Generation != nil {
			m.InputTokens += v.Generation.InputTokens
			m.OutputTokens += v.Generation.OutputTokens
			if v.Generation.InputTokens == 0 {
				m.UnknownUsageAttempts++
			}
		}
		if c.Blocked && v.Status == "blocked" {
			m.ExpectedBlocks++
			continue
		}
		if c.Blocked || v.Status == "blocked" {
			m.UnexpectedOutcomes++
			continue
		}
		if v.Status == "provider_failed" {
			m.ProviderFailures++
			continue
		}
		if v.Status != "pending" || v.Document.Proposal == nil {
			continue
		}
		m.Proposals++
		values := map[string]*float64{}
		for _, j := range v.Document.Proposal.Criteria {
			expected := c.Expected[j.CriterionID]
			m.CriterionComparisons++
			values[j.CriterionID] = j.Points
			m.CitationLocations += len(j.Citations)
			if expected == nil || j.Points == nil {
				if expected == nil && j.Points == nil {
					m.ExactMatches++
				} else {
					m.AssessabilityMismatches++
				}
				continue
			}
			d := *expected - *j.Points
			if d < 0 {
				d = -d
			}
			sum += d
			m.NumericComparisons++
			if d < 1e-9 {
				m.ExactMatches++
			}
		}
		scores[v.CaseID] = append(scores[v.CaseID], assessment.Digest(values))
	}
	if m.NumericComparisons > 0 {
		v := sum / float64(m.NumericComparisons)
		m.MeanAbsolutePointError = &v
	}
	for _, ss := range scores {
		if len(ss) > 1 {
			m.RepeatedCases++
			same := true
			for _, s := range ss[1:] {
				same = same && s == ss[0]
			}
			if same {
				m.ScoreStableCases++
			}
		}
	}
	return m
}
