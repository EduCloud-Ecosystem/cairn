// SPDX-License-Identifier: AGPL-3.0-or-later
// Package enrollment exports finite, operator-reviewed grants from Cairn's roster.
// Its private journal tracks issuance only; it is never an enrollment authority.
package enrollment

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/EduCloud-Ecosystem/cairn/internal/id"
	"github.com/EduCloud-Ecosystem/cairn/internal/store"
	"github.com/EduCloud-Ecosystem/cairn/pkg/adapter"
	_ "modernc.org/sqlite"
)

var opaque = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,64}$`)
var label = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)
var hexID = regexp.MustCompile(`^[0-9a-f]{32}$`)
var principal = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,128}$`)
var numeric = regexp.MustCompile(`^[0-9]{1,32}$`)
var digestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
var ErrInvalid = errors.New("invalid or conflicting enrollment configuration")

// Source is Cairn's existing store; exports never change roster or assignments.
type Source interface {
	GetRosterEntry(context.Context, string) (*store.RosterEntry, error)
	GetAssignment(context.Context, string) (*store.Assignment, error)
	GetClassroom(context.Context, string) (*store.Classroom, error)
}
type Assignment struct {
	ID       string `json:"assignment_id"`
	Digest   string `json:"assignment_sha256"`
	Exercise string `json:"exercise_id"`
	Version  string `json:"exercise_version"`
}
type Binding struct {
	Subject     string       `json:"subject"`
	ClassID     string       `json:"class_id"`
	RosterID    string       `json:"roster_id"`
	Host        adapter.Host `json:"host"`
	HostUserID  string       `json:"host_user_id"`
	Assignments []Assignment `json:"assignments"`
}
type Config struct {
	AuthorityID string    `json:"authority_id"`
	Issuer      string    `json:"issuer"`
	Audience    string    `json:"audience"`
	Institution string    `json:"institution_id"`
	Lifetime    int64     `json:"lifetime_seconds"`
	Bindings    []Binding `json:"bindings"`
}
type Authority struct {
	SchemaVersion int    `json:"schema_version"`
	ID            string `json:"authority_id"`
	Issuer        string `json:"issuer"`
	Audience      string `json:"audience"`
	Revision      int64  `json:"revision"`
	Issued        int64  `json:"issued_at"`
	Expires       int64  `json:"expires_at"`
}
type Member struct {
	Institution string            `json:"institution_id"`
	Class       string            `json:"class_id"`
	Learner     string            `json:"learner_id"`
	Kind        string            `json:"learner_id_kind"`
	Generation  string            `json:"grant_generation"`
	Assignments map[string]string `json:"assignments"`
}
type Snapshot struct {
	Authority Authority           `json:"authority"`
	Versions  map[string]string   `json:"exercise_versions"`
	Subjects  map[string][]Member `json:"subjects"`
}

// AssignmentDigest binds the full existing assignment record, not a branch label.
// A change requires explicit operator review of the tutor exercise mapping.
func AssignmentDigest(a *store.Assignment) string {
	b, _ := json.Marshal(a)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func hash(v any) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func (c Config) validate() error {
	u, err := url.Parse(c.Issuer)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || !hexID.MatchString(c.AuthorityID) || !opaque.MatchString(c.Audience) || !label.MatchString(c.Institution) || c.Lifetime < 1 || c.Lifetime > 300 || len(c.Bindings) > 500 {
		return ErrInvalid
	}
	seen := map[string]bool{}
	roster := map[string]bool{}
	versions := map[string]string{}
	for _, b := range c.Bindings {
		key := b.Subject + "/" + b.ClassID
		if !principal.MatchString(b.Subject) || !label.MatchString(b.ClassID) || !hexID.MatchString(b.RosterID) || !numeric.MatchString(b.HostUserID) || seen[key] || roster[b.RosterID] || len(b.Assignments) == 0 || len(b.Assignments) > 64 {
			return ErrInvalid
		}
		if b.Host != adapter.HostGitHub && b.Host != adapter.HostGitLab && b.Host != adapter.HostForgejo && b.Host != adapter.HostGitea {
			return ErrInvalid
		}
		seen[key] = true
		roster[b.RosterID] = true
		exercises := map[string]bool{}
		for _, a := range b.Assignments {
			if !opaque.MatchString(a.ID) || !opaque.MatchString(a.Exercise) || !opaque.MatchString(a.Version) || !digestPattern.MatchString(a.Digest) || exercises[a.Exercise] {
				return ErrInvalid
			}
			if v, ok := versions[a.Exercise]; ok && v != a.Version {
				return ErrInvalid
			}
			versions[a.Exercise] = a.Version
			exercises[a.Exercise] = true
		}
	}
	return nil
}

func privateDB(path string) (*sql.DB, error) {
	s, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !s.Mode().IsRegular() || s.Mode().Perm()&0077 != 0 {
		return nil, errors.New("private regular journal required")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	u := url.URL{Scheme: "file", Path: abs}
	db, err := sql.Open("sqlite", u.String()+"?mode=rw&_pragma=busy_timeout(1000)")
	if err == nil {
		db.SetMaxOpenConns(1)
	}
	return db, err
}

// Initialize is exclusive. Missing issuance history must not silently reset.
func Initialize(path, authorityID string) error {
	if !hexID.MatchString(authorityID) {
		return ErrInvalid
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_ = f.Close()
	db, err := privateDB(path)
	if err != nil {
		return err
	}
	defer db.Close()
	_, err = db.Exec(`CREATE TABLE issuance (id INTEGER PRIMARY KEY CHECK(id=1), authority_id TEXT NOT NULL, context TEXT NOT NULL, revision INTEGER NOT NULL, observed_at INTEGER NOT NULL, expires_at INTEGER NOT NULL);
 CREATE TABLE grants (binding_key TEXT PRIMARY KEY, binding_digest TEXT NOT NULL, generation TEXT NOT NULL, seen_revision INTEGER NOT NULL);`)
	if err != nil {
		return err
	}
	_, err = db.Exec("INSERT INTO issuance VALUES (1, ?, '', 0, 0, 0)", authorityID)
	return err
}

// Export observes current records and commits issuance before returning a document.
// Missing/inactive/mismatched records omit the grant; source errors abort export.
// A withdrawal must be observed by an export to rotate its later regrant.
func Export(ctx context.Context, source Source, journal, sourceIdentity string, c Config, now time.Time) (Snapshot, error) {
	out := Snapshot{Versions: map[string]string{}, Subjects: map[string][]Member{}}
	if err := c.validate(); err != nil {
		return out, err
	}
	db, err := privateDB(journal)
	if err != nil {
		return out, err
	}
	defer db.Close()
	conn, err := db.Conn(ctx)
	if err != nil {
		return out, err
	}
	defer conn.Close()
	if _, err = conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return out, err
	}
	defer conn.ExecContext(ctx, "ROLLBACK") // harmless after commit
	var aid, contextHash string
	var revision, last, previousExpiry int64
	if err = conn.QueryRowContext(ctx, "SELECT authority_id,context,revision,observed_at,expires_at FROM issuance WHERE id=1").Scan(&aid, &contextHash, &revision, &last, &previousExpiry); err != nil {
		return out, err
	}
	want := hash([]string{sourceIdentity, c.AuthorityID, c.Issuer, c.Audience, c.Institution})
	if aid != c.AuthorityID || (contextHash != "" && contextHash != want) || now.Unix() < last || revision >= 1<<53-1 {
		return out, ErrInvalid
	}
	revision++
	out.Authority = Authority{1, c.AuthorityID, c.Issuer, c.Audience, revision, now.Unix(), now.Unix() + c.Lifetime}
	for _, b := range c.Bindings {
		for _, a := range b.Assignments {
			out.Versions[a.Exercise] = a.Version
		}
		r, e := source.GetRosterEntry(ctx, b.RosterID)
		if errors.Is(e, store.ErrNotFound) {
			continue
		}
		if e != nil {
			return out, e
		}
		cl, e := source.GetClassroom(ctx, b.ClassID)
		if errors.Is(e, store.ErrNotFound) {
			continue
		}
		if e != nil {
			return out, e
		}
		if r.Status != store.RosterActive || r.ClassroomID != cl.ID || cl.Host != b.Host || r.Host != b.Host || r.HostUserID == "" || r.HostUserID != b.HostUserID {
			continue
		}
		m := Member{c.Institution, b.ClassID, b.RosterID, "cairn_roster_v1", "", map[string]string{}}
		valid := true
		for _, pin := range b.Assignments {
			a, e := source.GetAssignment(ctx, pin.ID)
			if errors.Is(e, store.ErrNotFound) {
				valid = false
				break
			}
			if e != nil {
				return out, e
			}
			if a.ClassroomID != b.ClassID || AssignmentDigest(a) != pin.Digest {
				valid = false
				break
			}
			m.Assignments[pin.Exercise] = pin.Version
		}
		if !valid {
			continue
		}
		// Never store host IDs or raw principals in the issuance journal.
		key := hash([]string{b.Subject, b.ClassID})
		binding := hash(b)
		var previous, generation string
		var seen int64
		e = conn.QueryRowContext(ctx, "SELECT binding_digest,generation,seen_revision FROM grants WHERE binding_key=?", key).Scan(&previous, &generation, &seen)
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return out, e
		}
		if e != nil || previous != binding || seen != revision-1 || now.Unix() >= previousExpiry {
			generation = id.New()
		}
		m.Generation = generation
		_, e = conn.ExecContext(ctx, `INSERT INTO grants VALUES(?,?,?,?) ON CONFLICT(binding_key) DO UPDATE SET binding_digest=excluded.binding_digest,generation=excluded.generation,seen_revision=excluded.seen_revision`, key, binding, generation, revision)
		if e != nil {
			return out, e
		}
		out.Subjects[b.Subject] = append(out.Subjects[b.Subject], m)
	}
	_, err = conn.ExecContext(ctx, "UPDATE issuance SET context=?,revision=?,observed_at=?,expires_at=? WHERE id=1", want, revision, now.Unix(), out.Authority.Expires)
	if err != nil {
		return out, err
	}
	_, err = conn.ExecContext(ctx, "COMMIT")
	return out, err
}

// WritePrivate atomically installs a snapshot without extending its deadline.
func WritePrivate(path string, value Snapshot) error {
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".enrollment-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(append(b, '\n')); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(f.Name(), path); err != nil {
		return fmt.Errorf("install snapshot: %w", err)
	}
	return nil
}
