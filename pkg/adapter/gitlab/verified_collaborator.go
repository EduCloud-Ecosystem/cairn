// SPDX-License-Identifier: Apache-2.0

package gitlab

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/EduCloud-Ecosystem/cairn/pkg/adapter"
)

var _ adapter.VerifiedCollaborator = (*Adapter)(nil)

// SetVerifiedCollaborator verifies the exact username/ID pair, then grants by
// numeric ID. A username reassignment after lookup cannot change the recipient.
func (a *Adapter) SetVerifiedCollaborator(ctx context.Context, repo adapter.RepoRef, username, hostUserID string, role adapter.Role) error {
	uid, err := strconv.ParseInt(hostUserID, 10, 64)
	if err != nil || uid <= 0 || username == "" {
		return adapter.ErrIdentityMismatch
	}
	var users []struct {
		ID       int64  `json:"id"`
		Username string `json:"username"`
	}
	if err := a.do(ctx, http.MethodGet, "/users?username="+url.QueryEscape(username), nil, &users, http.StatusOK); err != nil {
		return err
	}
	if len(users) != 1 || users[0].ID != uid || !strings.EqualFold(users[0].Username, username) {
		return adapter.ErrIdentityMismatch
	}
	return a.setCollaboratorID(ctx, repo, uid, role)
}
