// SPDX-License-Identifier: AGPL-3.0-or-later
package assessment

import (
	"errors"
	"io"
	"os"
	"strings"
)

// LoadOpenAIKeyFile parses only a literal OPENAI_API_KEY assignment, never shell
// code. It refuses links, broad permissions and oversized files; errors do not
// echo file content. Environment configuration can be used in containers.
func LoadOpenAIKeyFile(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return "", errors.New("OpenAI key file must be a regular owner-only file")
	}
	f, err := os.Open(path)
	if err != nil {
		return "", errors.New("cannot open OpenAI key file")
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, 8193))
	if err != nil || len(raw) > 8192 {
		return "", errors.New("OpenAI key file is unreadable or too large")
	}
	key := ""
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, value, ok := strings.Cut(line, "=")
		if !ok || name != "OPENAI_API_KEY" || key != "" {
			return "", errors.New("OpenAI key file must contain one OPENAI_API_KEY assignment")
		}
		key = strings.TrimSpace(value)
		if len(key) >= 2 && ((key[0] == '\'' && key[len(key)-1] == '\'') || (key[0] == '"' && key[len(key)-1] == '"')) {
			key = key[1 : len(key)-1]
		}
	}
	if key == "" || strings.ContainsAny(key, " \r\n\t") {
		return "", errors.New("OpenAI key is missing or invalid")
	}
	return key, nil
}
