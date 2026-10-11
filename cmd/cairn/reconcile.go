// SPDX-License-Identifier: AGPL-3.0-or-later
package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/EduCloud-Ecosystem/cairn/internal/reconcile"
	"github.com/EduCloud-Ecosystem/cairn/internal/store"
	"github.com/EduCloud-Ecosystem/cairn/pkg/adapter"
	forgejoadapter "github.com/EduCloud-Ecosystem/cairn/pkg/adapter/forgejo"
)

func runReconcile(args []string) error {
	if len(args) == 0 {
		return errors.New("expected inventory, plan-identity, apply or audit; see docs/reconciliation.md")
	}
	command := args[0]
	if command != "inventory" && command != "plan-identity" && command != "apply" && command != "audit" {
		return errors.New("expected inventory, plan-identity, apply or audit")
	}
	fs := flag.NewFlagSet("reconcile "+command, flag.ContinueOnError)
	out := fs.String("out", "", "new private JSON output file (required; will not overwrite)")
	probe := fs.Bool("probe", false, "inventory: probe configured providers using read-only requests")
	roster := fs.String("roster", "", "plan-identity: roster ID")
	providerID := fs.String("provider-id", "", "plan-identity: historical immutable numeric provider ID")
	operator := fs.String("operator", "", "plan-identity/apply: accountable operator identifier")
	evidence := fs.String("evidence-file", "", "plan-identity/apply: private historical identity evidence file; only SHA-256 is recorded")
	verified := fs.Bool("historical-identity-verified", false, "confirm evidence independently establishes the original learner's provider ID; current username lookup alone is insufficient")
	planPath := fs.String("plan", "", "apply: reviewed identity plan JSON")
	if err := fs.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 0 || *out == "" {
		return errors.New("--out is required and positional arguments are not accepted")
	}
	var p reconcile.Plan
	var digest string
	var err error
	if command == "plan-identity" || command == "apply" {
		if !*verified || *operator == "" || *evidence == "" {
			return errors.New("--operator, --evidence-file and --historical-identity-verified are required")
		}
		digest, err = reconciliationEvidenceDigest(*evidence)
		if err != nil {
			return err
		}
	}
	if command == "apply" {
		if err = readReconciliationPlan(*planPath, &p); err != nil {
			return err
		}
	}
	// Reserve the private result path before making any database changes.
	f, err := os.OpenFile(*out, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return fmt.Errorf("create private output: %w", err)
	}
	defer f.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	st, kind, err := openStore(ctx)
	if err != nil {
		return err
	}
	if closer, ok := st.(interface{ Close() error }); ok {
		defer closer.Close()
	}
	if command == "apply" && kind == "memory (ephemeral)" {
		return errors.New("reconciliation apply requires durable SQLite or PostgreSQL storage")
	}
	service := reconcile.Service{Store: st}
	if (command == "inventory" && *probe) || command == "plan-identity" || command == "apply" {
		service.Providers, err = reconciliationProviders()
		if err != nil {
			return err
		}
	}
	var result any
	switch command {
	case "inventory":
		result, err = service.Inventory(ctx, *probe)
	case "plan-identity":
		result, err = service.PlanIdentity(ctx, *roster, *providerID, *operator, digest)
	case "apply":
		var already bool
		already, err = service.Apply(ctx, p, *operator, digest)
		if err == nil {
			result = struct {
				PlanID         string `json:"plan_id"`
				AlreadyApplied bool   `json:"already_applied"`
				Status         string `json:"status"`
			}{p.ID, already, "applied"}
		}
	case "audit":
		rs, ok := st.(store.ReconciliationStore)
		if !ok {
			return errors.New("store does not support reconciliation audit")
		}
		result, err = rs.ListIdentityReconciliations(ctx)
	}
	if err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err = enc.Encode(result); err != nil {
		return err
	}
	return f.Sync()
}
func reconciliationProviders() (map[adapter.Host]reconcile.Provider, error) {
	out := map[adapter.Host]reconcile.Provider{}
	gh, err := githubAdapterFromEnv()
	if err != nil {
		return nil, err
	}
	if gh != nil {
		out[adapter.HostGitHub] = gh
	}
	cfg, err := forgejoConfigFromEnv()
	if err != nil {
		return nil, err
	}
	if cfg != nil {
		for _, h := range []adapter.Host{adapter.HostForgejo, adapter.HostGitea} {
			a, e := forgejoadapter.NewWithHost(*cfg, h)
			if e != nil {
				return nil, e
			}
			out[h] = a
		}
	}
	gl, err := gitlabAdapterFromEnv()
	if err != nil {
		return nil, err
	}
	if gl != nil {
		out[adapter.HostGitLab] = gl
	}
	return out, nil
}
func reconciliationEvidenceDigest(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Size() == 0 || info.Size() > 10<<20 {
		return "", errors.New("evidence must be a nonempty regular file of at most 10 MiB")
	}
	b, err := io.ReadAll(io.LimitReader(f, 10<<20+1))
	if err != nil {
		return "", err
	}
	if len(b) == 0 || len(b) > 10<<20 {
		return "", errors.New("invalid evidence size")
	}
	return fmt.Sprintf("%x", sha256.Sum256(b)), nil
}
func readReconciliationPlan(path string, p *reconcile.Plan) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	dec := json.NewDecoder(io.LimitReader(f, 64<<10))
	dec.DisallowUnknownFields()
	if err = dec.Decode(p); err != nil {
		return err
	}
	var extra any
	if err = dec.Decode(&extra); err != io.EOF {
		return errors.New("plan must contain exactly one JSON object")
	}
	return nil
}
