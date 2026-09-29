package tui

import (
	"strings"
	"testing"

	"github.com/Yash-K-Jagani/ycode/internal/config"
	"github.com/Yash-K-Jagani/ycode/internal/cost"
	"github.com/Yash-K-Jagani/ycode/internal/hooks"
	"github.com/Yash-K-Jagani/ycode/internal/modes"
	"github.com/Yash-K-Jagani/ycode/internal/router"
	"github.com/Yash-K-Jagani/ycode/internal/sessions"
	"github.com/Yash-K-Jagani/ycode/internal/tools"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
)

// bareModel is a Model with just enough wired to exercise the update loop and
// the transcript, with no provider behind it.
func bareModel(t *testing.T) Model {
	t.Helper()
	ta := textarea.New()
	ta.Placeholder = "Ask anything…  (/help, Tab modes, @file to attach)"
	ta.Focus()
	ta.CharLimit = 8000
	ta.SetHeight(3)
	cfg := config.Defaults()
	cfg.ActiveProvider = "ollama"
	cfg.ActiveModel = "fake-model"
	vp := newViewport(80, 20)
	workdir := t.TempDir()
	m := Model{
		cfg:    cfg,
		router: router.New(cfg),
		// A session, because submitting a turn needs one. Without it the
		// submit path panics, which looks like a bug in the input box rather
		// than a fixture that is missing half a Model.
		sess:    sessions.New("ollama", "fake-model"),
		ta:      ta,
		vp:      vp,
		mode:    modes.Build,
		workdir: workdir,
		toolreg: tools.DefaultRegistry(workdir),
		tracker: cost.New(),
		hookset: hooks.LoadFiles(nil),
	}
	return m
}

// modelWithTallTranscript gives the Model a transcript taller than the window,
// with the window sized so there is something to scroll.
//
// The content goes through the real path - appendMsg, which fills the joined
// copy as well as the viewport. Setting the viewport content directly would
// leave the joined transcript empty, and the first syncViewport would then
// replace the whole thing with whatever arrived since, which looks like the
// scroll being broken rather than the fixture.
func modelWithTallTranscript(t *testing.T) Model {
	t.Helper()
	m := bareModel(t)
	m.vp.Width = 40
	m.vp.Height = 10
	var b strings.Builder
	for i := 0; i < 100; i++ {
		b.WriteString("line of transcript that is deliberately long enough to wrap around the window\n")
	}
	m.appendMsg(strings.TrimRight(b.String(), "\n"))
	m.vp.GotoTop()
	return m
}

func scrollOffset(m Model) int { return m.vp.YOffset }

// Typing must never move the transcript.
//
// The viewport ships with a pager keymap that binds space to page-down, b to
// page-up, and u/d/j/k/h/l to half-page and line scrolling, and the update loop
// hands every key to the viewport as well as to the input box. So every space
// typed in a prompt paged the transcript down, and typing the word "the build"
// scrolled it six times. The model is a chat client: the input box has the
// keyboard, and the transcript is scrolled by keys that cannot occur in text.
func TestTypingNeverScrollsTheTranscript(t *testing.T) {
	m := modelWithTallTranscript(t)
	before := scrollOffset(m)
	if before != 0 {
		t.Fatalf("fixture did not start at the top: %d", before)
	}

	// Every rune a person actually types, including the ones the pager keymap
	// claims: space, b, j, k, h, l, u, d, f.
	typed := "the quick brown fox jumped over b k j h l u d f "
	for _, r := range typed {
		updated, _ := (&m).Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = *(updated.(*Model))
	}

	if got := scrollOffset(m); got != before {
		t.Fatalf("typing %q scrolled the transcript from %d to %d; the input box has the keyboard",
			typed, before, got)
	}
	if m.ta.Value() == "" {
		t.Fatal("the keystrokes did not reach the input box either")
	}
}

// The transcript must not be yanked back to the bottom while it is being
// scrolled. This was the complaint that started it: reading back through a long
// answer during a stream was impossible, because every new token jumped the
// view to the end.
func TestStreamingDoesNotYankAScrolledView(t *testing.T) {
	m := modelWithTallTranscript(t)
	// Scroll up the way a person reading history would. From the top there is
	// nowhere to go, so start from the bottom.
	m.vp.GotoBottom()
	m.vp.ScrollUp(4)
	parked := scrollOffset(m)
	if parked == 0 {
		t.Fatal("fixture did not scroll")
	}

	// Now stream more of the answer.
	for i := 0; i < 20; i++ {
		updated, _ := (&m).Update(deltaMsg("and here is more of the answer arriving\n"))
		m = *(updated.(*Model))
	}
	if got := scrollOffset(m); got != parked {
		t.Fatalf("streaming moved the view from %d to %d; the reader was yanked away", parked, got)
	}
}

// While following the output, new content must still scroll into view, or the
// fix would be "never auto-scroll" and the user would simply stop seeing the
// answer.
func TestStreamingFollowsTheBottomWhenPinned(t *testing.T) {
	m := modelWithTallTranscript(t)
	m.vp.GotoBottom()
	if !m.vp.AtBottom() {
		t.Fatal("fixture is not at the bottom")
	}
	for i := 0; i < 20; i++ {
		updated, _ := (&m).Update(deltaMsg("more answer text arriving in the transcript\n"))
		m = *(updated.(*Model))
		if !m.vp.AtBottom() {
			t.Fatalf("stopped following the output at offset %d", scrollOffset(m))
		}
	}
}

// A key to get back, and the indicator that says there is something below.
func TestJumpToLatestReturnsToTheBottom(t *testing.T) {
	m := modelWithTallTranscript(t)
	m.vp.ScrollUp(5)
	if m.pinned() {
		t.Fatal("scrolling up should unpin the view")
	}
	updated, _ := (&m).Update(tea.KeyMsg{Type: tea.KeyEnd})
	m = *(updated.(*Model))
	if !m.pinned() {
		t.Fatalf("End did not return to the bottom; offset %d", scrollOffset(m))
	}
}

func TestPinnedIndicatorAppearsOnlyWhenScrolledUp(t *testing.T) {
	m := modelWithTallTranscript(t)
	m.vp.GotoBottom()
	if m.pinned() != true {
		t.Fatal("a view at the bottom is pinned")
	}
	m.vp.ScrollUp(2)
	if m.pinned() {
		t.Fatal("a scrolled-up view is not pinned")
	}
	// And a short transcript has nothing to scroll to, so it is always pinned.
	short := bareModel(t)
	short.vp.Height = 10
	short.vp.SetContent("one line")
	if !short.pinned() {
		t.Fatal("a transcript that fits should count as pinned")
	}
}

// Scrolling keys that cannot be typed are the only ones that may move it.
// Each is started from the end it moves away from, since a page-down from the
// bottom and a page-up from the top both correctly do nothing.
func TestScrollKeysMoveTheTranscript(t *testing.T) {
	cases := []struct {
		name string
		key  tea.KeyMsg
		// startAtTop positions the view so the key has somewhere to go.
		startAtTop bool
	}{
		{"page down", tea.KeyMsg{Type: tea.KeyPgDown}, true},
		{"page up", tea.KeyMsg{Type: tea.KeyPgUp}, false},
		{"alt down", tea.KeyMsg{Type: tea.KeyDown, Alt: true}, true},
		{"alt up", tea.KeyMsg{Type: tea.KeyUp, Alt: true}, false},
		{"ctrl+down", tea.KeyMsg{Type: tea.KeyCtrlDown}, true},
		{"ctrl+up", tea.KeyMsg{Type: tea.KeyCtrlUp}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := modelWithTallTranscript(t)
			if c.startAtTop {
				m.vp.GotoTop()
			} else {
				m.vp.GotoBottom()
			}
			before := scrollOffset(m)
			updated, _ := (&m).Update(c.key)
			m = *(updated.(*Model))
			if scrollOffset(m) == before {
				t.Fatalf("%s did not move the transcript from %d", c.name, before)
			}
		})
	}
}

// The viewport must not be driven by its own keymap at all. This is the
// structural half of the fix: leaving the keymap in place is what made typing
// scroll, and a test that checks the offset is the only thing standing between
// a dependency upgrade and that bug coming back.
func TestViewportKeymapIsCleared(t *testing.T) {
	m := bareModel(t)
	// A zero key.Binding has no keys, so checking the enabled ones is the way
	// to assert it; key.Binding holds a slice and cannot be compared directly.
	km := m.vp.KeyMap
	for name, b := range map[string]key.Binding{
		"PageDown": km.PageDown, "PageUp": km.PageUp,
		"HalfPageUp": km.HalfPageUp, "HalfPageDown": km.HalfPageDown,
		"Down": km.Down, "Up": km.Up, "Left": km.Left, "Right": km.Right,
	} {
		if len(b.Keys()) != 0 {
			t.Errorf("%s is still bound to %v; typing will scroll the transcript", name, b.Keys())
		}
	}
}
