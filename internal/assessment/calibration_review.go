// SPDX-License-Identifier: AGPL-3.0-or-later
package assessment

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/EduCloud-Ecosystem/cairn/internal/store"
	"strings"
	"time"
)

// A section may narrow evidence paths and criteria, never change the parent's
// criterion contract or points. The stored row stays bound to the parent digest.
func validateSectionRubric(parent, section Rubric) error {
	if err := ValidateRubric(section); err != nil {
		return err
	}
	paths := map[string]bool{}
	for _, p := range parent.Paths {
		paths[p] = true
	}
	for _, p := range section.Paths {
		if !paths[p] {
			return errors.New("section paths must belong to the parent rubric")
		}
	}
	criteria := map[string]Criterion{}
	for _, c := range parent.Criteria {
		criteria[c.ID] = c
	}
	for _, c := range section.Criteria {
		if want, ok := criteria[c.ID]; !ok || want != c {
			return errors.New("section criteria must preserve the parent descriptions and points")
		}
	}
	return nil
}

// Reference judgments are immutable once saved and cannot be introduced after
// any provider attempt, including failures. They are never sent to the model.
func (s Service) SaveCalibrationReference(ctx context.Context, cid, owner, eid string, revision int, criteria []Judgment, note string) (*store.Calibration, error) {
	c, d, err := s.calibration(ctx, cid, owner)
	if err != nil {
		return nil, err
	}
	if c.Status != "draft" || c.Revision != revision {
		return nil, store.ErrConflict
	}
	if strings.TrimSpace(note) == "" || len(note) > 4000 {
		return nil, errors.New("provide a bounded reference rationale")
	}
	for i, e := range d.Examples {
		if e.ID != eid {
			continue
		}
		if e.Reference != nil || e.Document.Proposal != nil || e.ExclusionNote != "" || e.HasAssistedJudgments() {
			return nil, store.ErrConflict
		}
		if _, err = s.Store.GetGeneration(ctx, "calibration:"+cid+":"+eid); !errors.Is(err, store.ErrNotFound) {
			if err != nil {
				return nil, err
			}
			return nil, errors.New("reference must be saved before any model attempt")
		}
		if _, _, err = ValidateJudgments(e.Document, criteria, false); err != nil {
			return nil, err
		}
		d.Examples[i].Reference = &Review{Reviewer: owner, ReviewedAt: time.Now().UTC(), Note: note, Criteria: criteria}
		return s.saveCalibration(ctx, c, d)
	}
	return nil, store.ErrNotFound
}

type CalibrationInputCheck struct {
	ExampleID  string `json:"example_id"`
	InputBytes int    `json:"input_bytes"`
	LimitBytes int    `json:"limit_bytes"`
	Fits       bool   `json:"fits"`
	Issue      string `json:"issue,omitempty"`
}

func calibrationInputChecks(d CalibrationDocument, guidance string) []CalibrationInputCheck {
	checks := []CalibrationInputCheck{}
	for _, e := range d.Examples {
		if e.ExclusionNote != "" {
			continue
		}
		doc := e.Document
		// This is a hypothetical future request, not a new bound capture or approval.
		doc.Calibration = &CalibrationBinding{Guidance: guidance}
		size, err := PreflightProviderInput(doc)
		check := CalibrationInputCheck{ExampleID: e.ID, InputBytes: size, LimitBytes: MaxProviderInputBytes, Fits: err == nil}
		if err != nil {
			check.Issue = err.Error()
		}
		checks = append(checks, check)
	}
	return checks
}
func (s Service) PreflightCalibration(ctx context.Context, cid, owner string, revision int, guidance string) ([]CalibrationInputCheck, error) {
	c, d, err := s.calibration(ctx, cid, owner)
	if err != nil {
		return nil, err
	}
	if c.Revision != revision {
		return nil, store.ErrConflict
	}
	if len(guidance) > 2000 {
		return nil, errors.New("instructor guidance exceeds 2000 bytes")
	}
	return calibrationInputChecks(d, strings.TrimSpace(guidance)), nil
}

type CalibrationCoverage struct {
	Complete      bool     `json:"complete"`
	Missing       []string `json:"missing"`
	Overlapping   []string `json:"overlapping"`
	CoveredPoints float64  `json:"covered_points"`
	MaxPoints     float64  `json:"max_points"`
}

// Explicit selection avoids choosing silently among multiple rounds. Coverage
// counts criterion maxima once and is not a student score or profile approval.
func (s Service) CalibrationCoverage(ctx context.Context, assignment, owner string, ids []string) (CalibrationCoverage, error) {
	result := CalibrationCoverage{Missing: []string{}, Overlapping: []string{}}
	if len(ids) > 32 {
		return result, errors.New("select at most 32 profiles")
	}
	current, err := s.Store.GetAssessmentRubric(ctx, assignment)
	if err != nil {
		return result, err
	}
	var parent Rubric
	if err = json.Unmarshal(current.Document, &parent); err != nil {
		return result, err
	}
	seen := map[string]bool{}
	counts := map[string]int{}
	for _, id := range ids {
		if seen[id] {
			return result, errors.New("select each profile once")
		}
		seen[id] = true
		c, d, e := s.calibration(ctx, id, owner)
		if e != nil {
			return result, e
		}
		if c.AssignmentID != assignment || c.RubricDigest != current.Digest || c.Status != "ready" || d.Model != DefaultOpenAIModel || d.PromptVersion != PromptVersion || d.PolicyVersion != PolicyVersion {
			return result, errors.New("select current approved profiles from this assignment")
		}
		if e = validateSectionRubric(parent, d.Rubric); e != nil {
			return result, e
		}
		for _, criterion := range d.Rubric.Criteria {
			counts[criterion.ID]++
		}
	}
	for _, c := range parent.Criteria {
		result.MaxPoints += c.MaxPoints
		if counts[c.ID] == 0 {
			result.Missing = append(result.Missing, c.ID)
		} else {
			result.CoveredPoints += c.MaxPoints
		}
		if counts[c.ID] > 1 {
			result.Overlapping = append(result.Overlapping, c.ID)
		}
	}
	result.Complete = len(result.Missing) == 0 && len(result.Overlapping) == 0
	return result, nil
}
