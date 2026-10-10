// SPDX-License-Identifier: AGPL-3.0-or-later
package assessment

import "strings"

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
