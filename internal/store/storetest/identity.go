// SPDX-License-Identifier: AGPL-3.0-or-later
package storetest

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/EduCloud-Ecosystem/cairn/internal/store"
	"github.com/EduCloud-Ecosystem/cairn/pkg/adapter"
)

func testRosterIdentity(t *testing.T, st store.Store) {
	ctx := context.Background()
	if err := st.CreateUser(ctx, &store.User{ID: "identity-owner", Host: adapter.HostGitHub, HostUserID: "owner", HostUsername: "owner"}); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateClassroom(ctx, &store.Classroom{ID: "identity-class", Host: adapter.HostGitHub, HostNamespace: "test", CreatedBy: "identity-owner"}); err != nil {
		t.Fatal(err)
	}
	entry := &store.RosterEntry{ID: "identity-roster", ClassroomID: "identity-class", Host: adapter.HostGitHub, HostUsername: "learner", Status: store.RosterInvited}
	if err := st.CreateRosterEntry(ctx, entry); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, id := range []string{"original-id", "recycled-id"} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			claim := *entry
			claim.HostUserID = id
			now := time.Now()
			claim.ClaimedAt = &now
			results <- st.UpdateRosterEntry(ctx, &claim)
		}(id)
	}
	wg.Wait()
	close(results)
	successes, conflicts := 0, 0
	for err := range results {
		if err == nil {
			successes++
		} else if errors.Is(err, store.ErrConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("concurrent identity claims: success=%d conflicts=%d", successes, conflicts)
	}
	bound, err := st.GetRosterEntry(ctx, entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	if bound.HostUserID == "" {
		t.Fatal("identity not retained")
	}
	if err := st.UpdateRosterEntry(ctx, bound); err != nil {
		t.Fatal("idempotent identity update", err)
	}
	bound.HostUserID = ""
	if err := st.UpdateRosterEntry(ctx, bound); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("cleared identity: %v", err)
	}
	legacy := *entry
	legacy.ID = "legacy"
	legacy.HostUsername = "legacy"
	now := time.Now()
	legacy.ClaimedAt = &now
	if err := st.CreateRosterEntry(ctx, &legacy); err != nil {
		t.Fatal(err)
	}
	legacy.HostUserID = "new-owner"
	if err := st.UpdateRosterEntry(ctx, &legacy); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("silently rebound legacy coursework: %v", err)
	}
}
