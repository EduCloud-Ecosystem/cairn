// SPDX-License-Identifier: AGPL-3.0-or-later
package main

import (
	"context"
	"encoding/json"
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
	"testing"

	"github.com/EduCloud-Ecosystem/cairn/internal/enrollment"
	"github.com/EduCloud-Ecosystem/cairn/internal/store"
	"github.com/EduCloud-Ecosystem/cairn/internal/store/sqlite"
	"github.com/EduCloud-Ecosystem/cairn/pkg/adapter"
)

func TestCLIExportAndProtectedInputs(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "source.db")
	journal := filepath.Join(dir, "journal.db")
	configPath := filepath.Join(dir, "config.json")
	out := filepath.Join(dir, "out.json")
	st, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	rid := "47a8ed4608e14957bf74ac18f93655c1"
	aid := "a45abc18d8ca4ae6a7b32def7048d2ac"
	for _, err := range []error{
		st.CreateUser(ctx, &store.User{ID: "owner", Host: adapter.HostGitHub, HostUserID: "1", HostUsername: "teacher"}),
		st.CreateClassroom(ctx, &store.Classroom{ID: "course", CreatedBy: "owner", Host: adapter.HostGitHub, HostNamespace: "synthetic"}),
		st.CreateRosterEntry(ctx, &store.RosterEntry{ID: rid, ClassroomID: "course", Host: adapter.HostGitHub, HostUserID: "42", HostUsername: "private", Status: store.RosterActive}),
		st.CreateAssignment(ctx, &store.Assignment{ID: "assignment", ClassroomID: "course", Slug: "echo", Type: store.AssignmentIndividual}),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
	a, err := st.GetAssignment(ctx, "assignment")
	if err != nil {
		t.Fatal(err)
	}
	c := enrollment.Config{AuthorityID: aid, Issuer: "https://identity.invalid", Audience: "belay", Institution: "i", Lifetime: 60, Bindings: []enrollment.Binding{{Subject: "subject", ClassID: "course", RosterID: rid, Host: adapter.HostGitHub, HostUserID: "42", Assignments: []enrollment.Assignment{{ID: "assignment", Digest: enrollment.AssignmentDigest(a), Exercise: "echo-1", Version: "v1"}}}}}
	data, _ := json.Marshal(c)
	if err = os.WriteFile(configPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	init := []string{"-journal", journal, "-init-authority", aid}
	if err = run(init); err != nil {
		t.Fatal(err)
	}
	if err = run(init); err == nil {
		t.Fatal("reinitialized state")
	}
	base := []string{"-database", dbPath, "-journal", journal, "-config", configPath}
	if err = run(append(base, "-output", out)); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var snap enrollment.Snapshot
	if err = json.Unmarshal(body, &snap); err != nil {
		t.Fatal(err)
	}
	if len(snap.Subjects["subject"]) != 1 || snap.Authority.Revision != 1 {
		t.Fatal("missing CLI grant")
	}
	stat, err := os.Stat(out)
	if err != nil || stat.Mode().Perm() != 0600 {
		t.Fatal("not private")
	}
	for _, protected := range []string{dbPath, journal, configPath} {
		if err = run(append(base, "-output", protected)); err == nil {
			t.Fatal("replaced protected input")
		}
	}
	alias := filepath.Join(t.TempDir(), "alias")
	if err = os.Symlink(dir, alias); err != nil {
		t.Fatal(err)
	}
	if err = run(append(base, "-output", filepath.Join(alias, "journal.db"))); err == nil {
		t.Fatal("alias overwrote history")
	}
	if err = run([]string{"-database", dbPath, "-inspect-assignment", "assignment"}); err != nil {
		t.Fatal(err)
	}
	if err = run([]string{"-database", filepath.Join(dir, "absent.db"), "-inspect-assignment", "assignment"}); err == nil {
		t.Fatal("missing source accepted")
	}
}

func TestNonregularConfigRefuses(t *testing.T) {
	dir := t.TempDir()
	database := filepath.Join(dir, "source.db")
	st, err := sqlite.Open(database)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	journal := filepath.Join(dir, "journal.db")
	if err = enrollment.Initialize(journal, "a45abc18d8ca4ae6a7b32def7048d2ac"); err != nil {
		t.Fatal(err)
	}
	pipe := filepath.Join(dir, "pipe")
	if err = unix.Mkfifo(pipe, 0600); err != nil {
		t.Fatal(err)
	}
	if err = run([]string{"-database", database, "-journal", journal, "-config", pipe, "-output", filepath.Join(dir, "out.json")}); err == nil {
		t.Fatal("FIFO accepted")
	}
	if _, err = sqlite.OpenReadOnly(pipe); err == nil {
		t.Fatal("FIFO database accepted")
	}
}
