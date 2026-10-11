// SPDX-License-Identifier: Apache-2.0
package forgejo

import "context"

// VerifyIdentity checks a current username/immutable-ID pair without granting access.
func (a *Adapter) VerifyIdentity(ctx context.Context, username, id string) error {
	return a.verifyCollaboratorIdentity(ctx, username, id)
}

// IdentityAuthority identifies the configured provider instance for plan binding.
func (a *Adapter) IdentityAuthority() string { return a.baseURL }
