package upgrade

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const checksumName = "checksums.txt"

// sha256Sum is a thin wrapper so tests can build fixtures without importing
// crypto/sha256 themselves.
func sha256Sum(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// Checksum fetches the expected SHA256 for asset from the release's
// checksums.txt. It returns "" when the release does not publish one.
func Checksum(ctx context.Context, releaseURL, asset string) (string, error) {
	sums, err := fetch(ctx, releaseURL+"/"+checksumName)
	if err != nil {
		return "", err
	}
	// Format is "<hex>  <name>" (or "<hex> *<name>" from sha256sum text mode).
	for _, line := range strings.Split(string(sums), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		if strings.TrimPrefix(fields[1], "*") == asset {
			return strings.ToLower(fields[0]), nil
		}
	}
	return "", nil
}

func fetch(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", UserAgent)
	client := &http.Client{Timeout: 10 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d fetching %s", resp.StatusCode, url)
	}
	return io.ReadAll(resp.Body)
}

// Result reports what an Apply call did.
type Result struct {
	Version  string
	From     string
	To       string
	Verified bool
	Backup   string
}

// Apply downloads the release asset for the current platform, verifies it
// against the published checksum, and installs it at dest.
//
// The running binary is renamed rather than overwritten: Windows refuses to
// replace or delete an executable that is currently in use, so a rename is
// the only way to upgrade in place. The old binary is kept as dest+".old" and
// removed on the next run by CleanupBackup.
func Apply(ctx context.Context, rel *Release, from, dest string) (*Result, error) {
	asset := AssetName(runtime.GOOS, runtime.GOARCH)
	if asset == "" {
		return nil, fmt.Errorf("no published asset for %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	var url string
	for _, a := range rel.Assets {
		if a.Name == asset {
			url = a.URL
			break
		}
	}
	if url == "" {
		return nil, fmt.Errorf("release %s does not publish %s", rel.TagName, asset)
	}

	releaseURL := strings.TrimSuffix(rel.HTMLURL, "/")
	// Prefer the /releases/latest/download/ form, which is stable per asset and
	// not tied to the release page URL shape.
	downloadBase := releaseURL
	if i := strings.Index(releaseURL, "/releases/tag/"); i >= 0 {
		downloadBase = releaseURL[:i] + "/releases/download/" + rel.TagName
	}

	want, err := Checksum(ctx, downloadBase, asset)
	if err != nil {
		return nil, fmt.Errorf("fetching checksums: %w", err)
	}
	if want == "" {
		return nil, fmt.Errorf("release %s publishes no checksum for %s; refusing to install an unverified binary", rel.TagName, asset)
	}

	data, err := fetch(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("downloading %s: %w", asset, err)
	}
	got := sha256.Sum256(data)
	if hex.EncodeToString(got[:]) != want {
		return nil, fmt.Errorf("checksum mismatch for %s\n  expected %s\n  actual   %s\nrefusing to install; the download may be corrupted or tampered with", asset, want, hex.EncodeToString(got[:]))
	}

	// The checksum covers the published archive, not the executable inside it.
	// Install the extracted binary, never the archive.
	binName := "ycode"
	if runtime.GOOS == "windows" {
		binName = "ycode.exe"
	}
	exe, err := extractBinary(data, asset, binName)
	if err != nil {
		return nil, err
	}

	dir := filepath.Dir(dest)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	// Write beside the target first so a crash mid-write cannot leave a
	// truncated binary in place.
	staged := filepath.Join(dir, ".ycode-upgrade.tmp")
	if err := os.WriteFile(staged, exe, 0o755); err != nil {
		return nil, err
	}
	res := &Result{Version: rel.TagName, From: from, To: rel.TagName, Verified: true}
	if err := replaceExecutable(staged, dest); err != nil {
		_ = os.Remove(staged)
		return nil, err
	}
	// Clean up the backup left by a previous upgrade now that this one worked.
	CleanupBackup(dest)
	res.Backup = dest + ".old"
	return res, nil
}

// extractBinary pulls the executable out of a release archive. The format
// follows the goreleaser archive override: zip on Windows, tar.gz elsewhere.
func extractBinary(archive []byte, asset, binName string) ([]byte, error) {
	switch {
	case strings.HasSuffix(asset, ".zip"):
		zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
		if err != nil {
			return nil, fmt.Errorf("opening %s as a zip archive: %w", asset, err)
		}
		for _, f := range zr.File {
			// Archives also carry LICENSE and README.md; match the binary
			// exactly, and allow for a wrapping directory.
			if f.FileInfo().IsDir() {
				continue
			}
			if path.Base(f.Name) != binName {
				continue
			}
			rc, err := f.Open()
			if err != nil {
				return nil, fmt.Errorf("reading %s from %s: %w", binName, asset, err)
			}
			defer func() { _ = rc.Close() }()
			return io.ReadAll(rc)
		}
	case strings.HasSuffix(asset, ".tar.gz"), strings.HasSuffix(asset, ".tgz"):
		gz, err := gzip.NewReader(bytes.NewReader(archive))
		if err != nil {
			return nil, fmt.Errorf("opening %s as a gzip archive: %w", asset, err)
		}
		defer func() { _ = gz.Close() }()
		tr := tar.NewReader(gz)
		for {
			hdr, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, fmt.Errorf("reading %s: %w", asset, err)
			}
			if hdr.Typeflag != tar.TypeReg || path.Base(hdr.Name) != binName {
				continue
			}
			return io.ReadAll(tr)
		}
	default:
		return nil, fmt.Errorf("do not know how to extract %s", asset)
	}
	return nil, fmt.Errorf("%s does not contain a %s; refusing to install", asset, binName)
}

// replaceExecutable moves the current executable aside and puts the new one in
// its place. On Windows the running binary is locked for deletion but not for
// renaming, so the sequence is rename-away, rename-in.
func replaceExecutable(staged, dest string) error {
	old := dest + ".old"
	// A stale backup from a previous failed upgrade should not block this one.
	_ = os.Remove(old)
	if _, err := os.Stat(dest); err == nil {
		if err := os.Rename(dest, old); err != nil {
			return fmt.Errorf("could not move the running binary aside: %w", err)
		}
	}
	if err := os.Rename(staged, dest); err != nil {
		// Put the old binary back so the user is not left without ycode.
		if _, statErr := os.Stat(old); statErr == nil {
			_ = os.Rename(old, dest)
		}
		return fmt.Errorf("could not install the new binary: %w", err)
	}
	return nil
}

// CleanupBackup removes a leftover .old binary from a previous upgrade.
func CleanupBackup(dest string) {
	_ = os.Remove(dest + ".old")
}
