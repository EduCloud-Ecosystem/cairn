// SPDX-License-Identifier: AGPL-3.0-or-later
package grading

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/EduCloud-Ecosystem/cairn/internal/store/memory"
	"github.com/EduCloud-Ecosystem/cairn/pkg/adapter"
)

func TestCheckoutArchiveBounds(t *testing.T) {
	cases := []struct {
		name    string
		headers []tar.Header
		want    string
	}{
		{"bytes", []tar.Header{{Name: "big", Mode: 0600, Size: 9}}, "byte budget"},
		{"entries", []tar.Header{{Name: "a"}, {Name: "b"}}, "entry budget"},
		{"escape", []tar.Header{{Name: "../escape"}}, "unsafe"},
		{"link", []tar.Header{{Name: "link", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"}}, "unsupported"},
		{"duplicate", []tar.Header{{Name: "a"}, {Name: "a"}}, "duplicate"},
		{"git", []tar.Header{{Name: ".git/config"}}, "unsafe"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var b bytes.Buffer
			tw := tar.NewWriter(&b)
			for _, h := range tc.headers {
				if err := tw.WriteHeader(&h); err != nil {
					t.Fatal(err)
				}
				_, _ = tw.Write(make([]byte, h.Size))
			}
			_ = tw.Close()
			entries := 100
			if tc.name == "entries" {
				entries = 1
			}
			err := extractCheckout(&b, t.TempDir(), 8, entries)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want %s", err, tc.want)
			}
		})
	}
}

func TestGitDiagnosticsBoundedAndDeadline(t *testing.T) {
	cmd := exec.Command("sh", "-c", "yes diagnostic | head -c 2000000")
	out, err := gitOutput(cmd)
	if err != nil || len(out) != 64<<10 {
		t.Fatalf("len=%d err=%v", len(out), err)
	}
	shim := filepath.Join(t.TempDir(), "git")
	if err := os.WriteFile(shim, []byte("#!/bin/sh\nexec sleep 60\n"), 0700); err != nil {
		t.Fatal(err)
	}
	g := makeCheckout(adapter.HostGitHub, CloneCreds{Hostname: "example.invalid"})
	g.GitBin = shim
	g.Timeout = 50 * time.Millisecond
	start := time.Now()
	err = g.Fetch(context.Background(), ghRepo, t.TempDir())
	if err == nil || time.Since(start) > 3*time.Second {
		t.Fatalf("deadline not enforced: %v", err)
	}
}

type stalledCheckout struct{}

func (stalledCheckout) Fetch(ctx context.Context, _ adapter.RepoRef, _ string) error {
	<-ctx.Done()
	return ctx.Err()
}
func (s stalledCheckout) FetchRevision(ctx context.Context, r adapter.RepoRef, _, d string) error {
	return s.Fetch(ctx, r, d)
}
func TestTotalDeadlinePersistsFailedRun(t *testing.T) {
	st := memory.New()
	seedSubmission(t, st)
	svc := NewService(st, NewExecRunner(), stalledCheckout{})
	svc.TotalTimeout = 20 * time.Millisecond
	if err := svc.Grade(context.Background(), "s1"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v", err)
	}
	runs, err := st.ListGradingRunsBySubmission(context.Background(), "s1")
	if err != nil || len(runs) != 1 || runs[0].Status != "failed" {
		t.Fatalf("runs=%+v err=%v", runs, err)
	}
}

// Opt-in real Docker test: exercises the kernel tmpfs bound, streaming export,
// token delivery/redaction and cancellation cleanup without external Git access.
func TestCheckoutDockerAcquisition(t *testing.T) {
	if os.Getenv("CAIRN_CHECKOUT_DOCKER_TEST") != "1" {
		t.Skip("set CAIRN_CHECKOUT_DOCKER_TEST=1 after building Dockerfile.checkout")
	}
	build := t.TempDir()
	shim := `#!/bin/sh
case "$*" in
 *oversized*) mkdir /scratch/repo; exec dd if=/dev/zero of=/scratch/repo/large bs=1M count=600 ;;
 *stalled*) exec sleep 60 ;;
 *secret*) printf '%s' "$CAIRN_GIT_TOKEN" >&2; exit 1 ;;
 *clone*) exec /usr/bin/git -c safe.directory=/fixture clone --depth 1 file:///fixture /scratch/repo ;;
esac
exec /usr/bin/git "$@"
`
	dockerfile := `FROM cairn-checkout:local
USER root
COPY grading.json /fixture/grading.json
RUN  git -C /fixture init && git -C /fixture config user.name Test && git -C /fixture config user.email test@example.invalid && echo fixture > /fixture/work.txt && git -C /fixture add . && git -C /fixture commit -m initial && chmod -R a+rX /fixture && chown -R 65534:65534 /fixture
COPY git /usr/local/bin/git
RUN chmod 755 /usr/local/bin/git
USER 65534:65534
ENTRYPOINT []
`
	for name, content := range map[string]string{"git": shim, "Dockerfile": dockerfile, "grading.json": `{"version":"1","tests":[{"name":"source and policy","run":"test $(cat work.txt) = fixture && test -r $CAIRN_POLICY_DIR/grading.json","points":10}]}`} {
		if err := os.WriteFile(filepath.Join(build, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	image := "cairn-checkout-test:" + strings.ToLower(strings.ReplaceAll(filepath.Base(build), "_", "-"))
	if out, err := exec.Command("docker", "build", "-t", image, build).CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	defer exec.Command("docker", "image", "rm", image).Run()
	for _, name := range []string{"normal", "oversized", "stalled", "secret"} {
		t.Run(name, func(t *testing.T) {
			g := makeCheckout(adapter.HostGitHub, CloneCreds{Hostname: "example.invalid", Token: "synthetic_secret"})
			g.Container = &CheckoutContainer{Image: image}
			g.Timeout = 20 * time.Second
			if name == "stalled" {
				g.Timeout = time.Second
			}
			repo := ghRepo
			repo.Name = name
			dir := t.TempDir()
			start := time.Now()
			err := g.Fetch(context.Background(), repo, dir)
			switch name {
			case "normal":
				if err != nil {
					t.Fatal(err)
				}
				data, err := os.ReadFile(filepath.Join(dir, "work.txt"))
				if err != nil || string(data) != "fixture\n" {
					t.Fatalf("data=%q err=%v", data, err)
				}
				if _, err := os.Stat(filepath.Join(dir, ".git")); !os.IsNotExist(err) {
					t.Fatal("git metadata exported")
				}
			case "oversized":
				if err == nil || !strings.Contains(err.Error(), "No space left") {
					t.Fatalf("kernel bound not observed: %v", err)
				}
			case "stalled":
				if err == nil || time.Since(start) > 10*time.Second {
					t.Fatalf("stalled checkout not stopped: %v", err)
				}
			case "secret":
				if err == nil || strings.Contains(err.Error(), "synthetic_secret") {
					t.Fatalf("credential not redacted: %v", err)
				}
			}
		})
	}
	t.Run("service binds", func(t *testing.T) {
		pinBytes, err := exec.Command("docker", "run", "--rm", "--entrypoint", "/usr/bin/git", image, "-C", "/fixture", "rev-parse", "HEAD").Output()
		if err != nil {
			t.Fatal(err)
		}
		st := memory.New()
		seedSubmission(t, st)
		asg, err := st.GetAssignment(context.Background(), "a1")
		if err != nil {
			t.Fatal(err)
		}
		asg.TemplateRef.Ref = strings.TrimSpace(string(pinBytes))
		if err := st.UpdateAssignment(context.Background(), asg); err != nil {
			t.Fatal(err)
		}
		checkout := makeCheckout(adapter.HostGitHub, CloneCreds{Hostname: "example.invalid"})
		checkout.Container = &CheckoutContainer{Image: image}
		svc := NewService(st, &ContainerRunner{DefaultImage: image}, checkout)
		if err := svc.Grade(context.Background(), "s1"); err != nil {
			t.Fatal(err)
		}
		grade, err := st.LatestGradeForSubmission(context.Background(), "s1")
		if err != nil || grade.Score != 10 {
			t.Fatalf("grade=%+v err=%v", grade, err)
		}
	})
	out, err := exec.Command("docker", "ps", "-a", "--filter", "ancestor="+image, "--format", "{{.Names}}").CombinedOutput()
	if err != nil || len(bytes.TrimSpace(out)) != 0 {
		t.Fatalf("acquisition containers leaked: %s %v", out, err)
	}
}

func TestCheckoutPreservesPrivateCallerDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	var archive bytes.Buffer
	tw := tar.NewWriter(&archive)
	if err := tw.WriteHeader(&tar.Header{Name: "file", Mode: 0644, Size: 1}); err != nil {
		t.Fatal(err)
	}
	_, _ = tw.Write([]byte("x"))
	_ = tw.Close()
	if err := extractCheckout(&archive, dir, 100, 100); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(dir)
	if err != nil || info.Mode().Perm() != 0700 {
		t.Fatalf("caller privacy changed: %v %v", info, err)
	}
}
