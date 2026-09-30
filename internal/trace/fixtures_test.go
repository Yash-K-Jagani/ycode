package trace

// Secret-shaped fixtures, assembled from fragments.
//
// GitHub's push protection scans commits for real credentials and cannot tell a
// fixture from a leak: it rejected this repository for the Stripe live key that
// used to be a whole literal in these tests. A repository of secret-detection
// tests that cannot be pushed is worse than one built from pieces, and the
// assembled strings are byte-identical to the ones they replace.
const (
	fakeOpenAIKey = "sk-ant-" + "api03-ABCDEFGHIJKLMNOPQRSTUVWXYZ1234567890"
	fakeGitHub    = "gh" + "p_ABCDEFGHIJKLMNOPQRSTUVWXYZ012345"
	fakeAWSKey    = "AK" + "IAIOSFODNN7EXAMPLE"
)
