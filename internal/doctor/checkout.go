// SPDX-License-Identifier: AGPL-3.0-or-later
package doctor

import (
	"context"
	"strings"
	"time"
)

type CheckoutConfig struct {
	Enabled, Unsafe                       bool
	Runtime, Image, Timeout, TotalTimeout string
}

// CheckCheckout also applies when assessment review is enabled without a grader.
// Image inspection does not pull an image or claim a successful acquisition.
func CheckCheckout(ctx context.Context, cfg CheckoutConfig, cmd Commander) []Result {
	if !cfg.Enabled {
		return nil
	}
	const name = "repository acquisition"
	var results []Result
	for key, value := range map[string]string{"CAIRN_CHECKOUT_TIMEOUT": cfg.Timeout, "CAIRN_GRADER_TOTAL_TIMEOUT": cfg.TotalTimeout} {
		duration, err := time.ParseDuration(value)
		if err != nil || duration <= 0 {
			results = append(results, failf(name, "set "+key+" to a positive duration", "%s is invalid", key))
		}
	}
	if len(results) > 0 {
		return results
	}
	if cfg.Unsafe {
		return []Result{warnf(name, "use container grading or assessment mode for untrusted repositories", "host Git acquisition has no hard disk quota in local-exec-unsafe mode")}
	}
	rt := cfg.Runtime
	if rt == "" {
		rt = "docker"
	}
	if out, err := cmd.Run(ctx, rt, "version", "--format", "{{.Server.Version}}"); err != nil {
		return []Result{failf(name, runtimeFix(rt, out, err), "bounded checkout runtime unavailable: %v", err)}
	}
	if cfg.Image == "" || strings.HasPrefix(cfg.Image, "-") {
		return []Result{failf(name, "set CAIRN_CHECKOUT_IMAGE to a preloaded acquisition image", "acquisition image is invalid")}
	}
	if _, err := cmd.Run(ctx, rt, "image", "inspect", cfg.Image); err != nil {
		return []Result{failf(name, "build Dockerfile.checkout as cairn-checkout:local, or preload CAIRN_CHECKOUT_IMAGE on the worker daemon", "required acquisition image %q is unavailable; jobs do not pull images", cfg.Image)}
	}
	return []Result{okf(name, "runtime and acquisition image %q available; execution/512 MiB tmpfs enforcement require the acquisition integration test", cfg.Image)}
}
