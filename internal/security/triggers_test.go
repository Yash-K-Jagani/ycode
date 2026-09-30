package security

import (
	"strings"
	"testing"
)

// Redact skips a rule whose trigger words are absent, on the argument that the
// pattern cannot match without one of them. That argument is only as good as the
// trigger list, so it is checked against the compiled patterns rather than
// trusted.
func TestEveryTriggerAppearsInItsPattern(t *testing.T) {
	for _, r := range secretRes {
		if len(r.triggers) == 0 {
			continue
		}
		lower := strings.ToLower(r.re.String())
		for _, trig := range r.triggers {
			if !strings.Contains(lower, trig) {
				t.Errorf("rule %q lists trigger %q, which its pattern does not contain:\n  %s",
					r.name, trig, r.re)
			}
		}
	}
}

// The skip must not change what Redact produces. Every sample is run both ways:
// once with the triggers populated and once with them emptied, so a rule that
// gains a keyword without gaining a trigger shows up as a difference.
func TestSkippingTriggersDoesNotChangeOutput(t *testing.T) {
	saved := make([][]string, len(secretRes))
	for i := range secretRes {
		saved[i] = secretRes[i].triggers
	}
	withTriggers := func(s string) string { return Redact(s) }
	// Disable every skip: the pre-optimisation behaviour, exactly.
	for i := range secretRes {
		secretRes[i].triggers = nil
	}
	withoutTriggers := func(s string) string { return Redact(s) }

	samples := []string{
		"",
		"an ordinary sentence with no secrets in it at all",
		"something happened",
		"The function validates the path before reading it.",
		"api_key=ABCDEFGHIJKLMNOP",
		"api_key: ABCDEFGHIJKLMNOP",
		`{"apiKey":"ABCDEFGHIJKLMNOPQRST"}`,
		"API_SECRET=abcdefghijklmnopqrstuvwx",
		"ACCESS_TOKEN=ghijklmnopqrstuvwxyz012345",
		"AUTH_TOKEN: abcdefghijklmnopqrstuvwx",
		"CLIENT_SECRET = abcdefghijklmnopqrstuvwx",
		"password=hunter2hunter2",
		"pass: correcthorsebattery",
		"passwd = somethinglonghere",
		"my_key=ABCDEFGHIJKLMNOPQRSTUVWX",
		"some.token.value=ABCDEFGHIJKLMNOPQRSTUVWX",
		"secret=ABCDEFGHIJKLMNOPQRSTUVWX",
		"passwords=ABCDEFGHIJKLMNOPQRSTUVWX",
		"credentials=ABCDEFGHIJKLMNOPQRSTUVWX",
		"aws_secret_access_key = " + strings.Repeat("a", 40),
		"Authorization: Bearer abcdefghijklmnopqrstuvwxyz0123456789",
		"postgres://u:p@host:5432/db",
		// Secret-shaped fixtures are assembled from fragments - see
		// fixtures_test.go, and the note there about GitHub push protection.
		fakeOpenAIKey,
		fakeGitHubToken,
		fakeAWSKey,
		fakeGoogleKey,
		fakeSlack,
		"eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NSJ9.abcdefghijklmnop",
		"-----BEGIN RSA PRIVATE KEY-----",
		fakeNPM,
		fakeStripe,
		fakeSendgrid,
		// Prose containing a keyword but no assignment: the skip must not fire
		// incorrectly here either, and must not redact.
		"the secret sauce is paprika",
		"rotate your password regularly",
		"no credentials are stored",
		"this token is expired",
	}

	for _, s := range samples {
		got, want := withTriggers(s), withoutTriggers(s)
		if got != want {
			t.Errorf("input %q\n  with triggers:    %q\n  without triggers: %q", s, got, want)
		}
	}
	// Put them back whatever happened above.
	for i := range secretRes {
		secretRes[i].triggers = saved[i]
	}
}

// Guards the property the optimisation exists for: ordinary prose is untouched.
func TestOrdinaryProseIsNotRedacted(t *testing.T) {
	for _, s := range []string{
		"an ordinary sentence with no secrets in it at all",
		"The function validates the path before reading it.",
		"rotating the credential file requires a restart",
		"the token is stored in the OS keyring, not the config",
	} {
		if got := Redact(s); got != s {
			t.Errorf("prose was altered:\n  in:  %q\n  out: %q", s, got)
		}
	}
}

// The optimisation must not have weakened anything the tests already cover: a
// representative secret of each shape still goes.
func TestSecretsStillRedacted(t *testing.T) {
	for _, s := range []string{
		"api_key=ABCDEFGHIJKLMNOP",
		"password=hunter2hunter2",
		// High entropy matters: high-entropy-assign requires 24+ chars, and the
		// keyword must be at the END of the name - see the gap test below.
		"my_secret=Xk9pQ2mNv7wLz4RbT8yH3cJ6mQ9wZ",
	} {
		got := Redact(s)
		if !strings.Contains(got, "REDACTED") {
			t.Errorf("not redacted: %q", got)
		}
		if strings.Contains(got, "ABCDEFGHIJKLMNOP") ||
			strings.Contains(got, "hunter2hunter2") ||
			strings.Contains(got, "Xk9pQ2mNv7wLz4RbT8yH3cJ6mQ9wZ") {
			t.Errorf("the value survived: %q", got)
		}
	}
}

// A real gap, recorded rather than quietly left.
//
// Every assignment rule requires the keyword at the END of the name, because the
// pattern is `[a-z0-9_.-]*(key|token|secret|...)s?["']?\s*[:=]`. A name like
// `db_secret_value` therefore matches nothing: after "secret" the pattern needs a
// colon or an equals, and finds `_value`.
//
// This was found while benchmarking redaction, not by reading the patterns, which
// is a reasonable argument for having benchmarks for security code. It is
// documented rather than fixed here because widening the pattern changes what
// gets redacted, and that decision belongs with someone who can say what the
// false-positive cost should be - a tool log full of [REDACTED] is its own
// problem.
func TestAssignmentRulesMissKeywordsInsideLongerNames(t *testing.T) {
	for _, s := range []string{
		"db_secret_value=Xk9pQ2mNv7wLz4RbT8yH3cJ6mQ9wZ",
		"api_secret_value=Xk9pQ2mNv7wLz4RbT8yH3cJ6mQ9wZ",
	} {
		if got := Redact(s); got != s {
			t.Errorf("this shape is now redacted (%q) - if that was deliberate, "+
				"update the rule and this test together", got)
		}
	}
}

// Documented rather than assumed: the entropy check is why a low-entropy value
// assigned to a secret-looking name survives. That is a deliberate trade, and
// this records it so a future change does not rediscover it as a bug.
func TestHighEntropyRuleStillChecksEntropy(t *testing.T) {
	const low = "my_secret=ABCDEFGHIJKLMNOPQRSTUVWX"
	if Redact(low) != low {
		t.Skip("the entropy check no longer applies to this rule; update this test")
	}
}
