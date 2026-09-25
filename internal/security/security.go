package security

import (
	"fmt"
	"math"
	"regexp"
	"strings"
)

type Finding struct {
	Line int
	Text string
	Rule string
}

// secretRule describes a pattern that matches a hardcoded credential.
//
// valueGroup names the capture group holding just the secret, so redaction
// leaves surrounding structure (JSON keys, quotes, separators) intact. A rule
// with valueGroup 0 redacts the entire match.
//
// loose marks rules that key off a variable name rather than a fixed credential
// format. Loose rules can match documentation and placeholders, so their
// captured value must clear the placeholder and entropy checks. Fixed-format
// rules (AKIA…, ghp_…, xoxb-…) are already unambiguous, so they are not
// filtered — the AWS documentation itself ships example secrets containing the
// word EXAMPLE, and a blanket placeholder filter would blind us to them.
type secretRule struct {
	name       string
	re         *regexp.Regexp
	valueGroup int
	loose      bool
}

var secretRes = []secretRule{
	{name: "aws-key", re: regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`)},
	{name: "aws-temp-key", re: regexp.MustCompile(`\bASIA[0-9A-Z]{16}\b`)},
	{name: "aws-secret", re: regexp.MustCompile(`(?i)aws_secret_access_key(\s*[:=]\s*)["']?([A-Za-z0-9/+=]{40})`), valueGroup: 2},
	{name: "github-token", re: regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{20,}\b`)},
	{name: "npm-token", re: regexp.MustCompile(`\bnpm_[A-Za-z0-9]{36}\b`)},
	{name: "private-key", re: regexp.MustCompile(`-----BEGIN (RSA |EC |DSA |OPENSSH |PGP )?PRIVATE KEY`)},
	{name: "google-api-key", re: regexp.MustCompile(`\bAIza[0-9A-Za-z\-_]{35}\b`)},
	{name: "openai-key", re: regexp.MustCompile(`\bsk-(proj-)?[A-Za-z0-9\-_]{20,}\b`)},
	{name: "stripe-key", re: regexp.MustCompile(`\b[rspk]k_(live|test)_[A-Za-z0-9]{16,}\b`)},
	{name: "slack-token", re: regexp.MustCompile(`\bxox[baprs]-[A-Za-z0-9-]{10,}\b`)},
	{name: "sendgrid-key", re: regexp.MustCompile(`\bSG\.[A-Za-z0-9_-]{20,}\.[A-Za-z0-9_-]{20,}\b`)},
	{name: "jwt", re: regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\b`)},

	// Assignments to key-ish names. These capture the value in the last group
	// and deliberately leave both the key's quotes and the value's quotes out
	// of the match, so redaction cannot damage JSON or YAML structure.
	{name: "api-key-assign", re: regexp.MustCompile(`(?i)"?\b(api[_-]?key|api[_-]?secret|secret[_-]?key|access[_-]?token|auth[_-]?token|client[_-]?secret)\b"?(\s*[:=]\s*)["']?([A-Za-z0-9_\-]{12,})`), valueGroup: 3, loose: true},
	{name: "password-assign", re: regexp.MustCompile(`(?i)"?\b(passwd|password|pass)\b"?(\s*[:=]\s*)["']?([^\s"'` + "`" + `]{8,})`), valueGroup: 3, loose: true},

	{name: "bearer-token", re: regexp.MustCompile(`(?i)\bbearer\s+([A-Za-z0-9_\-\.~\+/]{20,}={0,2})`), valueGroup: 1},
	{name: "conn-string", re: regexp.MustCompile(`(?i)\b(mongodb(\+srv)?|postgres(ql)?|mysql|redis|amqp)://[^\s"'<>]+`)},

	// Generic high-entropy blobs assigned to a key-ish name. Catches provider
	// specific formats the explicit rules above do not know about.
	{name: "high-entropy-assign", re: regexp.MustCompile(`(?i)\b([a-z0-9_.-]*(key|token|secret|password|passwd|credential)s?["']?\s*[:=]\s*["']?)([A-Za-z0-9_\-+/=]{24,})`), valueGroup: 3, loose: true},
}

var injectionRes = []*regexp.Regexp{
	regexp.MustCompile(`(?i)ignore\s+(all\s+)?(the\s+)?(previous|prior|above|earlier|preceding)\s+instructions`),
	regexp.MustCompile(`(?i)disregard\s+(all\s+)?(the\s+)?(previous|prior|above|earlier)\s+instructions`),
	regexp.MustCompile(`(?i)forget\s+(everything|all)\s+(you|above|before)`),
	regexp.MustCompile(`(?i)you\s+are\s+now\s+(a|an)\s+`),
	regexp.MustCompile(`(?i)(new|updated|revised)\s+system\s+(prompt|instructions)`),
	regexp.MustCompile(`(?i)<\s*/?\s*(system|assistant)\s*>`),
	regexp.MustCompile(`(?i)reveal\s+(your|the)\s+(system|secret|hidden|initial)\s+(prompt|instructions|message)`),
	regexp.MustCompile(`(?i)print\s+(your|the)\s+(entire\s+)?(system\s+)?prompt`),
	regexp.MustCompile(`(?i)do\s+not\s+(tell|inform|mention\s+to)\s+the\s+user`),
	regexp.MustCompile(`(?i)without\s+(asking|informing|telling)\s+the\s+user`),
	// NOTE: no <tool:> forgery rule — the harness only parses assistant
	// messages, and the pattern false-positives on docs and tests.
}

// ScanSecrets returns findings for likely hardcoded secrets.
func ScanSecrets(text string) []Finding {
	var out []Finding
	lines := strings.Split(text, "\n")
	for i, ln := range lines {
		for _, r := range secretRes {
			hit := r.re.FindStringSubmatch(ln)
			if hit == nil {
				continue
			}
			// Rules keyed off a variable name can match documentation and
			// placeholders, so their value must clear the placeholder and
			// entropy checks. Fixed-format rules need neither.
			if r.loose {
				val := hit[r.valueGroup]
				if isPlaceholder(val) {
					continue
				}
				if r.name == "high-entropy-assign" && !looksHighEntropy(val) {
					continue
				}
			}
			out = append(out, Finding{Line: i + 1, Text: truncate(ln), Rule: r.name})
			break
		}
	}
	return out
}

// looksHighEntropy reports whether s has enough character variety to be a
// credential rather than a repeated placeholder like "xxxxxxxx".
func looksHighEntropy(s string) bool {
	if len(s) < 24 {
		return false
	}
	if isPlaceholder(s) {
		return false
	}
	// Shannon entropy in bits/char. Real base64/hex secrets land around
	// 4.0+; prose, words, and repeated digits sit well below.
	freq := map[rune]float64{}
	total := 0.0
	for _, c := range s {
		freq[c]++
		total++
	}
	if total == 0 {
		return false
	}
	var h float64
	for _, n := range freq {
		p := n / total
		h -= p * math.Log2(p)
	}
	return h >= 3.5
}

// isPlaceholder detects filler credentials that tools and docs commonly use.
func isPlaceholder(s string) bool {
	lower := strings.ToLower(s)
	for _, p := range []string{"example", "placeholder", "your", "changeme", "redacted", "dummy", "fake", "xxxxx", "todo", "insert", "sample", "test-key", "<", "$"} {
		if strings.Contains(lower, p) {
			return true
		}
	}
	// A single repeated character is never a real secret.
	if len([]rune(s)) > 0 {
		first := rune(s[0])
		same := true
		for _, c := range s {
			if c != first {
				same = false
				break
			}
		}
		if same {
			return true
		}
	}
	return false
}

// ScanInjection detects prompt-injection patterns in untrusted content (tool output, URLs).
func ScanInjection(text string) []string {
	var hits []string
	for _, re := range injectionRes {
		if m := re.FindString(text); m != "" {
			hits = append(hits, truncate(m))
		}
	}
	return hits
}

func FormatFindings(fs []Finding) string {
	if len(fs) == 0 {
		return "no secrets detected"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d potential secret(s):\n", len(fs))
	for _, f := range fs {
		fmt.Fprintf(&b, "line %d [%s] %s\n", f.Line, f.Rule, f.Text)
	}
	return b.String()
}

func truncate(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 160 {
		return s[:160] + "…"
	}
	return s
}
