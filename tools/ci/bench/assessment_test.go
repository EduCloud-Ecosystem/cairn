package bench

import (
	"fmt"
	"math"
	"path"
	"strings"
	"testing"

	"github.com/EduCloud-Ecosystem/cairn/internal/assessment"
)

func FuzzArtifactPathConfinement(f *testing.F) {
	for _, seed := range []string{"response.md", "../secret", "a/../../x", "/tmp/x", "a\\b", "a/.git/config", "notes/answer.py"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, p string) {
		if assessment.ValidPath(p) {
			root := "/synthetic-evidence-root"
			if !strings.HasPrefix(path.Join(root, p), root+"/") || strings.ContainsAny(p, "\\\x00\r\n") {
				t.Fatalf("accepted escaping path %q", p)
			}
		}
	})
}

func judgmentFixture() (assessment.Document, assessment.Judgment) {
	source := []byte("Evidence and reasoning.")
	hash := assessment.DigestBytes(source)
	doc := assessment.Document{Rubric: assessment.Rubric{Title: "Reasoning", Paths: []string{"response.md"}, Criteria: []assessment.Criterion{{ID: "reason", Description: "Support reasoning", MaxPoints: 10}}}, Artifacts: []assessment.Artifact{{Path: "response.md", Original: source, SHA256: hash, Segments: []assessment.Segment{{Location: "line:1", Text: string(source)}}}}}
	points := 5.0
	judgment := assessment.Judgment{CriterionID: "reason", Points: &points, Feedback: "Supported by the source.", Uncertainty: "Synthetic check.", Citations: []assessment.Citation{{Path: "response.md", SHA256: hash, Location: "line:1"}}}
	return doc, judgment
}

func FuzzGradeBounds(f *testing.F) {
	for _, v := range []float64{-1, 0, 5, 10, 11, math.Inf(1), math.NaN()} {
		f.Add(v)
	}
	f.Fuzz(func(t *testing.T, points float64) {
		doc, judgment := judgmentFixture()
		judgment.Points = &points
		score, maximum, err := assessment.ValidateJudgments(doc, []assessment.Judgment{judgment}, true)
		if err == nil && (math.IsNaN(score) || math.IsInf(score, 0) || score < 0 || score > maximum || maximum != 10) {
			t.Fatalf("unsafe grade accepted: %v/%v", score, maximum)
		}
	})
}

func BenchmarkAssessmentValidation(b *testing.B) {
	for _, size := range []int{1, 32} {
		b.Run(fmt.Sprintf("criteria-%d", size), func(b *testing.B) {
			doc, first := judgmentFixture()
			doc.Rubric.Criteria = nil
			judgments := make([]assessment.Judgment, size)
			for i := range judgments {
				id := fmt.Sprintf("criterion-%d", i)
				doc.Rubric.Criteria = append(doc.Rubric.Criteria, assessment.Criterion{ID: id, Description: "Support reasoning", MaxPoints: 10})
				judgments[i] = first
				judgments[i].CriterionID = id
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, _, err := assessment.ValidateJudgments(doc, judgments, true); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
