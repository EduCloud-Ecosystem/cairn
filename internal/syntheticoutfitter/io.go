// SPDX-License-Identifier: AGPL-3.0-or-later

package syntheticoutfitter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"time"

	"golang.org/x/sys/unix"
)

func decode(b []byte, v any, strict bool) error {
	d := json.NewDecoder(bytes.NewReader(b))
	if strict {
		d.DisallowUnknownFields()
	}
	if err := d.Decode(v); err != nil {
		return err
	}
	if d.Decode(new(any)) != io.EOF {
		return errors.New("trailing JSON")
	}
	return nil
}
func readBounded(root *os.Root, path string, limit int64) ([]byte, error) {
	// A fixture may contain a FIFO or be replaced while being inspected. Open
	// nonblocking and without following links, then check the opened descriptor.
	f, err := root.OpenFile(path, os.O_RDONLY|unix.O_NONBLOCK|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() || st.Size() > limit {
		return nil, errors.New("invalid or oversized regular file")
	}
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || int64(len(b)) > limit {
		return nil, errors.New("file read exceeds limit")
	}
	return b, nil
}
func manifest(dir string) ([]fileEntry, error) {
	if !filepath.IsAbs(dir) || filepath.Clean(dir) != dir {
		return nil, errors.New("absolute clean fixture directory required")
	}
	st, err := os.Lstat(dir)
	if err != nil || !st.IsDir() {
		return nil, errors.New("fixture directory unavailable or symlinked")
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	files := []fileEntry{}
	var total int64
	entries := 0
	err = fs.WalkDir(root.FS(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == "." {
			return nil
		}
		entries++
		if entries > 512 {
			return errors.New("too many fixture entries")
		}
		if !relative(p) || d.Name() == ".git" || d.Type()&os.ModeSymlink != 0 {
			return errors.New("unsafe fixture path; materialize a Git-free snapshot")
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil || !info.Mode().IsRegular() {
			return errors.New("fixture entry must be a regular file")
		}
		if len(files) >= 128 {
			return errors.New("too many fixture files")
		}
		b, err := readBounded(root, p, 16<<20-total)
		if err != nil {
			return err
		}
		total += int64(len(b))
		files = append(files, fileEntry{p, int64(len(b)), digest(b)})
		return nil
	})
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, err
}
func privateDir(path string) error {
	if err := os.Mkdir(path, 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	st, err := os.Lstat(path)
	if err != nil || !st.IsDir() || st.Mode().Perm()&0077 != 0 {
		return errors.New("binding directory must be private and not symlinked")
	}
	return nil
}
func (r *Runner) lock(ctx context.Context) (func(), error) {
	if err := privateDir(r.config.BindingDir); err != nil {
		return nil, err
	}
	fd, err := unix.Open(filepath.Join(r.config.BindingDir, ".lock"), unix.O_CREAT|unix.O_RDWR|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return nil, err
	}
	for {
		err = unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			return func() { _ = unix.Flock(fd, unix.LOCK_UN); _ = unix.Close(fd) }, nil
		}
		if err != unix.EWOULDBLOCK && err != unix.EAGAIN {
			_ = unix.Close(fd)
			return nil, err
		}
		select {
		case <-ctx.Done():
			_ = unix.Close(fd)
			return nil, ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
}
func (r *Runner) intentPath() string {
	return filepath.Join(r.config.BindingDir, requestID(r.config.ProjectID, r.config.JobID)+".json")
}
func (r *Runner) save(i intent) error {
	b := encode(sealedIntent{i, digest(encode(i))})
	f, err := os.OpenFile(r.intentPath(), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	// Partial or unsynchronized intents are deliberately never removed/retried.
	_, err = f.Write(b)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	d, err := os.Open(r.config.BindingDir)
	if err != nil {
		return err
	}
	defer d.Close()
	if err = d.Sync(); err != nil {
		return err
	}
	parent, err := os.Open(filepath.Dir(r.config.BindingDir))
	if err != nil {
		return err
	}
	defer parent.Close()
	return parent.Sync()
}
func (r *Runner) load() (intent, bool, error) {
	var s sealedIntent
	st, err := os.Lstat(r.intentPath())
	if errors.Is(err, os.ErrNotExist) {
		return s.Intent, false, nil
	}
	if err != nil || !st.Mode().IsRegular() || st.Mode().Perm()&0077 != 0 {
		return s.Intent, true, errors.New("invalid private intent")
	}
	root, err := os.OpenRoot(r.config.BindingDir)
	if err != nil {
		return s.Intent, true, err
	}
	defer root.Close()
	b, err := readBounded(root, filepath.Base(r.intentPath()), 128<<10)
	if err != nil || decode(b, &s, true) != nil || s.SHA256 != digest(encode(s.Intent)) {
		return s.Intent, true, errors.New("corrupt intent; reconciliation required")
	}
	i, c := s.Intent, r.config
	q := i.Request
	if i.Version != 1 || i.AuthorityID != c.AuthorityID || i.BinarySHA256 != c.BinarySHA256 || !reflect.DeepEqual(i.Policy, c.Policy) || q.SubmissionCommit != c.SubmissionRevision || q.PolicyCommit != c.Policy.Revision || q.PolicyManifestSHA256 != c.Policy.SHA256 || q.Consumer != consumer || q.ProjectID != c.ProjectID || q.JobID != c.JobID || q.Attempt != 1 || validateSpec(i.Spec) != nil || i.Preflight.RequestID != requestID(c.ProjectID, c.JobID) || !isHex(i.Preflight.BindingSHA256, 32) {
		return i, true, errors.New("persisted binding differs from pinned configuration")
	}
	return i, true, nil
}

// limitedOutput interrupts a misbehaving subprocess without unbounded buffering.
type limitedOutput struct {
	bytes.Buffer
	remaining int
	cancel    context.CancelFunc
	exceeded  bool
}

func (w *limitedOutput) Write(b []byte) (int, error) {
	n := len(b)
	if n > w.remaining {
		w.exceeded = true
		w.cancel()
		return 0, errors.New("broker output limit")
	}
	w.remaining -= n
	return w.Buffer.Write(b)
}
func (r *Runner) command(ctx context.Context, verb string, payload any) ([]byte, error) {
	c := r.config
	st, err := os.Lstat(c.Binary)
	if err != nil || !st.Mode().IsRegular() {
		return nil, errors.New("pinned broker binary unavailable")
	}
	root, err := os.OpenRoot(filepath.Dir(c.Binary))
	if err != nil {
		return nil, err
	}
	b, err := readBounded(root, filepath.Base(c.Binary), 256<<20)
	_ = root.Close()
	if err != nil || digest(b) != c.BinarySHA256 {
		return nil, errors.New("broker binary pin mismatch")
	}
	args := []string{"--state-dir", c.StateDir, verb}
	switch verb {
	case "consumer-preflight", "consumer-run":
		f, err := os.CreateTemp(c.BindingDir, ".request-")
		if err != nil {
			return nil, err
		}
		defer os.Remove(f.Name())
		b = encode(payload)
		if len(b) > 32768 {
			_ = f.Close()
			return nil, errors.New("request too large")
		}
		_, err = f.Write(b)
		closeErr := f.Close()
		if err != nil {
			return nil, err
		}
		if closeErr != nil {
			return nil, closeErr
		}
		args = append(args, "--request", f.Name())
	case "consumer-status":
		args = append(args, "--consumer", consumer, "--project-id", c.ProjectID, "--job-id", c.JobID)
	case "cancel":
		id, ok := payload.(string)
		if !ok {
			return nil, errors.New("cancel identity missing")
		}
		args = append(args, "--request-id", id)
	case "recovery-status":
	default:
		return nil, errors.New("unsupported broker operation")
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, c.Binary, args...)
	cmd.WaitDelay = 2 * time.Second
	out := &limitedOutput{remaining: 1 << 20, cancel: cancel}
	stderr := &limitedOutput{remaining: 4096, cancel: cancel}
	cmd.Stdout = out
	cmd.Stderr = stderr
	if err = cmd.Run(); err != nil || out.exceeded || stderr.exceeded {
		return nil, errors.New("broker command failed; status-only recovery required")
	}
	return out.Bytes(), nil
}
