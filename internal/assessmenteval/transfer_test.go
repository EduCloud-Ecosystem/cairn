// SPDX-License-Identifier: AGPL-3.0-or-later
package assessmenteval

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/EduCloud-Ecosystem/cairn/internal/assessment"
	"github.com/EduCloud-Ecosystem/cairn/internal/store/memory"
)

func TestTransferCaptureUsesCoursePolicyAndKeepsHumanReviewPending(t *testing.T) {
	r, err := RunSuite(context.Background(), memory.New(), nil, 2, TransferVersion, func(Report) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if r.CorpusDigest != "d736502b0e3f23f1da3e918ffa0c407ddc93f5e8d0beef2927822d68b8ea91e6" {
		t.Fatal("frozen transfer corpus changed")
	}
	if len(r.Cases) != 9 || len(r.Results) != 18 || r.Metrics.Proposals != 0 || r.Metrics.UnexpectedOutcomes != 0 {
		t.Fatalf("unexpected transfer capture: %+v", r.Metrics)
	}
	if err := ValidateReport(r); err != nil {
		t.Fatal(err)
	}
	for _, v := range r.Results {
		if v.Document.PolicyVersion != assessment.PolicyVersion || v.Status != "collected" {
			t.Fatal("case did not use current course capture policy")
		}
	}
	raw, _ := json.Marshal(r)
	s, err := Review(raw, NewWorksheet(r, assessment.DigestBytes(raw)))
	if err != nil || s.Complete || s.Reviewed != 0 {
		t.Fatal("offline evidence inferred human acceptance")
	}
	old, _ := CorpusForVersion(Version)
	if assessment.Digest(old) != "73aa96e96738faa709e5b17710eb7c8547a72359b0448d5d9eba9904ce53c29a" {
		t.Fatal("V2 regression corpus changed")
	}
}
