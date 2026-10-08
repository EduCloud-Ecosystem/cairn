// SPDX-License-Identifier: AGPL-3.0-or-later
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/EduCloud-Ecosystem/cairn/internal/assessmenteval"
)

func TestEvaluationCommandDefaultsOfflineAndRefusesOverwrite(t *testing.T) {
	out := filepath.Join(t.TempDir(), "run")
	if err := runAssessmentEval([]string{"run", "--out", out}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(out, "report.json"))
	if err != nil {
		t.Fatal(err)
	}
	var r assessmenteval.Report
	if err = json.Unmarshal(raw, &r); err != nil {
		t.Fatal(err)
	}
	if r.Live || !r.Complete || r.Metrics.ExpectedBlocks != 3 {
		t.Fatal("unexpected default behavior")
	}
	for _, name := range []string{"report.json", "human-review.json", "review.html", "evaluation.db"} {
		info, err := os.Stat(filepath.Join(out, name))
		if err != nil || info.Mode().Perm()&0077 != 0 {
			t.Fatalf("artifact permissions: %s %v", name, err)
		}
	}
	if err = runAssessmentEval([]string{"run", "--out", out}); err == nil {
		t.Fatal("overwrote previous run")
	}
	packetDir := filepath.Join(t.TempDir(), "packet")
	if err = runAssessmentEval([]string{"packet", "--report", filepath.Join(out, "report.json"), "--out", packetDir}); err != nil {
		t.Fatal(err)
	}
	if err = runAssessmentEval([]string{"review", "--report", filepath.Join(out, "report.json"), "--review", filepath.Join(out, "human-review.json")}); err != nil {
		t.Fatal(err)
	}
}
func TestLiveEvaluationRequiresExplicitPrivateKey(t *testing.T) {
	out := filepath.Join(t.TempDir(), "run")
	if err := runAssessmentEval([]string{"run", "--live", "--out", out}); err == nil {
		t.Fatal("live run without key accepted")
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatal("failed preflight created run")
	}
}
