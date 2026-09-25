// Package distribution holds the package-manager metadata that goreleaser
// writes into Homebrew, Scoop, and winget manifests.
//
// It exists so those values can be unit-tested. winget-pkgs CI rejects a
// manifest for a dozen small reasons — an over-length description, a missing
// publisher URL, too many tags — and the failure surfaces as a rejected pull
// request days after the release, which is a slow and confusing way to learn
// that a string is 3 characters too long.
package distribution

// Winget field limits enforced by the winget-pkgs manifest validator.
const (
	WingetMaxIdentifier    = 50
	WingetMaxShortDesc     = 80
	WingetMaxDescription   = 101
	WingetMaxPackageName   = 64
	WingetMaxPublisher     = 255
	WingetMaxTags          = 3
	WingetMaxTagLength     = 64
	WingetIdentifierMaxLen = WingetMaxIdentifier
)

// Winget is the metadata for the winget manifest.
type Winget struct {
	Publisher           string
	PublisherURL        string
	PublisherSupportURL string
	ShortDescription    string
	Description         string
	PackageName         string
	Homepage            string
	License             string
	LicenseURL          string
	Tags                []string
}

// Identifier is the package identifier users type into `winget install`.
// winget derives it as <publisher>.<name> with spaces stripped, which is what
// the README documents.
func (w Winget) Identifier(name string) string {
	return stripSpaces(w.Publisher) + "." + name
}

func stripSpaces(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		if r != ' ' {
			out = append(out, r)
		}
	}
	return string(out)
}

// Problems returns a human-readable list of reasons the winget manifest would
// be rejected. An empty slice means the manifest is well formed.
func (w Winget) Problems(name string) []string {
	var p []string
	if w.Publisher == "" {
		p = append(p, "publisher is required")
	}
	if w.License == "" {
		p = append(p, "license is required")
	}
	if w.ShortDescription == "" {
		p = append(p, "short_description is required")
	}
	if w.Description == "" {
		p = append(p, "description is required")
	}
	if w.PublisherURL == "" {
		p = append(p, "publisher_url is required")
	}
	if w.PublisherSupportURL == "" {
		p = append(p, "publisher_support_url is required")
	}
	// Length limits.
	if n := len([]rune(w.ShortDescription)); n > WingetMaxShortDesc {
		p = append(p, "short_description is "+itoa(n)+" chars, limit "+itoa(WingetMaxShortDesc))
	}
	if n := len([]rune(w.Description)); n > WingetMaxDescription {
		p = append(p, "description is "+itoa(n)+" chars, limit "+itoa(WingetMaxDescription))
	}
	if n := len([]rune(w.PackageName)); n > WingetMaxPackageName {
		p = append(p, "package_name is "+itoa(n)+" chars, limit "+itoa(WingetMaxPackageName))
	}
	if n := len([]rune(w.Identifier(name))); n > WingetMaxIdentifier {
		p = append(p, "package identifier "+w.Identifier(name)+" is "+itoa(n)+" chars, limit "+itoa(WingetMaxIdentifier))
	}
	if len(w.Tags) > WingetMaxTags {
		p = append(p, "too many tags ("+itoa(len(w.Tags))+"): winget allows at most "+itoa(WingetMaxTags))
	}
	for _, t := range w.Tags {
		if n := len([]rune(t)); n > WingetMaxTagLength {
			p = append(p, "tag "+t+" is "+itoa(n)+" chars, limit "+itoa(WingetMaxTagLength))
		}
	}
	return p
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
