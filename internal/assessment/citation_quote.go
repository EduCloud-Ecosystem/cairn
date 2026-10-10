// SPDX-License-Identifier: AGPL-3.0-or-later
package assessment

import (
	"fmt"
	"strings"
)

// Resolve a provider excerpt without normalizing or repairing any source text.
// A unique contiguous multiline excerpt in a line-based artifact expands to
// the existing per-line citation representation. Notebook cells stay atomic.
func resolveCitationQuotes(a Artifact, quote string) ([]Citation, error) {
	c, err := resolveCitationQuote(a, quote)
	if err == nil {
		return []Citation{c}, nil
	}
	if err != providerFailure("unmatched_citation_quote") || !strings.Contains(quote, "\n") {
		return nil, err
	}
	lines := make([]string, len(a.Segments))
	for i, s := range a.Segments {
		// Only reconstruct the same consecutive line stream sent to the provider.
		// Never bridge notebook cells, missing lines, or custom source locations.
		if s.Location != fmt.Sprintf("line:%d", i+1) || strings.Contains(s.Text, "\n") {
			return nil, err
		}
		lines[i] = s.Text
	}
	source := strings.Join(lines, "\n")
	start := strings.Index(source, quote)
	if start < 0 {
		return nil, err
	}
	// Include overlapping occurrences; a distinctive span must identify one place.
	if strings.Contains(source[start+1:], quote) {
		return nil, providerFailure("ambiguous_citation_quote")
	}
	line := strings.Count(source[:start], "\n")
	citations := []Citation{}
	for i, part := range strings.Split(quote, "\n") {
		// Blank separators participate in exact matching but are not evidence.
		if strings.TrimSpace(part) == "" {
			continue
		}
		citations = append(citations, Citation{Path: a.Path, SHA256: a.SHA256, Location: a.Segments[line+i].Location, Quote: part})
		if len(citations) > 32 {
			return nil, providerFailure("invalid_citation_quote")
		}
	}
	return citations, nil
}

// The provider selects evidence text; only Cairn supplies the source location.
// Reject ambiguous snippets rather than picking the first matching occurrence.
// This establishes exact source correspondence, not semantic support or authorship.
func resolveCitationQuote(a Artifact, quote string) (Citation, error) {
	if len(quote) > 1000 || strings.TrimSpace(quote) == "" {
		return Citation{}, providerFailure("invalid_citation_quote")
	}
	location := ""
	for _, s := range a.Segments {
		if !strings.Contains(s.Text, quote) {
			continue
		}
		if location != "" {
			return Citation{}, providerFailure("ambiguous_citation_quote")
		}
		location = s.Location
	}
	if location == "" {
		return Citation{}, providerFailure("unmatched_citation_quote")
	}
	return Citation{Path: a.Path, SHA256: a.SHA256, Location: location, Quote: quote}, nil
}
