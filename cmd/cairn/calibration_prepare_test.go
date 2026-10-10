// SPDX-License-Identifier: AGPL-3.0-or-later
package main

import (
	"encoding/json"
	"github.com/EduCloud-Ecosystem/cairn/internal/assessment"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPreparePrivateBundleAndEscapedInspection(t *testing.T) {
	root := t.TempDir()
	os.Mkdir(filepath.Join(root, "sample"), 0700)
	os.WriteFile(filepath.Join(root, "sample", "answer.qmd"), []byte("<script>evil()</script>\n```{r}\nstop('do not execute')\n```"), 0600)
	manifest := `{"purpose":"calibration","rubric":{"title":"Explanation","paths":["answer.qmd"],"criteria":[{"id":"reason","description":"Explain the choice","max_points":5}]},"examples":[{"id":"sample-01","directory":"sample"}]}`
	m := filepath.Join(root, "manifest.json")
	os.WriteFile(m, []byte(manifest), 0600)
	out := filepath.Join(root, "packet")
	args := []string{"--manifest", m, "--source-root", root, "--out", out}
	if err := runCalibrationPrepare(args); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"bundle.json", "rubric.json", "inspect.html"} {
		info, err := os.Stat(filepath.Join(out, name))
		if err != nil || info.Mode().Perm()&0077 != 0 {
			t.Fatal("output not private")
		}
	}
	html, _ := os.ReadFile(filepath.Join(out, "inspect.html"))
	if strings.Contains(string(html), "<script>") || !strings.Contains(string(html), "&lt;script&gt;") || !strings.Contains(string(html), "default-src 'none'") {
		t.Fatal("unsafe inspection HTML")
	}
	raw, _ := os.ReadFile(filepath.Join(out, "bundle.json"))
	var b assessment.CalibrationBundle
	if json.Unmarshal(raw, &b) != nil || len(b.Examples) != 1 || strings.Contains(string(raw), root) {
		t.Fatal("bundle malformed or leaked local path")
	}
	if runCalibrationPrepare(args) == nil {
		t.Fatal("overwrote existing packet")
	}
	// Paths cannot escape the source directory; no output is produced on failure.
	os.WriteFile(m, []byte(strings.Replace(manifest, `"directory":"sample"`, `"directory":"../sample"`, 1)), 0600)
	if runCalibrationPrepare([]string{"--manifest", m, "--source-root", root, "--out", filepath.Join(root, "bad")}) == nil {
		t.Fatal("unsafe sample path accepted")
	}
	os.Remove(filepath.Join(root, "sample", "answer.qmd"))
	os.Symlink(m, filepath.Join(root, "sample", "answer.qmd"))
	os.WriteFile(m, []byte(manifest), 0600)
	if runCalibrationPrepare([]string{"--manifest", m, "--source-root", root, "--out", filepath.Join(root, "linked")}) == nil {
		t.Fatal("symlink source accepted")
	}
}
