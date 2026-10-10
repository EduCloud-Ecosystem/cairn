// SPDX-License-Identifier: AGPL-3.0-or-later
package assessment

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/EduCloud-Ecosystem/cairn/internal/grading"
	"github.com/EduCloud-Ecosystem/cairn/internal/id"
	"github.com/EduCloud-Ecosystem/cairn/internal/store"
)

type CalibrationBinding struct {
	ID       string `json:"id"`
	OwnerID  string `json:"owner_id"`
	Revision int    `json:"revision"`
	Digest   string `json:"digest"`
	Guidance string `json:"guidance"`
}
type CalibrationExample struct {
	Reference          *Review           `json:"reference,omitempty"`
	SampleID           string            `json:"sample_id,omitempty"`
	ExclusionNote      string            `json:"exclusion_note,omitempty"`
	SourceSubmissionID string            `json:"source_submission_id,omitempty"`
	ID                 string            `json:"id"`
	Document           Document          `json:"document"`
	Review             *Review           `json:"review,omitempty"`
	Generation         *store.Generation `json:"generation,omitempty"`
}

func (s Service) CaptureCalibrationExample(ctx context.Context, cid, owner, submissionID, commit string, revision int) (*store.Calibration, error) {
	c, d, err := s.calibration(ctx, cid, owner)
	if err != nil {
		return nil, err
	}
	if c.Status != "draft" || c.Revision != revision || d.BundleDigest != "" {
		return nil, store.ErrConflict
	}
	if len(d.Examples) >= 9 {
		return nil, errors.New("use at most nine historical submissions per calibration")
	}
	if s.Checkout == nil {
		return nil, errors.New("repository capture is not configured")
	}
	sub, err := s.Store.GetSubmission(ctx, submissionID)
	if err != nil {
		return nil, err
	}
	if commit == "" {
		commit = sub.LatestCommit
	}
	if !grading.ValidPolicyRevision(commit) {
		return nil, errors.New("select a submission with a recorded commit or provide its full commit ID")
	}
	dir, err := os.MkdirTemp("", "cairn-calibration-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err = s.Checkout.FetchRevision(ctx, sub.Repo, commit, dir); err != nil {
		return nil, errors.New("could not capture historical submission commit")
	}
	evidence := Document{Calibration: d.BasedOn, CreatedAt: time.Now().UTC(), CapturedBy: owner, PolicyVersion: PolicyVersion, Revision: commit, Rubric: d.Rubric, Artifacts: Extract(dir, d.Rubric.Paths)}
	for _, a := range evidence.Artifacts {
		if a.Issue != "" {
			return nil, fmt.Errorf("historical source cannot be extracted: %s", a.Issue)
		}
	}
	evidence.InputDigest = InputDigest(evidence)
	d.Examples = append(d.Examples, CalibrationExample{ID: id.New(), SourceSubmissionID: submissionID, Document: evidence})
	c.SourceSubmissionID = submissionID
	return s.saveCalibration(ctx, c, d)
}

type CalibrationDocument struct {
	Section       bool                 `json:"section,omitempty"`
	BundleDigest  string               `json:"bundle_digest,omitempty"`
	SamplePurpose string               `json:"sample_purpose,omitempty"`
	BasedOn       *CalibrationBinding  `json:"based_on,omitempty"`
	Rubric        Rubric               `json:"rubric"`
	Model         string               `json:"model"`
	PromptVersion string               `json:"prompt_version"`
	PolicyVersion string               `json:"policy_version"`
	Guidance      string               `json:"guidance"`
	Examples      []CalibrationExample `json:"examples"`
	ApprovedAt    *time.Time           `json:"approved_at,omitempty"`
}

func (s Service) CreateCalibration(ctx context.Context, assignment, owner string) (*store.Calibration, error) {
	return s.CreateCalibrationFrom(ctx, assignment, owner, "")
}
func (s Service) CreateCalibrationFrom(ctx context.Context, assignment, owner, baseID string) (*store.Calibration, error) {
	return s.CreateCalibrationSection(ctx, assignment, owner, baseID, nil)
}

func (s Service) CreateCalibrationSection(ctx context.Context, assignment, owner, baseID string, section *Rubric) (*store.Calibration, error) {
	if owner == "" || owner == "local-development" {
		return nil, errors.New("sign in as an instructor to calibrate")
	}
	r, err := s.Store.GetAssessmentRubric(ctx, assignment)
	if err != nil {
		return nil, err
	}
	var rubric Rubric
	if err = json.Unmarshal(r.Document, &rubric); err != nil {
		return nil, err
	}
	if err = ValidateRubric(rubric); err != nil {
		return nil, err
	}
	if baseID != "" && section == nil {
		_, base, e := s.calibration(ctx, baseID, owner)
		if e != nil {
			return nil, e
		}
		if base.Section {
			section = &base.Rubric
		}
	}
	if section != nil {
		if err = validateSectionRubric(rubric, *section); err != nil {
			return nil, err
		}
		rubric = *section
	}
	d := CalibrationDocument{Section: section != nil, Rubric: rubric, Model: DefaultOpenAIModel, PromptVersion: PromptVersion, PolicyVersion: PolicyVersion, Examples: []CalibrationExample{}}
	if baseID != "" {
		d.BasedOn, err = s.calibrationBindingScoped(ctx, baseID, owner, assignment, r.Digest, Digest(d.Rubric))
		if err != nil {
			return nil, err
		}
		d.Guidance = d.BasedOn.Guidance
	}
	body, _ := json.Marshal(d)
	c := &store.Calibration{ID: id.New(), OwnerID: owner, AssignmentID: assignment, RubricDigest: r.Digest, Revision: 1, Status: "draft", Document: body}
	return c, s.Store.CreateCalibration(ctx, c)
}
func (s Service) calibration(ctx context.Context, cid, owner string) (*store.Calibration, CalibrationDocument, error) {
	c, err := s.Store.GetCalibration(ctx, cid, owner)
	if err != nil {
		return nil, CalibrationDocument{}, err
	}
	var d CalibrationDocument
	err = json.Unmarshal(c.Document, &d)
	return c, d, err
}
func (s Service) saveCalibration(ctx context.Context, c *store.Calibration, d CalibrationDocument) (*store.Calibration, error) {
	body, err := json.Marshal(d)
	if err != nil {
		return nil, err
	}
	if len(body) > 4<<20 {
		return nil, errors.New("calibration exceeds 4 MiB; use a smaller representative sample")
	}
	previous := c.Revision
	c.Revision++
	c.Document = body
	return c, s.Store.UpdateCalibration(ctx, c, previous)
}
func (s Service) AddCalibrationExample(ctx context.Context, cid, owner string, revision int, files map[string]string) (*store.Calibration, error) {
	c, d, err := s.calibration(ctx, cid, owner)
	if err != nil {
		return nil, err
	}
	if c.Status != "draft" || revision != c.Revision || d.BundleDigest != "" {
		return nil, store.ErrConflict
	}
	if len(d.Examples) >= 9 {
		return nil, errors.New("use at most nine representative historical submissions per calibration")
	}
	evidence, err := uploadedCalibrationDocument(d.Rubric, d.BasedOn, owner, files)
	if err != nil {
		return nil, err
	}
	d.Examples = append(d.Examples, CalibrationExample{ID: id.New(), Document: evidence})
	return s.saveCalibration(ctx, c, d)
}

func uploadedCalibrationDocument(rubric Rubric, binding *CalibrationBinding, owner string, files map[string]string) (Document, error) {
	if len(files) != len(rubric.Paths) {
		return Document{}, errors.New("provide exactly the rubric's required files")
	}
	dir, err := os.MkdirTemp("", "cairn-calibration-*")
	if err != nil {
		return Document{}, err
	}
	defer os.RemoveAll(dir)
	total := 0
	for _, p := range rubric.Paths {
		content, ok := files[p]
		if !ok || !ValidPath(p) {
			return Document{}, errors.New("required calibration file missing")
		}
		total += len(content)
		if len(content) > MaxFileBytes || total > MaxTotalBytes {
			return Document{}, errors.New("historical files exceed extraction limits")
		}
		dest := filepath.Join(dir, p)
		if err = os.MkdirAll(filepath.Dir(dest), 0700); err != nil {
			return Document{}, err
		}
		if err = os.WriteFile(dest, []byte(content), 0600); err != nil {
			return Document{}, err
		}
	}
	evidence := Document{Calibration: binding, CreatedAt: time.Now().UTC(), CapturedBy: owner, PolicyVersion: PolicyVersion, Revision: "historical-upload", Rubric: rubric, Artifacts: Extract(dir, rubric.Paths)}
	for _, a := range evidence.Artifacts {
		if a.Issue != "" {
			return Document{}, fmt.Errorf("historical source cannot be extracted: %s", a.Issue)
		}
	}
	evidence.InputDigest = InputDigest(evidence)
	return evidence, nil
}

func (s Service) ReviewCalibrationExample(ctx context.Context, cid, owner, eid string, revision int, criteria []Judgment, note string) (*store.Calibration, error) {
	c, d, err := s.calibration(ctx, cid, owner)
	if err != nil {
		return nil, err
	}
	if c.Status != "draft" || c.Revision != revision {
		return nil, store.ErrConflict
	}
	if strings.TrimSpace(note) == "" || len(note) > 4000 {
		return nil, errors.New("a review rationale is required")
	}
	for i := range d.Examples {
		e := &d.Examples[i]
		if e.ID != eid {
			continue
		}
		if e.Document.Proposal == nil {
			return nil, errors.New("generate feedback before reviewing this example")
		}
		// Unlike grade approval, a historical reference may legitimately remain null.
		if _, _, err = ValidateJudgments(e.Document, criteria, false); err != nil {
			return nil, err
		}
		e.Review = &Review{Reviewer: owner, ReviewedAt: time.Now().UTC(), Note: note, Criteria: criteria}
		return s.saveCalibration(ctx, c, d)
	}
	return nil, store.ErrNotFound
}
func (s Service) ApproveCalibration(ctx context.Context, cid, owner string, revision int, guidance string) (*store.Calibration, error) {
	c, d, err := s.calibration(ctx, cid, owner)
	if err != nil {
		return nil, err
	}
	if c.Status != "draft" || c.Revision != revision {
		return nil, store.ErrConflict
	}
	if len(d.Examples) == 0 || strings.TrimSpace(guidance) == "" || len(guidance) > 2000 {
		return nil, errors.New("review historical examples and provide up to 2000 bytes of reusable instructor guidance")
	}
	reviewed := 0
	for _, e := range d.Examples {
		if e.ExclusionNote != "" {
			continue
		}
		reviewed++
		if e.Review == nil || e.Document.Proposal == nil {
			return nil, errors.New("every example needs generated feedback and instructor review")
		}
	}
	if reviewed == 0 {
		return nil, errors.New("at least one generated example must be reviewed; excluded examples do not calibrate a profile")
	}
	r, err := s.Store.GetAssessmentRubric(ctx, c.AssignmentID)
	if err != nil {
		return nil, err
	}
	if r.Digest != c.RubricDigest || d.Model != DefaultOpenAIModel || d.PromptVersion != PromptVersion || d.PolicyVersion != PolicyVersion {
		return nil, errors.New("rubric or model configuration changed; start a fresh calibration")
	}
	checks := calibrationInputChecks(d, strings.TrimSpace(guidance))
	for _, check := range checks {
		if !check.Fits {
			return nil, errors.New("instructor guidance exceeds the input budget for an included example; run preflight before approval")
		}
	}
	d.Guidance = strings.TrimSpace(guidance)
	now := time.Now().UTC()
	d.ApprovedAt = &now
	c.Status = "ready"
	return s.saveCalibration(ctx, c, d)
}
func (s Service) CalibrationBinding(ctx context.Context, cid, owner, assignment, rubricDigest string) (*CalibrationBinding, error) {
	return s.calibrationBindingScoped(ctx, cid, owner, assignment, rubricDigest, "")
}
func (s Service) calibrationBindingScoped(ctx context.Context, cid, owner, assignment, rubricDigest, scopeDigest string) (*CalibrationBinding, error) {
	c, d, err := s.calibration(ctx, cid, owner)
	if err != nil {
		return nil, err
	}
	if (scopeDigest == "" && d.Section) || (scopeDigest != "" && Digest(d.Rubric) != scopeDigest) {
		return nil, errors.New("section calibration cannot be used outside its captured rubric scope")
	}
	if c.Status != "ready" || c.AssignmentID != assignment || c.RubricDigest != rubricDigest || d.Model != DefaultOpenAIModel || d.PromptVersion != PromptVersion || d.PolicyVersion != PolicyVersion {
		return nil, errors.New("calibration is not approved for this instructor, rubric and model configuration")
	}
	return &CalibrationBinding{ID: c.ID, OwnerID: owner, Revision: c.Revision, Digest: DigestBytes(c.Document), Guidance: d.Guidance}, nil
}

// Shared admission and single-flight with normal course assessment generation.
func (g *Generator) GenerateCalibration(ctx context.Context, cid, owner, eid string, revision int, inputDigest string) (*store.Calibration, error) {
	select {
	case g.slots <- struct{}{}:
		defer func() { <-g.slots }()
	default:
		return nil, ErrProviderBusy
	}
	c, d, err := g.Service.calibration(ctx, cid, owner)
	if err != nil {
		return nil, err
	}
	if c.Status != "draft" || c.Revision != revision {
		return nil, store.ErrConflict
	}
	a, err := g.Service.Store.GetAssignment(ctx, c.AssignmentID)
	if err != nil {
		return nil, err
	}
	if !g.Classrooms[a.ClassroomID] {
		return nil, errors.New("OpenAI has not been enabled for this classroom")
	}
	if d.Model != g.Provider.model || d.PromptVersion != PromptVersion || d.PolicyVersion != PolicyVersion {
		return nil, errors.New("model configuration changed; start a fresh calibration")
	}
	index := -1
	for i, e := range d.Examples {
		if e.ID == eid {
			index = i
			break
		}
	}
	if index < 0 {
		return nil, store.ErrNotFound
	}
	e := &d.Examples[index]
	if e.ExclusionNote != "" || e.Document.Proposal != nil || inputDigest != e.Document.InputDigest {
		return nil, store.ErrConflict
	}
	if d.Section && e.Reference == nil {
		return nil, errors.New("save an independent instructor reference before section generation")
	}
	current, err := g.Service.Store.GetAssessmentRubric(ctx, c.AssignmentID)
	if err != nil {
		return nil, err
	}
	if current.Digest != c.RubricDigest {
		return nil, errors.New("parent rubric changed; start a fresh calibration")
	}
	if err = g.validateCalibration(ctx, e.Document, c.AssignmentID, c.RubricDigest); err != nil {
		return nil, err
	}
	body, err := g.Provider.request(e.Document)
	if err != nil {
		return nil, err
	}
	attempt := &store.Generation{AssessmentID: "calibration:" + cid + ":" + eid, Day: time.Now().UTC().Format("2006-01-02"), ReservedUnits: len(body) + MaxProviderOutputTokens, Status: "running", Model: g.Provider.model}
	if err = g.Service.Store.ReserveCalibrationGeneration(ctx, c, attempt, g.Limits); err != nil {
		return nil, err
	}
	p, callErr := g.Provider.respond(ctx, body, e.Document)
	attempt.Status = "failed"
	attempt.ErrorCode = "provider_failure"
	if p.Usage != nil {
		attempt.InputTokens = p.Usage.InputTokens
		attempt.OutputTokens = p.Usage.OutputTokens
		attempt.ResponseID = p.Usage.ResponseID
	}
	var result *store.Calibration
	if callErr == nil {
		current, freshErr := g.Service.Store.GetAssessmentRubric(ctx, c.AssignmentID)
		if freshErr != nil {
			callErr = freshErr
		} else if current.Digest != c.RubricDigest {
			callErr = store.ErrConflict
		}
	}
	if callErr == nil {
		callErr = g.validateCalibration(ctx, e.Document, c.AssignmentID, c.RubricDigest)
	}
	if callErr == nil {
		e.Document.Proposal = &p
		result, callErr = g.Service.saveCalibration(ctx, c, d)
		if callErr == nil {
			attempt.Status = "succeeded"
			attempt.ErrorCode = ""
		} else {
			attempt.ErrorCode = "stale_or_unsaved"
		}
	} else {
		var failure providerFailure
		if errors.As(callErr, &failure) {
			attempt.ErrorCode = string(failure)
		}
	}
	auditCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err = g.Service.Store.FinishGeneration(auditCtx, attempt); err != nil {
		return nil, errors.New("calibration usage could not be finalized; refresh before further action")
	}
	return result, callErr
}

// Exclusion preserves the source, failure and rationale; it never refunds usage.
func (s Service) ExcludeCalibrationExample(ctx context.Context, cid, owner, eid string, revision int, note string) (*store.Calibration, error) {
	c, d, err := s.calibration(ctx, cid, owner)
	if err != nil {
		return nil, err
	}
	if c.Status != "draft" || c.Revision != revision {
		return nil, store.ErrConflict
	}
	if strings.TrimSpace(note) == "" || len(note) > 4000 {
		return nil, errors.New("an exclusion rationale is required")
	}
	for i, e := range d.Examples {
		if e.ID == eid {
			d.Examples[i].ExclusionNote = strings.TrimSpace(note)
			return s.saveCalibration(ctx, c, d)
		}
	}
	return nil, store.ErrNotFound
}
