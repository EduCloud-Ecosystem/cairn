// SPDX-License-Identifier: AGPL-3.0-or-later
// Package assessment captures evidence and validates proposals. It deliberately
// treats a validated model proposal as structurally valid, not a verified judgment.
package assessment

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"path"
	"regexp"
	"strings"
	"time"
)

const MaxFileBytes = 256 << 10
const MaxTotalBytes = 1 << 20
const MaxFiles = 16
const ExtractorVersion = "source-v1"

type Criterion struct {
	ID          string  `json:"id"`
	Description string  `json:"description"`
	MaxPoints   float64 `json:"max_points"`
}
type Rubric struct {
	Title    string      `json:"title"`
	Paths    []string    `json:"paths"`
	Criteria []Criterion `json:"criteria"`
}
type Segment struct {
	Location string `json:"location"`
	Text     string `json:"text"`
}
type Artifact struct {
	Path      string    `json:"path"`
	MediaType string    `json:"media_type"`
	SHA256    string    `json:"sha256,omitempty"`
	Size      int       `json:"size"`
	Original  []byte    `json:"original,omitempty"`
	Extractor string    `json:"extractor"`
	Segments  []Segment `json:"segments"`
	Issue     string    `json:"issue,omitempty"`
}
type Citation struct {
	Path     string `json:"path"`
	SHA256   string `json:"sha256"`
	Location string `json:"location"`
}
type Judgment struct {
	CriterionID string     `json:"criterion_id"`
	Points      *float64   `json:"points"`
	Feedback    string     `json:"feedback"`
	Uncertainty string     `json:"uncertainty"`
	Citations   []Citation `json:"citations"`
}
type Proposal struct {
	SubmissionStatus string         `json:"submission_status,omitempty"`
	Usage            *ProviderUsage `json:"usage,omitempty"`
	Source           string         `json:"source"`
	Model            string         `json:"model"`
	PromptVersion    string         `json:"prompt_version"`
	InputDigest      string         `json:"input_digest"`
	Criteria         []Judgment     `json:"criteria"`
}
type Review struct {
	Reviewer   string     `json:"reviewer"`
	ReviewedAt time.Time  `json:"reviewed_at"`
	Note       string     `json:"note"`
	Criteria   []Judgment `json:"criteria,omitempty"`
	GradeID    string     `json:"grade_id,omitempty"`
}
type Document struct {
	PolicyVersion string     `json:"policy_version,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	CapturedBy    string     `json:"captured_by"`
	Revision      string     `json:"revision"`
	Rubric        Rubric     `json:"rubric"`
	Artifacts     []Artifact `json:"artifacts"`
	InputDigest   string     `json:"input_digest"`
	Proposal      *Proposal  `json:"proposal,omitempty"`
	Review        *Review    `json:"review,omitempty"`
}

func DigestBytes(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func Digest(v any) string         { b, _ := json.Marshal(v); return DigestBytes(b) }

var criterionID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

func ValidPath(p string) bool {
	if p == "" || len(p) > 240 || path.Clean(p) != p || strings.HasPrefix(p, "/") || strings.ContainsAny(p, "\\\x00\r\n") {
		return false
	}
	for _, part := range strings.Split(p, "/") {
		if part == ".." || strings.HasPrefix(part, ".") {
			return false
		}
	}
	return true
}
func ValidateRubric(r Rubric) error {
	if strings.TrimSpace(r.Title) == "" || len(r.Title) > 200 || len(r.Paths) < 1 || len(r.Paths) > MaxFiles || len(r.Criteria) < 1 || len(r.Criteria) > 32 {
		return fmt.Errorf("rubric needs a title, 1–16 artifact paths and 1–32 criteria")
	}
	seen := map[string]bool{}
	for _, p := range r.Paths {
		if !ValidPath(p) || seen[p] {
			return fmt.Errorf("invalid or duplicate artifact path %q", p)
		}
		seen[p] = true
	}
	seen = map[string]bool{}
	total := 0.0
	for _, c := range r.Criteria {
		if !criterionID.MatchString(c.ID) || seen[c.ID] || strings.TrimSpace(c.Description) == "" || len(c.Description) > 4000 || !finite(c.MaxPoints) || c.MaxPoints <= 0 || c.MaxPoints > 10000 {
			return fmt.Errorf("invalid or duplicate criterion %q", c.ID)
		}
		seen[c.ID] = true
		total += c.MaxPoints
	}
	if total > 10000 {
		return fmt.Errorf("rubric maximum exceeds 10000 points")
	}
	return nil
}
func finite(n float64) bool { return !math.IsNaN(n) && !math.IsInf(n, 0) }
func ValidateJudgments(d Document, js []Judgment, approval bool) (score, max float64, err error) {
	if len(js) != len(d.Rubric.Criteria) {
		return 0, 0, invalidJudgment("criterion_coverage", "every rubric criterion must appear exactly once")
	}
	limits := map[string]float64{}
	for _, c := range d.Rubric.Criteria {
		limits[c.ID] = c.MaxPoints
		max += c.MaxPoints
	}
	evidence := map[string]bool{}
	for _, a := range d.Artifacts {
		if a.Issue != "" {
			return 0, 0, invalidJudgment("artifact_incomplete", "resolve artifact issues before assessment")
		}
		if DigestBytes(a.Original) != a.SHA256 {
			return 0, 0, invalidJudgment("artifact_digest", "artifact digest mismatch")
		}
		for _, seg := range a.Segments {
			evidence[a.Path+"\x00"+a.SHA256+"\x00"+seg.Location] = true
		}
	}
	seen := map[string]bool{}
	for _, j := range js {
		limit, ok := limits[j.CriterionID]
		if !ok || seen[j.CriterionID] {
			return 0, 0, invalidJudgment("criterion_identity", "unknown or duplicate criterion")
		}
		seen[j.CriterionID] = true
		if strings.TrimSpace(j.Feedback) == "" || len(j.Feedback) > 8000 || strings.TrimSpace(j.Uncertainty) == "" || len(j.Uncertainty) > 2000 {
			return 0, 0, invalidJudgment("feedback_bounds", "bounded feedback and uncertainty are required")
		}
		if len(j.Citations) > 32 {
			return 0, 0, invalidJudgment("citation_count", "at most 32 citations per criterion are accepted")
		}
		if j.Points == nil {
			if approval {
				return 0, 0, invalidJudgment("unassessable", "unassessable criterion requires instructor resolution")
			}
		} else {
			if !finite(*j.Points) || *j.Points < 0 || *j.Points > limit {
				return 0, 0, invalidJudgment("points_range", "points outside instructor rubric")
			}
			if len(j.Citations) == 0 {
				return 0, 0, invalidJudgment("citation_missing", "scored criteria need evidence citations")
			}
			score += *j.Points
		}
		for _, c := range j.Citations {
			if !evidence[c.Path+"\x00"+c.SHA256+"\x00"+c.Location] {
				return 0, 0, invalidJudgment("citation_location", "citation does not match captured evidence")
			}
		}
	}
	return score, max, nil
}
func ValidateProposal(d Document, p Proposal) error {
	if p.Usage != nil || (p.Source != "fixture" && p.Source != "instructor-import") {
		return fmt.Errorf("import source must be fixture or instructor-import, without provider attestation")
	}
	if p.InputDigest != d.InputDigest || strings.TrimSpace(p.Model) == "" || len(p.Model) > 200 || strings.TrimSpace(p.PromptVersion) == "" || len(p.PromptVersion) > 200 {
		return fmt.Errorf("proposal provenance does not match captured input")
	}
	return ValidateProposalJudgments(d, p)
}
