package tui

import (
	"strings"
	"testing"

	"github.com/Yash-K-Jagani/ycode/internal/config"
	"github.com/Yash-K-Jagani/ycode/internal/sessions"
	"github.com/Yash-K-Jagani/ycode/internal/tui/theme"
)

// benchModel is a Model with just enough state that Update and View do real
// work. A zero Model would render an empty viewport and make every number here
// meaningless, and a zero textarea panics in View rather than measuring
// anything.
func benchModel() *Model {
	m := &Model{cfg: config.Defaults(), th: theme.Dark(), sess: &sessions.Session{}}
	m.vp = newViewport(100, 30)
	m.ta = newTextarea()
	return m
}

// The chunk a benchmark emits. Two lines, because a real build interleaves
// progress and diagnostics and a one-line chunk would measure the fast path.
const benchChunk = "compiling package internal/thing\n\twriting output\n"

// BenchmarkToolChunkPath measures what one chunk of live tool output costs end
// to end: the message handler, the render, and the viewport sync.
//
// It is here because the whole design of live output rests on this number. At a
// millisecond a chunk, a build printing a thousand lines a second cannot be
// shown live however the output is batched, and the gate would be pointless. At
// a microsecond it is free and the batching is a refinement rather than a
// necessity.
func BenchmarkToolChunkPath(b *testing.B) {
	m := benchModel()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.Update(toolChunkMsg{tool: "bash", chunk: benchChunk})
		// Rendering is what a real chunk triggers. Leaving it out would measure
		// the cheap half and make the number a lie.
		_ = m.View()
	}
}

// The gate claims that batching turns many chunks into few messages. These two
// handle the same total output - 50 lines - so the comparison is measured
// rather than asserted.
func BenchmarkToolChunkUnbatched(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		m := benchModel()
		for j := 0; j < 50; j++ {
			m.Update(toolChunkMsg{tool: "bash", chunk: "a line of build output\n"})
			_ = m.View()
		}
	}
}

func BenchmarkToolChunkBatched(b *testing.B) {
	chunk := strings.Repeat("a line of build output\n", 50)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		m := benchModel()
		m.Update(toolChunkMsg{tool: "bash", chunk: chunk})
		_ = m.View()
	}
}
