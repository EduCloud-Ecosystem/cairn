// SPDX-License-Identifier: AGPL-3.0-or-later
package runtimeguard

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRecoveryRevalidatesPlanAndLeavesUnownedResources(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TMPDIR", root)
	job, e := NewJobDir()
	if e != nil {
		t.Fatal(e)
	}
	legacy := filepath.Join(root, "cairn-job-unmarked")
	os.Mkdir(legacy, 0700)
	l, e := Acquire(root, true)
	if e != nil {
		t.Fatal(e)
	}
	defer l.Close()
	id := strings.Repeat("a", 64)
	var mutations []string
	run := func(_ context.Context, _ string, a ...string) ([]byte, error) {
		switch strings.Join(a[:min(2, len(a))], " ") {
		case "ps -aq":
			return []byte(id), nil
		case "volume ls":
			return []byte("cairn-work-test\ncairn-work-unowned"), nil
		case "inspect " + id:
			b, _ := json.Marshal([]any{map[string]any{"Id": id, "Name": "/cairn-acquire-test", "Created": "now", "Config": map[string]any{"Labels": map[string]string{ScopeLabel: l.Scope, KindLabel: "acquisition"}}}})
			return b, nil
		case "volume inspect":
			kind := "workspace"
			if a[2] == "cairn-work-unowned" {
				kind = "other"
			}
			b, _ := json.Marshal([]any{map[string]any{"Name": a[2], "CreatedAt": "now", "Labels": map[string]string{ScopeLabel: l.Scope, KindLabel: kind}}})
			return b, nil
		default:
			mutations = append(mutations, strings.Join(a, " "))
			return nil, nil
		}
	}
	p, e := Inventory(context.Background(), l, "docker", run)
	if e != nil {
		t.Fatal(e)
	}
	if len(p.Resources) != 3 {
		t.Fatalf("inventory %+v", p)
	}
	bad := p
	bad.Scope = "changed"
	if e = Apply(context.Background(), l, bad, run); e == nil {
		t.Fatal("stale scope accepted")
	}
	if len(mutations) != 0 {
		t.Fatal("mutated before comparison")
	}
	if e = Apply(context.Background(), l, p, run); e != nil {
		t.Fatal(e)
	}
	if len(mutations) != 2 || !strings.HasPrefix(mutations[0], "rm --force ") || mutations[1] != "volume rm cairn-work-test" {
		t.Fatalf("unsafe order %+v", mutations)
	}
	if _, e = os.Stat(job); !os.IsNotExist(e) {
		t.Fatal("owned job remains")
	}
	if _, e = os.Stat(legacy); e != nil {
		t.Fatal("unmarked job removed")
	}
}
func TestRecoveryStopsOnPartialRuntimeFailure(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TMPDIR", root)
	job, e := NewJobDir()
	if e != nil {
		t.Fatal(e)
	}
	l, e := Acquire(root, true)
	if e != nil {
		t.Fatal(e)
	}
	defer l.Close()
	run := func(_ context.Context, _ string, a ...string) ([]byte, error) {
		if a[0] == "ps" {
			return nil, nil
		}
		if a[1] == "ls" {
			return []byte("cairn-work-busy"), nil
		}
		if a[1] == "inspect" {
			b, _ := json.Marshal([]any{map[string]any{"Name": "cairn-work-busy", "CreatedAt": "now", "Labels": map[string]string{ScopeLabel: l.Scope, KindLabel: "workspace"}}})
			return b, nil
		}
		return nil, errors.New("volume is mounted")
	}
	p, e := Inventory(context.Background(), l, "docker", run)
	if e != nil {
		t.Fatal(e)
	}
	if e = Apply(context.Background(), l, p, run); e == nil {
		t.Fatal("failure hidden")
	}
	if _, e = os.Stat(job); e != nil {
		t.Fatal("continued deleting after failure")
	}
}

// This creates only invocation-owned fixture resources on the local daemon.
func TestRuntimeRecoveryDocker(t *testing.T) {
	image := os.Getenv("CAIRN_RECOVERY_INTEGRATION_IMAGE")
	if image == "" {
		t.Skip("set CAIRN_RECOVERY_INTEGRATION_IMAGE to a preloaded shell image")
	}
	root := t.TempDir()
	t.Setenv("TMPDIR", root)
	job, e := NewJobDir()
	if e != nil {
		t.Fatal(e)
	}
	l, e := Acquire(root, true)
	if e != nil {
		t.Fatal(e)
	}
	defer l.Close()
	ctx := context.Background()
	suffix := filepath.Base(job)
	volume := "cairn-work-" + suffix
	name := "cairn-work-keeper-" + suffix
	run := func(a ...string) []byte {
		t.Helper()
		b, e := Execute(ctx, "docker", a...)
		if e != nil {
			t.Fatalf("%v: %v", a, e)
		}
		return b
	}
	run("volume", "create", "--label", ScopeLabel+"="+l.Scope, "--label", KindLabel+"=workspace", volume)
	defer Execute(ctx, "docker", "volume", "rm", volume)
	run("run", "--detach", "--pull=never", "--name", name, "--label", ScopeLabel+"="+l.Scope, "--label", KindLabel+"=grading", "--network=none", "--mount", "type=volume,source="+volume+",target=/work", image, "sh", "-c", "sleep 300")
	defer Execute(ctx, "docker", "rm", "--force", name)
	p, e := Inventory(ctx, l, "docker", Execute)
	if e != nil {
		t.Fatal(e)
	}
	if len(p.Resources) != 3 {
		t.Fatalf("want own container,volume,job: %+v", p)
	}
	if e = Apply(ctx, l, p, Execute); e != nil {
		t.Fatal(e)
	}
	next, e := Inventory(ctx, l, "docker", Execute)
	if e != nil || len(next.Resources) != 0 {
		t.Fatalf("leftovers %+v %v", next, e)
	}
}
