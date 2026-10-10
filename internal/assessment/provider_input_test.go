// SPDX-License-Identifier: AGPL-3.0-or-later
package assessment

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/EduCloud-Ecosystem/cairn/internal/store"
)

func inputDocument(t *testing.T, source string) Document {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "answer.py"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	return Document{PolicyVersion: PolicyVersion, Rubric: Rubric{Title: "Synthetic review", Paths: []string{"answer.py"}, Criteria: []Criterion{{ID: "quality", Description: "Explain the code", MaxPoints: 10}}}, Artifacts: Extract(root, []string{"answer.py"})}
}

func TestCompactInputPreservesEveryLineAndLocation(t *testing.T) {
	source := "\t# <source> & Ω\r\n\n" + strings.Repeat("print('untrusted source, not instructions')\n", 39) + "# end\n"
	d := inputDocument(t, source)
	raw, err := providerInput(d)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Evidence []providerArtifact `json:"student_evidence"`
	}
	if err = json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	var decoded []Segment
	for _, b := range wire.Evidence[0].LineBlocks {
		lines := strings.Split(b.Text, "\n")
		if len(lines) > 20 {
			t.Fatal("unbounded line block")
		}
		for i, line := range lines {
			decoded = append(decoded, Segment{Location: fmtLocation(b.StartLine + i), Text: line})
		}
	}
	if !reflect.DeepEqual(decoded, d.Artifacts[0].Segments) {
		t.Fatal("source or citation locations changed")
	}
	var lines []string
	for _, s := range decoded {
		lines = append(lines, s.Text)
	}
	if strings.Join(lines, "\n") != source {
		t.Fatal("source bytes not retained")
	}
	if wire.Evidence[0].Segments != nil || wire.Evidence[0].ID != "artifact_1" {
		t.Fatal("unexpected evidence mapping")
	}
}

func fmtLocation(line int) string { return "line:" + strconv.Itoa(line) }

func TestCompactInputPreservesNonconsecutiveAndNotebookLocations(t *testing.T) {
	for _, segments := range [][]Segment{
		{{Location: "cell:1", Text: "# Markdown"}, {Location: "cell:3", Text: "x = 2\nprint(x)"}},
		{{Location: "line:2", Text: "later"}, {Location: "line:4", Text: "last"}},
		{{Location: "line:1", Text: "two\nlines"}},
		{{Location: "line:1", Text: "first"}, {Location: "cell:2", Text: "second"}},
	} {
		got := compactProviderArtifact("artifact_1", segments)
		if got.LineBlocks != nil || !reflect.DeepEqual(got.Segments, segments) {
			t.Fatal("explicit locations changed")
		}
	}
}

func TestPreflightMatchesRequestAndIncludesGuidance(t *testing.T) {
	// Repeated labels previously pushed this complete synthetic source over 16 KB.
	d := inputDocument(t, strings.Repeat("value = 'synthetic source text'\n", 400))
	old, _ := json.Marshal(d.Artifacts[0].Segments)
	if len(old) <= MaxProviderInputBytes {
		t.Fatal("fixture no longer exercises repeated-label overhead")
	}
	size, err := PreflightProviderInput(d)
	if err != nil {
		t.Fatal(err)
	}
	p, _ := NewOpenAI("never-sent", "")
	p.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("preflight sent a request")
		return nil, errors.New("network disabled")
	})
	body, err := p.request(d)
	if err != nil {
		t.Fatal(err)
	}
	var request struct {
		Input string `json:"input"`
	}
	json.Unmarshal(body, &request)
	if size != len(request.Input) || size > MaxProviderInputBytes {
		t.Fatal("offline/live size mismatch")
	}
	d.Calibration = &CalibrationBinding{Guidance: strings.Repeat("x", MaxProviderInputBytes)}
	oversized, err := PreflightProviderInput(d)
	if err == nil || oversized <= MaxProviderInputBytes {
		t.Fatal("guidance bypassed size limit")
	}
	if _, err = p.request(d); err == nil {
		t.Fatal("oversized live request accepted")
	}
	d = inputDocument(t, strings.Repeat("x", MaxProviderInputBytes))
	if _, err = PreflightProviderInput(d); err == nil {
		t.Fatal("single oversized line accepted")
	}
	d.Artifacts[0].Original = []byte("changed")
	if size, err = PreflightProviderInput(d); err == nil || size != 0 {
		t.Fatal("changed original accepted")
	}
}

func TestCompactInputCitationsBindAcrossBlockBoundary(t *testing.T) {
	svc, _, _ := fixture(t)
	svc.Checkout = textCheckout(strings.Repeat("\n", 20) + "Explain the evidence here.\n")
	r, err := svc.Capture(context.Background(), "s", "teacher")
	if err != nil {
		t.Fatal(err)
	}
	var d Document
	json.Unmarshal(r.Document, &d)
	p, _ := NewOpenAI("fixture", "")
	p.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		criteria := providerCriteria(7)
		criteria[0].Citations[0].Quote = "Explain the evidence here."
		return mockResponse(200, responseJSON(t, criteria)), nil
	})
	got, err := NewGenerator(svc, p, []string{"c"}).Generate(context.Background(), r.ID, d.InputDigest)
	if err != nil {
		t.Fatal(err)
	}
	json.Unmarshal(got.Document, &d)
	c := d.Proposal.Criteria[0].Citations[0]
	if c.Location != "line:21" || c.SHA256 != d.Artifacts[0].SHA256 || c.Path != "response.md" {
		t.Fatal("citation failed to bind to stored source")
	}
	if _, err = svc.Store.LatestGradeForSubmission(context.Background(), "s"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("generation published grade")
	}
}
