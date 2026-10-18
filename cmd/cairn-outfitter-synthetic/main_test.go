// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/EduCloud-Ecosystem/cairn/internal/grading"
	"github.com/EduCloud-Ecosystem/cairn/internal/syntheticoutfitter"
	"golang.org/x/sys/unix"
)

// Exercise the actual CLI entry point: the runner cannot protect reads that
// happen before it is called. A context kill is a failure, not a passing exit.
func TestCLIRejectsNamedPipeConfigAndPolicy(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "fixture-cli")
	build := exec.Command("go", "build", "-o", binary, ".")
	if b, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, b)
	}
	for _, kind := range []string{"config", "policy"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			config := filepath.Join(root, "config.json")
			policy := filepath.Join(root, "grading.json")
			pipe := policy
			if kind == "config" {
				pipe = config
			}
			if err := unix.Mkfifo(pipe, 0600); err != nil {
				t.Fatal(err)
			}
			if kind == "policy" {
				c := syntheticoutfitter.Config{SyntheticOnly: true, Binary: filepath.Join(root, "broker"), BinarySHA256: strings.Repeat("a", 64), StateDir: filepath.Join(root, "state"), BindingDir: filepath.Join(root, "bindings"), AuthorityID: strings.Repeat("b", 32), ProjectID: "synthetic", JobID: "pipe", SubmissionRevision: strings.Repeat("c", 40), Policy: grading.PolicyEvidence{Revision: strings.Repeat("d", 40), Path: "grading.json", SHA256: strings.Repeat("e", 64)}}
				b, err := json.Marshal(c)
				if err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(config, b, 0600); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, binary, "--config", config, "--mode", "run", "--input", root, "--policy", root)
			out, err := cmd.Output()
			if ctx.Err() != nil {
				t.Fatalf("CLI blocked on %s FIFO", kind)
			}
			exit, ok := err.(*exec.ExitError)
			if !ok || exit.ExitCode() != 2 {
				t.Fatalf("expected bounded rejection: %v %s", err, out)
			}
			var result map[string]any
			if json.Unmarshal(out, &result) != nil || result["unassessable"] != true || result["result"] != nil {
				t.Fatalf("invalid refusal: %s", out)
			}
		})
	}
}
