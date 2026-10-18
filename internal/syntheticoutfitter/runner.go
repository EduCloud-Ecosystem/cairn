// SPDX-License-Identifier: AGPL-3.0-or-later

// Package syntheticoutfitter is an opt-in disposable-fixture Runner. It is not
// connected to Cairn's Service, queue, configuration, or grade publication.
package syntheticoutfitter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"time"

	"github.com/EduCloud-Ecosystem/cairn/internal/grading"
	"github.com/EduCloud-Ecosystem/cairn/pkg/gradingspec"
)

const consumer = "cairn-synthetic-v1"

// ErrUnassessable must never be translated into a zero grade or a refund.
var ErrUnassessable = errors.New("synthetic execution unassessable; retain binding and reconcile")

// Config has no environment/default wiring. All identifiers describe fixtures.
// AuthorityID and BinarySHA256 pin the separately initialized broker. A restored
// authority is usable only for reads and only with its explicit checkpoint pin.
type Config struct {
	SyntheticOnly            bool                   `json:"synthetic_only"`
	Binary                   string                 `json:"binary"`
	BinarySHA256             string                 `json:"binary_sha256"`
	StateDir                 string                 `json:"state_dir"`
	BindingDir               string                 `json:"binding_dir"`
	AuthorityID              string                 `json:"authority_id"`
	RecoveryCheckpointSHA256 string                 `json:"recovery_checkpoint_sha256,omitempty"`
	ProjectID                string                 `json:"project_id"`
	JobID                    string                 `json:"job_id"`
	SubmissionRevision       string                 `json:"submission_revision"`
	Policy                   grading.PolicyEvidence `json:"policy"`
}

type Runner struct {
	config Config
	call   commandFunc
}
type commandFunc func(context.Context, string, any) ([]byte, error)

var _ grading.Runner = (*Runner)(nil)
var _ grading.PolicyRunner = (*Runner)(nil)

func New(c Config) (*Runner, error) {
	if err := c.validate(); err != nil {
		return nil, err
	}
	r := &Runner{config: c}
	r.call = r.command
	return r, nil
}
func (r *Runner) Name() string { return "outfitter-disposable-synthetic" }
func (r *Runner) Run(context.Context, gradingspec.Spec, string) (grading.Result, error) {
	return grading.Result{}, errors.New("separate pinned instructor policy is required")
}

type fileEntry struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}
type workload struct {
	SchemaVersion         int      `json:"schema_version"`
	Image                 string   `json:"image"`
	Command               []string `json:"command"`
	InputDir              string   `json:"input_dir"`
	RecipeDir             string   `json:"recipe_dir"`
	CPUs                  float64  `json:"cpus"`
	MemoryMB              int      `json:"memory_mb"`
	PIDsLimit             int      `json:"pids_limit"`
	TimeoutSeconds        int      `json:"timeout_seconds"`
	MaxOutputBytes        int      `json:"max_output_bytes"`
	Isolation             string   `json:"isolation"`
	AllowSupervisedReaper bool     `json:"allow_supervised_reaper"`
}
type request struct {
	SchemaVersion        int         `json:"schema_version"`
	Consumer             string      `json:"consumer"`
	ProjectID            string      `json:"project_id"`
	JobID                string      `json:"job_id"`
	Attempt              int         `json:"attempt"`
	SubmissionCommit     string      `json:"submission_commit"`
	PolicyCommit         string      `json:"policy_commit"`
	PolicyManifestSHA256 string      `json:"policy_manifest_sha256"`
	InputFiles           []fileEntry `json:"input_files"`
	RecipeFiles          []fileEntry `json:"recipe_files"`
	Workload             workload    `json:"workload"`
}
type preflight struct {
	SchemaVersion      int    `json:"schema_version"`
	RequestID          string `json:"request_id"`
	BindingSHA256      string `json:"binding_sha256"`
	ReservedCPUSeconds int64  `json:"reserved_cpu_seconds"`
	GradePublished     bool   `json:"grade_published"`
}
type intent struct {
	Version      int                    `json:"version"`
	AuthorityID  string                 `json:"authority_id"`
	BinarySHA256 string                 `json:"binary_sha256"`
	Policy       grading.PolicyEvidence `json:"policy"`
	Spec         gradingspec.Spec       `json:"spec"`
	Request      request                `json:"request"`
	Preflight    preflight              `json:"preflight"`
}
type sealedIntent struct {
	Intent intent `json:"intent"`
	SHA256 string `json:"sha256"`
}

var identifier = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)

func isHex(s string, n int) bool {
	b, e := hex.DecodeString(s)
	return e == nil && len(b) == n && strings.ToLower(s) == s
}
func digest(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func encode(v any) []byte    { b, _ := json.Marshal(v); return b }
func commit(s string) bool   { return isHex(s, 20) || isHex(s, 32) }
func relative(s string) bool {
	return s != "" && s != "." && s != ".." && filepath.ToSlash(filepath.Clean(s)) == s && !filepath.IsAbs(s) && !strings.HasPrefix(s, "../") && !strings.ContainsAny(s, "\\\x00\r\n") && len(s) <= 256
}
func (c Config) validate() error {
	if !c.SyntheticOnly || !isHex(c.BinarySHA256, 32) || !isHex(c.AuthorityID, 16) || !identifier.MatchString(c.ProjectID) || !identifier.MatchString(c.JobID) || !commit(c.SubmissionRevision) || !commit(c.Policy.Revision) || !isHex(c.Policy.SHA256, 32) || !relative(c.Policy.Path) {
		return errors.New("explicit synthetic opt-in and pinned fixture identities required")
	}
	if c.RecoveryCheckpointSHA256 != "" && !isHex(c.RecoveryCheckpointSHA256, 32) {
		return errors.New("invalid recovery checkpoint pin")
	}
	for _, p := range []string{c.Binary, c.StateDir, c.BindingDir} {
		if !filepath.IsAbs(p) || filepath.Clean(p) != p {
			return errors.New("absolute clean broker and binding paths required")
		}
	}
	return nil
}
func validateSpec(s gradingspec.Spec) error {
	if s.Version != "1" || len(s.Setup) != 0 || len(s.Tests) != 1 || len(encode(s)) > 8192 || !(strings.HasPrefix(s.Image, "sha256:") && isHex(strings.TrimPrefix(s.Image, "sha256:"), 32) || strings.Contains(s.Image, "@sha256:") && isHex(strings.SplitN(s.Image, "@sha256:", 2)[1], 32)) {
		return errors.New("synthetic profile requires version 1, pinned image and one check without setup")
	}
	t := s.Tests[0]
	l := s.Limits
	if t.Run != "python /input/submission.py" || t.Name == "" || len(t.Name) > 128 || t.Limits != nil || math.IsNaN(t.Points) || math.IsInf(t.Points, 0) || t.Points < 0 || l.Timeout < time.Second || l.Timeout > 30*time.Second || l.Timeout%time.Second != 0 || math.IsNaN(l.CPUs) || l.CPUs <= 0 || l.CPUs > 1 || l.MemoryMB < 64 || l.MemoryMB > 256 || l.Network != "" && l.Network != gradingspec.NetworkNone {
		return errors.New("unsupported synthetic command or limits")
	}
	return nil
}
func requestID(project, job string) string {
	return "consumer-" + digest(encode([]string{"consumer-key-v1", consumer, project, job, "1"}))
}

func (r *Runner) prepare(s gradingspec.Spec, input, policy string) (intent, error) {
	c := r.config
	var empty intent
	if err := validateSpec(s); err != nil {
		return empty, err
	}
	inputs, err := manifest(input)
	if err != nil {
		return empty, err
	}
	entryFound := false
	for _, f := range inputs {
		if f.Path == "submission.py" {
			entryFound = true
		}
	}
	if !entryFound {
		return empty, errors.New("synthetic submission.py is required")
	}
	recipes, err := manifest(policy)
	if err != nil {
		return empty, err
	}
	var total int64
	for _, m := range [][]fileEntry{inputs, recipes} {
		for _, f := range m {
			total += f.Size
		}
	}
	if len(inputs)+len(recipes) > 128 || total > 16<<20 {
		return empty, errors.New("synthetic input limits exceeded")
	}
	root, err := os.OpenRoot(policy)
	if err != nil {
		return empty, err
	}
	defer root.Close()
	b, err := readBounded(root, c.Policy.Path, 8192)
	if err != nil {
		return empty, err
	}
	var parsed gradingspec.Spec
	if digest(b) != c.Policy.SHA256 || decode(b, &parsed, true) != nil || !reflect.DeepEqual(parsed, s) {
		return empty, errors.New("policy bytes, hash and scoring spec disagree")
	}
	// The policy hash must also describe the bytes the broker will snapshot.
	found := false
	for _, f := range recipes {
		if f.Path == c.Policy.Path {
			found = f.SHA256 == c.Policy.SHA256
		}
	}
	if !found {
		return empty, errors.New("policy manifest mismatch")
	}
	w := workload{1, s.Image, []string{"python", "/input/submission.py"}, input, policy, s.Limits.CPUs, s.Limits.MemoryMB, 64, int(s.Limits.Timeout / time.Second), 65536, "reviewed-local", true}
	q := request{1, consumer, c.ProjectID, c.JobID, 1, c.SubmissionRevision, c.Policy.Revision, c.Policy.SHA256, inputs, recipes, w}
	return intent{Version: 1, AuthorityID: c.AuthorityID, BinarySHA256: c.BinarySHA256, Policy: c.Policy, Spec: s, Request: q}, nil
}

// RunWithPolicy persists one immutable intent before the sole dispatch. Every
// later delivery is status-only, even if admission failed or no receipt exists.
func (r *Runner) RunWithPolicy(ctx context.Context, spec gradingspec.Spec, input, policy string) (grading.Result, error) {
	i, err := r.prepare(spec, input, policy)
	if err != nil {
		return grading.Result{}, err
	}
	unlock, err := r.lock(ctx)
	if err != nil {
		return grading.Result{}, err
	}
	old, exists, err := r.load()
	if err != nil {
		unlock()
		return grading.Result{}, err
	}
	if exists {
		if !sameIntent(old, i) {
			unlock()
			return grading.Result{}, errors.New("job binding conflict; no dispatch")
		}
		unlock()
		return r.status(ctx, old)
	}
	if err = r.authority(ctx, true); err != nil {
		unlock()
		return grading.Result{}, err
	}
	b, err := r.call(ctx, "consumer-preflight", i.Request)
	if err == nil {
		err = decode(b, &i.Preflight, false)
	}
	if err == nil && (i.Preflight.SchemaVersion != 1 || i.Preflight.RequestID != requestID(r.config.ProjectID, r.config.JobID) || !isHex(i.Preflight.BindingSHA256, 32) || i.Preflight.ReservedCPUSeconds != int64(math.Ceil(spec.Limits.CPUs*float64(spec.Limits.Timeout/time.Second))) || i.Preflight.GradePublished) {
		err = errors.New("invalid preflight")
	}
	if err == nil {
		err = r.save(i)
	}
	unlock()
	if err != nil {
		return grading.Result{}, err
	}
	// There is deliberately no rollback of intent, even before admission.
	b, err = r.call(ctx, "consumer-run", i.Request)
	if err != nil {
		return grading.Result{}, ErrUnassessable
	}
	return score(i, b)
}
func sameIntent(a, b intent) bool {
	a.Preflight = preflight{}
	b.Preflight = preflight{}
	a.Request.Workload.InputDir = ""
	a.Request.Workload.RecipeDir = ""
	b.Request.Workload.InputDir = ""
	b.Request.Workload.RecipeDir = ""
	return reflect.DeepEqual(a, b)
}
func (r *Runner) existing(ctx context.Context) (intent, error) {
	unlock, err := r.lock(ctx)
	if err != nil {
		return intent{}, err
	}
	defer unlock()
	i, exists, err := r.load()
	if err != nil {
		return i, err
	}
	if !exists {
		return i, errors.New("no local intent; recovery never dispatches")
	}
	return i, nil
}

// Recover needs no checkout paths and works against an explicitly pinned held
// restore. It neither activates the authority nor reruns any workload.
func (r *Runner) Recover(ctx context.Context) (grading.Result, error) {
	i, err := r.existing(ctx)
	if err != nil {
		return grading.Result{}, err
	}
	return r.status(ctx, i)
}
func (r *Runner) status(ctx context.Context, i intent) (grading.Result, error) {
	if err := r.authority(ctx, false); err != nil {
		return grading.Result{}, err
	}
	b, err := r.call(ctx, "consumer-status", nil)
	if err != nil {
		return grading.Result{}, ErrUnassessable
	}
	return score(i, b)
}

// Cancel preserves the intent. Cancellation cannot produce a grading result.
func (r *Runner) Cancel(ctx context.Context) error {
	i, err := r.existing(ctx)
	if err != nil {
		return err
	}
	if err = r.authority(ctx, true); err != nil {
		return err
	}
	_, err = r.call(ctx, "cancel", i.Preflight.RequestID)
	return err
}
func (r *Runner) authority(ctx context.Context, dispatch bool) error {
	b, err := r.call(ctx, "recovery-status", nil)
	if err != nil {
		return err
	}
	var a struct {
		SchemaVersion int    `json:"schema_version"`
		AuthorityID   string `json:"authority_id"`
		Admission     string `json:"admission"`
		Source        string `json:"source_authority_id"`
		Checkpoint    string `json:"checkpoint_sha256"`
	}
	if decode(b, &a, false) != nil || a.SchemaVersion != 1 || !isHex(a.AuthorityID, 16) || (a.Admission != "active" && a.Admission != "held" && a.Admission != "fenced") {
		return errors.New("invalid broker authority")
	}
	c := r.config
	original := a.AuthorityID == c.AuthorityID
	restored := !dispatch && c.RecoveryCheckpointSHA256 != "" && a.Source == c.AuthorityID && a.Checkpoint == c.RecoveryCheckpointSHA256
	if (!original && !restored) || dispatch && (a.Admission != "active" || c.RecoveryCheckpointSHA256 != "") {
		return errors.New("broker authority or admission differs from pinned intent")
	}
	return nil
}

type receipt struct {
	SchemaVersion    int    `json:"schema_version"`
	Consumer         string `json:"consumer"`
	ProjectID        string `json:"project_id"`
	JobID            string `json:"job_id"`
	Attempt          int    `json:"attempt"`
	RequestID        string `json:"request_id"`
	BindingSHA256    string `json:"binding_sha256"`
	Image            string `json:"image"`
	Status           string `json:"status"`
	ExitCode         *int   `json:"exit_code"`
	Stdout           string `json:"stdout"`
	Stderr           string `json:"stderr"`
	Error            string `json:"error"`
	OutputError      string `json:"output_error"`
	Admitted         bool   `json:"admitted"`
	ExecutionStarted bool   `json:"execution_started"`
	CleanupVerified  bool   `json:"cleanup_verified"`
	Unresolved       bool   `json:"unresolved_liability"`
	Reconciliation   bool   `json:"reconciliation_required"`
	GradePublished   bool   `json:"grade_published"`
	CancelRequested  bool   `json:"cancel_requested"`
	Compute          struct {
		Reserved   int64  `json:"reserved_cpu_seconds"`
		Charged    int64  `json:"charged_cpu_seconds"`
		Provenance string `json:"provenance"`
	} `json:"compute"`
}

func score(i intent, b []byte) (grading.Result, error) {
	fail := func() (grading.Result, error) { return grading.Result{}, ErrUnassessable }
	var keys map[string]json.RawMessage
	var r receipt
	if decode(b, &keys, false) != nil || decode(b, &r, false) != nil {
		return fail()
	}
	for _, k := range strings.Fields("schema_version consumer project_id job_id attempt request_id binding_sha256 image status exit_code stdout stderr error admitted execution_started cleanup_verified unresolved_liability reconciliation_required grade_published cancel_requested compute") {
		if v, ok := keys[k]; !ok || string(v) == "null" {
			return fail()
		}
	}
	q := i.Request
	if r.SchemaVersion != 1 || r.Consumer != consumer || r.ProjectID != q.ProjectID || r.JobID != q.JobID || r.Attempt != 1 || r.RequestID != i.Preflight.RequestID || r.BindingSHA256 != i.Preflight.BindingSHA256 || r.Image != q.Workload.Image || !r.Admitted || !r.ExecutionStarted || !r.CleanupVerified || r.Unresolved || r.Reconciliation || r.GradePublished || r.CancelRequested || r.OutputError != "" || r.ExitCode == nil || r.Compute.Reserved != i.Preflight.ReservedCPUSeconds || r.Compute.Charged != r.Compute.Reserved || r.Compute.Provenance != "estimated_upper_bound" || len(r.Stdout)+len(r.Stderr) > q.Workload.MaxOutputBytes {
		return fail()
	}
	if !(r.Status == "succeeded" && *r.ExitCode == 0 && r.Error == "" || r.Status == "failed" && *r.ExitCode > 0 && *r.ExitCode <= 255 && r.Error == fmt.Sprintf("workload exited with code %d", *r.ExitCode)) {
		return fail()
	}
	t := i.Spec.Tests[0]
	passed := *r.ExitCode == 0
	if t.Match != nil {
		actual, want := r.Stdout, t.Match.Expected
		if t.Match.Trim {
			actual = strings.TrimSpace(actual)
			want = strings.TrimSpace(want)
		}
		passed = passed && actual == want
	}
	points := float64(0)
	if passed {
		points = t.Points
	}
	return grading.Result{SubmissionRevision: q.SubmissionCommit, Policy: &i.Policy, Score: points, MaxScore: t.Points, Tests: []grading.TestResult{{ExitCode: r.ExitCode, Name: t.Name, Passed: passed, Points: points, MaxPoints: t.Points}}, Log: truncate(r.Stdout + r.Stderr)}, nil
}
func truncate(s string) string {
	if len(s) > 2000 {
		return s[:2000]
	}
	return s
}
