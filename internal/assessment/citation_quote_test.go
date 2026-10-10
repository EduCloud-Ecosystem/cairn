// SPDX-License-Identifier: AGPL-3.0-or-later
package assessment

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestQuoteResolutionUsesExactUniqueSegment(t *testing.T) {
	d := inputDocument(t, "# starter\r\n\ndef mean(values):\n    return sum(values) / len(values)\n# duplicate\n# duplicate\n")
	a := d.Artifacts[0]
	c, err := resolveCitationQuote(a, "return sum(values) / len(values)")
	if err != nil || c.Location != "line:4" || c.SHA256 != a.SHA256 || c.Quote == "" {
		t.Fatalf("resolved citation: %+v %v", c, err)
	}
	for _, quote := range []string{"", " \t", "invented operation", "# duplicate", strings.Repeat("a", 1001)} {
		if _, err := resolveCitationQuote(a, quote); err == nil {
			t.Fatal("invalid or ambiguous quote accepted")
		}
	}
	notebook := Artifact{Path: "answer.ipynb", SHA256: "digest", Segments: []Segment{{Location: "cell:1", Text: "x = 2\nprint(x)"}, {Location: "cell:2", Text: "x = 3"}}}
	c, err = resolveCitationQuote(notebook, "x = 2\nprint(x)")
	if err != nil || c.Location != "cell:1" {
		t.Fatal("notebook segment failed")
	}
	js := []Judgment{{CriterionID: "quality", Points: ptr(5), Feedback: "Implementation is direct.", Uncertainty: "Source only.", Citations: []Citation{c}}}
	// Imported/manual proposals cannot bind a real quote to a different source line.
	c, _ = resolveCitationQuote(a, "return sum(values) / len(values)")
	c.Location = "line:1"
	js[0].Citations = []Citation{c}
	if _, _, err = ValidateJudgments(d, js, false); err == nil {
		t.Fatal("quote/location mismatch accepted")
	}
	c.Location = "line:4"
	js[0].Citations = []Citation{c}
	if _, _, err = ValidateJudgments(d, js, false); err != nil {
		t.Fatal(err)
	}
}
func TestQuoteFailureRecordsUsageWithoutSourceOrRetry(t *testing.T) {
	for _, tc := range []struct{ quote, code string }{{"SECRET invented source", "unmatched_citation_quote"}, {"", "invalid_citation_quote"}, {"Evidence", "ambiguous_citation_quote"}} {
		t.Run(tc.code, func(t *testing.T) {
			svc, _, _ := fixture(t)
			svc.Checkout = textCheckout("Evidence and reasoning.\nEvidence repeated here.")
			r, err := svc.Capture(context.Background(), "s", "teacher")
			if err != nil {
				t.Fatal(err)
			}
			var d Document
			json.Unmarshal(r.Document, &d)
			p, _ := NewOpenAI("fixture", "")
			calls := 0
			p.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
				calls++
				js := providerCriteria(7)
				js[0].Citations[0].Quote = tc.quote
				return mockResponse(200, responseJSON(t, js)), nil
			})
			g := NewGenerator(svc, p, []string{"c"})
			_, err = g.Generate(context.Background(), r.ID, d.InputDigest)
			if err == nil || !strings.Contains(err.Error(), tc.code) || strings.Contains(err.Error(), "SECRET") {
				t.Fatalf("unsafe diagnostic: %v", err)
			}
			attempt, err := svc.Store.GetGeneration(context.Background(), r.ID)
			if err != nil || attempt.ErrorCode != tc.code || attempt.InputTokens != 100 || attempt.Status != "failed" {
				t.Fatalf("failure audit: %+v %v", attempt, err)
			}
			g.Generate(context.Background(), r.ID, d.InputDigest)
			if calls != 1 {
				t.Fatal("failed citation retried")
			}
			saved, _ := svc.Store.GetAssessment(context.Background(), r.ID)
			if saved.Status != "collected" {
				t.Fatal("invalid quote saved")
			}
		})
	}
}
