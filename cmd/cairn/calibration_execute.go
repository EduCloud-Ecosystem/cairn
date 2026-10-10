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
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/EduCloud-Ecosystem/cairn/internal/assessment"
	"github.com/EduCloud-Ecosystem/cairn/internal/grading"
	"github.com/EduCloud-Ecosystem/cairn/pkg/gradingspec"
)

type calibrationChecks struct {
	Version string            `json:"version"`
	Files   map[string]string `json:"files"`
	Spec    gradingspec.Spec  `json:"spec"`
}
type checkEvidence struct {
	SampleID     string                 `json:"sample_id"`
	SourceHashes map[string]string      `json:"source_sha256"`
	Status       string                 `json:"status"`
	Tests        []executionObservation `json:"tests,omitempty"`
}
type executionObservation struct {
	CriterionID string `json:"criterion_id"`
	Status      string `json:"status"`
	ExitCode    *int   `json:"exit_code,omitempty"`
	Detail      string `json:"detail,omitempty"`
}

func checkObservations(tests []grading.TestResult) []executionObservation {
	out := []executionObservation{}
	for _, t := range tests {
		status := "check_failed"
		switch {
		case t.TimedOut:
			status = "unassessable_timeout"
		case t.ExitCode == nil || *t.ExitCode < 0 || *t.ExitCode > 1:
			status = "unassessable_execution_error"
		case t.Passed:
			status = "passed"
		}
		out = append(out, executionObservation{CriterionID: t.Name, Status: status, ExitCode: t.ExitCode, Detail: t.Detail})
	}
	return out
}

type executionReport struct {
	Completed         bool               `json:"completed"`
	ExpectedSamples   int                `json:"expected_samples"`
	UncheckedCriteria []string           `json:"unchecked_criteria"`
	Version           string             `json:"version"`
	CreatedAt         time.Time          `json:"created_at"`
	BundleDigest      string             `json:"bundle_sha256"`
	RubricDigest      string             `json:"rubric_digest"`
	ChecksDigest      string             `json:"checks_sha256"`
	Image             string             `json:"image"`
	Isolation         string             `json:"isolation"`
	Limits            gradingspec.Limits `json:"limits"`
	Samples           []checkEvidence    `json:"samples"`
	Note              string             `json:"note"`
}

var pinnedCheckImage = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)

// Offline checks have no provider or serving-store access. They produce private
// execution observations, not grades or an importable provider attestation.
func runCalibrationExecute(args []string) error {
	fs := flag.NewFlagSet("calibration-execute", flag.ContinueOnError)
	bundlePath := fs.String("bundle", "", "reviewed source-only bundle")
	checksPath := fs.String("checks", "", "instructor-authored check files and grading spec")
	out := fs.String("out", "", "new private output directory")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 0 || *bundlePath == "" || *checksPath == "" || *out == "" {
		return errors.New("provide --bundle, --checks and --out")
	}
	raw, err := readEvaluationFile(*bundlePath)
	if err != nil {
		return err
	}
	var bundle assessment.CalibrationBundle
	if err = strictCheckJSON(raw, &bundle); err != nil {
		return err
	}
	if err = assessment.ValidateCalibrationBundle(bundle); err != nil {
		return err
	}
	policy, err := readEvaluationFile(*checksPath)
	if err != nil {
		return err
	}
	var checks calibrationChecks
	if err = strictCheckJSON(policy, &checks); err != nil {
		return err
	}
	if err = validateCalibrationChecks(bundle.Rubric, checks); err != nil {
		return err
	}
	// A remote Docker context would send private source to another host. Only a
	// local Unix socket is accepted; no implicit image pulls or runtime fallback.
	if err = localDockerContext(); err != nil {
		return err
	}
	if err = os.Mkdir(*out, 0700); err != nil {
		return fmt.Errorf("output must be a new directory: %w", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	runner := grading.NewContainerRunner(checks.Spec.Image)
	runner.User = "65534:65534"
	runner.ReadOnlyWork = true
	runner.DefaultPids = 64
	runner.ExtraArgs = []string{"--pull=never"}
	return executeCalibrationChecks(ctx, bundle, checks, assessment.DigestBytes(raw), assessment.DigestBytes(policy), *out, runner)
}
func strictCheckJSON(raw []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(v) != nil || d.Decode(new(any)) != io.EOF {
		return errors.New("invalid checks or bundle JSON")
	}
	return nil
}
func validateCalibrationChecks(r assessment.Rubric, c calibrationChecks) error {
	if (c.Spec.Version != "" && c.Spec.Version != gradingspec.Version) || c.Version != "cairn-calibration-checks-v1" || !pinnedCheckImage.MatchString(c.Spec.Image) || len(c.Spec.Setup) != 0 || len(c.Spec.Tests) < 1 || len(c.Spec.Tests) > 32 || len(c.Files) < 1 || len(c.Files) > 32 {
		return errors.New("checks require a pinned local image ID, 1–32 tests/files, and no setup")
	}
	ids := map[string]bool{}
	for _, criterion := range r.Criteria {
		ids[criterion.ID] = true
	}
	seen := map[string]bool{}
	for _, t := range c.Spec.Tests {
		if !ids[t.Name] || seen[t.Name] || t.Points != 0 || strings.TrimSpace(t.Run) == "" || len(t.Run) > 2000 || t.Limits != nil || t.Match == nil || t.Match.Expected == "" || len(t.Match.Expected) > 2000 {
			return errors.New("each check must name a unique rubric criterion, use zero points and bounded expected output, and have no limit overrides")
		}
		seen[t.Name] = true
	}
	total := 0
	for p, body := range c.Files {
		total += len(body)
		if !assessment.ValidPath(p) || len(body) > 128<<10 {
			return errors.New("invalid instructor check file")
		}
	}
	if total > 512<<10 {
		return errors.New("instructor checks exceed 512 KiB")
	}
	return nil
}
func executeCalibrationChecks(ctx context.Context, b assessment.CalibrationBundle, c calibrationChecks, bundleDigest, checksDigest, out string, runner grading.PolicyRunner) error {
	// Override all supplied limits; the instructor check cannot enable egress.
	c.Spec.Limits = gradingspec.Limits{Timeout: 15 * time.Second, MemoryMB: 512, CPUs: 1, Network: gradingspec.NetworkNone}
	unchecked := []string{}
	checked := map[string]bool{}
	for _, test := range c.Spec.Tests {
		checked[test.Name] = true
	}
	for _, criterion := range b.Rubric.Criteria {
		if !checked[criterion.ID] {
			unchecked = append(unchecked, criterion.ID)
		}
	}
	report := executionReport{ExpectedSamples: len(b.Examples), UncheckedCriteria: unchecked, Version: "cairn-calibration-execution-v1", CreatedAt: time.Now().UTC(), BundleDigest: bundleDigest, RubricDigest: assessment.Digest(b.Rubric), ChecksDigest: checksDigest, Image: c.Spec.Image, Isolation: "shared-kernel container; network none; read-only source, policy and rootfs; 64 PIDs", Limits: c.Spec.Limits, Samples: []checkEvidence{}, Note: "Private execution observations only. No grade, model request or instructor approval. A completed check does not establish adversarial tamper resistance or complete functional correctness. Unchecked or errored criteria remain unassessable."}
	save := func() error {
		raw, _ := json.MarshalIndent(report, "", "  ")
		return writeEvaluationFile(out, "execution.json", raw)
	}
	root, err := os.MkdirTemp("", "cairn-checks-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(root)
	policyDir := filepath.Join(root, "policy")
	if err = writeCheckFiles(policyDir, c.Files); err != nil {
		return err
	}
	for i, sample := range b.Examples {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		dir := filepath.Join(root, fmt.Sprintf("sample-%d", i))
		if err = writeCheckFiles(dir, sample.Files); err != nil {
			return err
		}
		e := checkEvidence{SampleID: sample.ID, SourceHashes: map[string]string{}, Status: "completed"}
		for p, source := range sample.Files {
			e.SourceHashes[p] = assessment.DigestBytes([]byte(source))
		}
		result, runErr := runner.RunWithPolicy(ctx, c.Spec, dir, policyDir)
		e.Tests = checkObservations(result.Tests)
		if runErr != nil {
			e.Status = "execution_error"
		}
		report.Samples = append(report.Samples, e)
		if err = save(); err != nil {
			return err
		}
		if runErr != nil {
			return errors.New("isolated execution failed; inspect private report; no grade was created")
		}
	}
	report.Completed = true
	if err = save(); err != nil {
		return err
	}
	fmt.Printf("Saved %d private execution observations. No model request or grade was created.\n", len(report.Samples))
	return nil
}
func writeCheckFiles(dir string, files map[string]string) error {
	if err := os.Mkdir(dir, 0755); err != nil {
		return err
	}
	for p, body := range files {
		if !assessment.ValidPath(p) {
			return errors.New("unsafe check path")
		}
		dest := filepath.Join(dir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
			return err
		}
		if err := os.WriteFile(dest, []byte(body), 0644); err != nil {
			return err
		}
	}
	return nil
}

func localDockerContext() error {
	endpoint := os.Getenv("DOCKER_HOST")
	if os.Getenv("DOCKER_CONTEXT") != "" || endpoint == "" {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		args := []string{"context", "inspect", "--format", "{{.Endpoints.docker.Host}}"}
		if c := os.Getenv("DOCKER_CONTEXT"); c != "" {
			args = append(args, c)
		}
		raw, err := exec.CommandContext(ctx, "docker", args...).Output()
		if err != nil {
			return errors.New("cannot verify a local Docker endpoint")
		}
		endpoint = strings.TrimSpace(string(raw))
	}
	if !strings.HasPrefix(endpoint, "unix:///") {
		return errors.New("offline calibration execution requires a local Unix-socket Docker endpoint")
	}
	return nil
}
