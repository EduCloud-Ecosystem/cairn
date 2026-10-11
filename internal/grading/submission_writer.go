// SPDX-License-Identifier: AGPL-3.0-or-later
package grading

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/EduCloud-Ecosystem/cairn/internal/handoff"
	"github.com/EduCloud-Ecosystem/cairn/internal/id"
	"github.com/EduCloud-Ecosystem/cairn/internal/runtimeguard"
	"github.com/EduCloud-Ecosystem/cairn/pkg/adapter"
)

const SubmissionMaxFiles = 32
const SubmissionMaxFileBytes = 1 << 20
const SubmissionMaxBytes = 4 << 20

// SubmissionWriter runs only Git object plumbing in a bounded, operator-owned
// container. It never checks out repository content or executes its hooks/filters.
// Hosts is trusted operator configuration; request repository URLs are not accepted.
type SubmissionWriter struct {
	Hosts     map[adapter.Host]CloneCreds
	Container CheckoutContainer
	Timeout   time.Duration
}

func NewSubmissionWriter(hosts map[adapter.Host]CloneCreds, container CheckoutContainer) *SubmissionWriter {
	return &SubmissionWriter{Hosts: hosts, Container: container}
}

var _ handoff.Writer = (*SubmissionWriter)(nil)
var submissionSegment = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)
var submissionSHA = regexp.MustCompile(`^[a-f0-9]{40}$`)
var submissionReceipt = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

// ValidateSubmissionFiles is intentionally narrower than Git's full pathname
// grammar. Automation/configuration files must use the ordinary reviewed Git flow.
func ValidateSubmissionFiles(files []handoff.File) error {
	if len(files) < 1 || len(files) > SubmissionMaxFiles {
		return errors.New("select 1–32 coursework files")
	}
	total := 0
	seen := map[string]bool{}
	reserved := map[string]bool{"dockerfile": true, "containerfile": true, "makefile": true, "gnumakefile": true, "jenkinsfile": true, "justfile": true, "taskfile.yml": true, "taskfile.yaml": true, "package.json": true, "package-lock.json": true, "pyproject.toml": true, "setup.py": true, "setup.cfg": true, "requirements.txt": true, "tox.ini": true, "noxfile.py": true, "conftest.py": true, "cairn.yaml": true, "cairn.yml": true, "grading.yaml": true, "grading.yml": true, "grading.json": true, "autograding.json": true, "renv.lock": true, "environment.yml": true, "environment.yaml": true}
	for _, f := range files {
		if len(f.Path) > 240 || path.Clean(f.Path) != f.Path || seen[f.Path] || len(f.Content) > SubmissionMaxFileBytes {
			return errors.New("invalid, duplicate, or oversized coursework file")
		}
		for _, part := range strings.Split(f.Path, "/") {
			if !submissionSegment.MatchString(part) || strings.HasPrefix(part, ".") || strings.Contains(part, "..") || reserved[strings.ToLower(part)] {
				return errors.New("reserved or unsafe coursework path")
			}
		}
		total += len(f.Content)
		seen[f.Path] = true
	}
	if total > SubmissionMaxBytes {
		return errors.New("coursework exceeds 4 MiB")
	}
	for p := range seen {
		for parent := path.Dir(p); parent != "."; parent = path.Dir(parent) {
			if seen[parent] {
				return errors.New("selected coursework paths conflict")
			}
		}
	}
	return nil
}

func (w *SubmissionWriter) endpoint(repo adapter.RepoRef) (string, string, error) {
	cc, ok := w.Hosts[repo.Host]
	if len(repo.Namespace) > 512 {
		return "", "", errors.New("repository namespace too long")
	}
	if !ok || cc.Hostname == "" {
		return "", "", errors.New("submission Git host is not configured")
	}
	if !submissionSegment.MatchString(repo.Name) || strings.Contains(repo.Name, "..") {
		return "", "", errors.New("invalid repository name")
	}
	for _, s := range strings.Split(repo.Namespace, "/") {
		if !submissionSegment.MatchString(s) || strings.Contains(s, "..") {
			return "", "", errors.New("invalid repository namespace")
		}
	}
	scheme := cc.Scheme
	if scheme == "" {
		scheme = "https"
	}
	if scheme != "http" && scheme != "https" {
		return "", "", errors.New("submission endpoint requires HTTP or HTTPS")
	}
	u, err := url.Parse(scheme + "://" + cc.Hostname)
	if err != nil || u.Hostname() == "" || u.Host != cc.Hostname || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || strings.ContainsAny(cc.Hostname, "\r\n?#\\") {
		return "", "", errors.New("invalid configured Git origin")
	}
	if len(cc.Token) > 16<<10 || strings.ContainsAny(cc.Token, "\x00\r\n") || strings.ContainsAny(cc.Username, "\x00\r\n") {
		return "", "", errors.New("invalid configured Git credentials")
	}
	if cc.Token != "" {
		username := cc.Username
		if username == "" {
			username = "x-access-token"
		}
		u.User = url.User(username)
	}
	u.Path = "/" + repo.Namespace + "/" + repo.Name + ".git"
	return u.String(), cc.Token, nil
}

func validateWrite(req handoff.WriteRequest) error {
	if err := ValidateSubmissionFiles(req.Files); err != nil {
		return err
	}
	if !submissionSHA.MatchString(req.BaseRevision) || !submissionReceipt.MatchString(req.ReceiptID) || req.CreatedAt.IsZero() || req.CreatedAt.Unix() < 0 || req.CreatedAt.Nanosecond() != 0 {
		return errors.New("submission requires frozen base, receipt and whole-second timestamp")
	}
	if !strings.HasPrefix(req.Branch, "refs/heads/") || len(req.Branch) > 255 || strings.ContainsAny(req.Branch, " \t\r\n\x00~^:?*[\\") || strings.Contains(req.Branch, "..") || strings.Contains(req.Branch, "@{") || strings.HasSuffix(req.Branch, "/") || strings.HasSuffix(req.Branch, ".") || strings.HasSuffix(req.Branch, ".lock") {
		return errors.New("invalid frozen submission branch")
	}
	return nil
}

func (w *SubmissionWriter) Inspect(ctx context.Context, repo adapter.RepoRef, files []handoff.File) (handoff.Snapshot, error) {
	if err := ValidateSubmissionFiles(files); err != nil {
		return handoff.Snapshot{}, err
	}
	raw, err := w.execute(ctx, "inspect", handoff.WriteRequest{Repo: repo, Files: files}, "")
	if err != nil {
		return handoff.Snapshot{}, err
	}
	lines := strings.Split(strings.TrimSpace(raw), "\n")
	if len(lines) != len(files)+2 || !strings.HasPrefix(lines[0], "refs/heads/") || !submissionSHA.MatchString(lines[1]) {
		return handoff.Snapshot{}, errors.New("invalid submission inspection response")
	}
	sorted := append([]handoff.File(nil), files...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Path < sorted[j].Path })
	result := handoff.Snapshot{Branch: lines[0], BaseRevision: lines[1], Changes: []handoff.Change{}}
	for i, f := range sorted {
		if lines[i+2] != "create" && lines[i+2] != "replace" {
			return handoff.Snapshot{}, errors.New("invalid coursework action")
		}
		sum := sha256.Sum256(f.Content)
		result.Changes = append(result.Changes, handoff.Change{Path: f.Path, SHA256: hex.EncodeToString(sum[:]), Bytes: len(f.Content), Action: lines[i+2]})
	}
	// Apply the same branch grammar to remotely supplied refs before persisting.
	if err := validateWrite(handoff.WriteRequest{Branch: result.Branch, BaseRevision: result.BaseRevision, Files: files, ReceiptID: "inspection", CreatedAt: time.Unix(1, 0)}); err != nil {
		return handoff.Snapshot{}, err
	}
	return result, nil
}

func (w *SubmissionWriter) Prepare(ctx context.Context, req handoff.WriteRequest) (string, error) {
	if err := validateWrite(req); err != nil {
		return "", err
	}
	raw, err := w.execute(ctx, "prepare", req, "")
	if err != nil {
		return "", err
	}
	commit := strings.TrimSpace(raw)
	if !submissionSHA.MatchString(commit) {
		return "", errors.New("invalid prepared submission commit")
	}
	return commit, nil
}

func (w *SubmissionWriter) Publish(ctx context.Context, req handoff.WriteRequest, candidate string) (handoff.Outcome, error) {
	if err := validateWrite(req); err != nil {
		return handoff.VerificationNeeded, err
	}
	if !submissionSHA.MatchString(candidate) {
		return handoff.VerificationNeeded, errors.New("invalid intended submission commit")
	}
	raw, err := w.execute(ctx, "publish", req, candidate)
	if err != nil {
		return handoff.VerificationNeeded, err
	}
	switch handoff.Outcome(strings.TrimSpace(raw)) {
	case handoff.Committed:
		return handoff.Committed, nil
	case handoff.Conflict:
		return handoff.Conflict, nil
	case handoff.VerificationNeeded:
		return handoff.VerificationNeeded, nil
	}
	return handoff.VerificationNeeded, errors.New("invalid submission publication response")
}

func submissionPayload(req handoff.WriteRequest) ([]byte, error) {
	var b bytes.Buffer
	tw := tar.NewWriter(&b)
	add := func(name string, data []byte) error {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0600, Size: int64(len(data)), Typeflag: tar.TypeReg}); err != nil {
			return err
		}
		_, err := tw.Write(data)
		return err
	}
	sorted := append([]handoff.File(nil), req.Files...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Path < sorted[j].Path })
	var paths []string
	for _, f := range sorted {
		paths = append(paths, f.Path)
		if err := add("files/"+f.Path, f.Content); err != nil {
			return nil, err
		}
	}
	for name, value := range map[string]string{"paths": strings.Join(paths, "\n") + "\n", "branch": req.Branch, "base": req.BaseRevision, "receipt": req.ReceiptID, "timestamp": fmt.Sprint(req.CreatedAt.Unix()), "retry": fmt.Sprint(req.Retry)} {
		if err := add(name, []byte(value)); err != nil {
			return nil, err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func (w *SubmissionWriter) execute(ctx context.Context, operation string, req handoff.WriteRequest, candidate string) (result string, retErr error) {
	endpoint, token, err := w.endpoint(req.Repo)
	if err != nil {
		return "", err
	}
	if w.Container.Image == "" || strings.HasPrefix(w.Container.Image, "-") {
		return "", errors.New("submission requires a preloaded bounded Git image")
	}
	rt := w.Container.Runtime
	if rt == "" {
		rt = "docker"
	}
	timeout := w.Timeout
	if timeout <= 0 || timeout > 2*time.Minute {
		timeout = 2 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	payload, err := submissionPayload(req)
	if err != nil {
		return "", err
	}
	lease, err := runtimeguard.Acquire("", false)
	if err != nil {
		return "", err
	}
	defer lease.Close()
	name := "cairn-acquire-" + id.New()
	defer func() {
		clean, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		raw, e := gitOutput(exec.CommandContext(clean, rt, "rm", "--force", name))
		if e != nil && !strings.Contains(string(raw), "No such container") {
			retErr = errors.Join(retErr, errors.New("submission container cleanup failed; operator recovery required"))
		}
	}()
	args := []string{"run", "--rm", "--interactive", "--name", name, "--label", runtimeguard.ScopeLabel + "=" + lease.Scope, "--label", runtimeguard.KindLabel + "=acquisition", "--label", "io.educloud.cairn.operation=submission-writer", "--pull=never", "--read-only", "--cap-drop=ALL", "--security-opt=no-new-privileges", "--user", "65534:65534", "--memory=768m", "--memory-swap=768m", "--cpus=1", "--pids-limit=64", "--log-driver=none", "--tmpfs", "/scratch:rw,nosuid,nodev,noexec,size=512m,nr_inodes=65536,mode=1777", "--entrypoint", "/bin/sh", w.Container.Image, "-c", submissionObjectScript, "submission", operation, endpoint, candidate}
	cmd := exec.CommandContext(ctx, rt, args...)
	cmd.WaitDelay = time.Second
	cmd.Stdin = bytes.NewReader(append([]byte(token+"\n"), payload...))
	var stdout, stderr boundedOutput
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("bounded submission deadline: %w", ctx.Err())
		}
		return "", errors.New("bounded submission object operation failed; no remote success is assumed")
	}
	if len(stdout.String()) > 8192 {
		return "", errors.New("submission response exceeds budget")
	}
	return stdout.String(), nil
}
