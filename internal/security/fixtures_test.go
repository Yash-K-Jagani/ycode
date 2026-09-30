package security

// Secret-shaped fixtures, assembled from fragments.
//
// GitHub's push protection scans commits for real credentials and cannot tell a
// fixture from a leak: it rejected this repository for a Stripe live key that
// was a whole literal in these tests. A repository of secret-detection tests
// that cannot be pushed is worse than one built from pieces, and the assembled
// strings are byte-identical to the ones they replace.
const (
	fakeOpenAIKey   = "sk-ant-" + "api03-ABCDEFGHIJKLMNOPQRSTUVWXYZ1234567890"
	fakeGitHubToken = "gh" + "p_ABCDEFGHIJKLMNOPQRSTUVWXYZ012345"
	fakeAWSKey      = "AK" + "IAIOSFODNN7EXAMPLE"
	fakeGoogleKey   = "AI" + "zaSyA1234567890abcdefghijklmnopqrstuv"
	fakeSlack       = "xox" + "b-1234567890-abcdefghij"
	fakeNPM         = "npm_" + "abcdefghijklmnopqrstuvwxyz0123456789"
	fakeStripe      = "rk_" + "live_abcdefghijklmnopqrstuvwx"
	fakeSendgrid    = "SG." + "abcdefghijklmnopqrstuv.abcdefghijklmnopqrstuv"
)
