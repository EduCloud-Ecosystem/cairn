// SPDX-License-Identifier: AGPL-3.0-or-later
// Operator-only finite enrollment exporter. No network or identity-provider calls.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"golang.org/x/sys/unix"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/EduCloud-Ecosystem/cairn/internal/enrollment"
	"github.com/EduCloud-Ecosystem/cairn/internal/store/sqlite"
)

func run(args []string) error {
	fs := flag.NewFlagSet("enrollment-export", flag.ContinueOnError)
	config := fs.String("config", "", "private reviewed bindings JSON")
	database := fs.String("database", "", "existing authoritative Cairn SQLite file (read-only)")
	journal := fs.String("journal", "", "private durable issuance journal")
	output := fs.String("output", "", "private authorization snapshot destination")
	init := fs.String("init-authority", "", "initialize journal once with this 32-hex authority ID")
	inspect := fs.String("inspect-assignment", "", "print only the digest of this assignment for operator review")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("unexpected argument")
	}
	if *init != "" {
		if *journal == "" || *database != "" || *config != "" || *output != "" || *inspect != "" {
			return errors.New("initialization requires only journal and authority")
		}
		return enrollment.Initialize(*journal, *init)
	}
	if *database == "" {
		return errors.New("existing database required")
	}
	path, err := filepath.EvalSymlinks(*database)
	if err != nil {
		return err
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return err
	}
	st, err := sqlite.OpenReadOnly(path)
	if err != nil {
		return errors.New("source unavailable")
	}
	defer st.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if *inspect != "" {
		a, err := st.GetAssignment(ctx, *inspect)
		if err != nil {
			return errors.New("assignment unavailable")
		}
		fmt.Println(enrollment.AssignmentDigest(a))
		return nil
	}
	if *config == "" || *journal == "" || *output == "" {
		return errors.New("config, journal and output required")
	}
	// Prevent replacing the authority database, journal or operator input.
	for _, protected := range []string{path, *journal, *config} {
		p, err := filepath.EvalSymlinks(protected)
		if err != nil {
			return errors.New("protected input unavailable")
		}
		p, _ = filepath.Abs(p)
		dir, err := filepath.EvalSymlinks(filepath.Dir(*output))
		if err != nil {
			return errors.New("output parent unavailable")
		}
		o, _ := filepath.Abs(filepath.Join(dir, filepath.Base(*output)))
		if p == o {
			return errors.New("output overlaps protected input")
		}
	}
	f, err := os.OpenFile(*config, os.O_RDONLY|unix.O_NONBLOCK|unix.O_NOFOLLOW, 0)
	if err != nil {
		return errors.New("configuration unavailable")
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return errors.New("bounded regular configuration required")
	}
	dec := json.NewDecoder(io.LimitReader(f, 1<<20))
	dec.DisallowUnknownFields()
	var c enrollment.Config
	if dec.Decode(&c) != nil || dec.Decode(new(any)) != io.EOF {
		return errors.New("invalid configuration")
	}
	snapshot, err := enrollment.Export(ctx, st, *journal, path, c, time.Now())
	if err != nil {
		return errors.New("enrollment export refused; inspect source and journal locally")
	}
	return enrollment.WritePrivate(*output, snapshot)
}
func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
