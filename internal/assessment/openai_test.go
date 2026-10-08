// SPDX-License-Identifier: AGPL-3.0-or-later
package assessment

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EduCloud-Ecosystem/cairn/internal/store"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func mockResponse(code int, body string) *http.Response {
	return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
}
func responseJSON(t *testing.T, criteria any) string {
	t.Helper()
	text, _ := json.Marshal(map[string]any{"submission_status": "relevant_work", "criteria": criteria})
	raw, _ := json.Marshal(map[string]any{"id": "resp_fixture", "model": DefaultOpenAIModel, "status": "completed", "usage": map[string]int{"input_tokens": 100, "output_tokens": 200}, "output": []any{map[string]any{"type": "message", "content": []any{map[string]string{"type": "output_text", "text": string(text)}}}}})
	return string(raw)
}
func providerCriteria(points float64) []providerJudgment {
	return []providerJudgment{{CriterionID: "reason", Points: ptr(points), Feedback: "Use the evidence to support the conclusion.", UncertaintyLevel: "medium", UncertaintyReason: "Check interpretation with your instructor.", Citations: []providerCitation{{ArtifactID: "artifact_1", Location: "line:1"}}}}
}
func TestOpenAIGenerateValidatesAndRequiresReview(t *testing.T) {
	svc, r, _ := fixture(t)
	provider, _ := NewOpenAI("private-test-key", "")
	calls := 0
	provider.client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		if req.URL.String() != openAIEndpoint || req.Header.Get("Authorization") != "Bearer private-test-key" {
			t.Error("unexpected credential destination")
		}
		b, _ := io.ReadAll(req.Body)
		var body map[string]any
		json.Unmarshal(b, &body)
		if body["store"] != false || body["max_output_tokens"] != float64(MaxProviderOutputTokens) {
			t.Error("privacy/output controls absent")
		}
		input := body["input"].(string)
		for _, forbidden := range []string{"response.md", "teacher", "captured_by", "original", "aaaaaaaa"} {
			if strings.Contains(input, forbidden) {
				t.Errorf("metadata sent: %s", forbidden)
			}
		}
		return mockResponse(200, responseJSON(t, providerCriteria(7))), nil
	})
	generator := NewGenerator(svc, provider, []string{"c"})
	var d Document
	json.Unmarshal(r.Document, &d)
	got, err := generator.Generate(context.Background(), r.ID, d.InputDigest)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "pending" || got.Generation.InputTokens != 100 {
		t.Fatalf("bad outcome %+v", got)
	}
	json.Unmarshal(got.Document, &d)
	if d.Proposal.Source != "openai" || d.Proposal.Usage == nil || d.Proposal.Criteria[0].Citations[0].Path != "response.md" {
		t.Fatal("server provenance/citation binding missing")
	}
	if _, err = svc.Store.LatestGradeForSubmission(context.Background(), "s"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("model published a grade")
	}
	if _, err = generator.Generate(context.Background(), r.ID, d.InputDigest); err == nil || calls != 1 {
		t.Fatal("duplicate provider call")
	}
	// Instructor import cannot spoof a provider-verified provenance block.
	if ValidateProposal(d, *d.Proposal) == nil {
		t.Fatal("provider provenance accepted from import")
	}
}
func TestOpenAIFailuresAreBoundedAndPrivate(t *testing.T) {
	for _, mode := range []string{"auth", "rate", "refusal", "truncated", "invalid_score", "invalid_citation", "oversize", "transport"} {
		t.Run(mode, func(t *testing.T) {
			svc, r, _ := fixture(t)
			p, _ := NewOpenAI("SECRET", "")
			calls := 0
			p.client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
				calls++
				switch mode {
				case "auth":
					return mockResponse(401, "SECRET content"), nil
				case "rate":
					return mockResponse(429, "SECRET content"), nil
				case "refusal":
					return mockResponse(200, `{"id":"resp_x","status":"completed","model":"`+DefaultOpenAIModel+`","usage":{"input_tokens":10,"output_tokens":10},"output":[{"type":"message","content":[{"type":"refusal","text":"SECRET"}]}]}`), nil
				case "truncated":
					return mockResponse(200, `{"status":"incomplete"}`), nil
				case "invalid_score":
					return mockResponse(200, responseJSON(t, providerCriteria(999))), nil
				case "invalid_citation":
					js := providerCriteria(7)
					js[0].Citations[0].Location = "line:999"
					return mockResponse(200, responseJSON(t, js)), nil
				case "oversize":
					return mockResponse(200, strings.Repeat("x", maxProviderResponseBytes+1)), nil
				default:
					return nil, errors.New("SECRET transport diagnostic")
				}
			})
			var d Document
			json.Unmarshal(r.Document, &d)
			g := NewGenerator(svc, p, []string{"c"})
			if _, err := g.Generate(context.Background(), r.ID, d.InputDigest); err == nil || strings.Contains(err.Error(), "SECRET") {
				t.Fatalf("unsafe failure %v", err)
			}
			if _, err := g.Generate(context.Background(), r.ID, d.InputDigest); !errors.Is(err, store.ErrGenerationLimit) || calls != 1 {
				t.Fatal("failed call retried or refunded")
			}
			attempt, err := svc.Store.GetGeneration(context.Background(), r.ID)
			if err != nil || attempt.Status != "failed" || strings.Contains(attempt.ErrorCode, "SECRET") {
				t.Fatalf("failure audit %+v %v", attempt, err)
			}
		})
	}
}
func TestProviderGatesBeforeTransmission(t *testing.T) {
	for _, mode := range []string{"classroom", "digest", "paused", "stale", "input-limit", "busy"} {
		t.Run(mode, func(t *testing.T) {
			svc, r, _ := fixture(t)
			p, _ := NewOpenAI("test", "")
			p.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
				t.Error("provider called before gate")
				return nil, errors.New("unexpected")
			})
			g := NewGenerator(svc, p, []string{"c"})
			var d Document
			json.Unmarshal(r.Document, &d)
			ctx := context.Background()
			switch mode {
			case "classroom":
				g.Classrooms = map[string]bool{}
			case "digest":
				d.InputDigest = "wrong"
			case "paused":
				svc.Store.SetGenerationPaused(ctx, true)
			case "stale":
				sub, _ := svc.Store.GetSubmission(ctx, "s")
				sub.LatestCommit = strings.Repeat("b", 40)
				svc.Store.UpdateSubmission(ctx, sub)
			case "busy":
				g.slots <- struct{}{}
			case "input-limit":
				d.Artifacts[0].Segments = []Segment{{Location: "line:1", Text: strings.Repeat("x", MaxProviderInputBytes)}}
				if _, err := p.request(d); err == nil {
					t.Fatal("oversize input accepted")
				}
				return
			}
			if _, err := g.Generate(ctx, r.ID, d.InputDigest); err == nil {
				t.Fatal("gate ignored")
			}
		})
	}
}
func TestPrivateKeyFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "key.env")
	os.WriteFile(p, []byte("# comment\nOPENAI_API_KEY=private-fixture\n"), 0600)
	key, err := LoadOpenAIKeyFile(p)
	if err != nil || key != "private-fixture" {
		t.Fatal("could not load private key")
	}
	os.Chmod(p, 0644)
	if _, err = LoadOpenAIKeyFile(p); err == nil {
		t.Fatal("broad key file accepted")
	}
	os.Chmod(p, 0600)
	os.WriteFile(p, []byte("OPENAI_API_KEY=SECRET\nexport OTHER=SECRET"), 0600)
	if _, err = LoadOpenAIKeyFile(p); err == nil || strings.Contains(err.Error(), "SECRET") {
		t.Fatal("unsafe parse error")
	}
}

func TestGenerationDiscardsResponseAfterSubmissionChanges(t *testing.T) {
	svc, r, _ := fixture(t)
	p, _ := NewOpenAI("fixture", "")
	p.client.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		sub, err := svc.Store.GetSubmission(context.Background(), "s")
		if err != nil {
			t.Fatal(err)
		}
		sub.LatestCommit = strings.Repeat("b", 40)
		if err := svc.Store.UpdateSubmission(context.Background(), sub); err != nil {
			t.Fatal(err)
		}
		return mockResponse(200, responseJSON(t, providerCriteria(7))), nil
	})
	var d Document
	json.Unmarshal(r.Document, &d)
	if _, err := NewGenerator(svc, p, []string{"c"}).Generate(context.Background(), r.ID, d.InputDigest); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("stale response accepted: %v", err)
	}
	got, _ := svc.Store.GetAssessment(context.Background(), r.ID)
	if got.Status != "collected" {
		t.Fatal("stale proposal saved")
	}
	a, err := svc.Store.GetGeneration(context.Background(), r.ID)
	if err != nil || a.Status != "failed" || a.ErrorCode != "stale_or_unsaved" || a.InputTokens != 100 {
		t.Fatalf("lost usage on stale response: %+v %v", a, err)
	}
}
