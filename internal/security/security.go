package security

import (
	"fmt"
	"regexp"
	"strings"
)

type Finding struct {
	Line int
	Text string
	Rule string
}

var secretRes = []struct {
	name string
	re   *regexp.Regexp
}{
	{"aws-key", regexp.MustCompile(`AKIA[0-9A-Z]{16}`)},
	{"github-token", regexp.MustCompile(`ghp_[A-Za-z0-9]{20,}`)},
	{"github-oauth", regexp.MustCompile(`gho_[A-Za-z0-9]{20,}`)},
	{"private-key", regexp.MustCompile(`-----BEGIN (RSA |EC |OPENSSH )?PRIVATE KEY`)},
	{"api-key-assign", regexp.MustCompile(`(?i)(api[_-]?key|api[_-]?secret|secret[_-]?key)\s*[:=]\s*["']?[A-Za-z0-9_\-]{12,}`)},
	{"bearer-token", regexp.MustCompile(`(?i)bearer\s+[A-Za-z0-9_\-\.~\+/]{20,}={0,2}`)},
	{"openai-key", regexp.MustCompile(`sk-(proj-)?[A-Za-z0-9]{20,}`)},
	{"conn-string", regexp.MustCompile(`(?i)(mongodb|postgres|mysql|redis)://[^\s"'<>]+`)},
}

var injectionRes = []*regexp.Regexp{
	regexp.MustCompile(`(?i)ignore\s+(all\s+)?(previous|prior|above)\s+instructions`),
	regexp.MustCompile(`(?i)disregard\s+(all\s+)?(previous|prior)\s+instructions`),
	regexp.MustCompile(`(?i)you\s+are\s+now\s+(a|an)\s+`),
	regexp.MustCompile(`(?i)system\s+prompt\s*:`),
	regexp.MustCompile(`(?i)reveal\s+(your|the)\s+(system|secret|hidden)`),
	// NOTE: no <tool:> forgery rule — harness only parses assistant
	// messages, and the pattern false-positives on docs/tests.
}

// ScanSecrets returns findings for likely hardcoded secrets.
func ScanSecrets(text string) []Finding {
	var out []Finding
	lines := strings.Split(text, "\n")
	for i, ln := range lines {
		for _, r := range secretRes {
			if loc := r.re.FindString(ln); loc != "" {
				out = append(out, Finding{Line: i + 1, Text: truncate(ln), Rule: r.name})
				break
			}
		}
	}
	return out
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
