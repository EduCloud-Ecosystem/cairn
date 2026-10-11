// SPDX-License-Identifier: AGPL-3.0-or-later
package storetest

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/EduCloud-Ecosystem/cairn/internal/store"
	"github.com/EduCloud-Ecosystem/cairn/pkg/adapter"
)

func testReconciliation(t *testing.T, st store.Store) {
	ctx := context.Background()
	rs, ok := st.(store.ReconciliationStore)
	if !ok {
		t.Fatal("missing atomic reconciliation capability")
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(st.CreateUser(ctx, &store.User{ID: "operator", Host: adapter.HostGitHub, HostUsername: "operator", HostUserID: "1"}))
	must(st.CreateClassroom(ctx, &store.Classroom{ID: "course", Host: adapter.HostGitHub, HostNamespace: "org", CreatedBy: "operator"}))
	claimed := time.Date(2026, 10, 11, 0, 0, 0, 123000000, time.UTC)
	must(st.CreateRosterEntry(ctx, &store.RosterEntry{ID: "legacy", ClassroomID: "course", Host: adapter.HostGitHub, HostUsername: "learner", Status: store.RosterActive, ClaimedAt: &claimed}))
	r, err := st.GetRosterEntry(ctx, "legacy")
	must(err)
	a := store.IdentityReconciliation{PlanID: strings.Repeat("a", 64), RosterID: r.ID, Snapshot: store.RosterSnapshot(r), ProviderID: "42", Operator: "operator", EvidenceSHA256: strings.Repeat("b", 64), AppliedAt: claimed}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, id := range []string{"42", "43"} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			v := a
			v.ProviderID = id
			v.PlanID += id
			_, e := rs.BindLegacyIdentity(ctx, v)
			results <- e
		}(id)
	}
	wg.Wait()
	close(results)
	successes := 0
	for e := range results {
		if e == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("concurrent repair successes = %d", successes)
	}
	audits, err := rs.ListIdentityReconciliations(ctx)
	must(err)
	if len(audits) != 1 {
		t.Fatalf("audit count %d", len(audits))
	}
	r, err = st.GetRosterEntry(ctx, "legacy")
	must(err)
	if r.HostUserID != audits[0].ProviderID || r.ClaimedAt == nil || !r.ClaimedAt.Equal(claimed) {
		t.Fatalf("binding or claim changed: %+v", r)
	}
	already, err := rs.BindLegacyIdentity(ctx, audits[0])
	must(err)
	if !already {
		t.Fatal("receipt replay should be idempotent")
	}
	must(st.DeleteRosterEntry(ctx, "legacy"))
	audits, err = rs.ListIdentityReconciliations(ctx)
	must(err)
	if len(audits) != 0 {
		t.Fatal("identity audit must follow learner erasure")
	}
}

// Normal enrollment and privileged reconciliation must share one uniqueness
// boundary, including requests that race on different roster rows.
func testReconciliationClaimRace(t *testing.T, st store.Store) {
	ctx := context.Background()
	rs := st.(store.ReconciliationStore)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(st.CreateUser(ctx, &store.User{ID: "race-owner", Host: adapter.HostGitHub, HostUsername: "owner", HostUserID: "1"}))
	must(st.CreateClassroom(ctx, &store.Classroom{ID: "race-course", Host: adapter.HostGitHub, HostNamespace: "org", CreatedBy: "race-owner"}))
	now := time.Now().UTC()
	for _, normal := range []string{"create", "claim"} {
		t.Run(normal, func(t *testing.T) {
			legacy := &store.RosterEntry{ID: "legacy-" + normal, ClassroomID: "race-course", Host: adapter.HostGitHub, HostUsername: "legacy-" + normal, Status: store.RosterActive, ClaimedAt: &now}
			must(st.CreateRosterEntry(ctx, legacy))
			// Read back timestamp precision and defaults before capturing the CAS digest.
			legacy, err := st.GetRosterEntry(ctx, legacy.ID)
			must(err)
			normalRow := &store.RosterEntry{ID: "normal-" + normal, ClassroomID: "race-course", Host: adapter.HostGitHub, HostUsername: "normal-" + normal, Status: store.RosterInvited}
			if normal == "claim" {
				must(st.CreateRosterEntry(ctx, normalRow))
			}
			id := "91"
			if normal == "claim" {
				id = "92"
			}
			normalRow.HostUserID = id
			normalRow.ClaimedAt = &now
			a := store.IdentityReconciliation{PlanID: "race-" + normal, RosterID: legacy.ID, Snapshot: store.RosterSnapshot(legacy), ProviderID: id, Operator: "operator", EvidenceSHA256: strings.Repeat("c", 64), AppliedAt: now}
			var wg sync.WaitGroup
			results := make(chan error, 2)
			wg.Add(2)
			go func() { defer wg.Done(); _, e := rs.BindLegacyIdentity(ctx, a); results <- e }()
			go func() {
				defer wg.Done()
				if normal == "claim" {
					results <- st.UpdateRosterEntry(ctx, normalRow)
				} else {
					results <- st.CreateRosterEntry(ctx, normalRow)
				}
			}()
			wg.Wait()
			close(results)
			successes := 0
			for e := range results {
				if e == nil {
					successes++
				}
			}
			if successes != 1 {
				t.Fatalf("successes %d; both paths must share uniqueness boundary", successes)
			}
			rows, err := st.ListRosterEntries(ctx, "race-course")
			must(err)
			bound := 0
			for _, r := range rows {
				if r.HostUserID == id {
					bound++
				}
			}
			if bound != 1 {
				t.Fatalf("duplicate bound rows: %d", bound)
			}
			// A later ordinary enrollment must not create the same identity either.
			duplicate := *normalRow
			duplicate.ID = "late-" + normal
			duplicate.HostUsername = "late-" + normal
			if err = st.CreateRosterEntry(ctx, &duplicate); err == nil {
				t.Fatal("late duplicate enrollment accepted")
			}
		})
	}
}
