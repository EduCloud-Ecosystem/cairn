// SPDX-License-Identifier: AGPL-3.0-or-later
package doctor

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestCheckCheckout(t *testing.T) {
	cfg := CheckoutConfig{Enabled: true, Runtime: "docker", Image: "cairn-checkout:local", Timeout: "2m", TotalTimeout: "15m"}
	cmd := &fakeCommander{version: "1"}
	results := CheckCheckout(context.Background(), cfg, cmd)
	if len(results) != 1 || results[0].Status != StatusOK || len(cmd.calls) != 2 || strings.Join(cmd.calls[1], " ") != "docker image inspect cairn-checkout:local" {
		t.Fatalf("results=%+v calls=%+v", results, cmd.calls)
	}
	cmd.runErr = errors.New("missing")
	if got := CheckCheckout(context.Background(), cfg, cmd); got[0].Status != StatusFail {
		t.Fatalf("missing image accepted: %+v", got)
	}
	cfg.Unsafe = true
	if got := CheckCheckout(context.Background(), cfg, cmd); got[0].Status != StatusWarn {
		t.Fatalf("unsafe mode not identified: %+v", got)
	}
	cfg.Timeout = "0"
	if got := CheckCheckout(context.Background(), cfg, cmd); got[0].Status != StatusFail {
		t.Fatalf("zero timeout accepted: %+v", got)
	}
	cfg.Enabled = false
	if got := CheckCheckout(context.Background(), cfg, cmd); len(got) != 0 {
		t.Fatalf("disabled: %+v", got)
	}
}
