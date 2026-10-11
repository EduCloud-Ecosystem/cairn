// SPDX-License-Identifier: AGPL-3.0-or-later
package assessment

import (
	"fmt"
	"path"
	"regexp"
	"strings"
	"unicode/utf8"
)

const PythonImplementation = "python_implementation"
const PythonAuthored = "python_authored"
const MaxStarterBytes = 256 << 10

const implementationEvidenceInstructions = ` A criterion with evidence=python_implementation requires at least one citation covering a complete Python operation. With evidence=python_authored the operation must also differ from the instructor-captured starter. The catalog lists server-checked eligible anchor IDs for each mode. Include other citations needed to substantiate each specific feedback claim. Signatures, comments, docstrings, imports and unimplemented stubs alone cannot satisfy these requirements. An eligible anchor is not proof of correct logic or independent authorship. If no eligible support is available, the criterion-specific schema permits only null points: leave that criterion unassessable for instructor review, including malformed attempted work, and never substitute zero. Explain the visible attempt and evidence limitation without describing an attempted answer as untouched scaffold. Null for one criterion does not make the entire submission no_relevant_work; classify relevance separately. When eligible evidence exists, incorrect but assessable implementation can still receive zero under the rubric. Starter contents are private and are not included as model examples.`

func hasImplementationRule(r Rubric) bool {
	for _, c := range r.Criteria {
		if c.Evidence != "" {
			return true
		}
	}
	return false
}

func validateEvidenceRules(r Rubric) error {
	python := map[string]bool{}
	for _, p := range r.Paths {
		if strings.EqualFold(path.Ext(p), ".py") {
			python[p] = true
		}
	}
	total := 0
	for p, source := range r.StarterFiles {
		total += len(source)
		if !python[p] || !utf8.ValidString(source) || strings.ContainsRune(source, 0) || len(strings.Split(source, "\n")) > 4096 || total > MaxStarterBytes {
			return fmt.Errorf("starter files must be UTF-8 Python rubric paths, at most 4096 lines each and 256 KiB total")
		}
	}
	for _, c := range r.Criteria {
		switch c.Evidence {
		case "":
		case PythonImplementation, PythonAuthored:
			if len(python) == 0 {
				return fmt.Errorf("Python evidence requirements need a .py rubric path")
			}
			if c.Evidence == PythonAuthored {
				for p := range python {
					if _, ok := r.StarterFiles[p]; !ok {
						return fmt.Errorf("authored Python evidence requires a captured starter for every .py path")
					}
				}
			}
		default:
			return fmt.Errorf("unknown criterion evidence requirement")
		}
	}
	return nil
}

type operationEvidence struct {
	text    string
	changed bool
}
type operationMap map[string]operationEvidence

func implementationSupport(d Document) operationMap {
	out := operationMap{}
	needed := false
	for _, c := range d.Rubric.Criteria {
		needed = needed || c.Evidence != ""
	}
	if !needed {
		return out
	}
	for _, a := range d.Artifacts {
		if !strings.EqualFold(path.Ext(a.Path), ".py") || a.Issue != "" || DigestBytes(a.Original) != a.SHA256 {
			continue
		}
		starter, captured := d.Rubric.StarterFiles[a.Path]
		baseline := map[string]bool{}
		_, clean := pythonLexicalLines(starter)
		for _, line := range clean {
			baseline[codeFingerprint(line)] = true
		}
		for _, text := range pythonOperationLines(starter) {
			baseline[codeFingerprint(text)] = true
		}
		ops := pythonOperationLines(string(a.Original))
		for line, text := range ops {
			key := fmt.Sprintf("%s\x00%s\x00line:%d", a.Path, a.SHA256, line+1)
			out[key] = operationEvidence{text, captured && !baseline[codeFingerprint(text)]}
		}
	}
	return out
}

func supportsImplementation(r Rubric, j Judgment, support operationMap) bool {
	mode := ""
	for _, c := range r.Criteria {
		if c.ID == j.CriterionID {
			mode = c.Evidence
		}
	}
	if mode == "" {
		return true
	}
	for _, c := range j.Citations {
		v, ok := support[c.Path+"\x00"+c.SHA256+"\x00"+c.Location]
		// A signature fragment on a line with an inline body is not that body.
		// Empty/partial quotes cannot borrow eligibility from surrounding source.
		if ok && strings.Contains(c.Quote, v.text) && (mode != PythonAuthored || v.changed) {
			return true
		}
	}
	return false
}

func codeFingerprint(s string) string { return strings.Join(strings.Fields(s), "") }

// This is a conservative lexical check, not a Python parser, correctness test,
// authorship attribution or semantic claim verifier. Strings/comments, imports,
// declarations and stubs cannot anchor an implementation score. A changed line
// means only that its whitespace/comment-normalized text is absent from the
// instructor's captured starter, not proof of who wrote it.
var operationStart = regexp.MustCompile(`^(return\b|yield\b|raise\b|assert\b|del\b|if\b|elif\b|for\b|while\b|with\b|async\s+(for|with)\b|await\b|[A-Za-z_][A-Za-z_0-9.]*\s*\(|[A-Za-z_][A-Za-z_0-9., \[\]:]*\s*(=|\+=|-=|\*=|/=))`)

func pythonOperationLines(source string) map[int]string {
	masked, clean := pythonLexicalLines(source)
	out := map[int]string{}
	header, depth := false, 0
	decoratorDepth := 0
	for n, line := range masked {
		t := strings.TrimSpace(line)
		if decoratorDepth > 0 || strings.HasPrefix(t, "@") {
			for _, c := range line {
				if strings.ContainsRune("([{", c) {
					decoratorDepth++
				}
				if strings.ContainsRune(")]}", c) {
					decoratorDepth--
				}
			}
			continue
		}
		if strings.HasPrefix(t, "def ") || strings.HasPrefix(t, "async def ") || strings.HasPrefix(t, "class ") {
			header, depth = true, 0
		}
		start := 0
		if header {
			start = -1
			for i, c := range line {
				if strings.ContainsRune("([{", c) {
					depth++
				}
				if strings.ContainsRune(")]}", c) {
					depth--
				}
				if c == ':' && depth == 0 {
					header = false
					start = i + 1
					break
				}
			}
			if start < 0 {
				continue
			}
			t = strings.TrimSpace(line[start:])
		}
		if strings.Contains(t, "NotImplementedError") || !operationStart.MatchString(t) {
			continue
		}
		text := strings.TrimSpace(clean[n][start:])
		if text != "" {
			out[n] = text
		}
	}
	return out
}

// Preserve byte positions and newlines while masking string contents and
// comments. The second stream retains strings but removes comments, allowing
// starter comparison without treating a newly added comment as authored logic.
// Unknown/unclosed literals are masked through EOF rather than guessed as code.
func pythonLexicalLines(source string) ([]string, []string) {
	masked, clean := []byte(source), []byte(source)
	quote := byte(0)
	triple := false
	escaped := false
	for i := 0; i < len(source); {
		c := source[i]
		if quote != 0 {
			if c != '\n' && c != '\r' {
				masked[i] = ' '
			}
			if escaped {
				escaped = false
				i++
				continue
			}
			if c == '\\' {
				escaped = true
				i++
				continue
			}
			if c == quote {
				if !triple {
					quote = 0
				} else if i+2 < len(source) && source[i+1] == quote && source[i+2] == quote {
					masked[i+1], masked[i+2] = ' ', ' '
					i += 2
					quote = 0
				}
			}
			i++
			continue
		}
		if c == '#' {
			for i < len(source) && source[i] != '\n' {
				masked[i], clean[i] = ' ', ' '
				i++
			}
			continue
		}
		if c == '\'' || c == '"' {
			quote, triple = c, i+2 < len(source) && source[i+1] == c && source[i+2] == c
			masked[i] = ' '
			if triple {
				masked[i+1], masked[i+2] = ' ', ' '
				i += 2
			}
		}
		i++
	}
	return strings.Split(string(masked), "\n"), strings.Split(string(clean), "\n")
}
