// SPDX-License-Identifier: AGPL-3.0-or-later
package assessment

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EduCloud-Ecosystem/cairn/internal/store"
	"github.com/EduCloud-Ecosystem/cairn/internal/store/sqlite"
	"github.com/EduCloud-Ecosystem/cairn/pkg/adapter"
)

func TestCSVExactCitableSource(t *testing.T) {
	dir := t.TempDir()
	raw := []byte("country,year,annual_inflation\r\n\"Example, North\",1993,1.25\r\nExample South,1994,\r\n\"multi\nline\",1995,=1+1\r\n")
	if err := os.WriteFile(filepath.Join(dir, "work.csv"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	a := Extract(dir, []string{"work.csv"})[0]
	if a.Issue != "" || a.MediaType != "text/csv" || a.Extractor != "csv-source-v1" || a.SHA256 != DigestBytes(raw) || string(a.Original) != string(raw) || a.Size != len(raw) {
		t.Fatalf("CSV original binding: %+v", a)
	}
	lines := strings.Split(string(raw), "\n")
	if len(a.Segments) != len(lines) || a.Segments[1].Location != "line:2" || a.Segments[1].Text != lines[1] || a.Segments[4].Text != "line\",1995,=1+1\r" {
		t.Fatal("CSV source lines changed, formulas evaluated or multiline fields collapsed")
	}
}

func TestCSVRefusalsAndPreservedLimits(t *testing.T) {
	cases := map[string][]byte{
		"blank": []byte(" \r\n\t"), "nul": {0}, "utf8": {255},
		"quote":      []byte("a,b\n\"unterminated,b\n"),
		"bare-quote": []byte("a,b\nx\"y,z\n"),
		"bytes":      []byte(strings.Repeat("x", MaxFileBytes+1)),
		"lines":      []byte(strings.Repeat("x\n", 4096)),
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "work.csv"), raw, 0600); err != nil {
				t.Fatal(err)
			}
			a := Extract(dir, []string{"work.csv"})[0]
			if a.Issue == "" {
				t.Fatal("invalid CSV accepted")
			}
			if len(raw) <= MaxFileBytes && (a.SHA256 != DigestBytes(raw) || string(a.Original) != string(raw)) {
				t.Fatal("bounded failed source lost its original binding")
			}
		})
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ok.csv"), []byte("a,b\nx,y,z\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if a := Extract(dir, []string{"ok.csv"})[0]; a.Issue != "" {
		t.Fatal("row-width academic error must remain citable")
	}
	if err := os.Symlink(filepath.Join(dir, "ok.csv"), filepath.Join(dir, "link.csv")); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"link.csv", "../outside.csv", "missing.csv", "work.xlsx"} {
		if a := Extract(dir, []string{name})[0]; a.Issue == "" {
			t.Fatalf("unsafe or unsupported path accepted: %s", name)
		}
	}
	total := MaxTotalBytes - 1
	a := extractFile(dir, Artifact{Path: "ok.csv", MediaType: "text/csv"}, &total)
	if a.Issue == "" || total != MaxTotalBytes-1 {
		t.Fatal("aggregate limit weakened")
	}
}

type csvCheckout struct {
	files map[string]string
}

func (c csvCheckout) FetchRevision(_ context.Context, _ adapter.RepoRef, rev, dir string) error {
	if rev != revision {
		return errors.New("wrong revision")
	}
	for name, content := range c.files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0600); err != nil {
			return err
		}
	}
	return nil
}

func csvFixture(t *testing.T, csvText string) (Service, *store.AssessmentRecord, Proposal) {
	t.Helper()
	st, err := sqlite.Open(filepath.Join(t.TempDir(), "a.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	ctx := context.Background()
	for _, err := range []error{st.CreateClassroom(ctx, &store.Classroom{ID: "c"}), st.CreateAssignment(ctx, &store.Assignment{ID: "a", ClassroomID: "c"}), st.CreateRosterEntry(ctx, &store.RosterEntry{ID: "r", ClassroomID: "c", Status: store.RosterActive}), st.CreateSubmission(ctx, &store.Submission{ID: "s", AssignmentID: "a", RosterEntryID: "r", Status: "active", LatestCommit: revision})} {
		if err != nil {
			t.Fatal(err)
		}
	}
	paths := []string{"lab3-q4.ipynb", "tidy-inflation.csv", "explanation.md", "assistance.md"}
	rub := Rubric{Title: "CW1 four-artifact evidence", Paths: paths, Criteria: []Criterion{{ID: "correctness", Description: "Inspect the table and submitted reasoning", MaxPoints: 10}}}
	b, err := json.Marshal(rub)
	if err != nil {
		t.Fatal(err)
	}
	if err = st.PutAssessmentRubric(ctx, &store.AssessmentRubric{AssignmentID: "a", Digest: DigestBytes(b), Document: b}); err != nil {
		t.Fatal(err)
	}
	svc := Service{Store: st, Checkout: csvCheckout{files: map[string]string{
		"lab3-q4.ipynb":      `{"nbformat":4,"cells":[{"cell_type":"code","source":"# submitted synthetic attempt"}]}`,
		"tidy-inflation.csv": csvText, "explanation.md": "One fictional country per year.\n", "assistance.md": "Declaration: no_ai\n",
	}}}
	r, err := svc.Capture(ctx, "s", "teacher")
	if err != nil {
		t.Fatal(err)
	}
	var d Document
	if err = json.Unmarshal(r.Document, &d); err != nil {
		t.Fatal(err)
	}
	p := Proposal{SubmissionStatus: "relevant_work", Source: "instructor-import", Model: "none", PromptVersion: "CW1-test", InputDigest: d.InputDigest, Criteria: []Judgment{{CriterionID: "correctness", Points: ptr(7), Feedback: "Synthetic source reviewed.", Uncertainty: "Fixture only, no model or learning-quality evidence."}}}
	for _, a := range d.Artifacts {
		if len(a.Segments) != 0 {
			p.Criteria[0].Citations = append(p.Criteria[0].Citations, Citation{Path: a.Path, SHA256: a.SHA256, Location: a.Segments[0].Location, Quote: a.Segments[0].Text})
		}
	}
	return svc, r, p
}

func TestCSVFourArtifactCaptureReview(t *testing.T) {
	svc, r, p := csvFixture(t, "country,year,annual_inflation\nExample North,1993,1.25\n")
	ctx := context.Background()
	if r.Status != "collected" {
		t.Fatalf("capture status %s", r.Status)
	}
	var d Document
	if err := json.Unmarshal(r.Document, &d); err != nil {
		t.Fatal(err)
	}
	if len(d.Artifacts) != 4 || d.Revision != revision {
		t.Fatal("four artifacts not bound to submitted revision")
	}
	if _, err := svc.Store.LatestGradeForSubmission(ctx, "s"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("capture published a grade")
	}
	if _, err := svc.Propose(ctx, r.ID, p); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Store.LatestGradeForSubmission(ctx, "s"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("proposal published a grade")
	}
	p.Criteria[0].Points = ptr(8)
	if _, err := svc.Review(ctx, r.ID, "teacher", "approve", "Instructor inspected all four artifacts", p.Criteria); err != nil {
		t.Fatal(err)
	}
	g, err := svc.Store.LatestGradeForSubmission(ctx, "s")
	if err != nil || g.Score != 8 {
		t.Fatalf("reviewed grade: %+v %v", g, err)
	}
}

func TestCSVAlteredUnsupportedAndStaleEvidenceBlocked(t *testing.T) {
	for _, raw := range []string{"", "\"unterminated"} {
		svc, r, p := csvFixture(t, raw)
		if r.Status != "blocked" {
			t.Fatal("unreadable CSV did not block capture")
		}
		if _, err := svc.Propose(context.Background(), r.ID, p); err == nil {
			t.Fatal("blocked source accepted proposal")
		}
	}
	svc, r, p := csvFixture(t, "country,year,annual_inflation\nExample North,1993,1.25\n")
	var d Document
	if err := json.Unmarshal(r.Document, &d); err != nil {
		t.Fatal(err)
	}
	d.Artifacts[1].Original = []byte("changed")
	if _, _, err := ValidateJudgments(d, p.Criteria, true); err == nil {
		t.Fatal("changed original passed digest gate")
	}
	if err := json.Unmarshal(r.Document, &d); err != nil {
		t.Fatal(err)
	}
	d.Artifacts[1].Issue = "Unsupported format; instructor review required"
	if _, _, err := ValidateJudgments(d, p.Criteria, true); err == nil {
		t.Fatal("unsupported evidence approved")
	}
	p.Criteria[0].Citations[1].SHA256 = strings.Repeat("0", 64)
	if _, err := svc.Propose(context.Background(), r.ID, p); err == nil {
		t.Fatal("altered CSV citation accepted")
	}
	p.Criteria[0].Citations[1].SHA256 = DigestBytes(d.Artifacts[1].Original)
	if _, err := svc.Propose(context.Background(), r.ID, p); err != nil {
		t.Fatal(err)
	}
	sub, err := svc.Store.GetSubmission(context.Background(), "s")
	if err != nil {
		t.Fatal(err)
	}
	sub.LatestCommit = strings.Repeat("b", 40)
	if err = svc.Store.UpdateSubmission(context.Background(), sub); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Review(context.Background(), r.ID, "teacher", "approve", "Stale capture", p.Criteria); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("stale capture approved: %v", err)
	}
}
