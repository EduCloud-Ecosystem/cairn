// SPDX-License-Identifier: AGPL-3.0-or-later
package api

import (
	_ "embed"
	"net/http"
)

// Public presentation assets contain no identity, course or learner data.
//
//go:embed studio-theme.js
var studioThemeJS []byte

//go:embed studio-theme.css
var studioThemeCSS []byte

func (s *Server) handleStudioTheme(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.URL.Path == "/studio/theme.js" {
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		_, _ = w.Write(studioThemeJS)
	} else {
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		_, _ = w.Write(studioThemeCSS)
	}
}
