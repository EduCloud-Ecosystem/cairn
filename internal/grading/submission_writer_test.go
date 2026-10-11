// SPDX-License-Identifier: AGPL-3.0-or-later
package grading

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"net/http/cgi"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/EduCloud-Ecosystem/cairn/internal/handoff"
	"github.com/EduCloud-Ecosystem/cairn/pkg/adapter"
)

func TestSubmissionWriterRejectsUnsafeInput(t *testing.T) {
	for _, p := range []string{"../answer.py", "/answer.py", ".github/workflows/ci.yml", "a/.git/config", "a/.env", "Makefile", "grading.yaml", "answer\n.py", "a\\b.py", "-answer.py", "a/../b.py"} {
		if err := ValidateSubmissionFiles([]handoff.File{{Path: p, Content: []byte("x")}}); err == nil {
			t.Errorf("accepted %q", p)
		}
	}
	if err := ValidateSubmissionFiles([]handoff.File{{Path: "a", Content: nil}, {Path: "a/b.py", Content: nil}}); err == nil {
		t.Fatal("path conflict accepted")
	}
	if err := ValidateSubmissionFiles([]handoff.File{{Path: "answer.py", Content: make([]byte, SubmissionMaxFileBytes+1)}}); err == nil {
		t.Fatal("oversized file accepted")
	}
	files := []handoff.File{}
	for _, p := range []string{"a.py", "b.py", "c.py", "d.py", "e.py"} {
		files = append(files, handoff.File{Path: p, Content: make([]byte, SubmissionMaxFileBytes)})
	}
	if err := ValidateSubmissionFiles(files); err == nil {
		t.Fatal("aggregate bytes unbounded")
	}
	w := NewSubmissionWriter(map[adapter.Host]CloneCreds{adapter.HostForgejo: {Hostname: "forge.example", Token: "secret"}}, CheckoutContainer{})
	for _, r := range []adapter.RepoRef{{Host: adapter.HostForgejo, Namespace: "../x", Name: "a"}, {Host: adapter.HostForgejo, Namespace: "x", Name: "a?token=x"}, {Host: adapter.HostForgejo, Namespace: "https://evil", Name: "a"}} {
		if _, _, err := w.endpoint(r); err == nil {
			t.Fatal("endpoint escape accepted")
		}
	}
	if _, err := w.Inspect(context.Background(), adapter.RepoRef{Host: adapter.HostForgejo, Namespace: "course", Name: "work"}, []handoff.File{{Path: "answer.py"}}); err == nil {
		t.Fatal("unbounded fallback accepted")
	}
}

func TestSubmissionWriterContainerBoundsAndCredentials(t *testing.T) {
	root := t.TempDir()
	argsPath := filepath.Join(root, "args")
	stdinPath := filepath.Join(root, "stdin")
	shim := filepath.Join(root, "runtime")
	// This test-only runtime captures stdin privately; production never writes a credential file.
	script := "#!/bin/sh\nif [ \"$1\" = rm ]; then exit 0; fi\numask 077\nprintf '%s\\n' \"$@\" > '" + argsPath + "'\ncat > '" + stdinPath + "'\nprintf 'refs/heads/main\\n1111111111111111111111111111111111111111\\ncreate\\n'\n"
	if err := os.WriteFile(shim, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	token := "synthetic-'$;`-secret"
	w := NewSubmissionWriter(map[adapter.Host]CloneCreds{adapter.HostForgejo: {Hostname: "forge.example", Username: "fixture", Token: token}}, CheckoutContainer{Runtime: shim, Image: "operator-image"})
	_, err := w.Inspect(context.Background(), adapter.RepoRef{Host: adapter.HostForgejo, Namespace: "course", Name: "work"}, []handoff.File{{Path: "answer.py", Content: []byte("print(5)")}})
	if err != nil {
		t.Fatal(err)
	}
	args, _ := os.ReadFile(argsPath)
	input, _ := os.ReadFile(stdinPath)
	if bytes.Contains(args, []byte(token)) || !bytes.HasPrefix(input, []byte(token+"\n")) {
		t.Fatal("credentials not isolated to stdin")
	}
	for _, expected := range []string{"--pull=never", "--read-only", "--cap-drop=ALL", "--security-opt=no-new-privileges", "--memory=768m", "--memory-swap=768m", "--cpus=1", "--pids-limit=64", "size=512m,nr_inodes=65536", "--log-driver=none", "io.educloud.cairn.kind=acquisition"} {
		if !strings.Contains(string(args), expected) {
			t.Errorf("missing bound %s", expected)
		}
	}
}

// Real smart HTTP validates credential exchange, deterministic object creation,
// exact-base CAS and post-push reconciliation. No trusted host writer mode exists.
func TestSubmissionWriterDockerObjectCAS(t *testing.T) {
	if os.Getenv("CAIRN_SUBMISSION_DOCKER_TEST") != "1" {
		t.Skip("set CAIRN_SUBMISSION_DOCKER_TEST=1 after preloading Dockerfile.checkout")
	}
	git := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("git", args...).CombinedOutput()
		if err != nil {
			t.Fatalf("fixture git: %v: %s", err, out)
		}
		return strings.TrimSpace(string(out))
	}
	root := t.TempDir()
	work := filepath.Join(root, "working")
	git("init", "-b", "main", work)
	git("-C", work, "config", "user.name", "Synthetic fixture")
	git("-C", work, "config", "user.email", "fixture@example.invalid")
	for name, content := range map[string]string{"answer.py": "print('old')\n", "unselected.txt": "must remain\n", ".gitattributes": "answer.py filter=untrusted\n", "parent": "ordinary blob"} {
		if err := os.WriteFile(filepath.Join(work, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("answer.py", filepath.Join(work, "link.py")); err != nil {
		t.Fatal(err)
	}
	git("-C", work, "add", ".")
	git("-C", work, "commit", "-m", "Synthetic source")
	base := git("-C", work, "rev-parse", "HEAD")
	git("-C", work, "update-index", "--add", "--cacheinfo", "160000,"+base+",module")
	git("-C", work, "commit", "-m", "Synthetic submodule entry")
	project := filepath.Join(root, "http")
	if err := os.MkdirAll(filepath.Join(project, "course"), 0700); err != nil {
		t.Fatal(err)
	}
	bare := filepath.Join(project, "course", "private.git")
	git("clone", "--bare", work, bare)
	git("--git-dir", bare, "config", "http.receivepack", "true")
	backend := &cgi.Handler{Path: filepath.Join(git("--exec-path"), "git-http-backend"), Root: "/", Env: []string{"GIT_PROJECT_ROOT=" + project, "GIT_HTTP_EXPORT_ALL=1", "REMOTE_USER=fixture"}}
	const token = "synthetic-'$;`-writer-token"
	var pushes atomic.Int32
	var loseResponse atomic.Bool
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(out http.ResponseWriter, r *http.Request) {
		user, password, ok := r.BasicAuth()
		if !ok || user != "fixture" || password != token {
			out.Header().Set("WWW-Authenticate", `Basic realm="synthetic"`)
			out.WriteHeader(401)
			return
		}
		if r.Method == "POST" && strings.HasSuffix(r.URL.Path, "git-receive-pack") {
			pushes.Add(1)
			if loseResponse.Swap(false) {
				record := httptest.NewRecorder()
				backend.ServeHTTP(record, r)
				conn, _, err := out.(http.Hijacker).Hijack()
				if err == nil {
					_ = conn.Close()
				}
				return
			}
		}
		backend.ServeHTTP(out, r)
	}))
	listener, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	_ = server.Listener.Close()
	server.Listener = listener
	server.Start()
	defer server.Close()
	host := "host.docker.internal"
	if runtime.GOOS == "linux" {
		raw, e := exec.Command("docker", "network", "inspect", "bridge", "--format", "{{(index .IPAM.Config 0).Gateway}}").Output()
		if e != nil {
			t.Fatal(e)
		}
		host = strings.TrimSpace(string(raw))
	}
	_, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	creds := map[adapter.Host]CloneCreds{adapter.HostForgejo: {Scheme: "http", Hostname: net.JoinHostPort(host, port), Username: "fixture", Token: token}}
	w := NewSubmissionWriter(creds, CheckoutContainer{Image: "cairn-checkout:local"})
	w.Timeout = 30 * time.Second
	repo := adapter.RepoRef{Host: adapter.HostForgejo, Namespace: "course", Name: "private"}
	files := []handoff.File{{Path: "answer.py", Content: []byte("print('new')\n")}, {Path: "new/results.txt", Content: []byte("new work\n")}}
	snapshot, err := w.Inspect(context.Background(), repo, files)
	if err != nil {
		t.Fatal(err)
	}
	req := handoff.WriteRequest{Repo: repo, Branch: snapshot.Branch, BaseRevision: snapshot.BaseRevision, Files: files, ReceiptID: "receipt-one", CreatedAt: time.Unix(1700000000, 0).UTC()}
	candidate, err := w.Prepare(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	again, err := w.Prepare(context.Background(), req)
	if err != nil || again != candidate {
		t.Fatalf("nondeterministic candidate: %q %q %v", candidate, again, err)
	}
	loseResponse.Store(true)
	outcome, err := w.Publish(context.Background(), req, candidate)
	if err != nil || outcome != handoff.Committed {
		t.Fatalf("lost response not reconciled: %s %v", outcome, err)
	}
	sent := pushes.Load()
	outcome, err = w.Publish(context.Background(), req, candidate)
	if err != nil || outcome != handoff.Committed || pushes.Load() != sent {
		t.Fatalf("retry duplicated push: %s %v", outcome, err)
	}
	if git("--git-dir", bare, "show", candidate+":answer.py") != "print('new')" || git("--git-dir", bare, "show", candidate+":unselected.txt") != "must remain" {
		t.Fatal("selected update or unselected preservation failed")
	}
	if git("--git-dir", bare, "rev-list", "--parents", "-n1", candidate) != candidate+" "+req.BaseRevision {
		t.Fatal("commit does not have exactly the frozen parent")
	}
	for _, p := range []string{"link.py", "module/result.py", "parent/result.py", "new"} {
		if _, err := w.Inspect(context.Background(), repo, []handoff.File{{Path: p, Content: []byte("x")}}); err == nil {
			t.Fatalf("unsafe target accepted: %s", p)
		}
	}
	// Both requests freeze the same base; only one may advance it.
	next, err := w.Inspect(context.Background(), repo, files)
	if err != nil {
		t.Fatal(err)
	}
	requests := []handoff.WriteRequest{req, req}
	candidates := make([]string, 2)
	for i := range requests {
		requests[i].BaseRevision = next.BaseRevision
		requests[i].ReceiptID = []string{"race-one", "race-two"}[i]
		requests[i].Files = []handoff.File{{Path: "answer.py", Content: []byte(requests[i].ReceiptID)}}
		candidates[i], err = w.Prepare(context.Background(), requests[i])
		if err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	results := make([]handoff.Outcome, 2)
	errs := make([]error, 2)
	for i := range requests {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = w.Publish(context.Background(), requests[i], candidates[i])
		}(i)
	}
	wg.Wait()
	winners := 0
	for i, r := range results {
		if errs[i] != nil {
			t.Fatal(errs[i])
		}
		if r == handoff.Committed {
			winners++
		} else if r != handoff.Conflict && r != handoff.VerificationNeeded {
			t.Fatalf("unexpected race outcome %s", r)
		}
	}
	if winners != 1 {
		t.Fatalf("CAS winners=%d", winners)
	}
	// If an earlier attempt might have committed before a later remote update,
	// a retry must not call that a definite conflict or write again.
	req.Retry = true
	sent = pushes.Load()
	outcome, err = w.Publish(context.Background(), req, candidate)
	if err != nil || outcome != handoff.VerificationNeeded || pushes.Load() != sent {
		t.Fatalf("uncertain earlier publish lost its ambiguity: %s %v", outcome, err)
	}
	retryCandidate, err := w.Prepare(context.Background(), req)
	if err != nil || retryCandidate != candidate {
		t.Fatalf("retry metadata changed deterministic commit: %s %v", retryCandidate, err)
	}
	// A possibly successful publish followed by an instructor reset must never
	// be replayed just because the remote tip again matches the frozen base.
	git("--git-dir", bare, "update-ref", req.Branch, req.BaseRevision)
	outcome, err = w.Publish(context.Background(), req, candidate)
	if err != nil || outcome != handoff.VerificationNeeded || pushes.Load() != sent || git("--git-dir", bare, "rev-parse", req.Branch) != req.BaseRevision {
		t.Fatalf("retry overwrote a possible instructor reset: %s %v", outcome, err)
	}
	// An arbitrary intended SHA is never treated as a publishable candidate.
	if outcome, err = w.Publish(context.Background(), req, req.BaseRevision); err == nil || outcome != handoff.VerificationNeeded {
		t.Fatal("mismatched candidate accepted")
	}
}

func TestSubmissionWriterDeadlineAndDiagnosticPrivacy(t *testing.T) {
	root := t.TempDir()
	shim := filepath.Join(root, "runtime")
	cleanup := filepath.Join(root, "cleaned")
	const token = "private-synthetic-token"
	// Simulate a stuck backend; cleanup must use its own live context.
	script := "#!/bin/sh\nif [ \"$1\" = rm ]; then printf cleaned > '" + cleanup + "'; exit 0; fi\nprintf '" + token + "' >&2\nexec sleep 60\n"
	if err := os.WriteFile(shim, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	w := NewSubmissionWriter(map[adapter.Host]CloneCreds{adapter.HostForgejo: {Hostname: "forge.example", Token: token}}, CheckoutContainer{Runtime: shim, Image: "operator-image"})
	w.Timeout = 50 * time.Millisecond
	started := time.Now()
	_, err := w.Inspect(context.Background(), adapter.RepoRef{Host: adapter.HostForgejo, Namespace: "course", Name: "work"}, []handoff.File{{Path: "answer.py"}})
	if err == nil || strings.Contains(err.Error(), token) || time.Since(started) > 3*time.Second {
		t.Fatalf("deadline/privacy failed: %v", err)
	}
	if _, err := os.Stat(cleanup); err != nil {
		t.Fatal("independent cleanup was not attempted")
	}
}

func TestSubmissionWriterDockerBounds(t *testing.T) {
	if os.Getenv("CAIRN_SUBMISSION_DOCKER_TEST") != "1" {
		t.Skip("set CAIRN_SUBMISSION_DOCKER_TEST=1 after preloading Dockerfile.checkout")
	}
	build := t.TempDir()
	// This trusted test image substitutes transport only. Without a working tmpfs
	// bound, the oversized case proceeds to a valid inspection and the test fails.
	shim := `#!/bin/sh
case "$1" in
 ls-remote)
  case "$*" in
   *oversized*) dd if=/dev/zero of=/scratch/oversized-pack bs=1M count=600 >&2 || exit 13 ;;
   *stalled*) exec sleep 60 ;;
   *secret*) printf '%s' "$CAIRN_GIT_TOKEN" >&2; exit 14 ;;
  esac
  exec /usr/bin/git -c protocol.file.allow=always ls-remote --symref /fixture HEAD ;;
 fetch)
  for last do :; done
  exec /usr/bin/git -c protocol.file.allow=always fetch --quiet --no-tags --depth=1 /fixture "$last" ;;
esac
exec /usr/bin/git "$@"
`
	dockerfile := `FROM cairn-checkout:local
USER root
RUN mkdir /fixture && git -C /fixture init -b main && git -C /fixture config user.name Fixture && git -C /fixture config user.email fixture@example.invalid && echo source > /fixture/answer.py && git -C /fixture add . && git -C /fixture commit -m fixture && chown -R 65534:65534 /fixture
COPY git /usr/local/bin/git
RUN chmod 755 /usr/local/bin/git
USER 65534:65534
`
	for name, content := range map[string]string{"git": shim, "Dockerfile": dockerfile} {
		if err := os.WriteFile(filepath.Join(build, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	image := "cairn-submission-bound-test:" + strings.ToLower(filepath.Base(filepath.Dir(build)))
	if output, err := exec.Command("docker", "build", "-t", image, build).CombinedOutput(); err != nil {
		t.Fatalf("build bounded fixture: %v %s", err, output)
	}
	defer exec.Command("docker", "image", "rm", image).Run()
	for _, name := range []string{"normal", "oversized", "stalled", "secret"} {
		t.Run(name, func(t *testing.T) {
			const token = "private-synthetic-bound-token"
			w := NewSubmissionWriter(map[adapter.Host]CloneCreds{adapter.HostForgejo: {Hostname: "fixture.invalid", Token: token}}, CheckoutContainer{Image: image})
			w.Timeout = 20 * time.Second
			if name == "stalled" {
				w.Timeout = time.Second
			}
			start := time.Now()
			_, err := w.Inspect(context.Background(), adapter.RepoRef{Host: adapter.HostForgejo, Namespace: "course", Name: name}, []handoff.File{{Path: "answer.py", Content: []byte("new")}})
			if name == "normal" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || strings.Contains(err.Error(), token) {
				t.Fatalf("unsafe bound/diagnostic result: %v", err)
			}
			if name == "oversized" && strings.Contains(err.Error(), "deadline") {
				t.Fatal("oversized pack reached timeout rather than filesystem refusal")
			}
			if name == "stalled" && time.Since(start) > 10*time.Second {
				t.Fatal("stalled transport not stopped promptly")
			}
		})
	}
}
