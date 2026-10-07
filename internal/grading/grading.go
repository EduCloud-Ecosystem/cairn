// SPDX-License-Identifier: AGPL-3.0-or-later

// Package grading executes an assignment's grading spec against a student's
// submission and records the score.
//
// SECURITY BOUNDARY: instructor policy comes from a pinned template commit;
// learner code still executes as untrusted input. ContainerRunner provides a
// read-only policy mount and resource bounds. ExecRunner is explicitly unsafe
// and only suitable for trusted local development. See docs/grading-policy.md.
package grading

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/EduCloud-Ecosystem/cairn/internal/id"
	"github.com/EduCloud-Ecosystem/cairn/internal/store"
	"github.com/EduCloud-Ecosystem/cairn/pkg/adapter"
	"github.com/EduCloud-Ecosystem/cairn/pkg/gradingspec"
)

// TestResult is the outcome of a single graded check.
type TestResult struct {
	Name      string  `json:"name"`
	Passed    bool    `json:"passed"`
	Points    float64 `json:"points"`     // points awarded (0 when failed)
	MaxPoints float64 `json:"max_points"` // points available
	Detail    string  `json:"detail,omitempty"`
}

// Result is the aggregate outcome of a grading run.
// PolicyEvidence identifies the instructor policy used for this historical score.
type PolicyEvidence struct {
	Repository adapter.RepoRef `json:"repository"`
	Revision   string          `json:"revision"`
	Path       string          `json:"path"`
	SHA256     string          `json:"sha256"`
}

type Result struct {
	SubmissionRevision string          `json:"submission_revision,omitempty"`
	Policy             *PolicyEvidence `json:"policy,omitempty"`
	Score              float64         `json:"score"`
	MaxScore           float64         `json:"max_score"`
	Tests              []TestResult    `json:"tests"`
	Log                string          `json:"log,omitempty"`
}

// Runner executes a grading spec against a checked-out repo at dir and returns
// the score. A non-zero student exit code is a failed test, not a Runner error;
// Runner returns a non-nil error only for infrastructural failures (could not
// start the process, etc.).
type Runner interface {
	Name() string
	Run(ctx context.Context, spec gradingspec.Spec, dir string) (Result, error)
}

// Checkout materializes a submission's repository into a local directory.
type Checkout interface {
	Fetch(ctx context.Context, repo adapter.RepoRef, dir string) error
}

// Service orchestrates a grading run: check out the repo, load the spec, run it,
// and persist a GradingRun plus a Grade.
type Service struct {
	Store    store.Store
	Runner   Runner
	Checkout Checkout
	Now      func() time.Time // injectable for tests; defaults to time.Now
}

// NewService wires a grading Service.
func NewService(s store.Store, r Runner, c Checkout) *Service {
	return &Service{Store: s, Runner: r, Checkout: c}
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Grade runs grading for one submission. It is the method the provisioning
// worker invokes for a JobGrade. TargetRef is the submission ID.
func (s *Service) Grade(ctx context.Context, submissionID string) error {
	return s.grade(ctx, submissionID, "")
}

// GradeRevision evaluates the immutable push revision, never a later moving head.
func (s *Service) GradeRevision(ctx context.Context, submissionID, revision string) error {
	if !ValidPolicyRevision(revision) {
		return errors.New("grading requires a full submission commit ID")
	}
	return s.grade(ctx, submissionID, strings.ToLower(revision))
}

func (s *Service) grade(ctx context.Context, submissionID, revision string) error {
	sub, err := s.Store.GetSubmission(ctx, submissionID)
	if err != nil {
		return fmt.Errorf("get submission: %w", err)
	}
	if sub.Repo.Name == "" {
		return errors.New("grading: submission has no provisioned repo")
	}
	asg, err := s.Store.GetAssignment(ctx, sub.AssignmentID)
	if err != nil {
		return fmt.Errorf("get assignment: %w", err)
	}

	started := s.now()
	run := &store.GradingRun{
		ID:           id.New(),
		SubmissionID: sub.ID,
		Status:       "running",
		Runner:       s.Runner.Name(),
		StartedAt:    &started,
	}
	if err := s.Store.CreateGradingRun(ctx, run); err != nil {
		return fmt.Errorf("create grading run: %w", err)
	}

	res, gradeErr := s.execute(ctx, sub.Repo, asg, revision)
	finished := s.now()
	run.FinishedAt = &finished

	if gradeErr != nil {
		run.Status = "failed"
		run.Result, _ = json.Marshal(map[string]string{"error": gradeErr.Error()})
		_ = s.Store.UpdateGradingRun(ctx, run)
		return gradeErr
	}

	breakdown, _ := json.Marshal(res)
	run.Status = "completed"
	run.Result = breakdown
	if err := s.Store.UpdateGradingRun(ctx, run); err != nil {
		return fmt.Errorf("update grading run: %w", err)
	}

	grade := &store.Grade{
		ID:           id.New(),
		SubmissionID: sub.ID,
		Score:        res.Score,
		MaxScore:     res.MaxScore,
		Breakdown:    breakdown,
		RunID:        run.ID,
		GradedAt:     s.now(),
	}
	if err := s.Store.CreateGrade(ctx, grade); err != nil {
		return fmt.Errorf("create grade: %w", err)
	}
	return nil
}

// PolicyRunner exposes instructor files independently of the writable submission.
// ContainerRunner mounts them read-only; local-exec remains explicitly unsafe.
type PolicyRunner interface {
	RunWithPolicy(context.Context, gradingspec.Spec, string, string) (Result, error)
}

type RevisionCheckout interface {
	FetchRevision(context.Context, adapter.RepoRef, string, string) error
}

func (s *Service) execute(ctx context.Context, repo adapter.RepoRef, asg *store.Assignment, revision string) (Result, error) {
	if !ValidPolicyRevision(asg.TemplateRef.Ref) || asg.TemplateRef.Namespace == "" || asg.TemplateRef.Name == "" {
		return Result{}, errors.New("grading requires an instructor template pinned to a full commit ID; configure the assignment grading policy")
	}
	if !filepath.IsLocal(asg.GradingSpec) || strings.Split(filepath.ToSlash(asg.GradingSpec), "/")[0] == ".git" {
		return Result{}, errors.New("grading spec must be a relative path inside the instructor template")
	}
	checkout, ok := s.Checkout.(RevisionCheckout)
	if !ok {
		return Result{}, errors.New("checkout does not support pinned grading policies")
	}
	runner, ok := s.Runner.(PolicyRunner)
	if !ok {
		return Result{}, errors.New("runner does not support separate instructor policy files")
	}
	policyDir, err := os.MkdirTemp("", "cairn-policy-*")
	if err != nil {
		return Result{}, err
	}
	defer os.RemoveAll(policyDir)
	template := adapter.RepoRef{Host: asg.TemplateRef.Host, Namespace: asg.TemplateRef.Namespace, Name: asg.TemplateRef.Name}
	if err := checkout.FetchRevision(ctx, template, asg.TemplateRef.Ref, policyDir); err != nil {
		return Result{}, fmt.Errorf("instructor policy checkout: %w", err)
	}
	if err := os.RemoveAll(filepath.Join(policyDir, ".git")); err != nil {
		return Result{}, err
	}
	root, err := os.OpenRoot(policyDir)
	if err != nil {
		return Result{}, err
	}
	defer root.Close()
	file, err := root.Open(asg.GradingSpec)
	if err != nil {
		return Result{}, fmt.Errorf("open instructor grading spec: %w", err)
	}
	defer file.Close()
	const maxSpecBytes = 1024 * 1024
	data, err := io.ReadAll(io.LimitReader(file, maxSpecBytes+1))
	if err != nil {
		return Result{}, err
	}
	if len(data) > maxSpecBytes {
		return Result{}, errors.New("grading spec exceeds 1 MiB")
	}
	spec, err := parseSpec(data)
	if err != nil {
		return Result{}, err
	}
	dir, err := os.MkdirTemp("", "cairn-grade-*")
	if err != nil {
		return Result{}, err
	}
	defer os.RemoveAll(dir)
	var fetchErr error
	if revision != "" {
		fetchErr = checkout.FetchRevision(ctx, repo, revision, dir)
	} else {
		fetchErr = s.Checkout.Fetch(ctx, repo, dir)
	}
	if err := fetchErr; err != nil {
		return Result{}, fmt.Errorf("submission checkout: %w", err)
	}
	if err := os.RemoveAll(filepath.Join(dir, ".git")); err != nil {
		return Result{}, fmt.Errorf("strip .git: %w", err)
	}
	// Never read grading.json or policy files from the learner's checkout.
	res, err := runner.RunWithPolicy(ctx, spec, dir, policyDir)
	res.SubmissionRevision = revision
	res.Policy = &PolicyEvidence{Repository: template, Revision: strings.ToLower(asg.TemplateRef.Ref), Path: asg.GradingSpec, SHA256: fmt.Sprintf("%x", sha256.Sum256(data))}
	return res, err
}

// loadSpec reads and parses a JSON grading spec. (The on-disk convention is
// grading.json; YAML support is a thin add for production but is omitted here to
// keep the bundled runner dependency-free.)
func loadSpec(path string) (gradingspec.Spec, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return gradingspec.Spec{}, fmt.Errorf("read grading spec: %w", err)
	}
	return parseSpec(b)
}

func parseSpec(b []byte) (gradingspec.Spec, error) {
	var spec gradingspec.Spec
	if err := json.Unmarshal(b, &spec); err != nil {
		return gradingspec.Spec{}, fmt.Errorf("parse grading spec: %w", err)
	}
	if len(spec.Tests) == 0 {
		return gradingspec.Spec{}, errors.New("grading spec defines no tests")
	}
	if spec.Version != "" && spec.Version != gradingspec.Version {
		return spec, errors.New("unsupported grading spec version")
	}
	if !isFinite(spec.MaxScore()) {
		return spec, errors.New("grading points total must be finite")
	}
	for i, t := range spec.Tests {
		if !isFinite(t.Points) || t.Points < 0 {
			return spec, fmt.Errorf("grading spec test[%d] points must be finite and non-negative", i)
		}
		if t.Run == "" {
			return gradingspec.Spec{}, fmt.Errorf("grading spec test[%d] %q has empty run field", i, t.Name)
		}
	}
	return spec, nil
}

// truncate caps a string for safe storage in logs/details.
func truncate(s string) string {
	const max = 2000
	if len(s) <= max {
		return s
	}
	return s[:max] + "…(truncated)"
}

func isFinite(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) }
