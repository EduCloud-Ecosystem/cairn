// SPDX-License-Identifier: AGPL-3.0-or-later
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"html/template"
	"io"
	"os"
	"path/filepath"

	"github.com/EduCloud-Ecosystem/cairn/internal/assessment"
)

type calibrationManifest struct {
	Purpose  string            `json:"purpose"`
	Rubric   assessment.Rubric `json:"rubric"`
	Examples []struct {
		ID        string `json:"id"`
		Directory string `json:"directory"`
	} `json:"examples"`
}

// Offline preparation deliberately has no provider, serving-store or GitHub
// credentials. Fetch pinned files separately and retain provenance privately.
func runCalibrationPrepare(args []string) error {
	fs := flag.NewFlagSet("calibration-prepare", flag.ContinueOnError)
	manifest := fs.String("manifest", "", "private JSON: purpose, rubric and sample IDs/directories")
	root := fs.String("source-root", "", "directory containing reviewed historical source")
	out := fs.String("out", "", "new private output directory")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 0 || *manifest == "" || *root == "" || *out == "" {
		return errors.New("provide --manifest, --source-root and --out")
	}
	raw, err := readEvaluationFile(*manifest)
	if err != nil {
		return err
	}
	var m calibrationManifest
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if dec.Decode(&m) != nil || dec.Decode(new(any)) != io.EOF {
		return errors.New("invalid calibration manifest")
	}
	if err = assessment.ValidateRubric(m.Rubric); err != nil {
		return err
	}
	if len(m.Examples) < 1 || len(m.Examples) > 9 {
		return errors.New("select 1–9 historical examples")
	}
	b := assessment.CalibrationBundle{Version: assessment.CalibrationBundleVersion, Purpose: m.Purpose, Rubric: m.Rubric}
	for _, sample := range m.Examples {
		if !assessment.ValidPath(sample.Directory) {
			return errors.New("sample directories must be safe relative paths")
		}
		paths := make([]string, len(m.Rubric.Paths))
		for i, p := range m.Rubric.Paths {
			paths[i] = sample.Directory + "/" + p
		}
		artifacts := assessment.Extract(*root, paths)
		files := map[string]string{}
		for i, a := range artifacts {
			if a.Issue != "" {
				return fmt.Errorf("sample extraction blocked: %s", a.Issue)
			}
			files[m.Rubric.Paths[i]] = string(a.Original)
		}
		b.Examples = append(b.Examples, assessment.CalibrationBundleExample{ID: sample.ID, Files: files})
	}
	if err = assessment.ValidateCalibrationBundle(b); err != nil {
		return err
	}
	body, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return err
	}
	// Keep the bundle within the API's bounded JSON body (source escapes can
	// expand significantly). Refuse, never truncate.
	if len(body) > assessment.MaxCalibrationBundleBytes {
		return errors.New("encoded bundle too large; reduce the sample")
	}
	var html bytes.Buffer
	if err = calibrationPacket.Execute(&html, b); err != nil {
		return err
	}
	if err = os.Mkdir(*out, 0700); err != nil {
		return fmt.Errorf("output must be a new directory: %w", err)
	}
	if err = writeEvaluationFile(*out, "bundle.json", body); err != nil {
		return err
	}
	rubric, _ := json.MarshalIndent(m.Rubric, "", "  ")
	if err = writeEvaluationFile(*out, "rubric.json", rubric); err != nil {
		return err
	}
	if err = writeEvaluationFile(*out, "inspect.html", html.Bytes()); err != nil {
		return err
	}
	fmt.Printf("Prepared %d %s examples in %s. No provider requests, instructor judgments or grades were created.\n", len(b.Examples), b.Purpose, filepath.Clean(*out))
	return nil
}

var calibrationPacket = template.Must(template.New("calibration").Parse(`<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'; base-uri 'none'; form-action 'none'"><title>Historical calibration source inspection</title><style>body{font:18px/1.6 system-ui;max-width:1000px;margin:40px auto;padding:0 24px;background:#f7f8f6;color:#20332f}article{padding:24px;background:white;border:1px solid #acbcb7;margin:24px 0}pre{font-size:15px;white-space:pre-wrap;overflow-wrap:anywhere}summary{cursor:pointer}h1,h2{line-height:1.2}</style><h1>Historical source inspection</h1><p>Sample purpose: {{.Purpose}}. Instructor review pending. This packet has no network access and never executes source or publishes grades.</p><p>Check the rubric and all source for identifying information before importing bundle.json into your empty calibration draft. Importing sends nothing to a model. A holdout bundle requires a new round based on your approved profile.</p><h2>{{.Rubric.Title}}</h2>{{range .Rubric.Criteria}}<p><strong>{{.ID}} · {{.MaxPoints}} points</strong>: {{.Description}}</p>{{end}}<p>Quarto/R Markdown is source only: plots, rendered output, includes and live apps are not inspected. Blank instructor scores are pending judgments, not zero. Preserve the original repository/commit/hash mapping separately; aliases are not an anonymity guarantee.</p>{{range .Examples}}<article><h2>{{.ID}}</h2>{{range $path,$source:=.Files}}<details><summary>{{$path}}</summary><pre>{{$source}}</pre></details>{{end}}</article>{{end}}</html>`))
