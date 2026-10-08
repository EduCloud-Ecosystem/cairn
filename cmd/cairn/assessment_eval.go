// SPDX-License-Identifier: AGPL-3.0-or-later
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"

	"github.com/EduCloud-Ecosystem/cairn/internal/assessment"
	"github.com/EduCloud-Ecosystem/cairn/internal/assessmenteval"
	"github.com/EduCloud-Ecosystem/cairn/internal/store/sqlite"
)

func runAssessmentEval(args []string) error {
	if len(args) == 0 {
		return errors.New("expected run, packet or review; use assessment-eval run --help")
	}
	switch args[0] {
	case "run":
		return runEvaluation(args[1:])
	case "review":
		return reviewEvaluation(args[1:])
	case "packet":
		return renderEvaluationPacket(args[1:])
	default:
		return errors.New("expected run, packet or review")
	}
}
func runEvaluation(args []string) error {
	fs := flag.NewFlagSet("assessment-eval run", flag.ContinueOnError)
	out := fs.String("out", "", "new private output directory (required)")
	live := fs.Bool("live", false, "send bundled synthetic source to OpenAI; up to 9 calls per trial")
	trials := fs.Int("trials", 1, "1 or 2 trials; maximum 18 calls")
	keyFile := fs.String("key-file", "", "private OPENAI_API_KEY file, required with --live")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 0 || *out == "" || *trials < 1 || *trials > 2 {
		return errors.New("provide --out and --trials 1 or 2; positional arguments are not supported")
	}
	var provider *assessment.OpenAI
	if *live {
		key, err := assessment.LoadOpenAIKeyFile(*keyFile)
		if err != nil {
			return err
		}
		provider, err = assessment.NewOpenAI(key, "")
		if err != nil {
			return err
		}
	}
	// Never open a serving database or overwrite a previous evaluation.
	if err := os.Mkdir(*out, 0700); err != nil {
		return fmt.Errorf("output must be a new directory: %w", err)
	}
	st, err := sqlite.Open(filepath.Join(*out, "evaluation.db"))
	if err != nil {
		return err
	}
	defer st.Close()
	if err = os.Chmod(filepath.Join(*out, "evaluation.db"), 0600); err != nil {
		return err
	}
	var generate assessmenteval.Generate
	if provider != nil {
		g := assessment.NewGenerator(assessment.Service{Store: st}, provider, []string{"eval"})
		generate = g.Generate
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	save := func(r assessmenteval.Report) error {
		b, err := json.MarshalIndent(r, "", "  ")
		if err != nil {
			return err
		}
		if err = writeEvaluationFile(*out, "report.json", b); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "Evaluation: %d/%d trials recorded; %d proposals, %d provider failures\n", len(r.Results), len(r.Cases)*r.Trials, r.Metrics.Proposals, r.Metrics.ProviderFailures)
		return nil
	}
	r, err := assessmenteval.Run(ctx, st, generate, *trials, save)
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(filepath.Join(*out, "report.json"))
	if err != nil {
		return err
	}
	digest := assessment.DigestBytes(raw)
	w, _ := json.MarshalIndent(assessmenteval.NewWorksheet(r, digest), "", "  ")
	if err = writeEvaluationFile(*out, "human-review.json", w); err != nil {
		return err
	}
	var html bytes.Buffer
	if err = assessmenteval.RenderPacket(&html, r, digest); err != nil {
		return err
	}
	if err = writeEvaluationFile(*out, "review.html", html.Bytes()); err != nil {
		return err
	}
	fmt.Println("Saved report.json, review.html and human-review.json. References are provisional; no course grades were created.")
	if r.Metrics.ProviderFailures > 0 || r.Metrics.UnexpectedOutcomes > 0 {
		return errors.New("evaluation recorded provider or extraction failures; inspect report")
	}
	return nil
}
func writeEvaluationFile(dir, name string, b []byte) error {
	f, err := os.CreateTemp(dir, ".evaluation-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err = f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, name))
}
func reviewEvaluation(args []string) error {
	fs := flag.NewFlagSet("assessment-eval review", flag.ContinueOnError)
	report := fs.String("report", "", "evaluation report.json")
	review := fs.String("review", "", "completed human-review.json")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 0 || *report == "" || *review == "" {
		return errors.New("provide --report and --review")
	}
	raw, err := readEvaluationFile(*report)
	if err != nil {
		return err
	}
	b, err := readEvaluationFile(*review)
	if err != nil {
		return err
	}
	var w assessmenteval.Worksheet
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if dec.Decode(&w) != nil || dec.Decode(new(any)) != io.EOF {
		return errors.New("invalid review JSON")
	}
	s, err := assessmenteval.Review(raw, w)
	if err != nil {
		return err
	}
	out, _ := json.MarshalIndent(s, "", "  ")
	fmt.Println(string(out))
	return nil
}
func readEvaluationFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 8<<20+1))
	if err != nil {
		return nil, err
	}
	if len(b) > 8<<20 {
		return nil, errors.New("evaluation file exceeds 8 MiB")
	}
	return b, nil
}

func renderEvaluationPacket(args []string) error {
	fs := flag.NewFlagSet("assessment-eval packet", flag.ContinueOnError)
	report := fs.String("report", "", "existing report.json; no provider call")
	out := fs.String("out", "", "new directory for review packet")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if *report == "" || *out == "" || fs.NArg() != 0 {
		return errors.New("provide --report and --out")
	}
	raw, err := readEvaluationFile(*report)
	if err != nil {
		return err
	}
	var r assessmenteval.Report
	if err = json.Unmarshal(raw, &r); err != nil {
		return err
	}
	if err = assessmenteval.ValidateReport(r); err != nil {
		return err
	}
	// Recompute display metrics; never trust a saved summary over its results.
	r.Metrics = assessmenteval.Measure(r)
	digest := assessment.DigestBytes(raw)
	var html bytes.Buffer
	if err = assessmenteval.RenderPacket(&html, r, digest); err != nil {
		return err
	}
	if err = os.Mkdir(*out, 0700); err != nil {
		return err
	}
	if err = writeEvaluationFile(*out, "review.html", html.Bytes()); err != nil {
		return err
	}
	w, _ := json.MarshalIndent(assessmenteval.NewWorksheet(r, digest), "", "  ")
	return writeEvaluationFile(*out, "human-review.json", w)
}
