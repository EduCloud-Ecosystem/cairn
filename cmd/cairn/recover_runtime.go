// SPDX-License-Identifier: AGPL-3.0-or-later
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/EduCloud-Ecosystem/cairn/internal/runtimeguard"
	"io"
	"os"
)

func runRecoverRuntime(args []string) error {
	fs := flag.NewFlagSet("recover-runtime", flag.ContinueOnError)
	root := fs.String("work-dir", os.TempDir(), "same TMPDIR used by the stopped Cairn workers")
	runtime := fs.String("runtime", "docker", "docker or podman")
	planPath := fs.String("plan", "", "write a new private dry-run plan to this path")
	applyPath := fs.String("apply", "", "apply exactly this reviewed plan after revalidation")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 || (*planPath != "" && *applyPath != "") {
		return errors.New("use --plan FILE or --apply FILE, without positional arguments")
	}
	lease, err := runtimeguard.Acquire(*root, true)
	if err != nil {
		return err
	}
	defer lease.Close()
	if *applyPath != "" {
		f, err := os.Open(*applyPath)
		if err != nil {
			return err
		}
		defer f.Close()
		info, err := f.Stat()
		if err != nil || !info.Mode().IsRegular() || info.Size() > 4<<20 {
			return errors.New("invalid recovery plan file")
		}
		var p runtimeguard.Plan
		d := json.NewDecoder(f)
		d.DisallowUnknownFields()
		if err = d.Decode(&p); err != nil {
			return err
		}
		if err = d.Decode(&struct{}{}); err != io.EOF {
			return errors.New("recovery plan must contain exactly one JSON document")
		}
		if p.Runtime != *runtime {
			return errors.New("--runtime must match plan runtime")
		}
		if err = runtimeguard.Apply(context.Background(), lease, p, runtimeguard.Execute); err != nil {
			return err
		}
		fmt.Println("Reviewed runtime recovery completed.")
		return nil
	}
	p, err := runtimeguard.Inventory(context.Background(), lease, *runtime, runtimeguard.Execute)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if *planPath == "" {
		_, err = os.Stdout.Write(data)
		return err
	}
	f, err := os.OpenFile(*planPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	ce := f.Close()
	return errors.Join(err, ce)
}
