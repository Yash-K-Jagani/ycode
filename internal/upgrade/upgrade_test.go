package upgrade

import "testing"

func TestIsNewer(t *testing.T) {
	cases := []struct {
		want, have string
		expect     bool
	}{
		{"v0.12.1", "v0.12.0", true},
		{"v0.12.0", "v0.12.0", false},
		{"0.13.0", "0.12.9", true},
		{"v1.0.0", "v0.99.99", true},
		{"v0.12.10", "v0.12.9", true}, // numeric, not lexical
		{"v0.12.9", "v0.12.10", false},
		{"v0.2.0", "v0.10.0", false},
		{"v0.10.0", "v0.2.0", true},

		// Leading v is optional on either side.
		{"0.12.1", "v0.12.0", true},
		{"v0.12.1", "0.12.0", true},

		// Build metadata is ignored.
		{"v0.12.1+abc123", "v0.12.1", false},
		{"v0.12.2+sha.abc", "v0.12.1+def", true},

		// A release outranks its own prerelease.
		{"v0.13.0", "v0.13.0-rc1", true},
		{"v0.13.0-rc1", "v0.13.0", false},
		{"v0.13.0", "v0.13.0", false},

		// Unparseable versions must never claim to be an upgrade, so a local
		// dev build is not pushed to an older release.
		{"v0.12.1", "0.0.0-dev", false},
		{"v0.12.1", "unknown", false},
		{"v0.12.1", "", false},
		{"v0.12.1", "not-a-version", false},
		{"garbage", "v1.0.0", false},
	}
	for _, c := range cases {
		if got := IsNewer(c.want, c.have); got != c.expect {
			t.Errorf("IsNewer(%q, %q) = %v, want %v", c.want, c.have, got, c.expect)
		}
	}
}

func TestCleanVersion(t *testing.T) {
	cases := map[string]bool{
		"v0.12.1":     true,
		"0.12.1":      true,
		"1.0.0":       true,
		"0.0.0-dev":   false,
		"v0.13.0-rc1": false,
		"unknown":     false,
		"":            false,
		"0.0.0":       false, // the dev-build sentinel
	}
	for in, want := range cases {
		if got := CleanVersion(in); got != want {
			t.Errorf("CleanVersion(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestAssetName(t *testing.T) {
	cases := map[[2]string]string{
		{"linux", "amd64"}:   "ycode_linux_amd64.tar.gz",
		{"linux", "arm64"}:   "ycode_linux_arm64.tar.gz",
		{"darwin", "amd64"}:  "ycode_darwin_amd64.tar.gz",
		{"darwin", "arm64"}:  "ycode_darwin_arm64.tar.gz",
		{"windows", "amd64"}: "ycode_windows_amd64.zip",
		{"windows", "arm64"}: "ycode_windows_arm64.zip",
	}
	for k, want := range cases {
		if got := AssetName(k[0], k[1]); got != want {
			t.Errorf("AssetName(%q, %q) = %q, want %q", k[0], k[1], got, want)
		}
	}
}

// The asset name must match what goreleaser actually publishes, since a
// mismatch here is a 404 at upgrade time.
func TestAssetNameMatchesReleaseNaming(t *testing.T) {
	if got, want := AssetName("windows", "amd64"), "ycode_windows_amd64.zip"; got != want {
		t.Errorf("windows asset = %q, want %q (goreleaser ships .zip on windows)", got, want)
	}
}
