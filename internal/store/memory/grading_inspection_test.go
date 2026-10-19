// SPDX-License-Identifier: AGPL-3.0-or-later
package memory

import (
	"context"
	"errors"
	"github.com/EduCloud-Ecosystem/cairn/internal/store"
	"testing"
)

func TestInspectionCancelledWhileWaitingForReadLock(t *testing.T) {
	s := New()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	observed := &observedContext{Context: ctx, first: make(chan struct{})}
	s.mu.Lock()
	done := make(chan error, 1)
	go func() {
		_, err := s.ReadGradingInspection(observed, store.GradingInspectionQuery{Limit: 1})
		done <- err
	}()
	<-observed.first
	cancel()
	s.mu.Unlock()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled blocked reader proceeded", err)
	}
}
