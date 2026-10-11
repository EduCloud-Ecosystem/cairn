// SPDX-License-Identifier: AGPL-3.0-or-later
package handoff

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/EduCloud-Ecosystem/cairn/internal/id"
	"github.com/EduCloud-Ecosystem/cairn/internal/store"
	"github.com/EduCloud-Ecosystem/cairn/pkg/adapter"
	"path"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

const MaxFiles = 32
const MaxFileBytes = 1 << 20
const MaxTotalBytes = 4 << 20
const MaxPending = 3
const JobType = "submission_handoff"

var ErrInvalid = errors.New("invalid submission files or request")
var ErrUnavailable = errors.New("submission is not currently available")
var ErrExpired = errors.New("submission preview expired; select files and review again")

type Actor struct {
	Host         adapter.Host
	Username, ID string
}
type Receipt struct {
	ID             string    `json:"id"`
	SubmissionID   string    `json:"submission_id"`
	Status         string    `json:"status"`
	Branch         string    `json:"branch,omitempty"`
	BaseRevision   string    `json:"base_revision,omitempty"`
	Changes        []Change  `json:"changes"`
	CreatedAt      time.Time `json:"created_at"`
	ExpiresAt      time.Time `json:"expires_at"`
	CommitRevision string    `json:"commit_revision,omitempty"`
	Message        string    `json:"message,omitempty"`
}
type document struct {
	ObservedRevision string       `json:"observed_revision"`
	Attempted        bool         `json:"attempted,omitempty"`
	Request          WriteRequest `json:"request"`
	Changes          []Change     `json:"changes"`
	Candidate        string       `json:"candidate,omitempty"`
	Message          string       `json:"message,omitempty"`
}
type Service struct {
	previewOnce      sync.Once
	previewSlots     chan struct{}
	Store            store.Store
	Writer           Writer
	GraderConfigured bool
	Classrooms       map[string]bool
	Now              func() time.Time
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC().Truncate(time.Second)
	}
	return time.Now().UTC().Truncate(time.Second)
}

var safeSegment = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*(?:\.[A-Za-z0-9_-]+)*$`)
var safeKey = regexp.MustCompile(`^[A-Za-z0-9_-]{8,128}$`)
var commitID = regexp.MustCompile(`^[a-f0-9]{40}$|^[a-f0-9]{64}$`)

func ValidateFiles(files []File, policy string) error {
	if len(files) < 1 || len(files) > MaxFiles {
		return ErrInvalid
	}
	seen := map[string]bool{}
	total := 0
	for _, f := range files {
		if len(f.Path) > 240 || len(f.Path) == 0 || f.Path != path.Clean(f.Path) || strings.Contains(f.Path, "\\") || seen[strings.ToLower(f.Path)] || len(f.Content) > MaxFileBytes {
			return ErrInvalid
		}
		for _, segment := range strings.Split(f.Path, "/") {
			if !safeSegment.MatchString(segment) {
				return ErrInvalid
			}
		}
		lower := strings.ToLower(f.Path)
		base := strings.ToLower(path.Base(f.Path))
		if lower == strings.ToLower(policy) || base == "dockerfile" || base == "makefile" || base == "jenkinsfile" || base == "grading.yaml" || base == "grading.yml" || base == "grading.json" || base == "autograding.json" || base == "requirements.txt" || base == "pyproject.toml" || base == "package.json" || base == "package-lock.json" || base == "renv.lock" || base == "environment.yml" || base == "environment.yaml" {
			return ErrInvalid
		}
		seen[lower] = true
		total += len(f.Content)
		if total > MaxTotalBytes {
			return ErrInvalid
		}
	}
	for a := range seen {
		for b := range seen {
			if strings.HasPrefix(a, b+"/") {
				return ErrInvalid
			}
		}
	}
	return nil
}
func digest(v any) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func (s *Service) authorize(ctx context.Context, subID string, actor Actor, write bool) (*store.Submission, *store.Assignment, error) {
	sub, err := s.Store.GetSubmission(ctx, subID)
	if err != nil {
		return nil, nil, store.ErrNotFound
	}
	roster, err := s.Store.GetRosterEntry(ctx, sub.RosterEntryID)
	if err != nil || actor.ID == "" || roster.Host != actor.Host || roster.HostUserID != actor.ID || roster.HostUsername != actor.Username {
		return nil, nil, store.ErrNotFound
	}
	a, err := s.Store.GetAssignment(ctx, sub.AssignmentID)
	if err != nil {
		return nil, nil, store.ErrNotFound
	}
	if s.Classrooms != nil && !s.Classrooms[a.ClassroomID] {
		return nil, nil, ErrUnavailable
	}
	if write && (roster.Status != store.RosterActive || sub.Status != "active" || sub.Repo.Name == "" || sub.Repo.Host != actor.Host || (a.Deadline != nil && !s.now().Before(*a.Deadline))) {
		return nil, nil, ErrUnavailable
	}
	return sub, a, nil
}
func receipt(h *store.SubmissionHandoff) Receipt {
	var d document
	_ = json.Unmarshal(h.Document, &d)
	changes := d.Changes
	if changes == nil {
		changes = []Change{}
	}
	return Receipt{ID: h.ID, SubmissionID: h.SubmissionID, Status: h.Status, Branch: d.Request.Branch, BaseRevision: d.Request.BaseRevision, Changes: changes, CreatedAt: time.Unix(h.CreatedAt, 0).UTC(), ExpiresAt: time.Unix(h.ExpiresAt, 0).UTC(), CommitRevision: d.Candidate, Message: d.Message}
}
func (s *Service) Preview(ctx context.Context, actor Actor, subID, key string, files []File) (Receipt, error) {
	sub, a, err := s.authorize(ctx, subID, actor, true)
	if err != nil {
		return Receipt{}, err
	}
	if s.Writer == nil || !safeKey.MatchString(key) || ValidateFiles(files, a.GradingSpec) != nil {
		return Receipt{}, ErrInvalid
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	payload := digest(files)
	existing, err := s.Store.ListSubmissionHandoffs(ctx, subID)
	if err != nil {
		return Receipt{}, err
	}
	pending := 0
	for _, h := range existing {
		if h.OwnerID == actor.ID && h.IdempotencyKey == key {
			if h.PayloadDigest != payload {
				return Receipt{}, store.ErrConflict
			}
			if h.Status == "expired" || (h.Status == "preview" && h.ExpiresAt <= s.now().Unix()) {
				return Receipt{}, ErrExpired
			}
			return receipt(h), nil
		}
		if h.Status == "queued" || h.Status == "publishing" || h.Status == "verification_needed" || (h.Status == "preview" && h.ExpiresAt > s.now().Unix()) {
			pending++
		}
	}
	if pending >= MaxPending {
		return Receipt{}, store.ErrConflict
	}
	s.previewOnce.Do(func() { s.previewSlots = make(chan struct{}, 2) })
	select {
	case s.previewSlots <- struct{}{}:
		defer func() { <-s.previewSlots }()
	default:
		return Receipt{}, ErrUnavailable
	}
	snapshot, err := s.Writer.Inspect(ctx, sub.Repo, files)
	if err != nil {
		return Receipt{}, ErrUnavailable
	}
	if !strings.HasPrefix(snapshot.Branch, "refs/heads/") || !commitID.MatchString(snapshot.BaseRevision) || len(snapshot.Changes) != len(files) {
		return Receipt{}, ErrUnavailable
	}
	// Verify the preview binds every selected byte, rather than trusting writer annotations.
	changes := map[string]Change{}
	for _, c := range snapshot.Changes {
		if _, ok := changes[c.Path]; ok {
			return Receipt{}, ErrUnavailable
		}
		changes[c.Path] = c
	}
	for _, f := range files {
		h := sha256.Sum256(f.Content)
		c, ok := changes[f.Path]
		if !ok || c.SHA256 != hex.EncodeToString(h[:]) || c.Bytes != len(f.Content) || (c.Action != "create" && c.Action != "replace") {
			return Receipt{}, ErrUnavailable
		}
	}
	if _, _, err = s.authorize(ctx, subID, actor, true); err != nil {
		return Receipt{}, err
	}
	now := s.now()
	rid := id.New()
	d := document{ObservedRevision: sub.LatestCommit, Request: WriteRequest{Repo: sub.Repo, Branch: snapshot.Branch, BaseRevision: snapshot.BaseRevision, Files: files, ReceiptID: rid, CreatedAt: now}, Changes: snapshot.Changes}
	raw, _ := json.Marshal(d)
	h := &store.SubmissionHandoff{ID: rid, SubmissionID: subID, OwnerID: actor.ID, IdempotencyKey: key, PayloadDigest: payload, Status: "preview", CreatedAt: now.Unix(), ExpiresAt: now.Add(15 * time.Minute).Unix(), Version: 1, Document: raw}
	if err = s.Store.CreateSubmissionHandoff(ctx, h, MaxPending); err != nil {
		if errors.Is(err, store.ErrConflict) {
			rows, e := s.Store.ListSubmissionHandoffs(ctx, subID)
			if e == nil {
				for _, old := range rows {
					if old.OwnerID == actor.ID && old.IdempotencyKey == key && old.PayloadDigest == payload {
						return receipt(old), nil
					}
				}
			}
		}
		return Receipt{}, err
	}
	return receipt(h), nil
}
func (s *Service) Get(ctx context.Context, actor Actor, subID, rid string) (Receipt, error) {
	if _, _, err := s.authorize(ctx, subID, actor, false); err != nil {
		return Receipt{}, err
	}
	h, err := s.Store.GetSubmissionHandoff(ctx, rid)
	if err != nil || h.SubmissionID != subID || h.OwnerID != actor.ID {
		return Receipt{}, store.ErrNotFound
	}
	r := receipt(h)
	if r.Status == "preview" && h.ExpiresAt <= s.now().Unix() {
		r.Status = "expired"
	}
	return r, nil
}
func (s *Service) List(ctx context.Context, actor Actor, subID string) ([]Receipt, error) {
	if _, _, err := s.authorize(ctx, subID, actor, false); err != nil {
		return nil, err
	}
	rows, err := s.Store.ListSubmissionHandoffs(ctx, subID)
	if err != nil {
		return nil, err
	}
	out := []Receipt{}
	for _, h := range rows {
		if h.OwnerID == actor.ID {
			r := receipt(h)
			if h.Status == "preview" && h.ExpiresAt <= s.now().Unix() {
				r.Status = "expired"
			}
			out = append(out, r)
		}
	}
	return out, nil
}
func (s *Service) Confirm(ctx context.Context, actor Actor, subID, rid string) (Receipt, error) {
	sub, _, err := s.authorize(ctx, subID, actor, true)
	if err != nil {
		return Receipt{}, err
	}
	h, err := s.Store.GetSubmissionHandoff(ctx, rid)
	if err != nil || h.SubmissionID != subID || h.OwnerID != actor.ID {
		return Receipt{}, store.ErrNotFound
	}
	if h.Status != "preview" {
		if h.Status == "expired" {
			return Receipt{}, ErrExpired
		}
		return receipt(h), nil
	}
	if h.ExpiresAt <= s.now().Unix() {
		return Receipt{}, ErrExpired
	}
	var d document
	if json.Unmarshal(h.Document, &d) != nil || d.Request.Repo != sub.Repo {
		return Receipt{}, store.ErrConflict
	}
	h.Status = "queued"
	j := &store.ProvisioningJob{ID: id.New(), Type: JobType, TargetRef: h.ID, Status: store.JobPending, IdempotencyKey: "handoff:" + h.ID, ScheduledAt: s.now()}
	if err = s.Store.TransitionSubmissionHandoff(ctx, h, h.Version, j); err != nil {
		if errors.Is(err, store.ErrConflict) {
			return s.Get(ctx, actor, subID, rid)
		}
		return Receipt{}, err
	}
	return receipt(h), nil
}
func (s *Service) Process(ctx context.Context, rid string) error {
	h, err := s.Store.GetSubmissionHandoff(ctx, rid)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if h.Status != "queued" && h.Status != "publishing" {
		return nil
	}
	var d document
	if json.Unmarshal(h.Document, &d) != nil {
		return errors.New("invalid stored handoff")
	}
	finish := func(state, message string, j *store.ProvisioningJob) error {
		h.Status = state
		d.Message = message
		if state != "verification_needed" {
			d.Request.Files = nil
		}
		h.Document, _ = json.Marshal(d)
		return s.Store.TransitionSubmissionHandoff(ctx, h, h.Version, j)
	}
	authorized := func() error {
		sub, err := s.Store.GetSubmission(ctx, h.SubmissionID)
		if err != nil {
			return ErrUnavailable
		}
		r, err := s.Store.GetRosterEntry(ctx, sub.RosterEntryID)
		if err != nil {
			return ErrUnavailable
		}
		_, a, err := s.authorize(ctx, h.SubmissionID, Actor{Host: r.Host, Username: r.HostUsername, ID: h.OwnerID}, true)
		if err != nil || sub.Repo != d.Request.Repo {
			return ErrUnavailable
		}
		return ValidateFiles(d.Request.Files, a.GradingSpec)
	}
	if err = authorized(); err != nil {
		if d.Attempted {
			return finish("verification_needed", "Assignment access changed after a possible publication; instructor reconciliation is required.", nil)
		}
		return finish("refused", "Assignment access or policy changed; no new submission write was authorized.", nil)
	}
	if h.Status == "queued" {
		candidate, err := s.Writer.Prepare(ctx, d.Request)
		if err != nil || !commitID.MatchString(candidate) {
			return finish("failed", "Could not prepare the immutable commit; preview again or contact the instructor.", nil)
		}
		d.Candidate = candidate
		h.Status = "publishing"
		h.Document, _ = json.Marshal(d)
		if err = s.Store.TransitionSubmissionHandoff(ctx, h, h.Version, nil); err != nil {
			return err
		}
		h.Version++
	}
	if err = authorized(); err != nil {
		if d.Attempted {
			return finish("verification_needed", "Assignment access changed after a possible publication; instructor reconciliation is required.", nil)
		}
		return finish("refused", "Assignment access or policy changed before publication.", nil)
	}
	request := d.Request
	request.Retry = d.Attempted
	d.Attempted = true
	h.Document, _ = json.Marshal(d)
	if err = s.Store.TransitionSubmissionHandoff(ctx, h, h.Version, nil); err != nil {
		return err
	}
	h.Version++
	outcome, err := s.Writer.Publish(ctx, request, d.Candidate)
	if err != nil || outcome == VerificationNeeded {
		return finish("verification_needed", "The remote outcome could not be verified. Do not submit again; instructor reconciliation is required.", nil)
	}
	if outcome == Conflict {
		return finish("conflict", "The repository changed since your preview. Review the latest files before making a new submission.", nil)
	}
	if outcome != Committed {
		return finish("verification_needed", "The remote outcome could not be verified; instructor reconciliation is required.", nil)
	}
	var job *store.ProvisioningJob
	if s.GraderConfigured {
		target, _ := json.Marshal(map[string]string{"submission_id": h.SubmissionID, "revision": d.Candidate})
		job = &store.ProvisioningJob{ID: id.New(), Type: "grade_revision", TargetRef: string(target), Status: store.JobPending, IdempotencyKey: "grade:webhook:" + h.SubmissionID + ":" + d.Candidate, ScheduledAt: s.now()}
	}
	return finish("committed", "Repository commit verified. Grading and instructor review are separate steps.", job)
}

// RunCleanup bounds retention of unconfirmed coursework even when learners leave.
func (s *Service) RunCleanup(ctx context.Context) {
	timer := time.NewTicker(time.Minute)
	defer timer.Stop()
	for {
		_ = s.Store.ExpireSubmissionHandoffs(ctx, s.now().Unix())
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
	}
}

func (s *Service) Exhausted(ctx context.Context, rid string) error {
	h, err := s.Store.GetSubmissionHandoff(ctx, rid)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if h.Status != "queued" && h.Status != "publishing" {
		return nil
	}
	var d document
	if json.Unmarshal(h.Document, &d) != nil {
		return errors.New("invalid stored handoff")
	}
	if d.Attempted {
		h.Status = "verification_needed"
		d.Message = "Submission processing stopped after an uncertain publication. Instructor reconciliation is required."
	} else {
		h.Status = "failed"
		d.Request.Files = nil
		d.Message = "Submission processing stopped before publication. Your local files are unchanged."
	}
	h.Document, _ = json.Marshal(d)
	return s.Store.TransitionSubmissionHandoff(ctx, h, h.Version, nil)
}
