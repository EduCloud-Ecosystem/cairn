// SPDX-License-Identifier: AGPL-3.0-or-later
// Package runtimeguard coordinates local grading with offline crash recovery.
package runtimeguard

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"golang.org/x/sys/unix"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const ScopeLabel = "io.educloud.cairn.scope"
const KindLabel = "io.educloud.cairn.kind"
const Marker = ".cairn-owner.json"

type Lease struct {
	file      *os.File
	Root      string
	Scope     string
	exclusive bool
}

func Acquire(root string, exclusive bool) (*Lease, error) {
	if root == "" {
		root = os.TempDir()
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	fd, err := unix.Open(filepath.Join(root, ".cairn-runtime.lock"), unix.O_CREAT|unix.O_RDWR|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return nil, fmt.Errorf("open runtime recovery lock: %w", err)
	}
	f := os.NewFile(uintptr(fd), "cairn-runtime-lock")
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		f.Close()
		return nil, errors.New("runtime lock must be a private regular file")
	}
	// A persistent random nonce distinguishes identical paths on different hosts
	// using a remote/shared daemon. Never unlink this file during recovery.
	if info.Size() == 0 {
		if err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
			f.Close()
			return nil, errors.New("runtime lock initialization busy; retry")
		}
		current, err := f.Stat()
		if err != nil {
			f.Close()
			return nil, err
		}
		if current.Size() == 0 {
			seed := make([]byte, 32)
			if _, err = rand.Read(seed); err == nil {
				_, err = f.WriteAt(seed, 0)
			}
			if err == nil {
				err = f.Sync()
			}
			if err != nil {
				f.Close()
				return nil, err
			}
		}
	}
	seed := make([]byte, 32)
	n, err := f.ReadAt(seed, 0)
	if err != nil && err != io.EOF || n != len(seed) {
		f.Close()
		return nil, errors.New("invalid runtime lock identity; preserve file and investigate")
	}
	sum := sha256.Sum256(append([]byte(root+"\x00"), seed...))
	scope := hex.EncodeToString(sum[:])
	op := unix.LOCK_SH
	if exclusive {
		op = unix.LOCK_EX
	}
	if err = unix.Flock(fd, op|unix.LOCK_NB); err != nil {
		f.Close()
		return nil, errors.New("runtime is active or recovery is in progress; stop workers and retry")
	}
	return &Lease{file: f, Root: root, Scope: scope, exclusive: exclusive}, nil
}
func (l *Lease) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	f := l.file
	l.file = nil
	return f.Close()
}

type Ownership struct {
	Scope   string `json:"scope"`
	Name    string `json:"name"`
	Version int    `json:"version"`
}

// NewJobDir must be used while holding a shared Lease until all work is complete.
func NewJobDir() (string, error) {
	l, err := Acquire("", false)
	if err != nil {
		return "", err
	}
	defer l.Close()
	dir, err := os.MkdirTemp(l.Root, "cairn-job-")
	if err != nil {
		return "", err
	}
	b, _ := json.Marshal(Ownership{Scope: l.Scope, Name: filepath.Base(dir), Version: 1})
	if err = os.WriteFile(filepath.Join(dir, Marker), b, 0600); err != nil {
		os.RemoveAll(dir)
		return "", err
	}
	return dir, nil
}

// OwnedJob refuses symlinks and legacy unmarked directories. Student files live
// in children and are never consulted to decide ownership of the parent.
func OwnedJob(root, name, scope string) bool {
	if !strings.HasPrefix(name, "cairn-job-") || filepath.Base(name) != name {
		return false
	}
	path := filepath.Join(root, name)
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0077 != 0 {
		return false
	}
	marker := filepath.Join(path, Marker)
	mi, err := os.Lstat(marker)
	if err != nil || !mi.Mode().IsRegular() || mi.Size() > 1024 || mi.Mode().Perm()&0077 != 0 {
		return false
	}
	b, err := os.ReadFile(marker)
	if err != nil {
		return false
	}
	var o Ownership
	return json.Unmarshal(b, &o) == nil && o.Scope == scope && o.Name == name && o.Version == 1
}
