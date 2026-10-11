// SPDX-License-Identifier: Apache-2.0
package gitlab

import (
	"context"
	"github.com/EduCloud-Ecosystem/cairn/pkg/adapter"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// VerifyIdentity checks a current username/immutable-ID pair without granting access.
func (a *Adapter) VerifyIdentity(ctx context.Context, username, id string) error {
	uid, err := strconv.ParseInt(id, 10, 64)
	if err != nil || uid <= 0 || username == "" {
		return adapter.ErrIdentityMismatch
	}
	var users []struct {
		ID       int64  `json:"id"`
		Username string `json:"username"`
	}
	if err = a.do(ctx, http.MethodGet, "/users?username="+url.QueryEscape(username), nil, &users, http.StatusOK); err != nil {
		return err
	}
	if len(users) != 1 || users[0].ID != uid || !strings.EqualFold(users[0].Username, username) {
		return adapter.ErrIdentityMismatch
	}
	return nil
}

// IdentityAuthority identifies the configured provider instance for plan binding.
func (a *Adapter) IdentityAuthority() string { return a.baseURL }
