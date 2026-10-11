// SPDX-License-Identifier: AGPL-3.0-or-later

package provisioning

import (
	"context"
	"errors"
	"testing"

	"github.com/EduCloud-Ecosystem/cairn/internal/store"
	"github.com/EduCloud-Ecosystem/cairn/internal/store/memory"
	"github.com/EduCloud-Ecosystem/cairn/pkg/adapter"
)

func seedProvisioning(t *testing.T, st store.Store, id, slug, username string, identity ...string) {
	t.Helper()
	hostUserID := "1"
	if len(identity) > 0 {
		hostUserID = identity[0]
	}
	ctx := context.Background()
	for _, err := range []error{
		st.CreateClassroom(ctx, &store.Classroom{ID: "c" + id, Host: adapter.HostGitHub, HostNamespace: "course"}),
		st.CreateAssignment(ctx, &store.Assignment{ID: "a" + id, ClassroomID: "c" + id, Slug: slug}),
		st.CreateRosterEntry(ctx, &store.RosterEntry{ID: "r" + id, ClassroomID: "c" + id, Host: adapter.HostGitHub, HostUsername: username, HostUserID: hostUserID}),
		st.CreateSubmission(ctx, &store.Submission{ID: id, AssignmentID: "a" + id, RosterEntryID: "r" + id, Status: "provisioning"}),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestProvisioningSeparatesAmbiguousAssignmentAndUsername(t *testing.T) {
	ctx := context.Background()
	st := memory.New()
	seedProvisioning(t, st, "s1", "lab-1", "alice")
	seedProvisioning(t, st, "s2", "lab", "1-alice")
	fa := &fakeAdapter{verifiedIDs: map[string]string{"bob": "1", "alice": "1", "1-alice": "1"}}
	w := &Worker{Store: st, Adapters: map[adapter.Host]adapter.Adapter{adapter.HostGitHub: fa}}
	for _, id := range []string{"s1", "s2"} {
		if err := w.createRepo(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	first, _ := st.GetSubmission(ctx, "s1")
	second, _ := st.GetSubmission(ctx, "s2")
	if first.Repo == second.Repo || len(fa.repos) != 2 || fa.collaboratorCalls != 2 {
		t.Fatalf("submissions shared a repository: %+v %+v", first, second)
	}
	if err := w.createRepo(ctx, "s1"); err != nil {
		t.Fatal(err)
	}
	if fa.createCalls != 2 {
		t.Fatal("bound retry attempted fresh creation")
	}
}

func TestProvisioningRejectsUnboundExistingRepository(t *testing.T) {
	ctx := context.Background()
	st := memory.New()
	seedProvisioning(t, st, "s1", "lab", "alice")
	foreign := adapter.RepoRef{Host: adapter.HostGitHub, Namespace: "course", Name: submissionRepoName("s1")}
	fa := &fakeAdapter{verifiedIDs: map[string]string{"bob": "1", "alice": "1", "1-alice": "1"}, repos: map[adapter.RepoRef]bool{foreign: true}}
	w := &Worker{Store: st, Adapters: map[adapter.Host]adapter.Adapter{adapter.HostGitHub: fa}}
	if err := w.createRepo(ctx, "s1"); !errors.Is(err, adapter.ErrRepoExists) {
		t.Fatalf("expected existing repository failure, got %v", err)
	}
	sub, _ := st.GetSubmission(ctx, "s1")
	if sub.Repo != (adapter.RepoRef{}) || fa.collaboratorCalls != 0 {
		t.Fatal("foreign repository was adopted or shared")
	}
}

type failBindingStore struct{ store.Store }

func (s failBindingStore) UpdateSubmission(context.Context, *store.Submission) error {
	return errors.New("database unavailable")
}

func TestProvisioningPersistsBindingBeforeGrantingAccess(t *testing.T) {
	ctx := context.Background()
	st := memory.New()
	seedProvisioning(t, st, "s1", "lab", "alice")
	fa := &fakeAdapter{verifiedIDs: map[string]string{"bob": "1", "alice": "1", "1-alice": "1"}}
	w := &Worker{Store: failBindingStore{st}, Adapters: map[adapter.Host]adapter.Adapter{adapter.HostGitHub: fa}}
	if err := w.createRepo(ctx, "s1"); err == nil {
		t.Fatal("ignored binding failure")
	}
	if fa.collaboratorCalls != 0 {
		t.Fatal("granted access without persisted ownership")
	}
	w.Store = st
	if err := w.createRepo(ctx, "s1"); !errors.Is(err, adapter.ErrRepoExists) {
		t.Fatalf("orphan must require reconciliation: %v", err)
	}
	if fa.collaboratorCalls != 0 {
		t.Fatal("retry adopted orphan")
	}
}

func TestProvisioningRejectsMismatchedAndMissingBindings(t *testing.T) {
	for _, mismatch := range []bool{true, false} {
		t.Run(map[bool]string{true: "mismatched", false: "missing"}[mismatch], func(t *testing.T) {
			ctx := context.Background()
			st := memory.New()
			seedProvisioning(t, st, "s1", "lab", "alice")
			sub, _ := st.GetSubmission(ctx, "s1")
			sub.Repo = adapter.RepoRef{Host: adapter.HostGitHub, Namespace: "course", Name: submissionRepoName("s1")}
			if mismatch {
				sub.Repo.Name = "legacy-or-foreign"
			}
			if err := st.UpdateSubmission(ctx, sub); err != nil {
				t.Fatal(err)
			}
			fa := &fakeAdapter{verifiedIDs: map[string]string{"bob": "1", "alice": "1", "1-alice": "1"}}
			w := &Worker{Store: st, Adapters: map[adapter.Host]adapter.Adapter{adapter.HostGitHub: fa}}
			if err := w.createRepo(ctx, "s1"); err == nil {
				t.Fatal("invalid persisted binding accepted")
			}
			if fa.collaboratorCalls != 0 || fa.createCalls != 0 {
				t.Fatal("invalid binding triggered host mutation")
			}
		})
	}
}

func TestProvisioningRejectsReassignedUsernameOnRetry(t *testing.T) {
	ctx := context.Background()
	st := memory.New()
	seedProvisioning(t, st, "s1", "lab", "alice")
	fa := &fakeAdapter{verifiedIDs: map[string]string{"alice": "1"}, webhookErr: errors.New("webhook outage")}
	w := &Worker{Store: st, Adapters: map[adapter.Host]adapter.Adapter{adapter.HostGitHub: fa}, WebhookBaseURL: "https://example.test", WebhookSecrets: map[adapter.Host]string{adapter.HostGitHub: "fixture"}}
	if err := w.createRepo(ctx, "s1"); err == nil {
		t.Fatal("expected webhook failure")
	}
	if fa.collaboratorCalls != 1 {
		t.Fatal("expected original verified grant")
	}
	fa.verifiedIDs["alice"] = "2"
	fa.webhookErr = nil
	if err := w.createRepo(ctx, "s1"); !errors.Is(err, adapter.ErrIdentityMismatch) {
		t.Fatalf("stale retry accepted: %v", err)
	}
	if fa.collaboratorCalls != 1 {
		t.Fatal("stale retry granted access to reassigned username")
	}
	sub, _ := st.GetSubmission(ctx, "s1")
	if sub.Status == "active" {
		t.Fatal("stale retry activated submission")
	}
}

type usernameOnlyAdapter struct{ adapter.Adapter }

func TestProvisioningRequiresVerifiedIdentityCapability(t *testing.T) {
	for _, mode := range []string{"unbound", "unsupported", "stale"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			st := memory.New()
			identity := "1"
			if mode == "unbound" {
				identity = ""
			}
			seedProvisioning(t, st, "s1", "lab", "alice", identity)
			fa := &fakeAdapter{verifiedIDs: map[string]string{"alice": "2"}}
			var ad adapter.Adapter = fa
			if mode == "unsupported" {
				ad = usernameOnlyAdapter{fa}
			}
			w := &Worker{Store: st, Adapters: map[adapter.Host]adapter.Adapter{adapter.HostGitHub: ad}}
			if err := w.createRepo(ctx, "s1"); err == nil {
				t.Fatal("unverified grant accepted")
			}
			if fa.collaboratorCalls != 0 {
				t.Fatal("fell back to username-only collaborator grant")
			}
		})
	}
}
