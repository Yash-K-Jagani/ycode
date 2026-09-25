package security

import (
	"encoding/json"
	"strings"
)

// Redact replaces likely secrets with [REDACTED:rule] markers.
//
// Only the secret value is substituted — never the surrounding assignment,
// separator, or quotes — so redacting a JSON document, a shell command, or a
// YAML manifest leaves it syntactically valid.
func Redact(text string) string {
	out := text
	for _, r := range secretRes {
		if r.valueGroup == 0 {
			out = r.re.ReplaceAllString(out, "[REDACTED:"+r.name+"]")
			continue
		}
		// Re-scan per rule so each hit can be rebuilt from its capture groups.
		out = replaceGroups(out, r)
	}
	return out
}

// replaceGroups substitutes group valueGroup with a marker, preserving every
// other captured group and all unmatched text between matches.
func replaceGroups(text string, r secretRule) string {
	var b []byte
	last := 0
	for _, loc := range r.re.FindAllStringSubmatchIndex(text, -1) {
		n := len(loc) / 2
		if r.valueGroup >= n {
			continue
		}
		gs, ge := loc[2*r.valueGroup], loc[2*r.valueGroup+1]
		if gs < 0 {
			// Optional group that did not participate; skip this match.
			continue
		}
		b = append(b, text[last:gs]...)
		b = append(b, ("[REDACTED:" + r.name + "]")...)
		last = ge
	}
	if len(b) == 0 {
		return text
	}
	b = append(b, text[last:]...)
	return string(b)
}

// RedactJSON redacts secrets in a JSON document and guarantees the result is
// still parseable. If redaction would break the document — which can happen
// when a matched value spans a JSON structural character — it falls back to
// replacing the whole document, trading structure for safety.
func RedactJSON(text string) string {
	out := Redact(text)
	if isJSONish(text) && !json.Valid([]byte(out)) {
		return "[REDACTED:unparseable-after-redaction]"
	}
	return out
}

// isJSONish reports whether text looks like a JSON object or array, which are
// the shapes that must survive redaction intact.
func isJSONish(text string) bool {
	t := strings.TrimSpace(text)
	if len(t) < 2 {
		return false
	}
	return (t[0] == '{' && t[len(t)-1] == '}') || (t[0] == '[' && t[len(t)-1] == ']')
}
