// SPDX-License-Identifier: AGPL-3.0-or-later
package grading

import (
	"archive/tar"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/EduCloud-Ecosystem/cairn/internal/id"
	"github.com/EduCloud-Ecosystem/cairn/internal/runtimeguard"
)

const checkoutBytes int64 = 512 << 20
const checkoutEntries = 65536

// CheckoutContainer requires a preloaded operator-owned image with sh, git and
// tar. It never mounts the host checkout or inherits host Git configuration.
type CheckoutContainer struct{ Runtime, Image string }

// stdout is exclusively a tar stream. Credentials are read from stdin rather
// than recorded in argv, Docker environment metadata, or a host credential file.
const checkoutScript = `set -eu
IFS= read -r CAIRN_GIT_TOKEN
export CAIRN_GIT_TOKEN GIT_TERMINAL_PROMPT=0 GIT_CONFIG_NOSYSTEM=1 HOME=/scratch
export GIT_CONFIG_GLOBAL=/dev/null
printf '#!/bin/sh\nexec printf "%%s\\n" "$CAIRN_GIT_TOKEN"\n' > /scratch/askpass
chmod 700 /scratch/askpass
export GIT_ASKPASS='sh /scratch/askpass'
git -c core.hooksPath=/dev/null clone --quiet --depth 1 -- "$1" /scratch/repo >&2
if [ -n "$2" ]; then
 git -C /scratch/repo -c core.hooksPath=/dev/null fetch --quiet --depth 1 origin "$2" >&2
 git -C /scratch/repo -c core.hooksPath=/dev/null checkout --quiet --detach FETCH_HEAD >&2
 test "$(git -C /scratch/repo rev-parse HEAD)" = "$2"
fi
unset CAIRN_GIT_TOKEN
rm -rf /scratch/repo/.git
exec tar -C /scratch/repo -cf - .
`

func (g *GitCheckout) fetchContainer(ctx context.Context, cloneURL, token, dir, revision string) (retErr error) {
	if strings.ContainsAny(token, "\r\n") {
		return errors.New("checkout token contains invalid newline")
	}
	c := g.Container
	rt := c.Runtime
	if rt == "" {
		rt = "docker"
	}
	if c.Image == "" || strings.HasPrefix(c.Image, "-") {
		return errors.New("bounded checkout requires an operator-selected image")
	}
	lease, err := runtimeguard.Acquire("", false)
	if err != nil {
		return err
	}
	defer lease.Close()
	name := "cairn-acquire-" + id.New()
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		out, err := gitOutput(exec.CommandContext(cleanup, rt, "rm", "--force", name))
		if err != nil && !strings.Contains(string(out), "No such container") {
			retErr = errors.Join(retErr, errors.New("bounded checkout cleanup failed; operator recovery required"))
		}
	}()
	args := []string{"run", "--rm", "--interactive", "--name", name,
		"--label", "io.educloud.cairn.scope=" + lease.Scope, "--label", "io.educloud.cairn.kind=acquisition",
		"--pull=never", "--read-only", "--cap-drop=ALL", "--security-opt=no-new-privileges",
		"--user", "65534:65534", "--memory=768m", "--memory-swap=768m", "--cpus=1", "--pids-limit=64",
		"--log-driver=none", "--tmpfs", "/scratch:rw,nosuid,nodev,noexec,size=512m,nr_inodes=65536,mode=1777",
		"--entrypoint", "/bin/sh", c.Image, "-c", checkoutScript, "checkout", cloneURL, revision}
	cmd := exec.CommandContext(ctx, rt, args...)
	cmd.WaitDelay = time.Second
	cmd.Stdin = strings.NewReader(token + "\n")
	var diagnostics boundedOutput
	cmd.Stderr = &diagnostics
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start bounded checkout: %w", err)
	}
	extractErr := extractCheckout(stdout, dir, checkoutBytes, checkoutEntries)
	if extractErr != nil {
		_ = cmd.Process.Kill()
	}
	runErr := cmd.Wait()
	if extractErr != nil {
		return fmt.Errorf("bounded checkout export: %w", extractErr)
	}
	if ctx.Err() != nil {
		return fmt.Errorf("bounded checkout deadline: %w", ctx.Err())
	}
	if runErr != nil {
		diagnostic := strings.ReplaceAll(diagnostics.String(), tokenPlaceholder(token), "[redacted]")
		return fmt.Errorf("bounded checkout failed (512 MiB/65536 entries): %v: %s", runErr, truncate(diagnostic))
	}
	return nil
}

// Extraction cannot escape dir or allocate beyond the declared budgets. Reject
// links/devices and duplicate entries; Git materializes only files/directories.
// Symlink submissions fail closed and can be reviewed/uploaded separately.
func extractCheckout(reader io.Reader, dir string, maxBytes int64, maxEntries int) error {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	tr := tar.NewReader(reader)
	seen := make(map[string]bool)
	var used int64
	for count := 0; ; count++ {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if count >= maxEntries {
			return errors.New("checkout exceeds entry budget")
		}
		name := filepath.Clean(h.Name)
		if name == "." && h.Typeflag == tar.TypeDir {
			continue
		}
		if len(name) > 4096 || !filepath.IsLocal(name) || strings.Split(filepath.ToSlash(name), "/")[0] == ".git" || seen[name] {
			return errors.New("unsafe or duplicate checkout entry")
		}
		seen[name] = true
		switch h.Typeflag {
		case tar.TypeDir:
			if err := root.Mkdir(name, 0755); err != nil {
				return err
			}
			if err := root.Chmod(name, 0755); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if h.Size < 0 || h.Size > maxBytes-used {
				return errors.New("checkout exceeds byte budget")
			}
			used += h.Size
			f, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644|os.FileMode(h.Mode&0111))
			if err != nil {
				return err
			}
			if err := f.Chmod(0644 | os.FileMode(h.Mode&0111)); err != nil {
				_ = f.Close()
				return err
			}
			_, copyErr := io.CopyN(f, tr, h.Size)
			closeErr := f.Close()
			if err := errors.Join(copyErr, closeErr); err != nil {
				return err
			}
		default:
			return errors.New("checkout contains unsupported link or special file")
		}
	}
}
