// SPDX-License-Identifier: AGPL-3.0-or-later
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/EduCloud-Ecosystem/cairn/internal/assessment"
)

func runCalibrationReviewPacket(args []string) error {
	fs := flag.NewFlagSet("calibration-review-packet", flag.ContinueOnError)
	bundle := fs.String("bundle", "", "reviewed source-only bundle")
	sample := fs.String("sample", "", "sample ID within the bundle")
	execution := fs.String("execution", "", "optional completed calibration-execute report")
	judgments := fs.String("judgments", "", "optional JSON array of attributed judgments")
	author := fs.String("author", "", "claimed author label, distinct from importing instructor")
	authorship := fs.String("authorship", "execution-only", "execution-only, assistant, instructor-assisted, or external-reviewer")
	note := fs.String("note", "", "rationale and scope of this supporting material")
	out := fs.String("out", "", "new private output directory")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 0 || *bundle == "" || *sample == "" || *out == "" {
		return errors.New("provide --bundle, --sample and --out")
	}
	read := func(path string, target any) error {
		raw, err := readEvaluationFile(path)
		if err != nil {
			return err
		}
		return strictCheckJSON(raw, target)
	}
	var b assessment.CalibrationBundle
	if err := read(*bundle, &b); err != nil {
		return err
	}
	p := assessment.CalibrationReviewPacket{SampleID: *sample, Authorship: *authorship, Author: *author, Note: *note}
	if *judgments != "" {
		if err := read(*judgments, &p.Judgments); err != nil {
			return err
		}
	}
	if *execution != "" {
		p.Execution = &assessment.CalibrationExecutionReport{}
		if err := read(*execution, p.Execution); err != nil {
			return err
		}
	}
	p, err := assessment.PrepareCalibrationReviewPacket(b, p)
	if err != nil {
		return err
	}
	if err = os.Mkdir(*out, 0700); err != nil {
		return fmt.Errorf("output must be a new directory: %w", err)
	}
	raw, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	if err = writeEvaluationFile(*out, "review-packet.json", raw); err != nil {
		return err
	}
	fmt.Println("Saved private supporting material. No reference, grade, profile approval or model request was created.")
	return nil
}
