// SPDX-License-Identifier: AGPL-3.0-or-later
package assessment

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// PromptInstructionsDigest freezes the exact current provider instruction text.
// It makes no provider request and carries no student source.
func PromptInstructionsDigest() string { return DigestBytes([]byte(assessmentInstructions)) }

// FeedbackAudit is a review aid, never proof of correctness or grading authority.
// Named-operation checks cover only explicit backticked calls. Documentation
// checks classify lexical comment/string-only lines, not authorship or meaning.
type FeedbackAudit struct {
	RequiresSemanticReview bool                     `json:"requires_semantic_review"`
	Criteria               []FeedbackCriterionAudit `json:"criteria"`
}
type FeedbackCriterionAudit struct {
	CriterionID            string                     `json:"criterion_id"`
	UncitedNamedOperations []string                   `json:"uncited_named_operations"`
	DocumentationState     string                     `json:"documentation_state"`
	Documentation          []DocumentationObservation `json:"documentation"`
}
type DocumentationObservation struct {
	Path           string `json:"path"`
	Location       string `json:"location"`
	MatchesStarter bool   `json:"matches_captured_starter"`
}

var feedbackCode = regexp.MustCompile("`([^`]+)`")
var feedbackCall = regexp.MustCompile(`\b([A-Za-z_][A-Za-z0-9_]*)\s*\(`)
var documentationMention = regexp.MustCompile(`(?i)\b(docstrings?|documentation|comments?)\b`)

func AuditFeedbackEvidence(d Document, judgments []Judgment) (FeedbackAudit, error) {
	out := FeedbackAudit{RequiresSemanticReview: true, Criteria: []FeedbackCriterionAudit{}}
	// Supporting review packets can cover a rubric subset. Keep the captured
	// contract for those criteria and let validation reject unknown/duplicate IDs.
	subset := d
	subset.Rubric = d.Rubric
	subset.Rubric.Criteria = nil
	requested := map[string]bool{}
	for _, judgment := range judgments {
		requested[judgment.CriterionID] = true
	}
	for _, criterion := range d.Rubric.Criteria {
		if requested[criterion.ID] {
			subset.Rubric.Criteria = append(subset.Rubric.Criteria, criterion)
		}
	}
	if _, _, err := ValidateJudgments(subset, judgments, false); err != nil {
		return out, err
	}
	for _, j := range judgments {
		entry := FeedbackCriterionAudit{CriterionID: j.CriterionID, UncitedNamedOperations: []string{}, DocumentationState: "not_mentioned", Documentation: []DocumentationObservation{}}
		cited := ""
		for _, c := range j.Citations {
			for _, a := range d.Artifacts {
				if a.Path != c.Path {
					continue
				}
				for _, s := range a.Segments {
					if s.Location != c.Location {
						continue
					}
					text := c.Quote
					if text == "" {
						text = s.Text
					}
					cited += text + "\n"
					if !strings.HasSuffix(strings.ToLower(a.Path), ".py") {
						continue
					}
					line, err := strconv.Atoi(strings.TrimPrefix(s.Location, "line:"))
					masked, _ := pythonLexicalLines(string(a.Original))
					if err != nil || line < 1 || line > len(masked) || strings.TrimSpace(masked[line-1]) != "" {
						continue
					}
					// Ignore delimiter-only lines. Equal text remains only an overlap clue.
					normalized := strings.Join(strings.Fields(text), " ")
					if len(strings.Trim(normalized, "'\"# ")) < 8 {
						continue
					}
					starter := strings.Join(strings.Fields(d.Rubric.StarterFiles[a.Path]), " ")
					entry.Documentation = append(entry.Documentation, DocumentationObservation{a.Path, s.Location, starter != "" && strings.Contains(starter, normalized)})
				}
			}
		}
		seen := map[string]bool{}
		for _, code := range feedbackCode.FindAllStringSubmatch(j.Feedback, -1) {
			for _, call := range feedbackCall.FindAllStringSubmatch(code[1], -1) {
				name := call[1]
				if seen[name] {
					continue
				}
				seen[name] = true
				match, err := regexp.MatchString(`\b`+regexp.QuoteMeta(name)+`\s*\(`, cited)
				if err != nil {
					return out, fmt.Errorf("named-operation check: %w", err)
				}
				if !match {
					entry.UncitedNamedOperations = append(entry.UncitedNamedOperations, name)
				}
			}
		}
		if documentationMention.MatchString(j.Feedback) {
			entry.DocumentationState = "no_cited_comment_or_string_line"
			if len(entry.Documentation) > 0 {
				entry.DocumentationState = "instructor_attribution_review_required"
			}
			for _, observation := range entry.Documentation {
				if observation.MatchesStarter {
					entry.DocumentationState = "captured_starter_overlap_review_required"
				}
			}
		}
		out.Criteria = append(out.Criteria, entry)
	}
	return out, nil
}
