// SPDX-License-Identifier: AGPL-3.0-or-later
// Package reconcile provides privileged, reviewable identity repairs. It never
// adopts, deletes or grants access to a repository and never replaces a bound ID.
package reconcile

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/EduCloud-Ecosystem/cairn/internal/store"
	"github.com/EduCloud-Ecosystem/cairn/pkg/adapter"
)

type IdentityVerifier interface {
	VerifyIdentity(context.Context, string, string) error
}
type Provider interface {
	IdentityAuthority() string
	IdentityVerifier
	RepoExists(context.Context, adapter.RepoRef) (bool, error)
}
type Service struct {
	Store     store.Store
	Providers map[adapter.Host]Provider
	Now       func() time.Time
}

func (s Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

type Plan struct {
	Version           int          `json:"version"`
	ID                string       `json:"id"`
	CreatedAt         time.Time    `json:"created_at"`
	ExpiresAt         time.Time    `json:"expires_at"`
	RosterID          string       `json:"roster_id"`
	ClassroomID       string       `json:"classroom_id"`
	Host              adapter.Host `json:"host"`
	ProviderAuthority string       `json:"provider_authority"`
	Username          string       `json:"username"`
	Snapshot          string       `json:"snapshot"`
	ProviderID        string       `json:"provider_id"`
	Operator          string       `json:"operator"`
	EvidenceSHA256    string       `json:"evidence_sha256"`
}

func planID(p Plan) string {
	p.ID = ""
	b, _ := json.Marshal(p)
	return fmt.Sprintf("%x", sha256.Sum256(b))
}
func validDigest(v string) bool { b, e := hex.DecodeString(v); return e == nil && len(b) == 32 }
func (s Service) PlanIdentity(ctx context.Context, rosterID, providerID, operator, evidenceDigest string) (Plan, error) {
	p := Plan{}
	id, err := strconv.ParseInt(providerID, 10, 64)
	if err != nil || id <= 0 || strconv.FormatInt(id, 10) != providerID {
		return p, errors.New("positive canonical numeric provider ID required")
	}
	if strings.TrimSpace(operator) == "" || len(operator) > 200 || !validDigest(evidenceDigest) {
		return p, errors.New("operator and SHA-256 of reviewed historical identity evidence required")
	}
	r, err := s.Store.GetRosterEntry(ctx, rosterID)
	if err != nil {
		return p, err
	}
	if r.HostUserID != "" || r.Status == store.RosterRemoved {
		return p, errors.New("only an unbound, non-removed roster entry can be reconciled")
	}
	provider := s.Providers[r.Host]
	if provider == nil {
		return p, errors.New("host has no configured read-only identity verifier")
	}
	authority, err := url.Parse(provider.IdentityAuthority())
	if err != nil || authority.Host == "" || authority.User != nil || authority.RawQuery != "" || authority.Fragment != "" {
		return p, errors.New("provider authority must be an explicit instance URL without credentials or query")
	}
	if err = provider.VerifyIdentity(ctx, r.HostUsername, providerID); err != nil {
		return p, fmt.Errorf("verify current provider identity: %w", err)
	}
	now := s.now()
	p = Plan{Version: 1, CreatedAt: now, ExpiresAt: now.Add(time.Hour), RosterID: r.ID, ClassroomID: r.ClassroomID, Host: r.Host, ProviderAuthority: provider.IdentityAuthority(), Username: r.HostUsername, Snapshot: store.RosterSnapshot(r), ProviderID: providerID, Operator: operator, EvidenceSHA256: evidenceDigest}
	p.ID = planID(p)
	return p, nil
}
func (s Service) Apply(ctx context.Context, p Plan, operator, evidenceDigest string) (bool, error) {
	if p.Version != 1 || p.ID != planID(p) || !validDigest(p.Snapshot) || !validDigest(p.EvidenceSHA256) || p.Operator != operator || p.EvidenceSHA256 != evidenceDigest {
		return false, errors.New("plan, operator or evidence mismatch")
	}
	rs, ok := s.Store.(store.ReconciliationStore)
	if !ok {
		return false, errors.New("store does not support atomic reconciliation")
	}
	// Retry a committed operation without a second write, even after expiry. A
	// durable receipt is authoritative; a matching current identity is still required.
	audits, err := rs.ListIdentityReconciliations(ctx)
	if err != nil {
		return false, err
	}
	for _, a := range audits {
		if a.PlanID == p.ID {
			r, e := s.Store.GetRosterEntry(ctx, p.RosterID)
			if e != nil {
				return false, e
			}
			if r.HostUserID != p.ProviderID || a.RosterID != p.RosterID || a.Snapshot != p.Snapshot || a.ProviderID != p.ProviderID || a.ProviderAuthority != p.ProviderAuthority || a.Operator != p.Operator || a.EvidenceSHA256 != p.EvidenceSHA256 {
				return false, store.ErrConflict
			}
			return true, nil
		}
	}
	now := s.now()
	if now.Before(p.CreatedAt) || !now.Before(p.ExpiresAt) || p.ExpiresAt.Sub(p.CreatedAt) != time.Hour {
		return false, errors.New("plan expired or has an invalid validity window; create and review a fresh plan")
	}
	r, err := s.Store.GetRosterEntry(ctx, p.RosterID)
	if err != nil {
		return false, err
	}
	if store.RosterSnapshot(r) != p.Snapshot || r.HostUserID != "" || r.Host != p.Host || r.ClassroomID != p.ClassroomID || r.HostUsername != p.Username {
		return false, store.ErrConflict
	}
	provider := s.Providers[p.Host]
	if provider == nil {
		return false, errors.New("host has no configured read-only identity verifier")
	}
	if provider.IdentityAuthority() != p.ProviderAuthority {
		return false, errors.New("provider instance changed since plan; no changes applied")
	}
	if err = provider.VerifyIdentity(ctx, p.Username, p.ProviderID); err != nil {
		return false, fmt.Errorf("provider identity changed or unavailable; no changes applied: %w", err)
	}
	return rs.BindLegacyIdentity(ctx, store.IdentityReconciliation{PlanID: p.ID, RosterID: p.RosterID, Snapshot: p.Snapshot, ProviderID: p.ProviderID, ProviderAuthority: p.ProviderAuthority, Operator: operator, EvidenceSHA256: evidenceDigest, AppliedAt: now})
}

type Issue struct {
	Code         string           `json:"code"`
	ClassroomID  string           `json:"classroom_id"`
	RosterID     string           `json:"roster_id,omitempty"`
	SubmissionID string           `json:"submission_id,omitempty"`
	Repo         *adapter.RepoRef `json:"repo,omitempty"`
	Next         string           `json:"next"`
}
type Inventory struct {
	Version        int       `json:"version"`
	GeneratedAt    time.Time `json:"generated_at"`
	ProviderProbes bool      `json:"provider_probes"`
	Issues         []Issue   `json:"issues"`
	Limitations    []string  `json:"limitations"`
}

func (s Service) Inventory(ctx context.Context, probe bool) (Inventory, error) {
	out := Inventory{Version: 1, GeneratedAt: s.now(), ProviderProbes: probe, Issues: []Issue{}, Limitations: []string{"Provider collaborator and pending invitation lists are not collected; inspect both by immutable identity before retrying grants.", "Repository existence and a matching name are not ownership evidence; no automatic adoption or revocation is available.", "Queue history is not exposed by this inventory; include worker errors and provider audit history in investigation."}}
	classes, err := s.Store.ListClassrooms(ctx)
	if err != nil {
		return out, err
	}
	for _, c := range classes {
		roster, err := s.Store.ListRosterEntries(ctx, c.ID)
		if err != nil {
			return out, err
		}
		for _, r := range roster {
			if r.HostUserID == "" && r.Status != store.RosterRemoved {
				out.Issues = append(out.Issues, Issue{Code: "unbound_identity", ClassroomID: c.ID, RosterID: r.ID, Next: "Obtain historical identity evidence independently of the current username; review a plan-identity packet."})
			} else if probe && r.HostUserID != "" {
				p := s.Providers[r.Host]
				code := ""
				if p == nil {
					code = "identity_unverified"
				} else if e := p.VerifyIdentity(ctx, r.HostUsername, r.HostUserID); e != nil {
					code = "identity_probe_failed"
				}
				if code != "" {
					out.Issues = append(out.Issues, Issue{Code: code, ClassroomID: c.ID, RosterID: r.ID, Next: "Inspect provider immutable ID and username; never replace or clear the bound ID."})
				}
			}
		}
		subs, err := s.Store.ListSubmissionsByClassroom(ctx, c.ID)
		if err != nil {
			return out, err
		}
		for _, sub := range subs {
			expected := adapter.RepoRef{Host: c.Host, Namespace: c.HostNamespace, Name: fmt.Sprintf("cairn-%x", sha256.Sum256([]byte(sub.ID)))}
			repo := sub.Repo
			code := ""
			next := ""
			if repo == (adapter.RepoRef{}) {
				repo = expected
				code = "unbound_repository"
				next = "Inspect creation logs and provider history; never adopt the expected name alone."
			} else if repo != expected {
				code = "legacy_or_mismatched_repository"
				next = "Preserve the existing repository and coursework; inspect historical mapping before provisioning retries."
			} else if sub.Status != "active" && sub.Status != "locked" {
				code = "incomplete_provisioning"
				next = "Inspect persisted binding, collaborators and pending invitations by immutable identity before retrying."
			}
			if code != "" {
				ref := repo
				out.Issues = append(out.Issues, Issue{Code: code, ClassroomID: c.ID, RosterID: sub.RosterEntryID, SubmissionID: sub.ID, Repo: &ref, Next: next})
			}
			if probe {
				p := s.Providers[repo.Host]
				status := ""
				if p == nil {
					status = "repository_unverified"
				} else {
					exists, e := p.RepoExists(ctx, repo)
					if e != nil {
						status = "repository_probe_failed"
					} else if !exists && sub.Repo != (adapter.RepoRef{}) {
						status = "bound_repository_missing"
					} else if exists && sub.Repo == (adapter.RepoRef{}) {
						status = "possible_orphan_repository"
					}
				}
				if status != "" {
					ref := repo
					out.Issues = append(out.Issues, Issue{Code: status, ClassroomID: c.ID, RosterID: sub.RosterEntryID, SubmissionID: sub.ID, Repo: &ref, Next: "Read-only probe is not ownership or access proof; investigate without changing the binding."})
				}
			}
		}
	}
	sort.Slice(out.Issues, func(i, j int) bool {
		a, b := out.Issues[i], out.Issues[j]
		return a.ClassroomID+a.RosterID+a.SubmissionID+a.Code < b.ClassroomID+b.RosterID+b.SubmissionID+b.Code
	})
	return out, nil
}
