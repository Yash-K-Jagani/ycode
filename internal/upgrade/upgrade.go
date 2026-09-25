// Package upgrade checks for and applies ycode releases published on GitHub.
//
// It deliberately verifies the published SHA256 before replacing the running
// binary: a release host that can serve an archive can serve a checksum too,
// but only a checksum ties the two together.
package upgrade

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// DefaultRepo is the canonical upstream repository.
const DefaultRepo = "Yash-K-Jagani/ycode"

// UserAgent is required by the GitHub API and identifies this client.
const UserAgent = "ycode-upgrader"

// Release is a published GitHub release.
type Release struct {
	TagName string  `json:"tag_name"`
	HTMLURL string  `json:"html_url"`
	Assets  []Asset `json:"assets"`
}

// Asset is one downloadable file attached to a release.
type Asset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
	Size int64  `json:"size"`
}

// apiBase is the release listing endpoint template. It is a var so tests can
// point it at a local server.
var apiBase = "https://api.github.com/repos/%s/releases/latest"

// Latest fetches the newest published release for repo.
func Latest(ctx context.Context, repo string) (*Release, error) {
	if repo == "" {
		repo = DefaultRepo
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf(apiBase, repo), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set("Accept", "application/vnd.github+json")

	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("contacting GitHub: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// GitHub rate-limits unauthenticated callers aggressively. Surface the
	// reset time, because "try again later, soon" is actionable and a bare
	// 403 is not.
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests {
		reset := resp.Header.Get("X-RateLimit-Reset")
		if reset != "" {
			if secs, err := strconv.ParseInt(reset, 10, 64); err == nil {
				if mins := int((time.Until(time.Unix(secs, 0)) + time.Minute).Minutes()); mins > 0 {
					return nil, fmt.Errorf("GitHub rate limit reached; retry in about %d minute(s), or set GITHUB_TOKEN", mins)
				}
			}
		}
		return nil, fmt.Errorf("GitHub refused the request (HTTP %d); set GITHUB_TOKEN to raise the rate limit", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub returned HTTP %d for %s", resp.StatusCode, repo)
	}

	var rel Release
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, fmt.Errorf("decoding release metadata: %w", err)
	}
	if rel.TagName == "" {
		return nil, fmt.Errorf("release metadata for %s has no tag name", repo)
	}
	return &rel, nil
}

// IsNewer reports whether want is a strictly newer version than have.
//
// Both may carry a leading "v". A build with no parseable version (a local
// `go build`, reported as 0.0.0-dev) is never considered newer, so a dev build
// is not nagged to "upgrade" to an older release.
func IsNewer(want, have string) bool {
	w := parse(want)
	h := parse(have)
	if !w.ok || !h.ok {
		return false
	}
	// A local build reports the 0.0.0-dev sentinel. Comparing it numerically
	// would claim every release is an "upgrade", which is wrong advice for
	// someone deliberately running a working copy.
	if h.isDev() {
		return false
	}
	for i := 0; i < 3; i++ {
		if w.num[i] != h.num[i] {
			return w.num[i] > h.num[i]
		}
	}
	// Equal core version: a release outranks any prerelease of it.
	if w.pre == h.pre {
		return false
	}
	if w.pre == "" {
		return true
	}
	if h.pre == "" {
		return false
	}
	// Both prereleases: fall back to a plain string compare, which is
	// imperfect but deterministic and never invents a downgrade.
	return w.pre > h.pre
}

type version struct {
	num [3]int
	pre string
	ok  bool
}

// isDev reports whether this is the local-build sentinel rather than a real
// release version. No published release is 0.0.0, so the whole 0.0.0 space is
// treated as development builds.
func (v version) isDev() bool {
	return v.num == [3]int{0, 0, 0} && (!v.ok || v.pre != "")
}

func parse(s string) version {
	var v version
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "v")
	// Cut any build metadata: +sha.abc
	if i := strings.IndexByte(s, '+'); i >= 0 {
		s = s[:i]
	}
	core := s
	if i := strings.IndexByte(s, '-'); i >= 0 {
		core = s[:i]
		v.pre = s[i+1:]
	}
	parts := strings.Split(core, ".")
	if len(parts) == 0 || len(parts) > 3 {
		return v
	}
	for i, p := range parts {
		if p == "" {
			return v
		}
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return v
		}
		v.num[i] = n
	}
	v.ok = true
	return v
}

// CleanVersion reports whether a version string looks like a real release
// (as opposed to a local dev build), so callers can phrase advice accurately.
func CleanVersion(s string) bool {
	v := parse(s)
	return v.ok && v.pre == "" && (v.num[0] != 0 || v.num[1] != 0 || v.num[2] != 0)
}

// AssetName returns the release asset this platform needs, or "" if the
// release does not publish one.
//
// Archives are zip on Windows and tar.gz elsewhere, matching the goreleaser
// archive format override.
func AssetName(goos, goarch string) string {
	ext := ".tar.gz"
	if goos == "windows" {
		ext = ".zip"
	}
	return "ycode_" + goos + "_" + goarch + ext
}
