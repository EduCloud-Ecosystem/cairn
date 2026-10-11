// SPDX-License-Identifier: AGPL-3.0-or-later
package grading

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/EduCloud-Ecosystem/cairn/pkg/gradingspec"
)

func TestWorkspaceIsBoundedSharedAndSourceIsReadOnly(t *testing.T) {
	fr := &fakeRunner{}
	r := &ContainerRunner{DefaultImage: "fixture", User: "65534:65534", Isolation: IsolationGVisor, exec: fr}
	_, err := r.Run(context.Background(), gradingspec.Spec{Setup: []string{"setup-marker"}, Tests: []gradingspec.Test{{Run: "check-marker", Points: 1}}}, "/private/source")
	if err != nil {
		t.Fatal(err)
	}
	if len(fr.allCalls) != 7 { // create, keeper, initialize, setup, test, remove keeper, remove volume
		t.Fatalf("unexpected lifecycle: %+v", fr.allCalls)
	}
	create := fr.allCalls[0].args
	volume := create[len(create)-1]
	if !hasArg(create, "type=tmpfs") || !hasArg(create, "device=tmpfs") || !hasArg(create, "o=size=512m,nr_inodes=65536,mode=1777") {
		t.Fatalf("workspace lacks hard bounds: %v", create)
	}
	for _, call := range fr.allCalls[1:5] {
		if flagValue(call.args, "-v") != volume+":/work:nocopy" || flagValue(call.args, "--runtime") != "runsc" || !hasArg(call.args, "--read-only") || flagValue(call.args, "--cap-drop") != "ALL" || flagValue(call.args, "--security-opt") != "no-new-privileges" {
			t.Fatalf("workspace/tier/hardening changed between containers: %+v", call)
		}
	}
	if !hasArg(fr.allCalls[1].args, "--detach") || flagValue(fr.allCalls[1].args, "--network") != "none" {
		t.Fatal("workspace keeper is not isolated")
	}
	if flagValue(fr.allCalls[2].args, "--mount") != "type=bind,source=/private/source,target=/cairn-source,readonly" || flagValue(fr.allCalls[2].args, "--network") != "none" {
		t.Fatal("initialization source was not read-only/network isolated")
	}
	for _, call := range fr.allCalls[3:5] {
		if strings.Contains(strings.Join(call.args, " "), "/private/source") {
			t.Fatal("host source leaked into student execution container")
		}
	}
	if a := fr.allCalls[5].args; a[0] != "rm" || a[1] != "--force" || a[2] != flagValue(fr.allCalls[1].args, "--name") {
		t.Fatal("keeper was not removed first")
	}
	if a := fr.allCalls[6].args; strings.Join(a, " ") != "volume rm --force "+volume {
		t.Fatal("workspace was not removed")
	}
}

type failingWorkspaceRunner struct {
	fakeRunner
	failAt string
	cancel context.CancelFunc
}

func (f *failingWorkspaceRunner) run(ctx context.Context, name string, args []string, timeout time.Duration) (cmdResult, error) {
	result, err := f.fakeRunner.run(ctx, name, args, timeout)
	last := args[len(args)-1]
	if (f.failAt == "create" && args[0] == "volume" && args[1] == "create") || (f.failAt == "cleanup" && args[0] == "volume" && args[1] == "rm") || last == f.failAt {
		if f.cancel != nil {
			f.cancel()
		}
		return cmdResult{exitCode: 1}, nil
	}
	return result, err
}

func TestWorkspaceCleanupFailureIsReported(t *testing.T) {
	fr := &failingWorkspaceRunner{failAt: "cleanup"}
	r := &ContainerRunner{DefaultImage: "fixture", exec: fr}
	if _, err := r.Run(context.Background(), oneTest(), "/source"); err == nil {
		t.Fatal("workspace cleanup failure was hidden")
	}
}

func TestWorkspaceFailsClosedAndCleansUpAfterCancellation(t *testing.T) {
	for _, point := range []string{"create", workspaceHoldCommand, workspaceCopyCommand, "student-step"} {
		t.Run(point, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			fr := &failingWorkspaceRunner{failAt: point, cancel: cancel}
			r := &ContainerRunner{DefaultImage: "fixture", exec: fr}
			_, err := r.Run(ctx, gradingspec.Spec{Tests: []gradingspec.Test{{Run: "student-step", Points: 1}}}, "/private/source")
			if point != "student-step" && err == nil {
				t.Fatal("workspace setup failure did not fail closed")
			}
			for _, call := range fr.allCalls {
				if point != "student-step" && call.args[len(call.args)-1] == "student-step" {
					t.Fatal("student code ran after workspace setup failure")
				}
				if call.args[0] == "rm" || call.args[0] == "volume" && call.args[1] == "rm" {
					if call.canceled {
						t.Fatal("cleanup inherited canceled grading context")
					}
				}
			}
			last := fr.allCalls[len(fr.allCalls)-1].args
			if last[0] != "volume" || last[1] != "rm" {
				t.Fatal("volume cleanup was not attempted")
			}
		})
	}
}

func TestReadOnlyWorkspaceDoesNotCreateVolume(t *testing.T) {
	fr := &fakeRunner{}
	r := &ContainerRunner{DefaultImage: "fixture", ReadOnlyWork: true, exec: fr}
	if _, err := r.Run(context.Background(), oneTest(), "/source"); err != nil {
		t.Fatal(err)
	}
	if len(fr.allCalls) != 1 || flagValue(fr.allCalls[0].args, "-v") != "/source:/work:ro" {
		t.Fatal("read-only evidence path changed")
	}
}

type recordingRuntime struct {
	calls   []fakeCall
	results []cmdResult
}

func (r *recordingRuntime) run(ctx context.Context, name string, args []string, timeout time.Duration) (cmdResult, error) {
	r.calls = append(r.calls, fakeCall{name: name, args: append([]string(nil), args...), timeout: timeout})
	result, err := (execCommandRunner{}).run(ctx, name, args, timeout)
	r.results = append(r.results, result)
	return result, err
}

// Explicit opt-in uses only an already-local image. This verifies actual tmpfs
// byte/inode limits and cross-container persistence, not merely CLI arguments.
func TestBoundedWorkspaceDockerIntegration(t *testing.T) {
	image := os.Getenv("CAIRN_WORKSPACE_INTEGRATION_IMAGE")
	if image == "" {
		t.Skip("set CAIRN_WORKSPACE_INTEGRATION_IMAGE to an existing local Alpine-compatible image")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "source.txt"), []byte("original\n"), 0644); err != nil {
		t.Fatal(err)
	}
	recorder := &recordingRuntime{}
	r := &ContainerRunner{DefaultImage: image, User: "65534:65534", DefaultMemoryMB: 1024, exec: recorder, ExtraArgs: []string{"--pull=never"}}
	spec := gradingspec.Spec{
		Setup: []string{"test ! -e /cairn-source && test \"$(cat source.txt)\" = original && echo changed > source.txt && echo persisted > setup-marker"},
		Tests: []gradingspec.Test{
			{Name: "persistence", Run: "test \"$(cat setup-marker)\" = persisted && test \"$(cat source.txt)\" = changed", Points: 1},
			{Name: "inode-bound", Run: "test \"$(stat -f -c %T /work)\" = tmpfs && test \"$(stat -f -c %c /work)\" = 65536", Points: 1},
			{Name: "byte-bound", Run: "if dd if=/dev/zero of=limit.bin bs=1048576 count=520 >/tmp/fill-log 2>&1; then exit 1; fi; grep -q 'No space left on device' /tmp/fill-log && rm limit.bin", Points: 1},
		},
	}
	result, err := r.Run(context.Background(), spec, dir)
	if err != nil {
		for i, call := range recorder.calls {
			t.Logf("%v: %+v", call.args, recorder.results[i])
		}
		t.Fatal(err)
	}
	if result.Score != 3 {
		t.Fatalf("live workspace checks failed: %+v", result)
	}
	source, err := os.ReadFile(filepath.Join(dir, "source.txt"))
	if err != nil || string(source) != "original\n" {
		t.Fatal("grading modified host source", err)
	}
	for _, call := range recorder.calls {
		args := call.args
		if args[0] == "volume" && args[1] == "create" {
			if exec.Command("docker", "volume", "inspect", args[len(args)-1]).Run() == nil {
				t.Fatal("workspace volume leaked")
			}
		}
		if args[0] == "run" && hasArg(args, "--detach") {
			if exec.Command("docker", "inspect", flagValue(args, "--name")).Run() == nil {
				t.Fatal("workspace keeper leaked")
			}
		}
	}
}
