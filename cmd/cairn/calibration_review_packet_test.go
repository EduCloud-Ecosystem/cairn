// SPDX-License-Identifier: AGPL-3.0-or-later
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/EduCloud-Ecosystem/cairn/internal/assessment"
)

func TestOfflineAttributedPacket(t *testing.T) {
	b, _ := testCheckPlan()
	root := t.TempDir()
	raw, _ := json.Marshal(b)
	bundle := filepath.Join(root, "bundle.json")
	os.WriteFile(bundle, raw, 0600)
	js := []assessment.Judgment{{CriterionID: "quality", Points: nil, Feedback: "Starter-only work", Uncertainty: "No substantive work to assess", Citations: []assessment.Citation{}}}
	raw, _ = json.Marshal(js)
	judgments := filepath.Join(root, "judgments.json")
	os.WriteFile(judgments, raw, 0600)
	out := filepath.Join(root, "prepared")
	args := []string{"--bundle", bundle, "--sample", "sample-01", "--judgments", judgments, "--authorship", "assistant", "--author", "Synthetic assistant", "--note", "Delegated adjudication, not independent reference", "--out", out}
	if err := runCalibrationReviewPacket(args); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(out, "review-packet.json"))
	if err != nil {
		t.Fatal(err)
	}
	var p assessment.CalibrationReviewPacket
	if json.Unmarshal(raw, &p) != nil || p.Authorship != "assistant" || p.SourceHashes["answer.py"] != assessment.DigestBytes([]byte("raise NotImplementedError")) || p.Judgments[0].Points != nil {
		t.Fatal("packet lost authorship, null score or source binding")
	}
	st, _ := os.Stat(filepath.Join(out, "review-packet.json"))
	if st.Mode().Perm() != 0600 {
		t.Fatal("private packet permissions")
	}
	if err = runCalibrationReviewPacket(args); err == nil {
		t.Fatal("overwrote prior review packet")
	}
	args[3] = "missing"
	args[len(args)-1] = filepath.Join(root, "invalid")
	if err = runCalibrationReviewPacket(args); err == nil {
		t.Fatal("unknown sample accepted")
	}
	if _, err = os.Stat(args[len(args)-1]); !os.IsNotExist(err) {
		t.Fatal("invalid packet created output")
	}
}
