// SPDX-License-Identifier: AGPL-3.0-or-later
package assessment

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func TestMultilineQuoteResolution(t *testing.T) {
	for _, tc := range []struct {
		name, source, quote string
		locations           []string
		code                string
	}{
		{"operations", "def run(X):\n    model.fit(X)\n    return model.predict(X)\n", "model.fit(X)\n    return model.predict(X)", []string{"line:2", "line:3"}, ""},
		{"partial ends", "prefix first\nsecond suffix", "first\nsecond", []string{"line:1", "line:2"}, ""},
		{"blank separators", "first\n \t\nlast\n", "first\n \t\nlast\n", []string{"line:1", "line:3"}, ""},
		{"CRLF preserved", "first\r\nlast\r\n", "first\r\nlast", []string{"line:1", "line:2"}, ""},
		{"CRLF not normalized", "first\r\nlast\r\n", "first\nlast", nil, "unmatched_citation_quote"},
		{"indent not repaired", "first\n    last", "first\nlast", nil, "unmatched_citation_quote"},
		{"missing line not joined", "first\nmiddle\nlast", "first\nlast", nil, "unmatched_citation_quote"},
		{"ellipsis not expanded", "first\nmiddle\nlast", "first\n...\nlast", nil, "unmatched_citation_quote"},
		{"duplicated span", "first\nlast\nfirst\nlast", "first\nlast", nil, "ambiguous_citation_quote"},
		{"overlapping span", "x\nx\nx", "x\nx", nil, "ambiguous_citation_quote"},
		{"unique span repeated lines", "first\nlast\nfirst\nother", "first\nlast", []string{"line:1", "line:2"}, ""},
		{"repeated parameter needs context", "first = KMeans(\n    n_init=10,\n)\nsecond = KMeans(\n    n_init=10,\n)", "    n_init=10,", nil, "ambiguous_citation_quote"},
		{"context disambiguates parameter", "first = KMeans(\n    n_init=10,\n)\nsecond = KMeans(\n    n_init=10,\n)", "second = KMeans(\n    n_init=10,", []string{"line:4", "line:5"}, ""},
		{"block boundary", strings.Repeat("# context\n", 19) + "first\nlast", "first\nlast", []string{"line:20", "line:21"}, ""},
		{"too many lines", "first\n" + strings.Repeat("x\n", 32), "first\n" + strings.Repeat("x\n", 32), nil, "invalid_citation_quote"},
		{"too long", "first\n" + strings.Repeat("x", 1000), "first\n" + strings.Repeat("x", 1000), nil, "invalid_citation_quote"},
		{"blank only", "\n \n", "\n \n", nil, "invalid_citation_quote"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := inputDocument(t, tc.source)
			citations, err := resolveCitationQuotes(d.Artifacts[0], tc.quote)
			if tc.code != "" {
				if err != providerFailure(tc.code) || len(citations) != 0 {
					t.Fatalf("expected %s, got %+v %v", tc.code, citations, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			locations := []string{}
			for _, c := range citations {
				locations = append(locations, c.Location)
			}
			if !reflect.DeepEqual(locations, tc.locations) {
				t.Fatalf("locations: %v", locations)
			}
			js := []Judgment{{CriterionID: "quality", Points: ptr(5), Feedback: "Supported by source.", Uncertainty: "Source only.", Citations: citations}}
			if _, _, err := ValidateJudgments(d, js, false); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestMultilineQuotesDoNotBridgeNonLineSegments(t *testing.T) {
	for _, segments := range [][]Segment{
		{{Location: "cell:1", Text: "first"}, {Location: "cell:2", Text: "last"}},
		{{Location: "line:1", Text: "first"}, {Location: "line:3", Text: "last"}},
		{{Location: "line:1", Text: "first"}, {Location: "cell:2", Text: "last"}},
	} {
		if _, err := resolveCitationQuotes(Artifact{Segments: segments}, "first\nlast"); err != providerFailure("unmatched_citation_quote") {
			t.Fatalf("joined separate segments: %v", err)
		}
	}
	a := Artifact{Segments: []Segment{{Location: "cell:1", Text: "first\nlast"}}}
	cs, err := resolveCitationQuotes(a, "first\nlast")
	if err != nil || len(cs) != 1 || cs[0].Location != "cell:1" || cs[0].Quote != "first\nlast" {
		t.Fatalf("notebook cell changed: %+v %v", cs, err)
	}
}

func TestProviderMultilineCitationsRequireReview(t *testing.T) {
	svc, _, _ := fixture(t)
	svc.Checkout = textCheckout("model.fit(X)\nreturn model.predict(X)\n")
	r, err := svc.Capture(context.Background(), "s", "teacher")
	if err != nil {
		t.Fatal(err)
	}
	var d Document
	if err := json.Unmarshal(r.Document, &d); err != nil {
		t.Fatal(err)
	}
	p, _ := NewOpenAI("fixture", "")
	calls := 0
	p.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		js := providerCriteria(7)
		js[0].Citations[0].Quote = "model.fit(X)\nreturn model.predict(X)"
		return mockResponse(200, responseJSON(t, js)), nil
	})
	g := NewGenerator(svc, p, []string{"c"})
	got, err := g.Generate(context.Background(), r.ID, d.InputDigest)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(got.Document, &d); err != nil {
		t.Fatal(err)
	}
	if got.Status != "pending" || d.Proposal.PromptVersion != PromptVersion || len(d.Proposal.Criteria[0].Citations) != 2 {
		t.Fatalf("unexpected proposal status %s or citation count", got.Status)
	}
	if _, err = g.Generate(context.Background(), r.ID, d.InputDigest); err == nil || calls != 1 {
		t.Fatal("duplicate provider call")
	}
}

func TestMultilineProviderQuoteCannotJoinArtifactsOrExceedCriterionLimit(t *testing.T) {
	for _, mode := range []string{"cross-artifact", "expanded-count"} {
		t.Run(mode, func(t *testing.T) {
			d := inputDocument(t, "first\nlast")
			js := providerCriteria(5)
			js[0].CriterionID = "quality"
			js[0].Citations[0].Quote = "first\nlast"
			want := "invalid_proposal_citation_count"
			if mode == "cross-artifact" {
				d = inputDocument(t, "first")
				other := inputDocument(t, "last").Artifacts[0]
				other.Path = "other.py"
				d.Artifacts = append(d.Artifacts, other)
				want = "unmatched_citation_quote"
			} else {
				for len(js[0].Citations) < 17 {
					js[0].Citations = append(js[0].Citations, js[0].Citations[0])
				}
			}
			p, _ := NewOpenAI("fixture", "")
			p.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) { return mockResponse(200, responseJSON(t, js)), nil })
			body, err := p.request(d)
			if err != nil {
				t.Fatal(err)
			}
			proposal, err := p.respond(context.Background(), body, d)
			if err != providerFailure(want) || proposal.Usage == nil {
				t.Fatalf("expected bounded %s with usage, got %v", want, err)
			}
		})
	}
}

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
