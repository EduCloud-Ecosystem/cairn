// SPDX-License-Identifier: AGPL-3.0-or-later
package assessment

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/EduCloud-Ecosystem/cairn/internal/store"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBundleImportIsAtomicOwnedAndUnreviewed(t *testing.T) {
	svc, _, _ := fixture(t)
	ctx := context.Background()
	c, err := svc.CreateCalibration(ctx, "a", "teacher")
	if err != nil {
		t.Fatal(err)
	}
	rubric := calibrationDoc(t, c).Rubric
	b := CalibrationBundle{Version: CalibrationBundleVersion, Purpose: "calibration", Rubric: rubric, Examples: []CalibrationBundleExample{{ID: "one", Files: map[string]string{"response.md": "First explanation."}}, {ID: "two", Files: map[string]string{"response.md": "bad\x00text"}}}}
	if _, err = svc.ImportCalibrationBundle(ctx, c.ID, "other", c.Revision, b); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("ownership bypass")
	}
	if _, err = svc.ImportCalibrationBundle(ctx, c.ID, "teacher", c.Revision, b); err == nil {
		t.Fatal("invalid second example accepted")
	}
	unchanged, _ := svc.Store.GetCalibration(ctx, c.ID, "teacher")
	if unchanged.Revision != 1 || len(calibrationDoc(t, unchanged).Examples) != 0 {
		t.Fatal("partial import persisted")
	}
	b.Examples[1].Files["response.md"] = "Second explanation."
	wrong := b
	wrong.Rubric.Title = "Different rubric"
	if _, err = svc.ImportCalibrationBundle(ctx, c.ID, "teacher", c.Revision, wrong); err == nil {
		t.Fatal("mismatched rubric imported")
	}
	c, err = svc.ImportCalibrationBundle(ctx, c.ID, "teacher", c.Revision, b)
	if err != nil {
		t.Fatal(err)
	}
	d := calibrationDoc(t, c)
	if c.Status != "draft" || c.Revision != 2 || d.BundleDigest != Digest(b) || d.SamplePurpose != "calibration" || len(d.Examples) != 2 {
		t.Fatal("bad import state")
	}
	for _, e := range d.Examples {
		if e.Review != nil || e.Document.Proposal != nil || e.Generation != nil || e.SampleID == "" {
			t.Fatal("import fabricated judgment or generation")
		}
	}
	if _, err = svc.AddCalibrationExample(ctx, c.ID, "teacher", c.Revision, map[string]string{"response.md": "extra"}); !errors.Is(err, store.ErrConflict) {
		t.Fatal("sample membership changed")
	}
	if _, err = svc.CaptureCalibrationExample(ctx, c.ID, "teacher", "s", revision, c.Revision); !errors.Is(err, store.ErrConflict) {
		t.Fatal("capture changed sample membership")
	}
	if _, err = svc.Store.LatestGradeForSubmission(ctx, "s"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("import published grade")
	}
}

func TestHoldoutRejectsExactSourceReuseAcrossLineage(t *testing.T) {
	svc, _, _ := fixture(t)
	ctx := context.Background()
	base, _ := svc.CreateCalibration(ctx, "a", "teacher")
	base, _ = svc.AddCalibrationExample(ctx, base.ID, "teacher", base.Revision, map[string]string{"response.md": "Previously calibrated source."})
	// Store a synthetic approved fixture; no human judgment is being claimed.
	base.Status = "ready"
	base.Revision++
	if err := svc.Store.UpdateCalibration(ctx, base, base.Revision-1); err != nil {
		t.Fatal(err)
	}
	round, err := svc.CreateCalibrationFrom(ctx, "a", "teacher", base.ID)
	if err != nil {
		t.Fatal(err)
	}
	b := CalibrationBundle{Version: CalibrationBundleVersion, Purpose: "holdout", Rubric: calibrationDoc(t, round).Rubric, Examples: []CalibrationBundleExample{{ID: "new-alias", Files: map[string]string{"response.md": "Previously calibrated source."}}}}
	if _, err = svc.ImportCalibrationBundle(ctx, round.ID, "teacher", round.Revision, b); err == nil {
		t.Fatal("renaming made training source held-out")
	}
	empty, _ := svc.CreateCalibration(ctx, "a", "teacher")
	if _, err = svc.ImportCalibrationBundle(ctx, empty.ID, "teacher", empty.Revision, b); err == nil {
		t.Fatal("holdout without base accepted")
	}
	// A second generation must still detect reuse from its grandparent.
	round.Status = "ready"
	round.Revision++
	svc.Store.UpdateCalibration(ctx, round, round.Revision-1)
	third, err := svc.CreateCalibrationFrom(ctx, "a", "teacher", round.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = svc.ImportCalibrationBundle(ctx, third.ID, "teacher", third.Revision, b); err == nil {
		t.Fatal("ancestor source reused")
	}
	b.Examples[0].Files["response.md"] = "Fresh reasoning."
	got, err := svc.ImportCalibrationBundle(ctx, third.ID, "teacher", third.Revision, b)
	if err != nil {
		t.Fatal(err)
	}
	if calibrationDoc(t, got).Examples[0].Document.Calibration.ID != round.ID {
		t.Fatal("holdout omitted selected guidance")
	}
}

func TestBundleValidationAndQuartoPassiveExtraction(t *testing.T) {
	for _, ext := range []string{"qmd", "Rmd"} {
		t.Run(ext, func(t *testing.T) {
			dir := t.TempDir()
			path := "answer." + ext
			source := "---\ntitle: review\n---\n```{r}\nsystem('touch MUST_NOT_EXIST')\n```\n{{< include secret.txt >}}\n<script>alert(1)</script>"
			os.WriteFile(filepath.Join(dir, path), []byte(source), 0600)
			artifacts := Extract(dir, []string{path})
			if artifacts[0].Issue != "" || artifacts[0].MediaType != "text/markdown" || string(artifacts[0].Original) != source {
				t.Fatal("source extraction failed")
			}
			if _, err := os.Stat(filepath.Join(dir, "MUST_NOT_EXIST")); !os.IsNotExist(err) {
				t.Fatal("source executed")
			}
			rubric := Rubric{Title: "Narrative", Paths: []string{path}, Criteria: []Criterion{{ID: "explain", Description: "Explain", MaxPoints: 5}}}
			b := CalibrationBundle{Version: CalibrationBundleVersion, Purpose: "calibration", Rubric: rubric, Examples: []CalibrationBundleExample{{ID: "one", Files: map[string]string{path: source}}}}
			if err := ValidateCalibrationBundle(b); err != nil {
				t.Fatal(err)
			}
			b.Examples = append(b.Examples, CalibrationBundleExample{ID: "renamed", Files: map[string]string{path: source}})
			if ValidateCalibrationBundle(b) == nil {
				t.Fatal("duplicate content accepted")
			}
			raw, _ := json.Marshal(b)
			if !strings.Contains(string(raw), "cairn-calibration-bundle") {
				t.Fatal("missing version")
			}
		})
	}
}

func TestBundleEncodedLimitIncludesJSONEscaping(t *testing.T) {
	b := CalibrationBundle{Version: CalibrationBundleVersion, Purpose: "calibration", Rubric: Rubric{Title: "Reason", Paths: []string{"response.md"}, Criteria: []Criterion{{ID: "reason", Description: "Explain", MaxPoints: 5}}}, Examples: []CalibrationBundleExample{{ID: "one", Files: map[string]string{"response.md": strings.Repeat("<", 200000)}}}}
	if err := ValidateCalibrationBundle(b); err == nil {
		t.Fatal("JSON escape expansion bypassed body bound")
	}
}
