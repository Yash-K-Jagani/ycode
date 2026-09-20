package security

// Redact replaces likely secrets with [REDACTED:rule] markers.
func Redact(text string) string {
	out := text
	for _, r := range secretRes {
		out = r.re.ReplaceAllStringFunc(out, func(m string) string {
			return "[REDACTED:" + r.name + "]"
		})
	}
	return out
}
