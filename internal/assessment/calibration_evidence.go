// SPDX-License-Identifier: AGPL-3.0-or-later
package assessment

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/EduCloud-Ecosystem/cairn/internal/store"
	"github.com/EduCloud-Ecosystem/cairn/pkg/gradingspec"
)

type CalibrationExecutionObservation struct {
	CriterionID string `json:"criterion_id"`
	Status      string `json:"status"`
	ExitCode    *int   `json:"exit_code,omitempty"`
	Detail      string `json:"detail,omitempty"`
}
type CalibrationExecutionSample struct {
	SampleID     string                            `json:"sample_id"`
	SourceHashes map[string]string                 `json:"source_sha256"`
	Status       string                            `json:"status"`
	Tests        []CalibrationExecutionObservation `json:"tests,omitempty"`
}
type CalibrationExecutionReport struct {
	Completed         bool                         `json:"completed"`
	ExpectedSamples   int                          `json:"expected_samples"`
	UncheckedCriteria []string                     `json:"unchecked_criteria"`
	Version           string                       `json:"version"`
	CreatedAt         time.Time                    `json:"created_at"`
	BundleDigest      string                       `json:"bundle_sha256"`
	RubricDigest      string                       `json:"rubric_digest"`
	ChecksDigest      string                       `json:"checks_sha256"`
	Image             string                       `json:"image"`
	Isolation         string                       `json:"isolation"`
	Limits            gradingspec.Limits           `json:"limits"`
	Samples           []CalibrationExecutionSample `json:"samples"`
	Note              string                       `json:"note"`
}

const CalibrationReviewPacketVersion = "cairn-calibration-review-packet-v1"
const MaxCalibrationReviewPacketBytes = 256 << 10

// This is supporting material, never an imported instructor reference or approval.
// Rubric and source digests are checked; authorship and execution remain claims
// supplied by the importer. The server records the authenticated importing owner.
type CalibrationReviewPacket struct {
	Version      string                      `json:"version"`
	SampleID     string                      `json:"sample_id"`
	RubricDigest string                      `json:"rubric_digest"`
	SourceHashes map[string]string           `json:"source_sha256"`
	Authorship   string                      `json:"authorship"`
	Author       string                      `json:"author"`
	Note         string                      `json:"note"`
	Judgments    []Judgment                  `json:"judgments,omitempty"`
	Execution    *CalibrationExecutionReport `json:"execution,omitempty"`
}
type CalibrationExecutionEvidence struct {
	ReportDigest      string                     `json:"report_digest"`
	ChecksDigest      string                     `json:"checks_sha256"`
	BundleDigest      string                     `json:"bundle_sha256"`
	ReportedAt        time.Time                  `json:"reported_at"`
	Image             string                     `json:"image"`
	Sample            CalibrationExecutionSample `json:"sample"`
	UncheckedCriteria []string                   `json:"unchecked_criteria"`
}
type CalibrationReviewEvidence struct {
	PacketDigest string                        `json:"packet_digest"`
	ImportedBy   string                        `json:"imported_by"`
	ImportedAt   time.Time                     `json:"imported_at"`
	SampleID     string                        `json:"sample_id"`
	RubricDigest string                        `json:"rubric_digest"`
	SourceHashes map[string]string             `json:"source_sha256"`
	Authorship   string                        `json:"authorship"`
	Author       string                        `json:"author"`
	Note         string                        `json:"note"`
	Judgments    []Judgment                    `json:"judgments,omitempty"`
	Execution    *CalibrationExecutionEvidence `json:"execution,omitempty"`
}

func (e CalibrationExample) HasAssistedJudgments() bool {
	for _, record := range e.ReviewEvidence {
		if len(record.Judgments) > 0 {
			return true
		}
	}
	return false
}

var evidenceDigest = regexp.MustCompile(`^[a-f0-9]{64}$`)

func sourceHashesMatch(d Document, hashes map[string]string) bool {
	if len(hashes) != len(d.Artifacts) {
		return false
	}
	for _, a := range d.Artifacts {
		if a.Issue != "" || !evidenceDigest.MatchString(a.SHA256) || a.SHA256 != DigestBytes(a.Original) || hashes[a.Path] != a.SHA256 {
			return false
		}
	}
	return true
}

// Validation is shared with the offline packet builder. It does not execute
// checks, consult a provider, change proposals, or manufacture test attestations.
func ValidateCalibrationReviewPacket(d Document, p CalibrationReviewPacket) (CalibrationReviewEvidence, error) {
	var out CalibrationReviewEvidence
	raw, err := json.Marshal(p)
	if err != nil || len(raw) > MaxCalibrationReviewPacketBytes {
		return out, errors.New("review packet exceeds 256 KiB")
	}
	if p.Version != CalibrationReviewPacketVersion || !criterionID.MatchString(p.SampleID) || p.RubricDigest != Digest(d.Rubric) || !sourceHashesMatch(d, p.SourceHashes) {
		return out, errors.New("review packet must match the captured rubric and exact source files")
	}
	if strings.TrimSpace(p.Author) == "" || len(p.Author) > 200 || strings.TrimSpace(p.Note) == "" || len(p.Note) > 4000 {
		return out, errors.New("provide a bounded author label and review rationale")
	}
	switch p.Authorship {
	case "assistant", "instructor-assisted", "external-reviewer":
		if len(p.Judgments) == 0 {
			return out, errors.New("adjudication authorship requires judgments")
		}
	case "execution-only":
		if len(p.Judgments) != 0 || p.Execution == nil {
			return out, errors.New("execution-only packets cannot contain judgments")
		}
	default:
		return out, errors.New("select explicit assisted authorship; independent references cannot be imported")
	}
	// Partial adjudications are allowed, but omitted criteria remain omitted.
	if len(p.Judgments) > 0 {
		selected := map[string]bool{}
		for _, j := range p.Judgments {
			selected[j.CriterionID] = true
		}
		subset := d
		subset.Rubric.Criteria = nil
		for _, c := range d.Rubric.Criteria {
			if selected[c.ID] {
				subset.Rubric.Criteria = append(subset.Rubric.Criteria, c)
			}
		}
		if _, _, err = ValidateJudgments(subset, p.Judgments, false); err != nil {
			return out, err
		}
		for _, j := range p.Judgments {
			for _, c := range j.Citations {
				if strings.TrimSpace(c.Quote) == "" {
					return out, errors.New("imported judgments require exact source quotes")
				}
			}
		}
	}
	out = CalibrationReviewEvidence{PacketDigest: Digest(p), SampleID: p.SampleID, RubricDigest: p.RubricDigest, SourceHashes: p.SourceHashes, Authorship: p.Authorship, Author: p.Author, Note: p.Note, Judgments: p.Judgments}
	out.Judgments = append([]Judgment(nil), p.Judgments...)
	for i := range out.Judgments {
		if out.Judgments[i].Citations == nil {
			out.Judgments[i].Citations = []Citation{}
		}
	}
	if p.Execution != nil {
		r := p.Execution
		if r.Version != "cairn-calibration-execution-v1" || !r.Completed || r.ExpectedSamples != len(r.Samples) || len(r.Samples) < 1 || len(r.Samples) > 9 || r.RubricDigest != p.RubricDigest || !evidenceDigest.MatchString(r.ChecksDigest) || !evidenceDigest.MatchString(r.BundleDigest) || !strings.HasPrefix(r.Image, "sha256:") || !evidenceDigest.MatchString(strings.TrimPrefix(r.Image, "sha256:")) || r.CreatedAt.IsZero() {
			return CalibrationReviewEvidence{}, errors.New("execution report must be complete and identify its rubric, checks, bundle and pinned image")
		}
		ids := map[string]bool{}
		var sample *CalibrationExecutionSample
		for i, s := range r.Samples {
			if !criterionID.MatchString(s.SampleID) || ids[s.SampleID] {
				return CalibrationReviewEvidence{}, errors.New("duplicate or invalid execution sample")
			}
			ids[s.SampleID] = true
			if s.SampleID == p.SampleID {
				sample = &r.Samples[i]
			}
		}
		if sample == nil || sample.Status != "completed" || !sourceHashesMatch(d, sample.SourceHashes) {
			return CalibrationReviewEvidence{}, errors.New("execution sample does not match captured source")
		}
		seen := map[string]bool{}
		valid := map[string]bool{}
		for _, c := range d.Rubric.Criteria {
			valid[c.ID] = true
		}
		if len(sample.Tests) == 0 || len(sample.Tests) > 32 {
			return CalibrationReviewEvidence{}, errors.New("execution sample needs 1–32 observations")
		}
		for _, test := range sample.Tests {
			if !valid[test.CriterionID] || seen[test.CriterionID] || len(test.Detail) > 4000 {
				return CalibrationReviewEvidence{}, errors.New("invalid execution criterion or detail")
			}
			seen[test.CriterionID] = true
			switch test.Status {
			case "passed":
				if test.ExitCode == nil || *test.ExitCode != 0 {
					return CalibrationReviewEvidence{}, errors.New("passed observation requires exit code zero")
				}
			case "check_failed":
				if test.ExitCode == nil || (*test.ExitCode != 0 && *test.ExitCode != 1) {
					return CalibrationReviewEvidence{}, errors.New("failed check needs an assertion or output mismatch")
				}
			case "unassessable_execution_error":
				if test.ExitCode != nil && (*test.ExitCode == 0 || *test.ExitCode == 1) {
					return CalibrationReviewEvidence{}, errors.New("execution error has inconsistent exit code")
				}
			case "unassessable_timeout":
			default:
				return CalibrationReviewEvidence{}, errors.New("unknown execution outcome")
			}
		}
		for _, id := range r.UncheckedCriteria {
			if !valid[id] || seen[id] {
				return CalibrationReviewEvidence{}, errors.New("unchecked criteria overlap or are unknown")
			}
			seen[id] = true
		}
		if len(seen) != len(valid) {
			return CalibrationReviewEvidence{}, errors.New("execution report must account for unchecked criteria")
		}
		out.Execution = &CalibrationExecutionEvidence{ReportDigest: Digest(r), ChecksDigest: r.ChecksDigest, BundleDigest: r.BundleDigest, ReportedAt: r.CreatedAt, Image: r.Image, Sample: *sample, UncheckedCriteria: r.UncheckedCriteria}
	}
	return out, nil
}

func (s Service) ImportCalibrationReviewEvidence(ctx context.Context, cid, owner, eid string, revision int, p CalibrationReviewPacket) (*store.Calibration, error) {
	c, d, err := s.calibration(ctx, cid, owner)
	if err != nil {
		return nil, err
	}
	if c.Status != "draft" || c.Revision != revision {
		return nil, store.ErrConflict
	}
	current, err := s.Store.GetAssessmentRubric(ctx, c.AssignmentID)
	if err != nil {
		return nil, err
	}
	if current.Digest != c.RubricDigest {
		return nil, errors.New("parent rubric changed; start a fresh calibration")
	}
	for i := range d.Examples {
		e := &d.Examples[i]
		if e.ID != eid {
			continue
		}
		if e.ExclusionNote != "" || e.Review != nil || len(e.ReviewEvidence) >= 4 {
			return nil, errors.New("attach at most four evidence records before final instructor review")
		}
		if e.SampleID != "" && e.SampleID != p.SampleID {
			return nil, errors.New("packet sample ID differs from this imported example")
		}
		record, err := ValidateCalibrationReviewPacket(e.Document, p)
		if err != nil {
			return nil, err
		}
		for _, old := range e.ReviewEvidence {
			if old.PacketDigest == record.PacketDigest {
				return nil, store.ErrConflict
			}
		}
		record.ImportedBy = owner
		record.ImportedAt = time.Now().UTC()
		e.ReviewEvidence = append(e.ReviewEvidence, record)
		return s.saveCalibration(ctx, c, d)
	}
	return nil, store.ErrNotFound
}

// PrepareCalibrationReviewPacket binds local supporting material to one source
// bundle example. It performs passive extraction only, never executes source.
func PrepareCalibrationReviewPacket(b CalibrationBundle, p CalibrationReviewPacket) (CalibrationReviewPacket, error) {
	if err := ValidateCalibrationBundle(b); err != nil {
		return p, err
	}
	for _, e := range b.Examples {
		if e.ID != p.SampleID {
			continue
		}
		d, err := uploadedCalibrationDocument(b.Rubric, nil, "offline-review-packet", e.Files)
		if err != nil {
			return p, err
		}
		p.Version = CalibrationReviewPacketVersion
		p.RubricDigest = Digest(b.Rubric)
		p.SourceHashes = map[string]string{}
		for _, a := range d.Artifacts {
			p.SourceHashes[a.Path] = a.SHA256
		}
		_, err = ValidateCalibrationReviewPacket(d, p)
		return p, err
	}
	return p, errors.New("sample not found in source bundle")
}
