// SPDX-License-Identifier: AGPL-3.0-or-later
package grading

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"time"

	"github.com/EduCloud-Ecosystem/cairn/internal/id"
	"github.com/EduCloud-Ecosystem/cairn/pkg/gradingspec"
)

const (
	workspaceMountOptions = "size=512m,nr_inodes=65536,mode=1777"
	workspaceHoldCommand  = "while :; do sleep 60; done"
	workspaceCopyCommand  = "cp -R /cairn-source/. /work/"
)

// prepareWorkspace never gives student code a writable host bind. A local tmpfs
// volume has byte/inode bounds, but is emptied when its final mount disappears.
// The keeper holds that mount across the fresh containers used for each step.
func (r *ContainerRunner) prepareWorkspace(ctx context.Context, cr commandRunner, rt, image, source string, tier IsolationTier) (string, func() error, error) {
	if !filepath.IsAbs(source) || strings.ContainsAny(source, ",\n\r:") {
		return "", nil, errors.New("invalid source checkout directory")
	}
	volume := "cairn-work-" + id.New()
	keeper := "cairn-work-keeper-" + id.New()
	keeperAttempted := false
	cleanup := func() error {
		// Cleanup must still reach the daemon after cancellation of grading.
		cleanCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		var failures []error
		if keeperAttempted {
			out, err := cr.run(cleanCtx, rt, []string{"rm", "--force", keeper}, 10*time.Second)
			if err != nil || out.timedOut || out.exitCode != 0 {
				failures = append(failures, errors.New("could not remove grading workspace keeper"))
			}
		}
		out, err := cr.run(cleanCtx, rt, []string{"volume", "rm", "--force", volume}, 10*time.Second)
		if err != nil || out.timedOut || out.exitCode != 0 {
			failures = append(failures, errors.New("could not remove bounded grading workspace"))
		}
		return errors.Join(failures...)
	}
	fail := func(cause error) (string, func() error, error) {
		return "", nil, errors.Join(cause, cleanup())
	}
	create := []string{"volume", "create", "--driver", "local", "--opt", "type=tmpfs", "--opt", "device=tmpfs", "--opt", "o=" + workspaceMountOptions, volume}
	out, err := cr.run(ctx, rt, create, 15*time.Second)
	if err != nil || out.timedOut || out.exitCode != 0 {
		return fail(errors.New("runtime cannot create bounded tmpfs grading workspace"))
	}
	// The keeper executes no student code, has no host bind or policy mount, and
	// needs only a shell and sleep from the same operator-selected grading image.
	holder := *r
	holder.ExtraArgs = []string{"--pull=never"}
	lim := resolvedLimits{timeout: 15 * time.Second, memoryMB: 64, cpus: 0.1, pids: 8, network: gradingspec.NetworkNone}
	args := holder.buildRunArgs(image, workspaceHoldCommand, lim, volume, keeper, tier)
	args = append(args[:1], append([]string{"--detach"}, args[1:]...)...)
	keeperAttempted = true
	out, err = cr.run(ctx, rt, args, lim.timeout)
	if err != nil || out.timedOut || out.exitCode != 0 {
		return fail(errors.New("runtime cannot mount bounded tmpfs grading workspace"))
	}
	initializer := *r
	initializer.ExtraArgs = append(append([]string(nil), r.ExtraArgs...), "--mount", "type=bind,source="+source+",target=/cairn-source,readonly")
	lim = r.resolveLimits(gradingspec.Spec{}, nil)
	lim.network = gradingspec.NetworkNone
	out, err = initializer.exec1(ctx, cr, rt, image, workspaceCopyCommand, lim, volume, tier)
	if err != nil || out.timedOut || out.exitCode != 0 {
		return fail(errors.New("could not initialize bounded grading workspace (512 MiB, 65536 inodes): source too large, unreadable, or runtime unavailable"))
	}
	return volume, cleanup, nil
}
