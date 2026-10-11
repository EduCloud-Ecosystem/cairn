// SPDX-License-Identifier: AGPL-3.0-or-later
package runtimeguard

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

type Resource struct {
	Type    string `json:"type"`
	ID      string `json:"id"`
	Created string `json:"created"`
}
type Plan struct {
	Version   int        `json:"version"`
	Root      string     `json:"root"`
	Scope     string     `json:"scope"`
	Runtime   string     `json:"runtime"`
	Resources []Resource `json:"resources"`
}
type Command func(context.Context, string, ...string) ([]byte, error)
type cappedOutput struct {
	bytes.Buffer
	overflow bool
}

func (b *cappedOutput) Write(p []byte) (int, error) {
	n := len(p)
	left := (4 << 20) - b.Len()
	if left < len(p) {
		p = p[:max(0, left)]
		b.overflow = true
	}
	b.Buffer.Write(p)
	return n, nil
}
func Execute(ctx context.Context, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	var out cappedOutput
	cmd.Stdout = &out
	cmd.Stderr = &out
	cmd.WaitDelay = time.Second
	err := cmd.Run()
	if out.overflow {
		return nil, errors.New("runtime response exceeds 4 MiB")
	}
	if err != nil {
		return nil, fmt.Errorf("runtime command failed: %w", err)
	}
	return out.Bytes(), nil
}

var containerID = regexp.MustCompile(`^[a-f0-9]{64}$`)

func Inventory(ctx context.Context, l *Lease, runtime string, run Command) (Plan, error) {
	p := Plan{Version: 1, Root: l.Root, Scope: l.Scope, Runtime: runtime, Resources: []Resource{}}
	if runtime != "docker" && runtime != "podman" {
		return p, errors.New("runtime must be docker or podman")
	}
	for _, kind := range []string{"container", "volume"} {
		args := []string{"ps", "-aq", "--no-trunc", "--filter", "label=" + ScopeLabel + "=" + l.Scope}
		if kind == "volume" {
			args = []string{"volume", "ls", "-q", "--filter", "label=" + ScopeLabel + "=" + l.Scope}
		}
		raw, err := run(ctx, runtime, args...)
		if err != nil {
			return p, err
		}
		for _, id := range strings.Fields(string(raw)) {
			if kind == "container" && !containerID.MatchString(id) {
				return p, errors.New("invalid container ID")
			}
			if kind == "volume" && (!strings.HasPrefix(id, "cairn-work-") || strings.ContainsAny(id, "/\\")) {
				continue
			}
			inspect := []string{"inspect", id}
			if kind == "volume" {
				inspect = []string{"volume", "inspect", id}
			}
			raw, err = run(ctx, runtime, inspect...)
			if err != nil {
				return p, err
			}
			var rows []struct {
				ID        string
				Name      string
				Created   string
				CreatedAt string
				Labels    map[string]string
				Config    struct{ Labels map[string]string }
			}
			if json.Unmarshal(raw, &rows) != nil || len(rows) != 1 {
				return p, errors.New("invalid runtime inspection")
			}
			row := rows[0]
			labels := row.Labels
			created := row.CreatedAt
			if kind == "container" {
				labels = row.Config.Labels
				created = row.Created
				if row.ID != id {
					return p, errors.New("container identity changed")
				}
			}
			if labels[ScopeLabel] != l.Scope {
				return p, errors.New("runtime ownership changed")
			}
			if kind == "volume" {
				if labels[KindLabel] != "workspace" || row.Name != id {
					continue
				}
			} else {
				name := strings.TrimPrefix(row.Name, "/")
				k := labels[KindLabel]
				if !(k == "grading" && (strings.HasPrefix(name, "cairn-work-keeper-") || strings.HasPrefix(name, "cairn-grade-")) || k == "acquisition" && strings.HasPrefix(name, "cairn-acquire-")) {
					continue
				}
			}
			if created == "" {
				return p, errors.New("runtime creation timestamp missing")
			}
			p.Resources = append(p.Resources, Resource{Type: kind, ID: id, Created: created})
		}
	}
	entries, err := os.ReadDir(l.Root)
	if err != nil {
		return p, err
	}
	for _, e := range entries {
		if OwnedJob(l.Root, e.Name(), l.Scope) {
			info, err := e.Info()
			if err != nil {
				return p, err
			}
			p.Resources = append(p.Resources, Resource{Type: "directory", ID: e.Name(), Created: info.ModTime().UTC().Format(time.RFC3339Nano)})
		}
	}
	sort.Slice(p.Resources, func(i, j int) bool {
		a, b := p.Resources[i], p.Resources[j]
		if a.Type != b.Type {
			return a.Type < b.Type
		}
		return a.ID < b.ID
	})
	return p, nil
}

// Apply requires the exclusive lease remain held from inventory through removal.
// It compares the whole inventory before changing anything. Partial failures stop
// immediately; create a new plan to inspect and resume the remaining resources.
func Apply(ctx context.Context, l *Lease, want Plan, run Command) error {
	if l == nil || l.file == nil || !l.exclusive {
		return errors.New("recovery requires an exclusive lease")
	}
	got, err := Inventory(ctx, l, want.Runtime, run)
	if err != nil {
		return err
	}
	a, _ := json.Marshal(want)
	b, _ := json.Marshal(got)
	if !bytes.Equal(a, b) {
		return errors.New("recovery plan is stale or belongs to a different work directory")
	}
	for _, kind := range []string{"container", "volume", "directory"} {
		for _, r := range got.Resources {
			if r.Type != kind {
				continue
			}
			switch kind {
			case "container":
				_, err = run(ctx, got.Runtime, "rm", "--force", r.ID)
			case "volume":
				_, err = run(ctx, got.Runtime, "volume", "rm", r.ID) // never force a mounted volume
			case "directory":
				if !OwnedJob(l.Root, r.ID, l.Scope) {
					return errors.New("directory ownership changed")
				}
				err = os.RemoveAll(filepath.Join(l.Root, r.ID))
			}
			if err != nil {
				return fmt.Errorf("recovery stopped at %s %s; inventory again: %w", kind, r.ID, err)
			}
		}
	}
	return nil
}
