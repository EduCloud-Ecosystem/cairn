// SPDX-License-Identifier: AGPL-3.0-or-later
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"html/template"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"sort"
	"strings"

	"github.com/EduCloud-Ecosystem/cairn/internal/assessment"
)

type readinessManifest struct {
	Version         string                  `json:"version"`
	InstructorScope string                  `json:"instructor_scope"`
	PriorBundles    []string                `json:"prior_bundles"`
	Sections        []readinessSectionInput `json:"sections"`
}
type readinessSectionInput struct {
	ID            string `json:"id"`
	Bundle        string `json:"bundle"`
	DraftGuidance string `json:"draft_guidance"`
}
type readinessSample struct {
	ID                     string              `json:"id"`
	InputDigest            string              `json:"input_digest"`
	SourceHashes           map[string]string   `json:"source_sha256"`
	PriorExactOverlap      bool                `json:"prior_exact_overlap"`
	PriorWhitespaceOverlap bool                `json:"prior_whitespace_overlap"`
	InputBytes             int                 `json:"input_bytes"`
	Fits                   bool                `json:"fits"`
	PreflightIssue         string              `json:"preflight_issue,omitempty"`
	Document               assessment.Document `json:"document"`
}
type readinessSection struct {
	ID             string            `json:"id"`
	BundleSHA256   string            `json:"bundle_file_sha256"`
	RubricSHA256   string            `json:"rubric_sha256"`
	DraftGuidance  string            `json:"draft_guidance"`
	GuidanceSHA256 string            `json:"draft_guidance_sha256"`
	Samples        []readinessSample `json:"samples"`
}
type readinessReport struct {
	PromptSHA256            string             `json:"prompt_instructions_sha256"`
	Version                 string             `json:"version"`
	InstructorScope         string             `json:"instructor_scope"`
	Status                  string             `json:"status"`
	PromptVersion           string             `json:"prompt_version"`
	PolicyVersion           string             `json:"policy_version"`
	Model                   string             `json:"model"`
	CodeRevision            string             `json:"binary_code_revision"`
	CodeModified            *bool              `json:"binary_code_modified"`
	CodeProvenanceAvailable bool               `json:"binary_code_provenance_available"`
	BinarySHA256            string             `json:"executed_binary_sha256"`
	PriorBundleSHA256       []string           `json:"prior_bundle_file_sha256"`
	SourceReviewRequired    bool               `json:"source_review_required"`
	IndependentReferences   int                `json:"independent_instructor_references"`
	ModelCalls              int                `json:"model_calls"`
	ProductionActivation    bool               `json:"production_activation"`
	Sections                []readinessSection `json:"sections"`
}

func runCalibrationReadiness(args []string) error {
	fs := flag.NewFlagSet("calibration-readiness", flag.ContinueOnError)
	manifest := fs.String("manifest", "", "private JSON manifest with source-only bundles, draft guidance, and prior bundles")
	out := fs.String("out", "", "new private output directory")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if *manifest == "" || *out == "" || fs.NArg() != 0 {
		return errors.New("provide --manifest and --out")
	}
	raw, err := readEvaluationFile(*manifest)
	if err != nil {
		return err
	}
	var m readinessManifest
	if err = strictCheckJSON(raw, &m); err != nil {
		return err
	}
	if m.Version != "cairn-readiness-manifest-v1" || strings.TrimSpace(m.InstructorScope) == "" || len(m.InstructorScope) > 200 || len(m.Sections) < 1 || len(m.Sections) > 8 || len(m.PriorBundles) > 256 {
		return errors.New("manifest needs an instructor scope, 1–8 sections and at most 256 prior bundles")
	}
	readBundle := func(path string) (assessment.CalibrationBundle, []byte, error) {
		if !filepath.IsAbs(path) {
			path = filepath.Join(filepath.Dir(*manifest), path)
		}
		raw, err := readEvaluationFile(path)
		if err != nil {
			return assessment.CalibrationBundle{}, nil, err
		}
		var b assessment.CalibrationBundle
		if err = strictCheckJSON(raw, &b); err == nil {
			err = assessment.ValidateCalibrationBundle(b)
		}
		return b, raw, err
	}
	report := readinessReport{PromptSHA256: assessment.PromptInstructionsDigest(), Version: "cairn-calibration-readiness-v1", InstructorScope: m.InstructorScope, Status: "awaiting-instructor-source-review", PromptVersion: assessment.PromptVersion, PolicyVersion: assessment.PolicyVersion, Model: assessment.DefaultOpenAIModel, SourceReviewRequired: true, PriorBundleSHA256: []string{}, Sections: []readinessSection{}}
	if info, ok := debug.ReadBuildInfo(); ok {
		report.CodeRevision, report.CodeModified, report.CodeProvenanceAvailable = readinessBuildProvenance(info.Settings)
	}
	binaryPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate executed binary: %w", err)
	}
	report.BinarySHA256, err = readinessFileSHA256(binaryPath)
	if err != nil {
		return fmt.Errorf("hash executed binary: %w", err)
	}
	prior, normalized := map[string]bool{}, map[string]bool{}
	for _, path := range m.PriorBundles {
		b, raw, err := readBundle(path)
		if err != nil {
			return fmt.Errorf("prior bundle: %w", err)
		}
		report.PriorBundleSHA256 = append(report.PriorBundleSHA256, assessment.DigestBytes(raw))
		for _, e := range b.Examples {
			for _, source := range e.Files {
				prior[assessment.DigestBytes([]byte(source))] = true
				normalized[assessment.DigestBytes([]byte(strings.Join(strings.Fields(source), " ")))] = true
			}
		}
	}
	seen, selected := map[string]bool{}, map[string]bool{}
	reviewRows := []map[string]any{}
	idPattern := regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	for _, input := range m.Sections {
		if !idPattern.MatchString(input.ID) || seen[input.ID] || len(input.DraftGuidance) > 2000 {
			return errors.New("section IDs must be unique safe identifiers and guidance bounded to 2000 bytes")
		}
		seen[input.ID] = true
		b, raw, err := readBundle(input.Bundle)
		if err != nil {
			return err
		}
		section := readinessSection{ID: input.ID, BundleSHA256: assessment.DigestBytes(raw), RubricSHA256: assessment.Digest(b.Rubric), DraftGuidance: input.DraftGuidance, GuidanceSHA256: assessment.DigestBytes([]byte(input.DraftGuidance)), Samples: []readinessSample{}}
		for _, e := range b.Examples {
			contents := make([]string, 0, len(e.Files))
			for _, source := range e.Files {
				contents = append(contents, assessment.DigestBytes([]byte(source)))
			}
			sort.Strings(contents)
			identity := assessment.Digest(contents)
			if selected[identity] {
				return errors.New("selected packet repeats exact source under another sample or section")
			}
			selected[identity] = true
			d, err := assessment.CalibrationBundleDocument(b, e.ID)
			if err != nil {
				return err
			}
			sample := readinessSample{ID: e.ID, InputDigest: d.InputDigest, SourceHashes: map[string]string{}, Document: d}
			for _, a := range d.Artifacts {
				sample.SourceHashes[a.Path] = a.SHA256
				sample.PriorExactOverlap = sample.PriorExactOverlap || prior[a.SHA256]
				sample.PriorWhitespaceOverlap = sample.PriorWhitespaceOverlap || normalized[assessment.DigestBytes([]byte(strings.Join(strings.Fields(string(a.Original)), " ")))]
			}
			hypothetical := d
			if input.DraftGuidance != "" {
				hypothetical.Calibration = &assessment.CalibrationBinding{Guidance: input.DraftGuidance}
			}
			sample.InputBytes, err = assessment.PreflightProviderInput(hypothetical)
			sample.Fits = err == nil
			if err != nil {
				sample.PreflightIssue = err.Error()
			}
			section.Samples = append(section.Samples, sample)
			for _, c := range d.Rubric.Criteria {
				reviewRows = append(reviewRows, map[string]any{"section": input.ID, "sample": e.ID, "criterion": c.ID, "status": "unreviewed", "points": nil, "rationale": "", "claim_to_citation_review": []any{}, "documentation_attribution_review": "unreviewed"})
			}
		}
		report.Sections = append(report.Sections, section)
	}
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	worksheet, err := json.MarshalIndent(map[string]any{"version": "cairn-readiness-worksheet-v1", "report_sha256": assessment.DigestBytes(encoded), "status": "unreviewed-not-an-instructor-reference", "rows": reviewRows}, "", "  ")
	if err != nil {
		return err
	}
	var html bytes.Buffer
	if err = readinessTemplate.Execute(&html, report); err != nil {
		return err
	}
	if err = os.Mkdir(*out, 0700); err != nil {
		return fmt.Errorf("output must be a new private directory: %w", err)
	}
	files := map[string][]byte{"readiness.json": encoded, "instructor-review.json": worksheet, "REVIEW.html": html.Bytes(), "manifest-frozen.json": raw}
	freeze := map[string]string{}
	for name, data := range files {
		if err = writeEvaluationFile(*out, name, data); err != nil {
			return err
		}
		freeze[name] = assessment.DigestBytes(data)
	}
	checks, _ := json.MarshalIndent(freeze, "", "  ")
	if err = writeEvaluationFile(*out, "checksums.json", checks); err != nil {
		return err
	}
	fmt.Println("Saved private source-first readiness packet. No model requests, independent references, profile approvals, or grades created.")
	return nil
}

// An absent or malformed modified setting must never become an implicit clean build.
func readinessBuildProvenance(settings []debug.BuildSetting) (string, *bool, bool) {
	var revision string
	var modified *bool
	for _, setting := range settings {
		switch setting.Key {
		case "vcs.revision":
			revision = setting.Value
		case "vcs.modified":
			if setting.Value == "true" || setting.Value == "false" {
				value := setting.Value == "true"
				modified = &value
			}
		}
	}
	return revision, modified, strings.TrimSpace(revision) != "" && modified != nil
}

func readinessFileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

var readinessTemplate = template.Must(template.New("readiness").Funcs(template.FuncMap{"source": func(a assessment.Artifact) string {
	lines := strings.Split(string(a.Original), "\n")
	for i, line := range lines {
		lines[i] = fmt.Sprintf("%d: %s", i+1, line)
	}
	return strings.Join(lines, "\n")
}}).Parse(`<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width"><meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'; base-uri 'none'"><title>Cairn instructor readiness review</title><style>body{font:17px/1.5 system-ui;max-width:960px;margin:30px auto;padding:0 20px;color:#18232d}pre{white-space:pre-wrap;overflow-wrap:anywhere;font-size:13px}details{border:1px solid #bbb;padding:12px;margin:14px 0}summary{cursor:pointer;font-weight:bold}table{border-collapse:collapse}td,th{border:1px solid #aaa;padding:8px;text-align:left}.warning{background:#fff0d1;padding:12px}</style><h1>Instructor readiness review</h1><p>Private · {{.InstructorScope}} · {{.PromptVersion}} · {{.Model}}</p><p>Binary code provenance available: {{.CodeProvenanceAvailable}} · executed binary SHA-256: <code>{{.BinarySHA256}}</code>. Missing embedded provenance is unknown, not a verified clean build. Retain a separately verified build record binding this hash to the source revision.</p><p class="warning">Source-first preparation only. No new model calls, instructor reference judgments, approvals or grades. Prior assisted decisions remain separate. Score fields are deliberately blank.</p><ol><li>Review each source and captured starter against the rubric. Write your judgment before seeing a new model proposal.</li><li>For every later feedback claim, identify the exact cited operation supporting it. Check every credited step, deduction and required versus optional change. A valid citation alone does not establish meaning.</li><li>Attribute comments and docstrings to the captured starter, student changes, or unknown provenance. Matching text is overlap, not proof of authorship. Do not credit inherited documentation as newly authored.</li><li>Keep unassessable criteria null until you resolve the evidence. Preserve the authorized localized duplication decision without applying a universal penalty.</li></ol><p>Record notes in instructor-review.json or the instructor console. This worksheet cannot import an independent reference or approve a profile. Use authenticated reference capture before any new model attempt. Draft guidance below is neither approved nor active.</p>{{range .Sections}}<h2>{{.ID}}</h2><p>Rubric SHA-256: <code>{{.RubricSHA256}}</code></p><details><summary>Frozen draft guidance</summary><p>{{.DraftGuidance}}</p></details>{{range .Samples}}<h3>{{.ID}}</h3><p>Prior exact-source overlap: {{.PriorExactOverlap}} · prior whitespace-normalized overlap: {{.PriorWhitespaceOverlap}}. These checks cover supplied prior bundles only; they do not establish independent holdout status or semantic novelty.</p><p>Current request preflight: {{.InputBytes}} bytes · fits: {{.Fits}} {{.PreflightIssue}}</p><table><tr><th>Criterion</th><th>Maximum</th><th>Your judgment</th></tr>{{range .Document.Rubric.Criteria}}<tr><td>{{.ID}}: {{.Description}}</td><td>{{.MaxPoints}}</td><td>Not reviewed</td></tr>{{end}}</table>{{range .Document.Artifacts}}<details><summary>Captured source: {{.Path}}</summary><p>SHA-256: {{.SHA256}}</p><pre>{{source .}}</pre></details>{{end}}{{range $path,$starter:=.Document.Rubric.StarterFiles}}<details><summary>Captured instructor starter: {{$path}}</summary><pre>{{$starter}}</pre></details>{{end}}{{end}}{{end}}<p>Preflight success is a size/schema check, not a calibration or quality result. Exact and whitespace overlap checks do not detect all related submissions. Do not publish this packet or its source.</p></html>`))
