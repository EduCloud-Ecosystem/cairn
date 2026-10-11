// SPDX-License-Identifier: AGPL-3.0-or-later
package assessment

import "errors"

// No evidence, criterion text, filenames or provider response enters these codes.
// Keep the classifier fail-closed if additional validation errors are introduced.
type judgmentError struct{ code, message string }

func (e judgmentError) Error() string            { return e.message }
func invalidJudgment(code, message string) error { return judgmentError{code: code, message: message} }
func judgmentFailureCode(err error) string {
	var e judgmentError
	if errors.As(err, &e) {
		switch e.code {
		case "evidence_policy", "citation_implementation":
			return "invalid_proposal_" + e.code
		case "assessment_policy", "submission_status", "missing_work_policy", "criterion_coverage", "criterion_identity", "artifact_incomplete", "artifact_digest", "feedback_bounds", "unassessable", "points_range", "citation_missing", "citation_location", "citation_count", "citation_quote":
			return "invalid_proposal_" + e.code
		}
	}
	return "invalid_proposal"
}
