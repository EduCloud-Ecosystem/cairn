// SPDX-License-Identifier: AGPL-3.0-or-later
package enrollment

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/EduCloud-Ecosystem/cairn/internal/store"
	"github.com/EduCloud-Ecosystem/cairn/internal/store/sqlite"
	"github.com/EduCloud-Ecosystem/cairn/pkg/adapter"
)

const rosterID = "47a8ed4608e14957bf74ac18f93655c1"
const authorityID = "a45abc18d8ca4ae6a7b32def7048d2ac"

func fixture(t *testing.T) (*sqlite.Store, Config, string, string) {
	t.Helper()
	dir := t.TempDir()
	source := filepath.Join(dir, "source.db")
	st, err := sqlite.Open(source)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	ctx := context.Background()
	for _, err := range []error{
		st.CreateUser(ctx, &store.User{ID: "owner", Host: adapter.HostGitHub, HostUserID: "1", HostUsername: "instructor"}),
		st.CreateClassroom(ctx, &store.Classroom{ID: "course", Host: adapter.HostGitHub, HostNamespace: "synthetic", CreatedBy: "owner", JoinPolicy: "roster"}),
		st.CreateRosterEntry(ctx, &store.RosterEntry{ID: rosterID, ClassroomID: "course", Host: adapter.HostGitHub, HostUsername: "private-username", HostUserID: "829174", Status: store.RosterActive}),
		st.CreateAssignment(ctx, &store.Assignment{ID: "assignment", ClassroomID: "course", Slug: "echo", Title: "Synthetic", Type: store.AssignmentIndividual}),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
	a, err := st.GetAssignment(ctx, "assignment")
	if err != nil {
		t.Fatal(err)
	}
	c := Config{authorityID, "https://identity.test.invalid", "belay-tests", "institution", 120, []Binding{{"verified-subject", "course", rosterID, adapter.HostGitHub, "829174", []Assignment{{"assignment", AssignmentDigest(a), "echo-1", "v1"}}}}}
	journal := filepath.Join(dir, "journal.db")
	if err := Initialize(journal, authorityID); err != nil {
		t.Fatal(err)
	}
	return st, c, journal, source
}
func export(t *testing.T, st Source, c Config, journal, source string, now time.Time) Snapshot {
	t.Helper()
	v, err := Export(context.Background(), st, journal, source, c, now)
	if err != nil {
		t.Fatal(err)
	}
	return v
}
func TestLifecycle(t *testing.T) {
	st, c, journal, source := fixture(t)
	now := time.Now()
	ctx := context.Background()
	a := export(t, st, c, journal, source, now)
	b := export(t, st, c, journal, source, now.Add(time.Second))
	first := a.Subjects["verified-subject"][0].Generation
	if b.Subjects["verified-subject"][0].Generation != first || b.Authority.Revision != 2 {
		t.Fatal("refresh changed generation")
	}
	r, _ := st.GetRosterEntry(ctx, rosterID)
	r.Status = store.RosterRemoved
	if err := st.UpdateRosterEntry(ctx, r); err != nil {
		t.Fatal(err)
	}
	revoked := export(t, st, c, journal, source, now.Add(2*time.Second))
	if len(revoked.Subjects) != 0 {
		t.Fatal("removed grant exported")
	}
	r.Status = store.RosterActive
	if err := st.UpdateRosterEntry(ctx, r); err != nil {
		t.Fatal(err)
	}
	renewed := export(t, st, c, journal, source, now.Add(3*time.Second))
	if renewed.Subjects["verified-subject"][0].Generation == first {
		t.Fatal("reused withdrawn generation")
	}
	afterGap := export(t, st, c, journal, source, now.Add(200*time.Second))
	if afterGap.Subjects["verified-subject"][0].Generation == renewed.Subjects["verified-subject"][0].Generation {
		t.Fatal("resurrected expired generation")
	}
	raw, _ := json.Marshal(a)
	for _, private := range []string{"private-username", "829174", "host_user_id", "host_username"} {
		if strings.Contains(string(raw), private) {
			t.Fatal("private source field exported")
		}
	}
}
func TestRefusals(t *testing.T) {
	for _, change := range []string{"invited", "removed", "unverified", "different-host-id", "wrong-class", "wrong-assignment-class", "stale-assignment", "missing-roster", "wrong-host"} {
		t.Run(change, func(t *testing.T) {
			st, c, journal, source := fixture(t)
			ctx := context.Background()
			r, _ := st.GetRosterEntry(ctx, rosterID)
			a, _ := st.GetAssignment(ctx, "assignment")
			switch change {
			case "invited":
				r.Status = store.RosterInvited
			case "removed":
				r.Status = store.RosterRemoved
			case "unverified":
				r.ID = "95bc972abaec499498b84ddcf38ae8fa"
				r.HostUsername = "unclaimed"
				r.HostUserID = ""
				c.Bindings[0].RosterID = r.ID
				if err := st.CreateRosterEntry(ctx, r); err != nil {
					t.Fatal(err)
				}
			case "different-host-id":
				c.Bindings[0].HostUserID = "42"
			case "wrong-class":
				c.Bindings[0].ClassID = "other"
			case "wrong-assignment-class":
				a.ClassroomID = "other"
				_ = st.CreateClassroom(ctx, &store.Classroom{ID: "other", Host: adapter.HostGitHub, HostNamespace: "other", CreatedBy: "owner"})
				_ = st.UpdateAssignment(ctx, a)
				c.Bindings[0].Assignments[0].Digest = AssignmentDigest(a)
			case "stale-assignment":
				a.Title = "changed"
				_ = st.UpdateAssignment(ctx, a)
			case "missing-roster":
				c.Bindings[0].RosterID = "95bc972abaec499498b84ddcf38ae8fa"
			case "wrong-host":
				c.Bindings[0].Host = adapter.HostGitLab
			}
			if err := st.UpdateRosterEntry(ctx, r); err != nil {
				t.Fatal(err)
			}
			s := export(t, st, c, journal, source, time.Now())
			if len(s.Subjects) != 0 {
				t.Fatal("refused membership granted")
			}
		})
	}
}
func TestJournalAndConfiguration(t *testing.T) {
	for _, change := range []string{"missing-state", "wrong-authority", "wrong-source", "clock-rollback", "duplicate", "conflicting-version", "bad-issuer", "long-lifetime", "bad-label", "missing-generation-schema"} {
		t.Run(change, func(t *testing.T) {
			st, c, journal, source := fixture(t)
			now := time.Now()
			export(t, st, c, journal, source, now)
			switch change {
			case "missing-state":
				_ = os.Remove(journal)
			case "wrong-authority":
				c.AuthorityID = "95bc972abaec499498b84ddcf38ae8fa"
			case "wrong-source":
				source += "other"
			case "clock-rollback":
				now = now.Add(-time.Second)
			case "duplicate":
				c.Bindings = append(c.Bindings, c.Bindings[0])
			case "conflicting-version":
				a := c.Bindings[0].Assignments[0]
				a.Version = "v2"
				c.Bindings[0].Assignments = append(c.Bindings[0].Assignments, a)
			case "bad-issuer":
				c.Issuer = "http://identity.invalid"
			case "long-lifetime":
				c.Lifetime = 301
			case "bad-label":
				c.Bindings[0].RosterID = "learner@example.invalid"
			case "missing-generation-schema":
				_ = os.WriteFile(journal, []byte("not sqlite"), 0600)
			}
			if _, err := Export(context.Background(), st, journal, source, c, now); err == nil {
				t.Fatal("accepted invalid authority")
			}
		})
	}
}
func TestReadOnlySource(t *testing.T) {
	st, _, _, path := fixture(t)
	ro, err := sqlite.OpenReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()
	ctx := context.Background()
	r, err := ro.GetRosterEntry(ctx, rosterID)
	if err != nil {
		t.Fatal(err)
	}
	r.Status = store.RosterRemoved
	if err := ro.UpdateRosterEntry(ctx, r); err == nil {
		t.Fatal("read-only source wrote")
	}
	if err := st.UpdateRosterEntry(ctx, r); err != nil {
		t.Fatal(err)
	}
	still, err := ro.GetRosterEntry(ctx, rosterID)
	if err != nil || still.Status != store.RosterActive {
		t.Fatal("source snapshot changed")
	}
	if _, err := sqlite.OpenReadOnly(path + ".missing"); err == nil {
		t.Fatal("missing source opened")
	}
}

// Writes only synthetic native-store evidence for the cross-component evaluator.
func TestWriteConsumerFixture(t *testing.T) {
	dir := os.Getenv("ENROLLMENT_FIXTURE_DIR")
	if dir == "" {
		t.Skip("explicit synthetic fixture output not requested")
	}
	st, c, journal, source := fixture(t)
	ctx := context.Background()
	now := time.Now()
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	save := func(name string) {
		t.Helper()
		v := export(t, st, c, journal, source, now)
		if err := WritePrivate(filepath.Join(dir, name+".json"), v); err != nil {
			t.Fatal(err)
		}
	}
	save("active")
	save("refresh")
	r, _ := st.GetRosterEntry(ctx, rosterID)
	r.Status = store.RosterRemoved
	if err := st.UpdateRosterEntry(ctx, r); err != nil {
		t.Fatal(err)
	}
	save("revoked")
	r.Status = store.RosterActive
	if err := st.UpdateRosterEntry(ctx, r); err != nil {
		t.Fatal(err)
	}
	save("renewed")
	a, _ := st.GetAssignment(ctx, "assignment")
	a.Title = "new assignment revision"
	_ = st.UpdateAssignment(ctx, a)
	save("stale-assignment")
}
