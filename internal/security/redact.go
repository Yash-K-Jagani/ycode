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
	lowered := strings.ToLower(text)
	for _, r := range secretRes {
		// Skip a rule whose trigger words are not present at all.
		//
		// The two assignment rules are by far the most expensive here - measured
		// at 18.5us and 9.3us on a 48-byte string, which together are a third of
		// the whole cost - because they open with `[a-z0-9_.-]*` before a keyword
		// alternation, so the engine re-walks the text looking for a prefix that
		// could match. Both require one of a small set of literal words to be
		// present, so their absence makes a match impossible and running the
		// regex is pure cost.
		//
		// This is a correctness-preserving skip, not a heuristic: if none of the
		// literals occurs in the text, the pattern provably cannot match. A
		// test runs both paths over a corpus of real-looking secrets and requires
		// identical output, so a literal added to a rule without adding it here
		// fails the test rather than leaking.
		if len(r.triggers) > 0 && !anyPresent(lowered, r.triggers) {
			continue
		}
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
//
// Rules flagged loose apply the same placeholder and entropy gates that
// ScanSecrets applies. They have to, because both walk the same secretRes list:
// ScanSecrets gating a value while Redact substitutes it left a trace full of
// [REDACTED] where a scan reported nothing, which is the false-positive cost the
// entropy rule exists to avoid. `my_secret=ABCDEFGHIJKLMNOPQRSTUVWX` was the
// visible case - the alphabet scores 4.58 bits/char, so it read as a credential.
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
		if r.loose && skipLooseValue(text[gs:ge], r) {
			// Not a secret. Leaving last alone means the text is copied through
			// by the next match, so nothing is dropped.
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

// skipLooseValue reports whether a captured value is too un-secret to redact.
//
// Shared with ScanSecrets so the two cannot drift again - the drift is what let
// the false positive through while the corresponding test skipped.
func skipLooseValue(val string, r secretRule) bool {
	if isPlaceholder(val) {
		return true
	}
	return r.name == "high-entropy-assign" && !looksHighEntropy(val)
}

// anyPresent reports whether any trigger occurs in the already-lowercased text.
func anyPresent(lowered string, triggers []string) bool {
	for _, t := range triggers {
		if strings.Contains(lowered, t) {
			return true
		}
	}
	return false
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
