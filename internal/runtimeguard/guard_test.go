// SPDX-License-Identifier: AGPL-3.0-or-later
package runtimeguard

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExclusiveRecoveryExcludesActiveWorkers(t *testing.T) {
	root := t.TempDir()
	a, e := Acquire(root, false)
	if e != nil {
		t.Fatal(e)
	}
	b, e := Acquire(root, false)
	if e != nil {
		t.Fatal(e)
	}
	if x, e := Acquire(root, true); e == nil {
		x.Close()
		t.Fatal("recovery overlapped workers")
	}
	a.Close()
	b.Close()
	x, e := Acquire(root, true)
	if e != nil {
		t.Fatal(e)
	}
	defer x.Close()
	if y, e := Acquire(root, false); e == nil {
		y.Close()
		t.Fatal("worker overlapped recovery")
	}
}
func TestOwnedJobRejectsSymlinksAndUnmarked(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TMPDIR", root)
	l, e := Acquire("", false)
	if e != nil {
		t.Fatal(e)
	}
	defer l.Close()
	p, e := NewJobDir()
	if e != nil {
		t.Fatal(e)
	}
	if !OwnedJob(root, filepath.Base(p), l.Scope) {
		t.Fatal("owned job missing")
	}
	if OwnedJob(root, filepath.Base(p), "other") {
		t.Fatal("wrong scope accepted")
	}
	alias := filepath.Join(root, "cairn-job-alias")
	if e = os.Symlink(p, alias); e != nil {
		t.Fatal(e)
	}
	if OwnedJob(root, filepath.Base(alias), l.Scope) {
		t.Fatal("symlink accepted")
	}
	os.Remove(filepath.Join(p, Marker))
	if OwnedJob(root, filepath.Base(p), l.Scope) {
		t.Fatal("unmarked accepted")
	}
}
func TestLockRefusesSymlink(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "target")
	os.WriteFile(p, []byte("safe"), 0600)
	os.Symlink(p, filepath.Join(root, ".cairn-runtime.lock"))
	if l, e := Acquire(root, true); e == nil {
		l.Close()
		t.Fatal("followed symlink")
	}
}
