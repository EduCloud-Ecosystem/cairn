// SPDX-License-Identifier: AGPL-3.0-or-later
package assessmenteval

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"strings"
	"time"

	"github.com/EduCloud-Ecosystem/cairn/internal/assessment"
	"github.com/EduCloud-Ecosystem/cairn/pkg/adapter"
)

type HumanJudgment struct {
	CaseID                 string   `json:"case_id"`
	Trial                  int      `json:"trial"`
	CriterionID            string   `json:"criterion_id"`
	Assessable             *bool    `json:"assessable"`
	Points                 *float64 `json:"points"`
	EvidenceSupported      *bool    `json:"evidence_supported"`
	FeedbackUseful         *bool    `json:"feedback_useful"`
	UncertaintyAppropriate *bool    `json:"uncertainty_appropriate"`
	Note                   string   `json:"note"`
}
type Worksheet struct {
	MissingWorkPolicy string          `json:"missing_work_policy"`
	PolicyNote        string          `json:"policy_note"`
	ReportDigest      string          `json:"report_digest"`
	Reviewer          string          `json:"reviewer"`
	ReviewedAt        string          `json:"reviewed_at"`
	Judgments         []HumanJudgment `json:"judgments"`
}
type HumanSummary struct {
	MissingWorkPolicy        string `json:"missing_work_policy"`
	PolicyReviewComplete     bool   `json:"policy_review_complete"`
	ProviderFailures         int    `json:"provider_failures"`
	ReportDigest             string `json:"report_digest"`
	Reviewer                 string `json:"reviewer"`
	Complete                 bool   `json:"human_review_complete"`
	Required                 int    `json:"required_judgments"`
	Reviewed                 int    `json:"reviewed_judgments"`
	ExactScoreMatches        int    `json:"exact_score_matches"`
	AssessabilityMismatches  int    `json:"assessability_mismatches"`
	UnsupportedEvidence      int    `json:"unsupported_evidence"`
	UnhelpfulFeedback        int    `json:"unhelpful_feedback"`
	InappropriateUncertainty int    `json:"inappropriate_uncertainty"`
	Limitation               string `json:"limitation"`
}

func resultKey(id string, trial int, criterion string) string {
	return fmt.Sprintf("%s/%d/%s", id, trial, criterion)
}
func NewWorksheet(r Report, digest string) Worksheet {
	w := Worksheet{ReportDigest: digest, Judgments: []HumanJudgment{}}
	for _, v := range r.Results {
		if v.Status == "pending" && v.Document.Proposal != nil {
			for _, j := range v.Document.Proposal.Criteria {
				w.Judgments = append(w.Judgments, HumanJudgment{CaseID: v.CaseID, Trial: v.Trial, CriterionID: j.CriterionID})
			}
		}
	}
	return w
}
func ValidateReport(r Report) error {
	corpus, versionErr := CorpusForVersion(r.Version)
	if versionErr != nil || r.Trials < 1 || r.Trials > 2 || r.CorpusDigest != assessment.Digest(corpus) || assessment.Digest(r.Cases) != r.CorpusDigest {
		return errors.New("unknown evaluation version or changed corpus")
	}
	cases := map[string]Case{}
	for _, c := range r.Cases {
		cases[c.ID] = c
	}
	expectedArtifacts := map[string][]assessment.Artifact{}
	for _, c := range r.Cases {
		artifacts, err := corpusArtifacts(c)
		if err != nil {
			return err
		}
		expectedArtifacts[c.ID] = artifacts
	}
	seen := map[string]bool{}
	for _, v := range r.Results {
		c, ok := cases[v.CaseID]
		key := resultKey(v.CaseID, v.Trial, "")
		if !ok || v.Trial < 1 || v.Trial > r.Trials || seen[key] || assessment.Digest(v.Document.Rubric) != assessment.Digest(c.Rubric) {
			return errors.New("invalid or duplicate evaluation result")
		}

		if v.Document.Revision != strings.Repeat("a", 40) || assessment.Digest(v.Document.Artifacts) != assessment.Digest(expectedArtifacts[v.CaseID]) {
			return errors.New("captured evidence does not match the bundled synthetic case")
		}
		digest := assessment.Digest(struct {
			Revision  string
			Rubric    assessment.Rubric
			Artifacts []assessment.Artifact
		}{v.Document.Revision, v.Document.Rubric, v.Document.Artifacts})
		if v.Document.InputDigest != digest {
			return errors.New("captured input digest does not match evidence")
		}
		if v.Document.Proposal != nil && v.Document.Proposal.InputDigest != digest {
			return errors.New("proposal is bound to different evidence")
		}
		if v.Status != "pending" && v.Document.Proposal != nil {
			return errors.New("proposal attached to a non-pending result")
		}
		hasIssue := false
		for _, a := range v.Document.Artifacts {
			hasIssue = hasIssue || a.Issue != ""
		}
		if (v.Status == "blocked") != hasIssue {
			return errors.New("outcome contradicts captured extraction state")
		}
		if r.Complete && r.Live && v.Status == "collected" {
			return errors.New("completed live run is missing a provider outcome")
		}
		seen[key] = true
		switch v.Status {
		case "pending":
			if v.Document.Proposal == nil {
				return errors.New("missing proposal")
			}
			if _, _, err := assessment.ValidateJudgments(v.Document, v.Document.Proposal.Criteria, false); err != nil {
				return err
			}
		case "blocked", "collected", "provider_failed":
		default:
			return errors.New("unknown evaluation outcome")
		}
	}
	if r.Complete && len(r.Results) != len(cases)*r.Trials {
		return errors.New("incomplete result set")
	}
	return nil
}

// Review records locally asserted human judgments; it neither authenticates the
// reviewer nor authorizes provider activation or changes a course grade.
func Review(raw []byte, w Worksheet) (HumanSummary, error) {
	var r Report
	if err := json.Unmarshal(raw, &r); err != nil {
		return HumanSummary{}, err
	}
	if err := ValidateReport(r); err != nil {
		return HumanSummary{}, err
	}
	if w.ReportDigest != assessment.DigestBytes(raw) {
		return HumanSummary{}, errors.New("review belongs to a different report")
	}
	s := HumanSummary{ReportDigest: w.ReportDigest, Reviewer: w.Reviewer, Limitation: "Local reviewer identity is self-asserted. Completion is not course acceptance, provider authorization or a grade."}
	expected := map[string]assessment.Judgment{}
	limits := map[string]float64{}
	for _, v := range r.Results {
		if v.Status == "pending" && v.Document.Proposal != nil {
			for _, c := range v.Document.Rubric.Criteria {
				limits[resultKey(v.CaseID, v.Trial, c.ID)] = c.MaxPoints
			}
			for _, j := range v.Document.Proposal.Criteria {
				expected[resultKey(v.CaseID, v.Trial, j.CriterionID)] = j
			}
		}
	}
	s.ProviderFailures = Measure(r).ProviderFailures
	s.MissingWorkPolicy = w.MissingWorkPolicy
	if w.MissingWorkPolicy != "" && w.MissingWorkPolicy != "zero" && w.MissingWorkPolicy != "unassessable" {
		return s, errors.New("missing_work_policy must be zero or unassessable")
	}
	if len(w.PolicyNote) > 4000 {
		return s, errors.New("policy note exceeds 4000 characters")
	}
	s.PolicyReviewComplete = w.MissingWorkPolicy != "" && strings.TrimSpace(w.PolicyNote) != ""
	s.Required = len(expected)
	seen := map[string]bool{}
	for _, j := range w.Judgments {
		key := resultKey(j.CaseID, j.Trial, j.CriterionID)
		original, ok := expected[key]
		if !ok || seen[key] {
			return s, errors.New("unknown or duplicate human judgment")
		}
		seen[key] = true
		if j.Assessable == nil || j.EvidenceSupported == nil || j.FeedbackUseful == nil || j.UncertaintyAppropriate == nil || strings.TrimSpace(j.Note) == "" {
			continue
		}
		if len(j.Note) > 4000 {
			return s, errors.New("review note exceeds 4000 characters")
		}
		if *j.Assessable {
			if j.Points == nil || math.IsNaN(*j.Points) || math.IsInf(*j.Points, 0) || *j.Points < 0 || *j.Points > limits[key] {
				return s, errors.New("human points are missing or outside rubric")
			}
		} else if j.Points != nil {
			return s, errors.New("unassessable human judgment must have null points")
		}
		s.Reviewed++
		if (j.Points == nil) != (original.Points == nil) {
			s.AssessabilityMismatches++
		} else if j.Points == nil || math.Abs(*j.Points-*original.Points) < 1e-9 {
			s.ExactScoreMatches++
		}
		if !*j.EvidenceSupported {
			s.UnsupportedEvidence++
		}
		if !*j.FeedbackUseful {
			s.UnhelpfulFeedback++
		}
		if !*j.UncertaintyAppropriate {
			s.InappropriateUncertainty++
		}
	}
	if s.Reviewed > 0 || s.PolicyReviewComplete {
		if strings.TrimSpace(w.Reviewer) == "" || len(w.Reviewer) > 200 {
			return s, errors.New("reviewer is required")
		}
		if _, err := time.Parse(time.RFC3339, w.ReviewedAt); err != nil {
			return s, errors.New("reviewed_at must be an RFC3339 timestamp")
		}
	}
	s.Complete = r.Complete && s.Required > 0 && s.Reviewed == s.Required
	return s, nil
}

// Re-extract only bundled synthetic files. Matching self-reported hashes alone
// would allow a report to substitute unrelated source under a known case ID.
func corpusArtifacts(c Case) ([]assessment.Artifact, error) {
	dir, err := os.MkdirTemp("", "cairn-eval-check-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	if err = (checkout(c.Files)).FetchRevision(context.Background(), adapter.RepoRef{}, "", dir); err != nil {
		return nil, err
	}
	return assessment.Extract(dir, c.Rubric.Paths), nil
}
