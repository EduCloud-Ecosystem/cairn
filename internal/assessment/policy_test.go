// SPDX-License-Identifier: AGPL-3.0-or-later
package assessment

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/EduCloud-Ecosystem/cairn/internal/store"
)

func TestProviderUncertaintyCannotInvertConfidence(t *testing.T) {
	for _, tc := range []struct {
		level, reason string
		valid         bool
	}{
		{"low", "The numeric expressions explicitly supply both values.", true},
		{"medium", "Code and explanation disagree about the denominator.", true},
		{"high", "The required method is absent from the source.", true},
		{"low", "Low confidence; the relevant numeric expressions are explicit in the source.", false},
		{"low", "High uncertainty despite explicit evidence.", false},
		{"certain", "Explicit source.", false},
		{"low", "", false},
	} {
		t.Run(tc.level+tc.reason, func(t *testing.T) {
			svc, r, _ := fixture(t)
			provider, _ := NewOpenAI("fixture", "")
			js := providerCriteria(7)
			js[0].UncertaintyLevel, js[0].UncertaintyReason = tc.level, tc.reason
			provider.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) { return mockResponse(200, responseJSON(t, js)), nil })
			var d Document
			json.Unmarshal(r.Document, &d)
			got, err := NewGenerator(svc, provider, []string{"c"}).Generate(context.Background(), r.ID, d.InputDigest)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v err=%v", tc.valid, err)
			}
			if tc.valid {
				json.Unmarshal(got.Document, &d)
				want := strings.ToUpper(tc.level[:1]) + tc.level[1:] + " uncertainty: " + tc.reason
				if d.Proposal.Criteria[0].Uncertainty != want {
					t.Fatal("noncanonical uncertainty persisted")
				}
			} else {
				attempt, _ := svc.Store.GetGeneration(context.Background(), r.ID)
				if attempt.ErrorCode != "invalid_uncertainty" || attempt.InputTokens != 100 {
					t.Fatal("lost safe diagnostic or usage")
				}
			}
		})
	}
}

func TestCapturedPolicyAndInstructorResolution(t *testing.T) {
	ctx := context.Background()
	svc, r, p := fixture(t)
	var d Document
	json.Unmarshal(r.Document, &d)
	if d.PolicyVersion != PolicyVersion || d.InputDigest != InputDigest(d) {
		t.Fatal("policy not captured and bound")
	}
	legacy := d
	legacy.PolicyVersion = ""
	if InputDigest(legacy) == d.InputDigest {
		t.Fatal("policy omitted from input binding")
	}
	provider, _ := NewOpenAI("fixture", "")
	if _, err := provider.request(legacy); err == nil {
		t.Fatal("legacy capture used with new prompt")
	}
	request, err := provider.request(d)
	if err != nil || !strings.Contains(string(request), "Never assign zeros in that situation") {
		t.Fatal("course request omitted policy")
	}
	p.SubmissionStatus = "no_relevant_work"
	if _, err := svc.Propose(ctx, r.ID, p); err == nil {
		t.Fatal("numeric missing-work proposal accepted")
	}
	p.Criteria[0].Points = nil
	if _, err := svc.Propose(ctx, r.ID, p); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Review(ctx, r.ID, "teacher", "approve", "Unresolved", p.Criteria); err == nil {
		t.Fatal("null published as zero")
	}
	if _, err := svc.Store.LatestGradeForSubmission(ctx, "s"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("grade published before resolution")
	}
	p.Criteria[0].Points = ptr(6)
	got, err := svc.Review(ctx, r.ID, "teacher", "approve", "Relevant answer found in captured evidence; corrected model classification", p.Criteria)
	if err != nil {
		t.Fatal(err)
	}
	json.Unmarshal(got.Document, &d)
	if d.Proposal.Criteria[0].Points != nil || d.Proposal.SubmissionStatus != "no_relevant_work" || *d.Review.Criteria[0].Points != 6 {
		t.Fatal("review overwrote original classification")
	}
}

func TestBlankSourceCannotBeScoredEvenWhenModelClaimsRelevantWork(t *testing.T) {
	svc, _, _ := fixture(t)
	svc.Checkout = textCheckout(" \n\t\n")
	r, err := svc.Capture(context.Background(), "s", "teacher")
	if err != nil || r.Status != "collected" {
		t.Fatalf("blank readable source was blocked: %v", err)
	}
	var d Document
	json.Unmarshal(r.Document, &d)
	provider, _ := NewOpenAI("fixture", "")
	provider.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		js := providerCriteria(0)
		js[0].Citations = nil
		return mockResponse(200, responseJSON(t, js)), nil
	})
	_, err = NewGenerator(svc, provider, []string{"c"}).Generate(context.Background(), r.ID, d.InputDigest)
	if err == nil || !strings.Contains(err.Error(), "invalid_proposal_submission_status") {
		t.Fatalf("blank scored: %v", err)
	}
}

func TestLegacyProposalRemainsReviewable(t *testing.T) {
	_, r, p := fixture(t)
	var d Document
	json.Unmarshal(r.Document, &d)
	d.PolicyVersion = ""
	d.InputDigest = InputDigest(d)
	p.InputDigest = d.InputDigest
	p.SubmissionStatus = ""
	if err := ValidateProposal(d, p); err != nil {
		t.Fatal(err)
	}
}
