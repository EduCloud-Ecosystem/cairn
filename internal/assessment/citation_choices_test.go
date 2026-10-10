// SPDX-License-Identifier: AGPL-3.0-or-later
package assessment

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestCitationChoicesAreExactAndContextual(t *testing.T) {
	d := inputDocument(t, "first = KMeans(\n    n_init=10,\n)\nsecond = KMeans(\n    n_init=10,\n)\n")
	a := d.Artifacts[0]
	choices := citationChoices(a)
	contexts := map[string]bool{}
	for _, q := range choices {
		if _, err := resolveCitationQuotes(a, q); err != nil {
			t.Fatal(err)
		}
		if q == "    n_init=10," {
			t.Fatal("ambiguous parameter offered")
		}
		if strings.Contains(q, "n_init=10") && strings.Contains(q, "KMeans(") {
			contexts[q] = true
		}
	}
	if !contexts["first = KMeans(\n    n_init=10,"] || !contexts["second = KMeans(\n    n_init=10,"] {
		t.Fatalf("expected both unique constructor contexts, got %v", choices)
	}
	if !reflect.DeepEqual(choices, citationChoices(a)) {
		t.Fatal("unstable choice order")
	}
	// Literal ellipses in source remain evidence; invented omissions do not.
	a = inputDocument(t, "before\n...\nafter").Artifacts[0]
	for _, q := range citationChoices(a) {
		if _, err := resolveCitationQuotes(a, q); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCitationChoicesRespectCellBoundariesAndUnicode(t *testing.T) {
	a := Artifact{Segments: []Segment{{Location: "cell:1", Text: "left = 1\nprint(left)"}, {Location: "cell:2", Text: "right = 2\nprint(right)"}}}
	for _, q := range citationChoices(a) {
		cs, err := resolveCitationQuotes(a, q)
		if err != nil || len(cs) != 1 {
			t.Fatalf("invalid cell choice: %v", err)
		}
		if strings.Contains(q, "left") && strings.Contains(q, "right") {
			t.Fatal("bridged cells")
		}
	}
	a = inputDocument(t, strings.Repeat("Ω", 650)+"終").Artifacts[0]
	choices := citationChoices(a)
	if len(choices) != 2 {
		t.Fatalf("long Unicode line lacks bounded excerpts: %d", len(choices))
	}
	for _, q := range choices {
		if !utf8.ValidString(q) || len(q) > 1000 {
			t.Fatal("invalid Unicode excerpt")
		}
	}
}

func TestCitationChoiceSchemaBindsArtifactsAndKeepsBlankWorkUnassessable(t *testing.T) {
	d := inputDocument(t, "first file operation")
	a := inputDocument(t, "second file operation").Artifacts[0]
	a.Path = "private-student-filename.py"
	d.Artifacts = append(d.Artifacts, a)
	p, _ := NewOpenAI("fixture", "")
	raw, err := p.request(d)
	if err != nil {
		t.Fatal(err)
	}
	var req struct {
		Text struct {
			Format struct{ Schema map[string]any }
		}
	}
	if err = json.Unmarshal(raw, &req); err != nil {
		t.Fatal(err)
	}
	props := req.Text.Format.Schema["properties"].(map[string]any)
	criteria := props["criteria"].(map[string]any)["items"].(map[string]any)["properties"].(map[string]any)
	branches := criteria["citations"].(map[string]any)["items"].(map[string]any)["anyOf"].([]any)
	if len(branches) != 2 {
		t.Fatal("missing artifact branches")
	}
	for i, b := range branches {
		fields := b.(map[string]any)["properties"].(map[string]any)
		id := fields["artifact_id"].(map[string]any)["enum"].([]any)
		ids := fields["excerpt_id"].(map[string]any)["enum"].([]any)
		description := fields["excerpt_id"].(map[string]any)["description"].(string)
		if len(id) != 1 || id[0] != fmt.Sprintf("artifact_%d", i+1) || len(ids) != 1 || ids[0] != "q1" || !strings.Contains(description, d.Artifacts[i].Segments[0].Text) {
			t.Fatal("cross-artifact quote choice")
		}
	}
	if strings.Contains(string(raw), a.Path) {
		t.Fatal("source path sent in schema")
	}
	blank, err := citationChoiceSchema(inputDocument(t, "\n \t\n"))
	if err != nil || blank["maxItems"] != 0 {
		t.Fatal("blank source can manufacture citation choices")
	}
}

func TestCitationSchemaLimitsArePreflightedWithoutSourceLoss(t *testing.T) {
	lines := []string{}
	for i := 0; i < 901; i++ {
		lines = append(lines, fmt.Sprintf("v%04d", i))
	}
	d := inputDocument(t, strings.Join(lines, "\n"))
	if _, err := providerInput(d); err != nil {
		t.Fatalf("fixture should fit source limit: %v", err)
	}
	size, err := PreflightProviderInput(d)
	if size == 0 || err == nil || !strings.Contains(err.Error(), "schema limits") {
		t.Fatalf("schema limit omitted from preflight: %d %v", size, err)
	}
	p, _ := NewOpenAI("fixture", "")
	if _, err = p.request(d); err == nil {
		t.Fatal("oversized schema accepted")
	}
}
func TestRepeatedSourceDoesNotOfferArbitraryOccurrences(t *testing.T) {
	d := inputDocument(t, strings.Repeat("same operation\n", 500))
	if choices := citationChoices(d.Artifacts[0]); len(choices) != 0 {
		t.Fatal("ambiguous repeated source offered as unique")
	}
}

func TestCitationCatalogByteLimit(t *testing.T) {
	d := inputDocument(t, "x")
	lines := []string{}
	for i := 0; i < 21; i++ {
		lines = append(lines, strings.Repeat("x", 700)+fmt.Sprintf("%02d", i))
	}
	a := inputDocument(t, strings.Join(lines, "\n")).Artifacts[0]
	d.Artifacts = []Artifact{a, a, a, a, a}
	if _, err := citationChoiceSchema(d); err == nil {
		t.Fatal("unbounded catalog accepted")
	}
}
func TestProviderExcerptChoicesAreResolvedAndValidatedLocally(t *testing.T) {
	d := inputDocument(t, "first = KMeans(\n    n_init=10,\n)\nsecond = KMeans(\n    n_init=10,\n)")
	choices := citationChoices(d.Artifacts[0])
	selected := ""
	for i, q := range choices {
		if q == "second = KMeans(\n    n_init=10," {
			selected = fmt.Sprintf("q%d", i+1)
		}
	}
	if selected == "" {
		t.Fatal("missing fixture context")
	}
	for _, mode := range []string{"valid", "unknown", "cross-artifact", "mixed"} {
		t.Run(mode, func(t *testing.T) {
			js := providerCriteria(5)
			js[0].CriterionID = "quality"
			c := providerCitation{ArtifactID: "artifact_1", ExcerptID: selected}
			want := ""
			switch mode {
			case "unknown":
				c.ExcerptID = "q99999"
				want = "invalid_citation_choice"
			case "cross-artifact":
				c.ArtifactID = "artifact_2"
				want = "invalid_citation"
			case "mixed":
				c.Quote = "first = KMeans("
				want = "invalid_citation_choice"
			}
			js[0].Citations = []providerCitation{c}
			p, _ := NewOpenAI("fixture", "")
			p.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) { return mockResponse(200, responseJSON(t, js)), nil })
			body, err := p.request(d)
			if err != nil {
				t.Fatal(err)
			}
			result, err := p.respond(context.Background(), body, d)
			if want != "" {
				if err == nil || !strings.Contains(err.Error(), want) || result.Usage == nil {
					t.Fatalf("expected %s with usage, got %v", want, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			cs := result.Criteria[0].Citations
			if len(cs) != 2 || cs[0].Location != "line:4" || cs[1].Location != "line:5" || cs[1].Quote != "    n_init=10," {
				t.Fatalf("wrong source binding: %+v", cs)
			}
		})
	}
}
