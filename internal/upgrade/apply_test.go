package upgrade

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// platformAsset is the asset name this test host would actually download, so
// the suite passes on linux CI as well as on Windows.
func platformAsset() string { return AssetName(runtime.GOOS, runtime.GOARCH) }

// makeArchive builds a real release-shaped archive containing payload as the
// ycode executable, plus the LICENSE and README.md that goreleaser also ships.
// Serving a genuine archive is the point: an earlier version of Apply
// installed the archive itself, which a test that served the bare binary as
// the "asset" could not detect.
func makeArchive(t *testing.T, name, payload string) []byte {
	t.Helper()
	binName := "ycode"
	if strings.HasSuffix(name, ".zip") {
		binName = "ycode.exe"
	}
	var buf bytes.Buffer
	if strings.HasSuffix(name, ".zip") {
		zw := zip.NewWriter(&buf)
		for _, f := range []struct{ n, body string }{
			{binName, payload},
			{"LICENSE", "MIT"},
			{"README.md", "# ycode"},
		} {
			w, err := zw.Create(f.n)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := w.Write([]byte(f.body)); err != nil {
				t.Fatal(err)
			}
		}
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
		return buf.Bytes()
	}
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	for _, f := range []struct {
		n, body string
		mode    int64
	}{
		{binName, payload, 0o755},
		{"LICENSE", "MIT", 0o644},
		{"README.md", "# ycode", 0o644},
	} {
		if err := tw.WriteHeader(&tar.Header{Name: f.n, Mode: f.mode, Size: int64(len(f.body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(f.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// checksumServer serves a checksums.txt and an asset, so the verify-and-replace
// path can be exercised without touching the network.
func checksumServer(t *testing.T, asset, body, sum string) *httptest.Server {
	t.Helper()
	if sum == "" {
		sum = sha256Hex(body) + "  " + asset
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/checksums.txt", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(sum + "\n"))
	})
	mux.HandleFunc("/"+asset, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func sha256Hex(s string) string {
	return sha256Sum([]byte(s))
}

func TestChecksumParsing(t *testing.T) {
	// Both separator forms that goreleaser and sha256sum can emit.
	bodies := []string{
		"abc123  ycode_windows_amd64.zip\ndef456  ycode_linux_amd64.tar.gz\n",
		"abc123 *ycode_windows_amd64.zip\ndef456 *ycode_linux_amd64.tar.gz\n",
		"abc123 ycode_windows_amd64.zip\r\ndef456 ycode_linux_amd64.tar.gz\r\n",
	}
	for _, b := range bodies {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(b))
		}))
		got, err := Checksum(context.Background(), srv.URL, "ycode_windows_amd64.zip")
		srv.Close()
		if err != nil {
			t.Fatalf("Checksum error for %q: %v", b, err)
		}
		if got != "abc123" {
			t.Errorf("Checksum(%q) = %q, want abc123", b, got)
		}
	}
}

func TestChecksumMissingAsset(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("abc123  something_else.tar.gz\n"))
	}))
	defer srv.Close()
	got, err := Checksum(context.Background(), srv.URL, "ycode_windows_amd64.zip")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "" {
		t.Errorf("expected empty hash for an unlisted asset, got %q", got)
	}
}

func TestChecksumUpcases(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ABCDEF  ycode_linux_arm64.tar.gz\n"))
	}))
	defer srv.Close()
	got, _ := Checksum(context.Background(), srv.URL, "ycode_linux_arm64.tar.gz")
	if got != "abcdef" {
		t.Errorf("expected lowercased hash, got %q", got)
	}
}

// A release with no published checksum must be refused, not installed blind.
func TestApplyRefusesUnverifiedRelease(t *testing.T) {
	asset := platformAsset()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "checksums.txt") {
			// Lists a different asset only.
			_, _ = w.Write([]byte("deadbeef  other.tar.gz\n"))
			return
		}
		_, _ = w.Write([]byte("binary"))
	}))
	defer srv.Close()

	rel := &Release{TagName: "v9.9.9", HTMLURL: srv.URL, Assets: []Asset{{Name: asset, URL: srv.URL + "/" + asset}}}
	dir := t.TempDir()
	_, err := Apply(context.Background(), rel, "v0.0.1", filepath.Join(dir, "ycode"))
	if err == nil {
		t.Fatal("expected an error when no checksum is published")
	}
	if !strings.Contains(err.Error(), "no checksum") {
		t.Errorf("error should explain the missing checksum, got: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "ycode")); statErr == nil {
		t.Error("nothing should have been installed")
	}
}

// A tampered payload must be rejected and must not overwrite the current
// binary.
func TestApplyRejectsChecksumMismatch(t *testing.T) {
	asset := platformAsset()
	body := makeArchive(t, asset, "the real binary")
	srv := checksumServer(t, asset, string(body), strings.Repeat("0", 64)+"  "+asset)

	rel := &Release{TagName: "v9.9.9", HTMLURL: srv.URL, Assets: []Asset{{Name: asset, URL: srv.URL + "/" + asset}}}
	dir := t.TempDir()
	dest := filepath.Join(dir, "ycode")
	if err := os.WriteFile(dest, []byte("current"), 0o755); err != nil {
		t.Fatal(err)
	}

	_, err := Apply(context.Background(), rel, "v0.0.1", dest)
	if err == nil {
		t.Fatal("expected a checksum mismatch error")
	}
	if !strings.Contains(err.Error(), "checksum mismatch") {
		t.Errorf("unexpected error: %v", err)
	}
	got, readErr := os.ReadFile(dest)
	if readErr != nil {
		t.Fatalf("current binary should still exist: %v", readErr)
	}
	if string(got) != "current" {
		t.Errorf("current binary was modified: %q", got)
	}
}

// The installed file must be the executable extracted from the archive, not the
// archive itself. Getting this wrong produced a ycode.exe that was a renamed
// zip and could not run.
func TestApplyInstallsExtractedBinaryNotArchive(t *testing.T) {
	asset := platformAsset()
	payload := "this is the actual executable"
	archive := makeArchive(t, asset, payload)
	srv := checksumServer(t, asset, string(archive), "")

	rel := &Release{TagName: "v9.9.9", HTMLURL: srv.URL, Assets: []Asset{{Name: asset, URL: srv.URL + "/" + asset}}}
	dir := t.TempDir()
	dest := filepath.Join(dir, "ycode.exe")
	if err := os.WriteFile(dest, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}

	if _, err := Apply(context.Background(), rel, "v0.0.1", dest); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != payload {
		t.Errorf("installed %d bytes; want the %d-byte executable, not the %d-byte archive", len(got), len(payload), len(archive))
	}
	if bytes.HasPrefix(got, []byte("PK")) {
		t.Error("installed a zip archive instead of the executable")
	}
	if bytes.HasPrefix(got, []byte{0x1f, 0x8b}) {
		t.Error("installed a gzip archive instead of the executable")
	}
}

func TestApplyVerifiesAndReplaces(t *testing.T) {
	asset := platformAsset()
	payload := "brand new binary"
	body := makeArchive(t, asset, payload)
	srv := checksumServer(t, asset, string(body), "")

	rel := &Release{TagName: "v9.9.9", HTMLURL: srv.URL, Assets: []Asset{{Name: asset, URL: srv.URL + "/" + asset}}}
	dir := t.TempDir()
	dest := filepath.Join(dir, "ycode")
	if err := os.WriteFile(dest, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}

	res, err := Apply(context.Background(), rel, "v0.0.1", dest)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !res.Verified {
		t.Error("result should report the checksum as verified")
	}
	if res.Version != "v9.9.9" {
		t.Errorf("Version = %q, want v9.9.9", res.Version)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("new binary missing: %v", err)
	}
	if string(got) != payload {
		t.Errorf("installed binary = %q, want %q", got, payload)
	}
	// No stale temp file should be left behind.
	if _, err := os.Stat(filepath.Join(dir, ".ycode-upgrade.tmp")); err == nil {
		t.Error("staging file was not cleaned up")
	}
}

// An archive missing the executable must be refused rather than installed.
func TestApplyRefusesArchiveWithoutBinary(t *testing.T) {
	asset := platformAsset()
	// Rebuild the archive without the ycode member.
	var buf bytes.Buffer
	if strings.HasSuffix(asset, ".zip") {
		zw := zip.NewWriter(&buf)
		w, _ := zw.Create("README.md")
		_, _ = w.Write([]byte("readme"))
		_ = zw.Close()
	} else {
		gw := gzip.NewWriter(&buf)
		tw := tar.NewWriter(gw)
		_ = tw.WriteHeader(&tar.Header{Name: "README.md", Mode: 0o644, Size: 6, Typeflag: tar.TypeReg})
		_, _ = tw.Write([]byte("readme"))
		_ = tw.Close()
		_ = gw.Close()
	}
	archive := buf.Bytes()

	srv := checksumServer(t, asset, string(archive), "")
	rel := &Release{TagName: "v9.9.9", HTMLURL: srv.URL, Assets: []Asset{{Name: asset, URL: srv.URL + "/" + asset}}}
	dir := t.TempDir()
	dest := filepath.Join(dir, "ycode")
	if _, err := Apply(context.Background(), rel, "v0.0.1", dest); err == nil {
		t.Fatal("expected an error for an archive with no executable")
	}
	if _, err := os.Stat(dest); err == nil {
		t.Error("nothing should have been installed")
	}
}

func TestApplyRejectsMissingAsset(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("x"))
	}))
	defer srv.Close()
	// Release publishes only an asset for some other platform; the lookup must
	// fail cleanly rather than downloading something unintended.
	other := "ycode_plan9_amd64.tar.gz"
	rel := &Release{TagName: "v9.9.9", HTMLURL: srv.URL, Assets: []Asset{{Name: other, URL: srv.URL}}}
	dir := t.TempDir()
	if _, err := Apply(context.Background(), rel, "v0.0.1", filepath.Join(dir, "ycode")); err == nil {
		t.Fatal("expected an error when the platform asset is absent")
	}
}

func TestLatestParsesRelease(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(Release{
			TagName: "v1.2.3",
			HTMLURL: "https://example.test",
			Assets:  []Asset{{Name: "checksums.txt"}},
		})
	}))
	defer srv.Close()

	// Point Latest at the test server by swapping the endpoint template.
	old := apiBase
	apiBase = srv.URL + "/repos/%s/releases/latest"
	defer func() { apiBase = old }()

	rel, err := Latest(context.Background(), DefaultRepo)
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if rel.TagName != "v1.2.3" {
		t.Errorf("TagName = %q, want v1.2.3", rel.TagName)
	}
	if len(rel.Assets) != 1 {
		t.Errorf("got %d assets, want 1", len(rel.Assets))
	}
}

func TestLatestRejectsEmptyTag(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"tag_name":""}`))
	}))
	defer srv.Close()
	old := apiBase
	apiBase = srv.URL + "/repos/%s/releases/latest"
	defer func() { apiBase = old }()

	if _, err := Latest(context.Background(), DefaultRepo); err == nil {
		t.Fatal("expected an error for a release with no tag")
	}
}
