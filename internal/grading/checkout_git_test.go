// SPDX-License-Identifier: AGPL-3.0-or-later

package grading

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EduCloud-Ecosystem/cairn/pkg/adapter"
)

// newGitCheckout is a test helper that builds a GitCheckout from a variadic list
// of (Host, CloneCreds) pairs — avoids verbose map literals in each test case.
func makeCheckout(pairs ...any) *GitCheckout {
	hosts := make(map[adapter.Host]CloneCreds, len(pairs)/2)
	for i := 0; i+1 < len(pairs); i += 2 {
		hosts[pairs[i].(adapter.Host)] = pairs[i+1].(CloneCreds)
	}
	return NewGitCheckout(hosts)
}

var ghRepo = adapter.RepoRef{Host: adapter.HostGitHub, Namespace: "cs101-org", Name: "hw1-alice"}

func TestCloneRequestGitHubWithToken(t *testing.T) {
	g := makeCheckout(adapter.HostGitHub, CloneCreds{
		Hostname: "github.com",
		Username: "x-access-token",
		Token:    "ghp_secret",
	})
	url, tok, err := g.cloneRequest(ghRepo)
	if err != nil {
		t.Fatal(err)
	}
	if tok != "ghp_secret" {
		t.Errorf("token = %q, want ghp_secret", tok)
	}
	if !strings.HasPrefix(url, "https://x-access-token@github.com/") {
		t.Errorf("url = %q, want https://x-access-token@github.com/...", url)
	}
	// Token must never appear in the URL.
	if strings.Contains(url, "ghp_secret") {
		t.Errorf("token leaked into URL: %q", url)
	}
	if !strings.HasSuffix(url, "/cs101-org/hw1-alice.git") {
		t.Errorf("url = %q, missing namespace/name.git suffix", url)
	}
}

func TestCloneRequestGitHubNoToken(t *testing.T) {
	g := makeCheckout(adapter.HostGitHub, CloneCreds{
		Hostname: "github.com",
	})
	url, tok, err := g.cloneRequest(ghRepo)
	if err != nil {
		t.Fatal(err)
	}
	if tok != "" {
		t.Errorf("token = %q, want empty for public repo", tok)
	}
	want := "https://github.com/cs101-org/hw1-alice.git"
	if url != want {
		t.Errorf("url = %q, want %q", url, want)
	}
}

func TestCloneRequestForgejo(t *testing.T) {
	g := makeCheckout(adapter.HostForgejo, CloneCreds{
		Hostname: "forgejo.example.org",
		Username: "oauth2",
		Token:    "fgt_secret",
	})
	repo := adapter.RepoRef{Host: adapter.HostForgejo, Namespace: "cs101", Name: "hw1-bob"}
	url, tok, err := g.cloneRequest(repo)
	if err != nil {
		t.Fatal(err)
	}
	if tok != "fgt_secret" {
		t.Errorf("token = %q, want fgt_secret", tok)
	}
	if !strings.HasPrefix(url, "https://oauth2@forgejo.example.org/") {
		t.Errorf("url = %q, want https://oauth2@forgejo.example.org/...", url)
	}
	if strings.Contains(url, "fgt_secret") {
		t.Errorf("token leaked into URL: %q", url)
	}
	if !strings.HasSuffix(url, "/cs101/hw1-bob.git") {
		t.Errorf("url = %q, missing namespace/name.git suffix", url)
	}
}

func TestCloneRequestUnknownHostErrors(t *testing.T) {
	// Map contains GitHub but NOT GitLab — requesting a GitLab repo must error,
	// not silently fall back to github.com.
	g := makeCheckout(adapter.HostGitHub, CloneCreds{Hostname: "github.com"})
	repo := adapter.RepoRef{Host: adapter.HostGitLab, Namespace: "org", Name: "repo"}
	_, _, err := g.cloneRequest(repo)
	if err == nil {
		t.Fatal("want error for host not in map, got nil")
	}
	// Error must not suggest github.com as the target.
	if strings.Contains(err.Error(), "github.com") {
		t.Errorf("error unexpectedly mentions github.com: %v", err)
	}
}

func TestCloneRequestEmptyHostnameErrors(t *testing.T) {
	// A key present in the map with an empty Hostname must also error.
	g := makeCheckout(adapter.HostGitHub, CloneCreds{Token: "tok"}) // Hostname intentionally blank
	_, _, err := g.cloneRequest(ghRepo)
	if err == nil {
		t.Fatal("want error for empty Hostname, got nil")
	}
}

func TestCloneRequestGHESCustomHost(t *testing.T) {
	// HostGitHub with a GHES hostname — URL uses the custom host, not github.com.
	g := makeCheckout(adapter.HostGitHub, CloneCreds{
		Hostname: "ghe.example.com",
		Username: "x-access-token",
		Token:    "tok",
	})
	url, _, err := g.cloneRequest(ghRepo)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(url, "ghe.example.com") {
		t.Errorf("url = %q, want ghe.example.com in host", url)
	}
	if strings.Contains(url, "github.com") {
		t.Errorf("url = %q, must not fall back to github.com", url)
	}
}

func TestCloneRequestDefaultUsername(t *testing.T) {
	// When Username is empty and a Token is set, default to "x-access-token".
	g := makeCheckout(adapter.HostGitHub, CloneCreds{
		Hostname: "github.com",
		Token:    "tok",
		// Username intentionally unset
	})
	url, _, err := g.cloneRequest(ghRepo)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(url, "https://x-access-token@github.com/") {
		t.Errorf("url = %q, want default username x-access-token", url)
	}
}

func TestCloneRequestForgejoOverHTTP(t *testing.T) {
	// A Forgejo/Gitea instance served over plain HTTP (e.g. http://localhost:3000)
	// must be cloned over http, not forced through TLS.
	g := makeCheckout(adapter.HostForgejo, CloneCreds{
		Scheme:   "http",
		Hostname: "localhost:3000",
		Username: "username",
		Token:    "t",
	})
	repo := adapter.RepoRef{Host: adapter.HostForgejo, Namespace: "ns", Name: "name"}
	url, tok, err := g.cloneRequest(repo)
	if err != nil {
		t.Fatal(err)
	}
	if tok != "t" {
		t.Errorf("token = %q, want t", tok)
	}
	want := "http://username@localhost:3000/ns/name.git"
	if url != want {
		t.Errorf("url = %q, want %q", url, want)
	}
	// Token must never appear in the URL.
	if strings.Contains(url, "t@localhost") && !strings.HasPrefix(url, "http://username@") {
		t.Errorf("token may have leaked into URL: %q", url)
	}
}

func TestCloneRequestSchemeDefaultsToHTTPS(t *testing.T) {
	// An empty Scheme defaults to https (backward-compatible with existing config).
	g := makeCheckout(adapter.HostGitHub, CloneCreds{
		Hostname: "github.com",
		Username: "x-access-token",
		Token:    "t",
		// Scheme intentionally unset
	})
	url, _, err := g.cloneRequest(ghRepo)
	if err != nil {
		t.Fatal(err)
	}
	want := "https://x-access-token@github.com/cs101-org/hw1-alice.git"
	if url != want {
		t.Errorf("url = %q, want %q", url, want)
	}
}

func TestFetchRevisionUsesPinnedHistoricalCommit(t *testing.T) {
	repo := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git: %v %s", err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("-C", repo, "init")
	git("-C", repo, "config", "user.name", "Synthetic Teacher")
	git("-C", repo, "config", "user.email", "teacher@example.invalid")
	if err := os.WriteFile(filepath.Join(repo, "grading.json"), []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	git("-C", repo, "add", ".")
	git("-C", repo, "commit", "-m", "original")
	pin := git("-C", repo, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(repo, "grading.json"), []byte("later"), 0600); err != nil {
		t.Fatal(err)
	}
	git("-C", repo, "commit", "-am", "later")
	// Redirect only clone to a synthetic local repository. All fetch/checkout/
	// revision verification commands are real git commands against its objects.
	shim := filepath.Join(t.TempDir(), "git")
	script := "#!/bin/sh\nif [ \"$1\" = clone ]; then\nfor arg do dest=$arg; done\nexec git clone --depth 1 \"file://$CAIRN_TEST_REPO\" \"$dest\"\nfi\nexec git \"$@\"\n"
	if err := os.WriteFile(shim, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CAIRN_TEST_REPO", repo)
	g := makeCheckout(adapter.HostGitHub, CloneCreds{Hostname: "github.com"})
	g.GitBin = shim
	dir := t.TempDir()
	if err := g.FetchRevision(context.Background(), ghRepo, pin, dir); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "grading.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "original" {
		t.Fatalf("moving HEAD used instead of pin: %q", data)
	}
	for _, invalid := range []string{"main", "HEAD", "--upload-pack=other", pin[:8]} {
		if err := g.FetchRevision(context.Background(), ghRepo, invalid, t.TempDir()); err == nil {
			t.Fatalf("accepted revision %q", invalid)
		}
	}
}
