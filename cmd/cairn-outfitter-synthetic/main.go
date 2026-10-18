// SPDX-License-Identifier: AGPL-3.0-or-later

// Disposable fixture entry point; never imported by cmd/cairn.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/EduCloud-Ecosystem/cairn/internal/grading"
	"github.com/EduCloud-Ecosystem/cairn/internal/syntheticoutfitter"
	"github.com/EduCloud-Ecosystem/cairn/pkg/gradingspec"
	"golang.org/x/sys/unix"
)

func read(path string, v any) error {
	f, err := os.OpenFile(path, os.O_RDONLY|unix.O_NONBLOCK|unix.O_NOFOLLOW, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() || st.Size() > 32768 {
		return errors.New("config and policy must be bounded regular files")
	}
	b, err := io.ReadAll(io.LimitReader(f, 32769))
	if err != nil || len(b) > 32768 {
		return errors.New("oversized or unreadable JSON")
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err = d.Decode(v); err != nil {
		return err
	}
	if d.Decode(new(any)) != io.EOF {
		return errors.New("trailing or oversized JSON")
	}
	return nil
}
func run() (*grading.Result, error) {
	config := flag.String("config", "", "explicit disposable fixture config")
	mode := flag.String("mode", "status", "run, status or cancel")
	input := flag.String("input", "", "Git-free pinned submission snapshot")
	policy := flag.String("policy", "", "separate Git-free pinned instructor snapshot")
	flag.Parse()
	if flag.NArg() != 0 {
		return nil, errors.New("unexpected arguments")
	}
	var c syntheticoutfitter.Config
	if err := read(*config, &c); err != nil {
		return nil, err
	}
	r, err := syntheticoutfitter.New(c)
	if err != nil {
		return nil, err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var result grading.Result
	switch *mode {
	case "run":
		var spec gradingspec.Spec
		if err = read(filepath.Join(*policy, c.Policy.Path), &spec); err != nil {
			return nil, err
		}
		result, err = r.RunWithPolicy(ctx, spec, *input, *policy)
	case "status":
		result, err = r.Recover(ctx)
	case "cancel":
		return nil, r.Cancel(ctx)
	default:
		return nil, errors.New("unsupported mode")
	}
	if err != nil {
		return nil, err
	}
	return &result, nil
}
func main() {
	r, err := run()
	out := struct {
		GradePublished bool            `json:"grade_published"`
		Result         *grading.Result `json:"result,omitempty"`
		Unassessable   bool            `json:"unassessable"`
		Error          string          `json:"error,omitempty"`
	}{Result: r, Unassessable: err != nil}
	if err != nil {
		out.Error = err.Error()
	}
	if e := json.NewEncoder(os.Stdout).Encode(out); e != nil {
		fmt.Fprintln(os.Stderr, "cannot encode result")
		os.Exit(2)
	}
	if err != nil {
		os.Exit(2)
	}
}
