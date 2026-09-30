package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	tea "github.com/charmbracelet/bubbletea"
)

// Incremental transcript rendering.
//
// The transcript used to be rebuilt from scratch on every update. On every
// streamed token that meant two quadratic-in-the-answer costs:
//
//   - the in-flight message was rendered in full (glamour for prose, chroma
//     tokenising for each code fence, diffmatchpatch for each diff pair), and
//   - the entire message history was rejoined with strings.Join.
//
// Both are paid per token, so a 4,000-token answer paid them ~4,000 times: a
// few thousand full markdown renders and a few thousand re-joins of a history
// that had not changed. That is why a turn felt sluggish on a local model even
// when generation itself was fast - the UI was the bottleneck.
//
// The fix is that almost nothing about a streaming message actually changes
// between two deltas. Text arrives at the end, so everything before the last
// paragraph boundary is already final and will render identically forever. This
// file caches the rendered form of that frozen prefix and re-renders only the
// tail, which is bounded by one paragraph rather than by the whole answer.
//
// Two invariants make the result identical to a full re-render:
//
//   - A boundary is only accepted at a blank line *outside* a code fence.
//     Glamour parses a paragraph independently, so a paragraph that is already
//     terminated renders the same alone as it does followed by more text.
//     Inside a fence there is no such guarantee - chroma tokenises the block as
//     a whole - so an open fence is simply never cut, and the block re-renders
//     until it closes.
//   - The final render of a completed turn is still done from scratch, so any
//     difference in how a boundary happened to fall is corrected once, at the
//     end, rather than persisting.

// streamRender holds the incremental render state for one in-flight assistant
// message. It is a value type with no dependency on the Model so it can be
// tested directly.
type streamRender struct {
	// raw is the full text received so far.
	raw string
	// scanPos is how far raw has been scanned looking for boundaries. Advancing
	// it monotonically is what keeps the whole thing linear across a stream:
	// re-scanning from the start on every delta would reintroduce a quadratic
	// scan, just a much cheaper one than a render.
	scanPos int
	// inFence tracks whether the scan is currently inside a ``` block.
	inFence bool
	// lastBreak is the exclusive end of the newest safe cut point.
	lastBreak int
	// lastBreakEndsProse and lastBreakStartsProse classify what sits either
	// side of lastBreak, which is what decides the separator when the chunk
	// ending there is folded in. See appendChunk.
	lastBreakEndsProse   bool
	lastBreakStartsProse bool
	// stableEndsProse and pendingStartsProse are the same classification for
	// the join that is about to be made, carried forward from the boundary that
	// was last consumed.
	stableEndsProse    bool
	pendingStartsProse bool
	// stableRaw is how much of raw has been folded into stableOut.
	stableRaw int
	// stableOut is the rendered form of raw[:stableRaw].
	stableOut string
}

// reset clears the state for a new message.
func (s *streamRender) reset() {
	// The first chunk joins to nothing, so its incoming shape does not matter;
	// pendingStartsProse is set so that the *second* chunk is classified by the
	// boundary that produced it rather than by a zero value.
	*s = streamRender{pendingStartsProse: true}
}

// update folds newly-received text in and returns the full rendered message.
//
// render is the full renderer, used only for text that has just become final -
// once per paragraph, and never again. plain is the cheap renderer for the tail
// that is still arriving; it runs on every delta, so it has to be cheap enough
// to run hundreds of times. See renderStreaming.
func (s *streamRender) update(raw string, render, plain func(string) string) string {
	s.raw = raw
	s.scan()

	if s.lastBreak > s.stableRaw {
		// Everything up to the last safe boundary is final. Render it once,
		// properly, and keep the result: it will never need rendering again.
		// This also re-renders whatever the cheap renderer had been showing for
		// that text, which is how a paragraph gains its styling as it completes.
		chunk := render(s.raw[s.stableRaw:s.lastBreak])
		// The separator for this join was classified by the boundary that
		// started the chunk being appended, so take the incoming shape now and
		// hand back the shape of the boundary just consumed.
		s.stableOut = joinStyled(s.stableOut, chunk, s.stableEndsProse, s.pendingStartsProse)
		s.stableEndsProse = s.lastBreakEndsProse
		s.pendingStartsProse = s.lastBreakStartsProse
		s.stableRaw = s.lastBreak
	}
	if s.stableRaw >= len(s.raw) {
		return s.stableOut
	}
	// The tail is provisional: it is recomputed from scratch on every delta, so
	// it must be composed here and never stored. Folding it in here instead -
	// which the first version did - grew the stable output by the whole tail on
	// every token, turning the cache into the very quadratic it replaced.
	tail := plain(s.raw[s.stableRaw:])
	if s.stableOut == "" {
		return tail
	}
	return s.stableOut + s.separator() + tail
}

// separator is what a single full render would put between the frozen text and
// the text still arriving.
func (s *streamRender) separator() string {
	if s.stableEndsProse && s.pendingStartsProse {
		return "\n\n"
	}
	return "\n"
}

// joinStyled appends a newly rendered chunk to the stable output, separated the
// way a single full render would have separated it.
//
// This is the fiddly part of caching a renderer that is not compositional, and
// getting it wrong is visible. renderAssistant produces its spacing in two
// different ways:
//
//   - Between two prose paragraphs, glamour inserts a blank line, because it can
//     see the paragraph break in the markdown.
//   - Between any other pair of segments - prose then a code block, one code
//     block then another - renderAssistant writes a single "\n" itself, having
//     consumed the source's blank line in its own TrimSpace.
//
// So the separator depends on what sits either side of the cut, which is why the
// scan records the shape of each boundary. Joining with a fixed separator got
// this wrong in both directions: a blank line appeared between a paragraph and
// the code block beneath it, and paragraph spacing collapsed when it should not
// have.
func joinStyled(a, b string, endsProse, startsProse bool) string {
	if a == "" {
		return b
	}
	if b == "" {
		return a
	}
	if endsProse && startsProse {
		return a + "\n\n" + b
	}
	return a + "\n" + b
}

// fenceStateAt classifies a cut point: does the text just before it end in a
// code block, and does the text just after it begin with one?
//
// The answer decides the separator in appendChunk, and getting it wrong shows
// up as a stray blank line or a collapsed gap - small, but the transcript is the
// one thing a user looks at all day.
func fenceStateAt(s string, pos int) (endsProse, startsProse bool) {
	before := strings.TrimRight(s[:pos], " \t\r\n")
	endsProse = !strings.HasSuffix(before, "```")
	after := strings.TrimLeft(s[pos:], " \t\r\n")
	startsProse = !strings.HasPrefix(after, "```")
	return endsProse, startsProse
}

// maxMarkerLen is the length of the longest thing scan() looks for. It sets how
// much of the tail has to be re-examined on the next call.
const maxMarkerLen = 3

// scan advances the boundary search over whatever is new. It is incremental:
// each byte of the stream is examined once, for the whole turn, apart from the
// two-byte overlap described below.
func (s *streamRender) scan() {
	for s.scanPos < len(s.raw) {
		rest := s.raw[s.scanPos:]
		fence := strings.Index(rest, "```")
		blank := strings.Index(rest, "\n\n")
		switch {
		case fence < 0 && blank < 0:
			// Nothing new, but the scan must not simply jump to the end.
			//
			// A marker can straddle the edge between this call and the next: the
			// "\n\n" whose first newline was the last byte of the previous delta
			// is invisible to a search that starts after it. Jumping to
			// len(raw) threw those away, and since deltas are a few bytes each,
			// that is nearly every paragraph boundary in a real stream - the
			// optimization then looked like it worked, because tests that
			// chunked text into large pieces passed, while never once firing.
			//
			// So the last maxMarkerLen-1 bytes stay in view for the next call.
			// The return is load-bearing: the overlap makes scanPos smaller than
			// len(raw), so a loop that continued would re-examine the same two
			// bytes forever. Exiting here is also what guarantees the scan makes
			// progress, which is why the check is "nothing at all found" rather
			// than "not enough found".
			keep := len(s.raw) - (maxMarkerLen - 1)
			if keep < s.scanPos {
				keep = s.scanPos
			}
			s.scanPos = keep
			return
		case blank < 0 || (fence >= 0 && fence < blank):
			// A fence marker comes first. Toggling here is what stops a blank
			// line inside a code block from being mistaken for a paragraph end.
			s.inFence = !s.inFence
			s.scanPos += fence + 3
		default:
			if !s.inFence {
				s.lastBreak = s.scanPos + blank + 2
				s.lastBreakEndsProse, s.lastBreakStartsProse = fenceStateAt(s.raw, s.lastBreak)
			}
			s.scanPos += blank + 2
		}
	}
}

// --- incremental history assembly ---

// joined is the message history pre-joined, because reassembling it per token
// was the other half of the quadratic cost. Messages are only ever appended or
// cleared wholesale, so the joined form can be extended rather than rebuilt.
type joinedTranscript struct {
	body string
	// n counts messages, not characters. It exists because the separator has
	// to be decided by how many messages have gone in, and body alone cannot
	// answer that: a rendered message can legitimately be the empty string, and
	// testing body == "" would then drop the separator and weld the next
	// message onto it.
	n int
}

func (t *joinedTranscript) reset() {
	t.body = ""
	t.n = 0
}

// push appends one already-rendered message, matching strings.Join semantics
// exactly: the first message contributes no separator.
func (t *joinedTranscript) push(rendered string) {
	if t.n == 0 {
		t.body = rendered
	} else {
		t.body += "\n\n" + rendered
	}
	t.n++
}

// pushAll appends several, used when a session is reloaded in one go.
func (t *joinedTranscript) pushAll(rendered []string) {
	t.reset()
	for _, r := range rendered {
		t.push(r)
	}
}

// --- Model plumbing ---
//
// Every mutation of Model.msgs goes through these, so the joined form cannot
// drift from the slice. That matters because they are two copies of the same
// state and nothing but these helpers keeps them in step.

// appendMsg adds one rendered message and refreshes the viewport.
func (m *Model) appendMsg(rendered string) {
	m.msgs = append(m.msgs, rendered)
	m.joined.push(rendered)
	m.syncViewport()
}

// clearMsgs empties the transcript, for /new and session replacement.
func (m *Model) clearMsgs() {
	m.msgs = nil
	m.joined.reset()
	m.sr.reset()
	m.stream.Reset()
} // syncViewport pushes the current history into the viewport and scrolls to the
// bottom. tail is appended after the history and is how the in-flight message
// is shown; pass "" when there is nothing in flight.
//
// This replaces the strings.Join(m.msgs, "\n\n") + SetContent pattern that was
// repeated at nine call sites, each of which was a place to forget the joined
// copy.
//
// The startup splash is deliberately not re-applied here. It was written only
// on the first WindowSizeMsg and dropped again by the next message, so it
// appears while the transcript is empty and scrolls away as soon as there is
// anything to read. That behaviour is preserved rather than quietly changed.
func (m *Model) syncViewport(tail ...string) {
	body := m.joined.body
	for _, t := range tail {
		if t == "" {
			continue
		}
		if body != "" {
			body += "\n\n"
		}
		body += t
	}
	// Was the reader following the output? This has to be asked before the
	// content changes, because afterwards the answer is always yes: the new
	// lines are below, so the old offset is no longer the bottom.
	following := m.pinned()
	m.vp.SetContent(body)
	if following {
		m.vp.GotoBottom()
	}
	// The offset is restored above only for a reader who was following. For
	// one who had scrolled away, the position is adjusted just enough to keep
	// the text they were reading in view as content is appended below it,
	// which is what makes reading back through a long answer possible while the
	// model is still writing it. It was impossible before: every delta called
	// GotoBottom unconditionally, so a reader was dragged to the end once per
	// token.
	m.unseen = 0
	if !following && m.vp.AtBottom() {
		// Scrolling up to a point and then having the content change can leave
		// the offset at the end anyway, which is indistinguishable from having
		// followed. Nothing to recover here; leave it pinned.
		m.unseen = 0
	} else if !following {
		m.unseen = countLines(body) - m.vp.YOffset - m.vp.Height
		if m.unseen < 0 {
			m.unseen = 0
		}
	}
}

func countLines(s string) int { return strings.Count(s, "\n") + 1 }

// jumpPill is the "there is more below" affordance, or "" when the reader is
// following the output.
//
// It only appears when the view is not pinned, because the whole point of the
// pin is that scrolling away is a choice the UI made quietly; without a
// counter, a reader cannot tell whether the answer ended or just scrolled past
// them.
func (m *Model) jumpPill() string {
	if m.pinned() {
		return ""
	}
	label := "↓ jump to latest"
	if m.unseen > 1 {
		label = fmt.Sprintf("↓ %d new lines · End to jump", m.unseen)
	}
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color("#101018")).
		Background(m.th.Accent).
		Padding(0, 1).
		Render(label)
}

// pinned reports whether the transcript is following the output, which means
// the viewport is scrolled to the end.
//
// It is the single source of truth for auto-scroll, read before content
// changes. Everything else - the indicator, the End key, the scroll keys -
// agrees with it rather than keeping its own flag, because a second flag is a
// second thing to get wrong.
func (m *Model) pinned() bool { return m.vp.AtBottom() }

// scrollKey handles the keys that move the transcript, and reports whether it
// handled the message.
//
// Every one of them is a key that cannot be produced by typing a prompt, which
// is the whole reason the viewport's own keymap was cleared: the transcript is
// scrolled with PageUp/PageDown, the arrow keys with a modifier, and End snaps
// back to the bottom.
func (m *Model) scrollKey(msg tea.KeyMsg) bool {
	switch msg.Type {
	case tea.KeyPgUp:
		m.vp.HalfPageUp()
		return true
	case tea.KeyPgDown:
		m.vp.HalfPageDown()
		return true
	case tea.KeyEnd:
		m.vp.GotoBottom()
		return true
	case tea.KeyHome:
		m.vp.GotoTop()
		return true
	}
	switch msg.String() {
	// alt rather than shift: bubbletea v1's KeyMsg has no shift field, so a
	// shift+arrow cannot even be represented, let alone tested. Terminals that
	// do send it as alt+arrow anyway.
	case "alt+up":
		m.vp.ScrollUp(1)
		return true
	case "alt+down":
		m.vp.ScrollDown(1)
		return true
	case "ctrl+up":
		m.vp.HalfPageUp()
		return true
	case "ctrl+down":
		m.vp.HalfPageDown()
		return true
	}
	return false
}
