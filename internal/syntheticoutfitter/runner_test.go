// SPDX-License-Identifier: AGPL-3.0-or-later

package syntheticoutfitter

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/EduCloud-Ecosystem/cairn/internal/grading"
	"github.com/EduCloud-Ecosystem/cairn/internal/provisioning"
	"github.com/EduCloud-Ecosystem/cairn/internal/store/memory"
	"github.com/EduCloud-Ecosystem/cairn/pkg/adapter"
	"github.com/EduCloud-Ecosystem/cairn/pkg/gradingspec"
	"golang.org/x/sys/unix"
)

type fixture struct {
	r             *Runner
	spec          gradingspec.Spec
	input, policy string
	broker        *fakeBroker
}
type fakeBroker struct {
	mu                                       sync.Mutex
	r                                        *Runner
	runs, statuses                           int
	receipt                                  map[string]any
	runError                                 bool
	statusError                              bool
	authority, source, checkpoint, admission string
}

func setup(t *testing.T) fixture {
	t.Helper()
	dir := t.TempDir()
	input := filepath.Join(dir, "input")
	policy := filepath.Join(dir, "policy")
	for _, p := range []string{input, policy} {
		if err := os.Mkdir(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	spec := gradingspec.Spec{Version: "1", Image: "sha256:" + strings.Repeat("a", 64), Tests: []gradingspec.Test{{Name: "answer", Run: "python /input/submission.py", Points: 10, Match: &gradingspec.OutputMatch{Expected: "42", Trim: true}}}, Limits: gradingspec.Limits{Timeout: 30 * time.Second, MemoryMB: 256, CPUs: 1}}
	if err := os.WriteFile(filepath.Join(input, "submission.py"), []byte("print(42)\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(policy, "grading.json"), encode(spec), 0600); err != nil {
		t.Fatal(err)
	}
	c := Config{SyntheticOnly: true, Binary: filepath.Join(dir, "outfitter"), BinarySHA256: strings.Repeat("a", 64), StateDir: filepath.Join(dir, "state"), BindingDir: filepath.Join(dir, "bindings"), AuthorityID: strings.Repeat("b", 32), ProjectID: "synthetic", JobID: "job", SubmissionRevision: strings.Repeat("c", 40), Policy: grading.PolicyEvidence{Repository: adapter.RepoRef{Host: adapter.HostGitHub, Namespace: "synthetic", Name: "instructor"}, Revision: strings.Repeat("d", 40), Path: "grading.json", SHA256: digest(encode(spec))}}
	r, err := New(c)
	if err != nil {
		t.Fatal(err)
	}
	b := &fakeBroker{r: r, authority: c.AuthorityID, admission: "active"}
	r.call = b.call
	return fixture{r, spec, input, policy, b}
}
func (b *fakeBroker) call(ctx context.Context, verb string, payload any) ([]byte, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	switch verb {
	case "recovery-status":
		return encode(map[string]any{"schema_version": 1, "authority_id": b.authority, "admission": b.admission, "source_authority_id": b.source, "checkpoint_sha256": b.checkpoint}), nil
	case "consumer-preflight":
		return encode(preflight{1, requestID(b.r.config.ProjectID, b.r.config.JobID), strings.Repeat("e", 64), 30, false}), nil
	case "consumer-run":
		b.runs++
		i, exists, err := b.r.load()
		if err != nil || !exists {
			return nil, errors.New("dispatch before durable intent")
		}
		if b.receipt == nil {
			b.receipt = goodReceipt(i)
		}
		if b.runError {
			return nil, errors.New("response lost")
		}
		return encode(b.receipt), nil
	case "consumer-status":
		b.statuses++
		if b.statusError {
			return nil, errors.New("not found")
		}
		return encode(b.receipt), nil
	case "cancel":
		b.receipt["cancel_requested"] = true
		b.receipt["status"] = "cancelled"
		return encode(b.receipt), nil
	}
	return nil, errors.New("unexpected command")
}
func goodReceipt(i intent) map[string]any {
	return map[string]any{
		"schema_version": 1, "consumer": consumer, "project_id": i.Request.ProjectID, "job_id": i.Request.JobID, "attempt": 1, "request_id": i.Preflight.RequestID, "binding_sha256": i.Preflight.BindingSHA256, "image": i.Request.Workload.Image,
		"status": "succeeded", "exit_code": 0, "stdout": "42\n", "stderr": "", "error": "", "admitted": true, "execution_started": true, "cleanup_verified": true, "unresolved_liability": false, "reconciliation_required": false, "grade_published": false, "cancel_requested": false,
		"compute": map[string]any{"reserved_cpu_seconds": 30, "charged_cpu_seconds": 30, "provenance": "estimated_upper_bound"},
	}
}
func (f fixture) run() (grading.Result, error) {
	return f.r.RunWithPolicy(context.Background(), f.spec, f.input, f.policy)
}
func TestCorrectWrongAndNonzero(t *testing.T) {
	for _, tc := range []struct {
		name, stdout, status, problem string
		exit                          int
		want                          float64
	}{{"correct", "42\n", "succeeded", "", 0, 10}, {"wrong", "41\n", "succeeded", "", 0, 0}, {"nonzero-match", "42\n", "failed", "workload exited with code 1", 1, 0}} {
		t.Run(tc.name, func(t *testing.T) {
			f := setup(t)
			if _, err := f.run(); err != nil {
				t.Fatal(err)
			}
			f.broker.receipt["stdout"] = tc.stdout
			f.broker.receipt["status"] = tc.status
			f.broker.receipt["error"] = tc.problem
			f.broker.receipt["exit_code"] = tc.exit
			r, err := f.r.Recover(context.Background())
			if err != nil || r.Score != tc.want || r.SubmissionRevision != f.r.config.SubmissionRevision || r.Policy.SHA256 != f.r.config.Policy.SHA256 {
				t.Fatalf("result %+v error %v", r, err)
			}
		})
	}
}
func TestInvalidReceiptsNeverBecomeGrades(t *testing.T) {
	changes := map[string]any{"schema_version": 2, "consumer": "other", "project_id": "other", "job_id": "other", "attempt": 2, "request_id": "other", "binding_sha256": strings.Repeat("f", 64), "image": "other", "status": "unknown", "exit_code": nil, "stdout": nil, "stderr": nil, "error": "deadline exceeded", "admitted": false, "execution_started": false, "cleanup_verified": false, "unresolved_liability": true, "reconciliation_required": true, "grade_published": true, "cancel_requested": true, "output_error": "missing diagnostics", "compute": map[string]any{"reserved_cpu_seconds": 30, "charged_cpu_seconds": 0, "provenance": "reserved"}}
	for key, value := range changes {
		t.Run(key, func(t *testing.T) {
			f := setup(t)
			if _, err := f.run(); err != nil {
				t.Fatal(err)
			}
			f.broker.receipt[key] = value
			res, err := f.r.Recover(context.Background())
			if !errors.Is(err, ErrUnassessable) || len(res.Tests) != 0 {
				t.Fatalf("invalid receipt scored: %+v %v", res, err)
			}
		})
	}
	for _, key := range strings.Fields("stdout stderr error cleanup_verified unresolved_liability reconciliation_required grade_published cancel_requested") {
		t.Run("missing-"+key, func(t *testing.T) {
			f := setup(t)
			_, _ = f.run()
			delete(f.broker.receipt, key)
			res, err := f.r.Recover(context.Background())
			if err == nil || len(res.Tests) != 0 {
				t.Fatal("missing field became a grade")
			}
		})
	}
}
func TestRepeatedDeliveryRestartAndLostResponse(t *testing.T) {
	f := setup(t)
	f.broker.runError = true
	if _, err := f.run(); !errors.Is(err, ErrUnassessable) {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := f.run(); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if f.broker.runs != 1 || f.broker.statuses != 8 {
		t.Fatalf("runs=%d status=%d", f.broker.runs, f.broker.statuses)
	}
	restarted, err := New(f.r.config)
	if err != nil {
		t.Fatal(err)
	}
	restarted.call = f.broker.call
	if err = os.RemoveAll(f.input); err != nil {
		t.Fatal(err)
	}
	if err = os.RemoveAll(f.policy); err != nil {
		t.Fatal(err)
	}
	result, err := restarted.Recover(context.Background())
	if err != nil || result.Score != 10 {
		t.Fatalf("restart %+v %v", result, err)
	}
}
func TestNoRetryAfterNotFoundBudgetRefusalOrCancellation(t *testing.T) {
	for _, mode := range []string{"missing", "budget", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			f := setup(t)
			f.broker.runError = true
			_, _ = f.run()
			switch mode {
			case "missing":
				f.broker.statusError = true
			case "budget":
				f.broker.receipt["admitted"] = false
				f.broker.receipt["execution_started"] = false
			case "cancelled":
				if err := f.r.Cancel(context.Background()); err != nil {
					t.Fatal(err)
				}
			}
			for range 3 {
				result, err := f.run()
				if err == nil || len(result.Tests) != 0 {
					t.Fatal("uncertain receipt graded")
				}
			}
			if f.broker.runs != 1 || f.broker.statuses != 3 {
				t.Fatalf("redispatched %+v", f.broker)
			}
		})
	}
}
func TestIdentityConflictBeforeDispatch(t *testing.T) {
	for _, kind := range []string{"input", "policy-bytes", "spec", "submission", "policy-revision", "binary", "authority"} {
		t.Run(kind, func(t *testing.T) {
			f := setup(t)
			_, err := f.run()
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "input":
				err = os.WriteFile(filepath.Join(f.input, "submission.py"), []byte("print(41)\n"), 0600)
			case "policy-bytes":
				err = os.WriteFile(filepath.Join(f.policy, "grading.json"), []byte("{}"), 0600)
			case "spec":
				f.spec.Tests[0].Points = 11
			case "submission":
				f.r.config.SubmissionRevision = strings.Repeat("f", 40)
			case "policy-revision":
				f.r.config.Policy.Revision = strings.Repeat("f", 40)
			case "binary":
				f.r.config.BinarySHA256 = strings.Repeat("f", 64)
			case "authority":
				f.r.config.AuthorityID = strings.Repeat("f", 32)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err = f.run(); err == nil {
				t.Fatal("changed identity accepted")
			}
			if f.broker.runs != 1 {
				t.Fatal("conflict dispatched")
			}
		})
	}
}
func TestHeldRestoreAndAuthorityReplacement(t *testing.T) {
	f := setup(t)
	_, err := f.run()
	if err != nil {
		t.Fatal(err)
	}
	f.broker.authority = strings.Repeat("f", 32)
	f.broker.source = f.r.config.AuthorityID
	f.broker.checkpoint = strings.Repeat("e", 64)
	f.broker.admission = "held"
	if _, err = f.r.Recover(context.Background()); err == nil {
		t.Fatal("unapproved replacement authority accepted")
	}
	f.r.config.RecoveryCheckpointSHA256 = f.broker.checkpoint
	if res, err := f.r.Recover(context.Background()); err != nil || res.Score != 10 {
		t.Fatalf("held recovery: %+v %v", res, err)
	}
	if err = f.r.Cancel(context.Background()); err == nil {
		t.Fatal("held authority mutation accepted")
	}
	f.r.config.JobID = "fresh"
	if _, err = f.run(); err == nil {
		t.Fatal("fresh dispatch to restored authority accepted")
	}
	if f.broker.runs != 1 {
		t.Fatal("recovery executed workload")
	}
}
func TestPartialIntentIsPermanentHold(t *testing.T) {
	f := setup(t)
	unlock, err := f.r.lock(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	err = os.WriteFile(f.r.intentPath(), []byte(`{"intent":`), 0600)
	unlock()
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if _, err = f.run(); err == nil {
			t.Fatal("corrupt intent reused")
		}
	}
	if f.broker.runs != 0 {
		t.Fatal("partial intent redispatched")
	}
}
func TestIntentBeforeDispatchCrash(t *testing.T) {
	f := setup(t)
	f.r.call = func(ctx context.Context, verb string, payload any) ([]byte, error) {
		if verb == "consumer-run" {
			return nil, context.Canceled
		}
		return f.broker.call(ctx, verb, payload)
	}
	_, _ = f.run()
	f.broker.statusError = true
	for range 3 {
		if _, err := f.run(); err == nil {
			t.Fatal("missing receipt accepted")
		}
	}
	if f.broker.runs != 0 || f.broker.statuses != 3 {
		t.Fatal("crash window retried dispatch")
	}
}

type fixtureGrader struct{ f fixture }

func (g fixtureGrader) Grade(ctx context.Context, _ string) error {
	_, err := g.f.r.RunWithPolicy(ctx, g.f.spec, g.f.input, g.f.policy)
	return err
}
func TestGenericWorkerRetriesReuseIntent(t *testing.T) {
	f := setup(t)
	f.broker.runError = true
	f.broker.statusError = true
	s := memory.New()
	q := provisioning.NewService(s)
	if err := q.Enqueue(context.Background(), provisioning.JobGrade, "synthetic-submission", "synthetic-delivery"); err != nil {
		t.Fatal(err)
	}
	w := provisioning.Worker{Store: s, Grader: fixtureGrader{f}, MaxAttempts: 3, Backoff: func(int) time.Duration { return 0 }}
	for range 3 {
		did, err := w.RunOnce(context.Background())
		if !did || !errors.Is(err, ErrUnassessable) {
			t.Fatalf("worker %v %v", did, err)
		}
	}
	if f.broker.runs != 1 || f.broker.statuses != 2 {
		t.Fatal("infrastructure retries dispatched again")
	}
}
func TestProfileRefusesUnsupportedExecution(t *testing.T) {
	for _, kind := range []string{"shell", "setup", "network", "limits", "image", "symlink", "missing-opt-in", "no-policy"} {
		t.Run(kind, func(t *testing.T) {
			f := setup(t)
			switch kind {
			case "shell":
				f.spec.Tests[0].Run = "sh -c 'python /input/submission.py'"
			case "setup":
				f.spec.Setup = []string{"true"}
			case "network":
				f.spec.Limits.Network = gradingspec.NetworkRestricted
			case "limits":
				f.spec.Limits.Timeout = 31 * time.Second
			case "image":
				f.spec.Image = "python:latest"
			case "symlink":
				if err := os.Symlink("submission.py", filepath.Join(f.input, "link")); err != nil {
					t.Fatal(err)
				}
			case "missing-opt-in":
				c := f.r.config
				c.SyntheticOnly = false
				if _, err := New(c); err == nil {
					t.Fatal("missing opt-in accepted")
				}
				return
			case "no-policy":
				if _, err := f.r.Run(context.Background(), f.spec, f.input); err == nil {
					t.Fatal("no policy accepted")
				}
				return
			}
			if _, err := f.run(); err == nil {
				t.Fatal("unsupported profile admitted")
			}
			if f.broker.runs != 0 {
				t.Fatal("profile dispatched")
			}
		})
	}
}
func TestClientBinaryPinAndOutputLimit(t *testing.T) {
	f := setup(t)
	if err := os.WriteFile(f.r.config.Binary, []byte("#!/bin/sh\necho '{}'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := f.r.command(context.Background(), "recovery-status", nil); err == nil {
		t.Fatal("binary mismatch accepted")
	}
	cancelled := false
	w := limitedOutput{remaining: 3, cancel: func() { cancelled = true }}
	if _, err := w.Write([]byte("four")); err == nil || !cancelled || w.Len() != 0 {
		t.Fatal("unbounded output")
	}
}
func TestIntentJSONChecksum(t *testing.T) {
	f := setup(t)
	_, err := f.run()
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(f.r.intentPath())
	if err != nil {
		t.Fatal(err)
	}
	var obj map[string]any
	if err = json.Unmarshal(b, &obj); err != nil {
		t.Fatal(err)
	}
	obj["sha256"] = strings.Repeat("0", 64)
	if err = os.WriteFile(f.r.intentPath(), encode(obj), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = f.r.Recover(context.Background()); err == nil {
		t.Fatal("corrupt intent graded")
	}
}

func TestNamedPipeCannotBlockFixtureInspection(t *testing.T) {
	f := setup(t)
	pipe := filepath.Join(f.input, "extra")
	if err := unix.Mkfifo(pipe, 0600); err != nil {
		t.Fatal(err)
	}
	for _, direct := range []bool{false, true} {
		done := make(chan error, 1)
		go func() {
			if direct {
				root, err := os.OpenRoot(f.input)
				if err != nil {
					done <- err
					return
				}
				defer root.Close()
				_, err = readBounded(root, "extra", 1024)
				done <- err
			} else {
				_, err := f.run()
				done <- err
			}
		}()
		select {
		case err := <-done:
			if err == nil {
				t.Fatal("named pipe accepted")
			}
		case <-time.After(time.Second):
			// Release a regressed blocking reader so the failed test leaves no goroutine.
			fd, err := unix.Open(pipe, unix.O_RDWR|unix.O_NONBLOCK, 0)
			if err == nil {
				_ = unix.Close(fd)
			}
			select {
			case <-done:
			case <-time.After(time.Second):
			}
			t.Fatal("fixture inspection blocked on named pipe")
		}
	}
	if f.broker.runs != 0 {
		t.Fatal("special file reached dispatch")
	}
}

func TestCancellationRetainsUnresolvedCleanup(t *testing.T) {
	for _, change := range []string{"unknown", "cleanup", "liability", "identity", "missing"} {
		t.Run(change, func(t *testing.T) {
			f := setup(t)
			if _, err := f.run(); err != nil {
				t.Fatal(err)
			}
			f.r.call = func(ctx context.Context, verb string, payload any) ([]byte, error) {
				if verb != "cancel" {
					return f.broker.call(ctx, verb, payload)
				}
				r := f.broker.receipt
				switch change {
				case "unknown":
					r["status"] = "unknown"
				case "cleanup":
					r["cleanup_verified"] = false
				case "liability":
					r["unresolved_liability"] = true
				case "identity":
					r["request_id"] = "other"
				case "missing":
					delete(r, "cleanup_verified")
				}
				return encode(r), nil
			}
			if err := f.r.Cancel(context.Background()); !errors.Is(err, ErrUnassessable) {
				t.Fatalf("cancel hid uncertainty: %v", err)
			}
			if f.broker.runs != 1 {
				t.Fatal("cancel dispatched work")
			}
		})
	}
}
