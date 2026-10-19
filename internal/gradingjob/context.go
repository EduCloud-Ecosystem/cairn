// SPDX-License-Identifier: AGPL-3.0-or-later

// Package gradingjob defines pinned queue context, not execution authority.
package gradingjob

import (
	"encoding/json"
	"errors"
	"io"
	"path"
	"regexp"
	"strings"

	"github.com/EduCloud-Ecosystem/cairn/internal/store"
	"github.com/EduCloud-Ecosystem/cairn/pkg/adapter"
)

// ErrInvalid is permanent: old/unpinned entries must never pick up current policy.
var ErrInvalid = errors.New("grading job lacks valid version-1 pinned context; operator review and explicit manual re-enqueue required")

type Policy struct {
	Repository adapter.RepoRef `json:"repository"`
	Revision   string          `json:"revision"`
	Path       string          `json:"path"`
}

type Target struct {
	Version      int             `json:"version"`
	SubmissionID string          `json:"submission_id"`
	AssignmentID string          `json:"assignment_id"`
	Repository   adapter.RepoRef `json:"repository"`
	Revision     string          `json:"revision"`
	Policy       Policy          `json:"policy"`
}

// Context identifies a logical queue job. It is not a reservation or attempt.
type Context struct {
	JobID  string `json:"job_id"`
	Target Target `json:"target"`
}

var fullCommit = regexp.MustCompile(`^[a-f0-9]{40}$|^[a-f0-9]{64}$`)

// EligibleStatus permits manual/durable grading after a deadline lock. Push
// admission retains the webhook's separate active-only rule.
func EligibleStatus(status string) bool { return status == "active" || status == "locked" }

func opaque(s string) bool {
	return s != "" && len(s) <= 256 && strings.TrimSpace(s) == s && !strings.ContainsAny(s, "\x00\r\n\t")
}

func repository(r adapter.RepoRef) bool {
	switch r.Host {
	case adapter.HostGitHub, adapter.HostGitLab, adapter.HostForgejo, adapter.HostGitea:
	default:
		return false
	}
	for _, value := range []string{r.Namespace, r.Name} {
		if !opaque(value) || path.IsAbs(value) || path.Clean(value) != value || value == "." || value == ".." || strings.HasPrefix(value, "../") || strings.ContainsAny(value, "\\?#%@:") {
			return false
		}
	}
	return !strings.Contains(r.Name, "/")
}

func (t Target) Validate() error {
	p := t.Policy.Path
	if t.Version != 1 || !opaque(t.SubmissionID) || !opaque(t.AssignmentID) || !repository(t.Repository) || !repository(t.Policy.Repository) || !fullCommit.MatchString(t.Revision) || !fullCommit.MatchString(t.Policy.Revision) || !opaque(p) || path.IsAbs(p) || path.Clean(p) != p || p == "." || p == ".." || strings.HasPrefix(p, "../") || strings.Split(p, "/")[0] == ".git" || strings.Contains(p, "\\") {
		return ErrInvalid
	}
	return nil
}

func (c Context) Validate() error {
	if !opaque(c.JobID) {
		return ErrInvalid
	}
	return c.Target.Validate()
}

// Capture takes values already authenticated/selected by Cairn's API. It does
// not resolve Git branches or grant authority from a claimed hash.
func Capture(sub *store.Submission, asg *store.Assignment, revision string) (Target, error) {
	if sub == nil || asg == nil || sub.AssignmentID != asg.ID || !EligibleStatus(sub.Status) {
		return Target{}, ErrInvalid
	}
	t := Target{Version: 1, SubmissionID: sub.ID, AssignmentID: asg.ID, Repository: sub.Repo, Revision: strings.ToLower(revision), Policy: Policy{Repository: adapter.RepoRef{Host: asg.TemplateRef.Host, Namespace: asg.TemplateRef.Namespace, Name: asg.TemplateRef.Name}, Revision: strings.ToLower(asg.TemplateRef.Ref), Path: asg.GradingSpec}}
	return t, t.Validate()
}

func (t Target) Encode() (string, error) {
	if err := t.Validate(); err != nil {
		return "", err
	}
	b, err := json.Marshal(t)
	return string(b), err
}

func Decode(raw string) (Target, error) {
	var t Target
	if len(raw) > 8192 {
		return t, ErrInvalid
	}
	d := json.NewDecoder(strings.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&t) != nil || d.Decode(new(any)) != io.EOF || t.Validate() != nil {
		return Target{}, ErrInvalid
	}
	// encoding/json otherwise uses the last duplicate key, including inside
	// repository/policy objects. Ambiguous stored pins are never normalized.
	if uniqueKeys(json.NewDecoder(strings.NewReader(raw))) != nil {
		return Target{}, ErrInvalid
	}
	return t, nil
}

func uniqueKeys(d *json.Decoder) error {
	token, err := d.Token()
	if err != nil {
		return err
	}
	switch token {
	case json.Delim('{'):
		var seen []string
		for d.More() {
			key, err := d.Token()
			name, ok := key.(string)
			if err != nil || !ok {
				return ErrInvalid
			}
			// Match Go's case-folding, including Unicode field aliases.
			for _, previous := range seen {
				if strings.EqualFold(previous, name) {
					return ErrInvalid
				}
			}
			seen = append(seen, name)
			if err := uniqueKeys(d); err != nil {
				return err
			}
		}
		_, err = d.Token()
	case json.Delim('['):
		for d.More() {
			if err := uniqueKeys(d); err != nil {
				return err
			}
		}
		_, err = d.Token()
	}
	return err
}
