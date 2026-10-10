// SPDX-License-Identifier: AGPL-3.0-or-later
package assessment

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// Extract never executes content, follows links, reads Git metadata, or sends
// data over a network. Originals are retained privately for review/erasure.
func Extract(dir string, paths []string) []Artifact {
	out := make([]Artifact, 0, len(paths))
	total := 0
	for _, p := range paths {
		a := Artifact{Path: p, Extractor: ExtractorVersion, Segments: []Segment{}}
		ext := strings.ToLower(filepath.Ext(p))
		switch ext {
		case ".txt", ".py", ".r":
			a.MediaType = "text/plain"
		case ".md", ".qmd", ".rmd":
			a.MediaType = "text/markdown"
		case ".ipynb":
			a.MediaType = "application/x-ipynb+json"
		default:
			a.Issue = "Unsupported format; instructor review required"
		}
		if a.Issue == "" {
			a = extractFile(dir, a, &total)
		}
		out = append(out, a)
	}
	return out
}
func extractFile(dir string, a Artifact, total *int) Artifact {
	fail := func(msg string) Artifact { a.Issue = msg; return a }
	if !ValidPath(a.Path) {
		return fail("Unsafe artifact path")
	}
	current := dir
	for _, part := range strings.Split(a.Path, "/") {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			return fail("Required artifact missing or unreadable")
		}
		if current == filepath.Join(dir, filepath.FromSlash(a.Path)) && !info.Mode().IsRegular() {
			return fail("Artifact is not a regular file")
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fail("Symbolic links are not accepted")
		}
	}
	// Root confines opens even if a path changes after Lstat.
	root, err := os.OpenRoot(dir)
	if err != nil {
		return fail("Cannot open evidence root")
	}
	defer root.Close()
	f, err := root.Open(a.Path)
	if err != nil {
		return fail("Artifact unreadable")
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return fail("Artifact is not a regular file")
	}
	if info.Size() > MaxFileBytes {
		return fail("Artifact exceeds 256 KiB extraction limit")
	}
	b, err := io.ReadAll(io.LimitReader(f, MaxFileBytes+1))
	if err != nil || len(b) > MaxFileBytes {
		return fail("Artifact exceeds extraction limit or cannot be read")
	}
	if *total+len(b) > MaxTotalBytes {
		return fail("Artifacts exceed 1 MiB total extraction limit")
	}
	*total += len(b)
	a.Original = b
	a.Size = len(b)
	a.SHA256 = DigestBytes(b)
	if !utf8.Valid(b) || strings.ContainsRune(string(b), 0) {
		return fail("Artifact is not UTF-8 text")
	}
	if a.MediaType == "application/x-ipynb+json" {
		var nb struct {
			Version int `json:"nbformat"`
			Cells   []struct {
				Type   string          `json:"cell_type"`
				Source json.RawMessage `json:"source"`
			} `json:"cells"`
		}
		if json.Unmarshal(b, &nb) != nil || nb.Version != 4 || len(nb.Cells) > 512 {
			return fail("Invalid or oversized version 4 notebook")
		}
		for i, c := range nb.Cells {
			if c.Type != "code" && c.Type != "markdown" && c.Type != "raw" {
				return fail("Unsupported notebook cell type")
			}
			if len(c.Source) == 0 || (c.Source[0] != '"' && c.Source[0] != '[') {
				return fail("Unreadable notebook cell source")
			}
			var source string
			if json.Unmarshal(c.Source, &source) != nil {
				var lines []string
				if json.Unmarshal(c.Source, &lines) != nil {
					return fail("Unreadable notebook cell source")
				}
				source = strings.Join(lines, "")
			}
			a.Segments = append(a.Segments, Segment{Location: fmt.Sprintf("cell:%d", i+1), Text: source})
		}
	} else {
		lines := strings.Split(string(b), "\n")
		if len(lines) > 4096 {
			return fail("Artifact exceeds 4096 lines")
		}
		for i, line := range lines {
			a.Segments = append(a.Segments, Segment{Location: fmt.Sprintf("line:%d", i+1), Text: line})
		}
	}
	if len(a.Segments) == 0 {
		return fail("Artifact contains no assessable source")
	}
	return a
}
