// SPDX-License-Identifier: AGPL-3.0-or-later
package assessment

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
)

func TestProviderRecordsSafeValidationReasons(t *testing.T) {
	for _, code := range []string{"criterion_coverage", "criterion_identity", "points_range", "feedback_bounds", "citation_missing", "citation_count"} {
		t.Run(code, func(t *testing.T) {
			svc, r, _ := fixture(t)
			provider, _ := NewOpenAI("SECRET", "")
			js := providerCriteria(7)
			switch code {
			case "criterion_coverage":
				js = nil
			case "criterion_identity":
				js[0].CriterionID = "SECRET"
			case "points_range":
				js[0].Points = ptr(999)
			case "feedback_bounds":
				js[0].Feedback = ""
			case "citation_missing":
				js[0].Citations = nil
			case "citation_count":
				for len(js[0].Citations) < 33 {
					js[0].Citations = append(js[0].Citations, js[0].Citations[0])
				}
			}
			calls := 0
			provider.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
				calls++
				return mockResponse(200, responseJSON(t, js)), nil
			})
			var d Document
			json.Unmarshal(r.Document, &d)
			g := NewGenerator(svc, provider, []string{"c"})
			_, err := g.Generate(context.Background(), r.ID, d.InputDigest)
			want := "invalid_proposal_" + code
			if err == nil || !strings.Contains(err.Error(), want) || strings.Contains(err.Error(), "SECRET") {
				t.Fatalf("unsafe or unhelpful diagnostic: %v", err)
			}
			attempt, err := svc.Store.GetGeneration(context.Background(), r.ID)
			if err != nil || attempt.ErrorCode != want || attempt.InputTokens != 100 || attempt.Status != "failed" {
				t.Fatalf("bad attempt %+v %v", attempt, err)
			}
			got, _ := svc.Store.GetAssessment(context.Background(), r.ID)
			if got.Status != "collected" {
				t.Fatal("invalid proposal persisted")
			}
			g.Generate(context.Background(), r.ID, d.InputDigest)
			if calls != 1 {
				t.Fatal("diagnostic change introduced retry")
			}
		})
	}
}
func TestUnknownValidationErrorNeverLeaksDetails(t *testing.T) {
	for _, err := range []error{errors.New("SECRET source"), invalidJudgment("SECRET", "SECRET")} {
		if got := judgmentFailureCode(err); got != "invalid_proposal" {
			t.Fatalf("unsafe classifier: %s", got)
		}
	}
}
