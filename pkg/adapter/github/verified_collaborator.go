// SPDX-License-Identifier: Apache-2.0

package github

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/EduCloud-Ecosystem/cairn/pkg/adapter"
)

var _ adapter.VerifiedCollaborator = (*Adapter)(nil)

func (a *Adapter) verifyCollaboratorIdentity(ctx context.Context, username, hostUserID string) error {
	var user struct {
		ID    int64  `json:"id"`
		Login string `json:"login"`
	}
	if username == "" || hostUserID == "" {
		return adapter.ErrIdentityMismatch
	}
	if err := a.do(ctx, http.MethodGet, "/users/"+url.PathEscape(username), nil, &user, http.StatusOK); err != nil {
		return err
	}
	if user.ID <= 0 || strconv.FormatInt(user.ID, 10) != hostUserID || !strings.EqualFold(user.Login, username) {
		return adapter.ErrIdentityMismatch
	}
	return nil
}

// SetVerifiedCollaborator checks the mutable login immediately around GitHub's
// username-only grant. This detects stale queued work but cannot provide an
// atomic provider-side identity condition. A failed post-check triggers bounded
// cleanup, including cancellation of a returned invitation, and never succeeds.
func (a *Adapter) SetVerifiedCollaborator(ctx context.Context, repo adapter.RepoRef, username, hostUserID string, role adapter.Role) error {
	if err := a.verifyCollaboratorIdentity(ctx, username, hostUserID); err != nil {
		return err
	}
	permission := map[adapter.Role]string{adapter.RoleRead: "pull", adapter.RoleWrite: "push", adapter.RoleAdmin: "admin"}[role]
	if permission == "" {
		return errors.New("github: invalid collaborator role")
	}
	var invitation struct {
		ID      int64 `json:"id"`
		Invitee struct {
			ID    int64  `json:"id"`
			Login string `json:"login"`
		} `json:"invitee"`
	}
	path := "/repos/" + repo.Namespace + "/" + repo.Name
	grantErr := a.do(ctx, http.MethodPut, path+"/collaborators/"+url.PathEscape(username), map[string]string{"permission": permission}, &invitation, http.StatusCreated, http.StatusNoContent)
	verifyErr := a.verifyCollaboratorIdentity(ctx, username, hostUserID)
	if invitation.ID != 0 && (invitation.Invitee.ID <= 0 || strconv.FormatInt(invitation.Invitee.ID, 10) != hostUserID || !strings.EqualFold(invitation.Invitee.Login, username)) {
		verifyErr = errors.Join(verifyErr, adapter.ErrIdentityMismatch)
	}
	if verifyErr == nil {
		return grantErr
	}
	// Cancellation of the caller must not prevent a rollback attempt.
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	var cleanupErr error
	if invitation.ID > 0 {
		if err := a.do(cleanup, http.MethodDelete, path+"/invitations/"+strconv.FormatInt(invitation.ID, 10), nil, nil, http.StatusNoContent, http.StatusNotFound); err != nil {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("cancel invitation: %w", err))
		}
	}
	if err := a.do(cleanup, http.MethodDelete, path+"/collaborators/"+url.PathEscape(username), nil, nil, http.StatusNoContent, http.StatusNotFound); err != nil {
		cleanupErr = errors.Join(cleanupErr, fmt.Errorf("revoke collaborator: %w", err))
	}
	if cleanupErr != nil {
		cleanupErr = fmt.Errorf("identity reconciliation required: %w", cleanupErr)
	}
	return errors.Join(grantErr, fmt.Errorf("verify collaborator after grant: %w", verifyErr), cleanupErr)
}
