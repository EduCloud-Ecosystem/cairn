// SPDX-License-Identifier: Apache-2.0

package forgejo

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

// SetVerifiedCollaborator rejects stale queued usernames before granting access
// and checks again afterward. Forgejo/Gitea's username-only endpoint has no atomic
// identity precondition; failed post-checks require revocation and reconciliation.
func (a *Adapter) SetVerifiedCollaborator(ctx context.Context, repo adapter.RepoRef, username, hostUserID string, role adapter.Role) error {
	if err := a.verifyCollaboratorIdentity(ctx, username, hostUserID); err != nil {
		return err
	}
	grantErr := a.SetCollaborator(ctx, repo, username, role)
	verifyErr := a.verifyCollaboratorIdentity(ctx, username, hostUserID)
	if verifyErr == nil {
		return grantErr
	}
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	err := a.do(cleanup, http.MethodDelete, "/repos/"+repo.Namespace+"/"+repo.Name+"/collaborators/"+url.PathEscape(username), nil, nil, http.StatusNoContent, http.StatusNotFound)
	if err != nil {
		err = fmt.Errorf("identity reconciliation required; revoke collaborator: %w", err)
	}
	return errors.Join(grantErr, fmt.Errorf("verify collaborator after grant: %w", verifyErr), err)
}
