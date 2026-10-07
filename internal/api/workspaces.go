// SPDX-License-Identifier: AGPL-3.0-or-later
package api

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// ParseWorkspaceURLs validates operator configuration. A mapping never grants
// access at the destination; the workspace must enforce its own course roster.
func ParseWorkspaceURLs(raw string) (map[string]string, error) {
	result := map[string]string{}
	if strings.TrimSpace(raw) == "" {
		return result, nil
	}
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		return nil, fmt.Errorf("CAIRN_WORKSPACE_URLS must be a JSON object mapping classroom IDs to origins")
	}
	for classID, target := range result {
		if strings.TrimSpace(classID) == "" || !validWorkspaceURL(target) {
			return nil, fmt.Errorf("CAIRN_WORKSPACE_URLS contains an invalid classroom ID or workspace origin")
		}
	}
	return result, nil
}

func validWorkspaceURL(target string) bool {
	u, err := url.Parse(target)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return false
	}
	if strings.ContainsAny(target, "\\\"<> \t\r\n") {
		return false
	}
	return u.Scheme == "https" || (u.Scheme == "http" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "::1"))
}

// Copy only valid configuration so direct library callers also fail closed.
func validatedWorkspaceURLs(in map[string]string) map[string]string {
	out := map[string]string{}
	for classID, target := range in {
		if classID != "" && validWorkspaceURL(target) {
			out[classID] = target
		}
	}
	return out
}
