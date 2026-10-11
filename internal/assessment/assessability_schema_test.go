// SPDX-License-Identifier: AGPL-3.0-or-later
package assessment

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func criterionSchemas(t *testing.T, d Document) (map[string]map[string]any, map[string]any) {
	t.Helper()
	schema, err := proposalSchema(d)
	if err != nil {
		t.Fatal(err)
	}
	items := schema["properties"].(map[string]any)["criteria"].(map[string]any)["items"].(map[string]any)
	out := map[string]map[string]any{}
	for _, branch := range items["anyOf"].([]any) {
		p := branch.(map[string]any)["properties"].(map[string]any)
		id := p["criterion_id"].(map[string]any)["enum"].([]string)[0]
		out[id] = p
	}
	return out, schema
}

func TestCriterionSchemaRestrictsOnlyUnsupportedModes(t *testing.T) {
	d := inputDocument(t, "def doubled(x):\n    return x * 2\n")
	d.Rubric.StarterFiles = map[string]string{"answer.py": string(d.Artifacts[0].Original)}
	d.Rubric.Criteria = []Criterion{
		{ID: "authored", Description: "Changed operation", MaxPoints: 4, Evidence: PythonAuthored},
		{ID: "visible", Description: "Visible operation", MaxPoints: 4, Evidence: PythonImplementation},
		{ID: "general", Description: "General source", MaxPoints: 4},
	}
	branches, schema := criterionSchemas(t, d)
	if branches["authored"]["points"].(map[string]any)["type"] != "null" {
		t.Fatal("inherited operation may receive authored score")
	}
	for _, id := range []string{"visible", "general"} {
		if _, ok := branches[id]["points"].(map[string]any)["type"].([]string); !ok {
			t.Fatal("unrelated criterion forced null", id)
		}
	}
	if schema["$defs"].(map[string]any)["citations"] == nil {
		t.Fatal("shared catalog missing")
	}
	for _, p := range branches {
		if p["citations"].(map[string]any)["$ref"] != "#/$defs/citations" {
			t.Fatal("catalog duplicated in criterion")
		}
	}
	raw, _ := json.Marshal(schema)
	if strings.Count(string(raw), "Select an ID from this exact source catalog") != 1 {
		t.Fatal("source catalog repeated")
	}
}

func TestUnavailableCatalogAnchorRequiresNull(t *testing.T) {
	for _, source := range []string{
		"def f(x):\n    pass np.double(x)\n",
		"def f(x):\n    raise NotImplementedError\n",
		"# result = implementation()\n",
		"answer = \"" + strings.Repeat("x", 1100) + "\"\n",
	} {
		d := inputDocument(t, source)
		d.Rubric.Criteria[0].Evidence = PythonImplementation
		b, _ := criterionSchemas(t, d)
		if b["quality"]["points"].(map[string]any)["type"] != "null" {
			t.Fatalf("unselectable operation can receive points: %.40q", source)
		}
	}
}

func TestAvailableAnchorInAnotherArtifactAllowsScore(t *testing.T) {
	d := inputDocument(t, "# no operation\n")
	d.Rubric.Criteria[0].Evidence = PythonAuthored
	second := inputDocument(t, "return x * 2\n").Artifacts[0]
	second.Path = "other.py"
	d.Artifacts = append(d.Artifacts, second)
	d.Rubric.Paths = append(d.Rubric.Paths, second.Path)
	d.Rubric.StarterFiles = map[string]string{"answer.py": "# no operation\n", "other.py": "pass\n"}
	b, _ := criterionSchemas(t, d)
	if _, ok := b["quality"]["points"].(map[string]any)["type"].([]string); !ok {
		t.Fatal("eligible second artifact ignored")
	}
}

func TestNullSchemaDoesNotReplaceServerValidation(t *testing.T) {
	d := inputDocument(t, "def f(x):\n    pass np.double(x)\n")
	d.Rubric.Criteria[0].Evidence = PythonAuthored
	d.Rubric.StarterFiles = map[string]string{"answer.py": "def f(x):\n    pass\n"}
	p, _ := NewOpenAI("unused", "")
	req, err := p.request(d)
	if err != nil {
		t.Fatal(err)
	}
	for _, score := range []*float64{ptr(0), nil} {
		js := []providerJudgment{{CriterionID: "quality", Points: score, Feedback: "Malformed attempt needs instructor review", UncertaintyLevel: "high", UncertaintyReason: "No usable changed operation", Citations: []providerCitation{{ArtifactID: "artifact_1", Quote: "    pass np.double(x)"}}}}
		p.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) { return mockResponse(200, responseJSON(t, js)), nil })
		got, err := p.respond(context.Background(), req, d)
		if score != nil {
			if err == nil || err.Error() != providerFailure("invalid_proposal_citation_implementation").Error() {
				t.Fatal("invalid zero bypassed runtime check", err)
			}
		} else {
			if err != nil || got.Criteria[0].Points != nil {
				t.Fatal("null feedback rejected", err)
			}
		}
	}
}

func TestAssessableIncorrectImplementationMayReceiveZero(t *testing.T) {
	d := inputDocument(t, "def doubled(x):\n    return 0\n")
	d.Rubric.Criteria[0].Evidence = PythonAuthored
	d.Rubric.StarterFiles = map[string]string{"answer.py": "def doubled(x):\n    pass\n"}
	b, _ := criterionSchemas(t, d)
	if _, ok := b["quality"]["points"].(map[string]any)["type"].([]string); !ok {
		t.Fatal("incorrect assessable answer forced null")
	}
	j := implementationJudgment(d, 2, "return 0")
	j.Points = ptr(0)
	if _, _, err := ValidateJudgments(d, []Judgment{j}, false); err != nil {
		t.Fatal(err)
	}
}
