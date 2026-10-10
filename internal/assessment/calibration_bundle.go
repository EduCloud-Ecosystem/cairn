// SPDX-License-Identifier: AGPL-3.0-or-later
package assessment

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/EduCloud-Ecosystem/cairn/internal/id"
	"github.com/EduCloud-Ecosystem/cairn/internal/store"
)

const MaxCalibrationBundleBytes = 900 << 10

const CalibrationBundleVersion = "cairn-calibration-bundle-v1"

// Bundles carry source only. Instructor identities, judgments and approvals
// cannot be imported. Repository provenance belongs in a separate private log.
type CalibrationBundle struct {
	Version  string                     `json:"version"`
	Purpose  string                     `json:"purpose"`
	Rubric   Rubric                     `json:"rubric"`
	Examples []CalibrationBundleExample `json:"examples"`
}
type CalibrationBundleExample struct {
	ID    string            `json:"id"`
	Files map[string]string `json:"files"`
}

func ValidateCalibrationBundle(b CalibrationBundle) error {
	if b.Version != CalibrationBundleVersion || (b.Purpose != "calibration" && b.Purpose != "holdout") || len(b.Examples) < 1 || len(b.Examples) > 9 {
		return errors.New("bundle requires the supported version, calibration or holdout purpose, and 1–9 examples")
	}
	if err := ValidateRubric(b.Rubric); err != nil {
		return err
	}
	ids, contents := map[string]bool{}, map[string]bool{}
	total := 0
	for _, e := range b.Examples {
		digest := Digest(e.Files)
		if !criterionID.MatchString(e.ID) || ids[e.ID] || contents[digest] {
			return errors.New("bundle needs unique sample IDs and distinct source content")
		}
		ids[e.ID], contents[digest] = true, true
		if len(e.Files) != len(b.Rubric.Paths) {
			return errors.New("every example must provide exactly the rubric paths")
		}
		sampleBytes := 0
		for _, path := range b.Rubric.Paths {
			source, ok := e.Files[path]
			if !ok || len(source) > MaxFileBytes {
				return errors.New("bundle source is missing or exceeds per-file limits")
			}
			sampleBytes += len(source)
		}
		if sampleBytes > MaxTotalBytes {
			return errors.New("bundle example exceeds total extraction limit")
		}
		total += sampleBytes
	}
	if total > MaxTotalBytes {
		return errors.New("bundle source exceeds 1 MiB; split into smaller profiles")
	}
	encoded, err := json.Marshal(b)
	if err != nil || len(encoded) > MaxCalibrationBundleBytes {
		return errors.New("encoded bundle exceeds 900 KiB; use a smaller sample")
	}
	return nil
}

// Import is atomic: extraction/validation of every example precedes one CAS
// write. It can only populate an empty draft owned by the signed-in instructor.
func (s Service) ImportCalibrationBundle(ctx context.Context, cid, owner string, revision int, b CalibrationBundle) (*store.Calibration, error) {
	c, d, err := s.calibration(ctx, cid, owner)
	if err != nil {
		return nil, err
	}
	if c.Status != "draft" || c.Revision != revision || len(d.Examples) != 0 {
		return nil, store.ErrConflict
	}
	if err = ValidateCalibrationBundle(b); err != nil {
		return nil, err
	}
	if Digest(b.Rubric) != Digest(d.Rubric) {
		return nil, errors.New("bundle rubric differs from this profile; review and save the intended rubric first")
	}
	used := map[string]bool{}
	if b.Purpose == "holdout" {
		if d.BasedOn == nil {
			return nil, errors.New("holdout import requires a fresh round based on an approved profile")
		}
		// Walk the lineage so a later round cannot relabel prior training examples
		// as held-out work. Exact content equality is not semantic deduplication.
		base := d.BasedOn
		visited := map[string]bool{}
		for base != nil {
			if visited[base.ID] || len(visited) >= 100 {
				return nil, errors.New("invalid or excessive calibration lineage")
			}
			visited[base.ID] = true
			_, prior, e := s.calibration(ctx, base.ID, owner)
			if e != nil {
				return nil, e
			}
			for _, example := range prior.Examples {
				files := map[string]string{}
				for _, artifact := range example.Document.Artifacts {
					files[artifact.Path] = string(artifact.Original)
				}
				used[Digest(files)] = true
			}
			base = prior.BasedOn
		}
	}
	for _, e := range b.Examples {
		if used[Digest(e.Files)] {
			return nil, errors.New("holdout source repeats a prior calibration example")
		}
		doc, err := uploadedCalibrationDocument(d.Rubric, d.BasedOn, owner, e.Files)
		if err != nil {
			return nil, err
		}
		d.Examples = append(d.Examples, CalibrationExample{ID: id.New(), SampleID: e.ID, Document: doc})
	}
	d.BundleDigest = Digest(b)
	d.SamplePurpose = b.Purpose
	return s.saveCalibration(ctx, c, d)
}
