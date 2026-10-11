// SPDX-License-Identifier: Apache-2.0
package github

import (
	"context"
	"errors"
	"fmt"
	"github.com/EduCloud-Ecosystem/cairn/pkg/adapter"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestReconciliationIdentityLookupIsReadOnly(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != http.MethodGet {
			t.Errorf("unexpected mutation %s", r.Method)
			w.WriteHeader(500)
			return
		}
		fmt.Fprint(w, `{"id":11,"login":"alice"}`)
	}))
	defer srv.Close()
	a := &Adapter{httpc: srv.Client(), baseURL: srv.URL}
	if err := a.VerifyIdentity(context.Background(), "alice", "11"); err != nil {
		t.Fatal(err)
	}
	if err := a.VerifyIdentity(context.Background(), "alice", "22"); !errors.Is(err, adapter.ErrIdentityMismatch) {
		t.Fatalf("recycled username accepted: %v", err)
	}
	if calls != 2 {
		t.Fatalf("calls %d", calls)
	}
}
