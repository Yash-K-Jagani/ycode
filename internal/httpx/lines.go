package httpx

import (
	"bufio"
	"errors"
	"fmt"
	"io"
)

// ErrLineTooLong is returned when a single newline-delimited line exceeds the
// bound the caller chose.
//
// It exists as a sentinel so callers can tell "the provider sent a line I could
// not read" apart from "the connection died". Both used to arrive as
// "reading stream", which is indistinguishable from a dropped connection and
// sends people looking at their network instead of at the response.
var ErrLineTooLong = errors.New("response line too long")

// DefaultMaxLineBytes bounds a single response line.
//
// The bufio.Scanner default of 1MB cost a whole turn: exceeding it is reported as
// "token too long", which reached users as "reading stream" and discarded
// everything that had arrived before it, on what is otherwise an ordinary line.
// 8MB is far beyond any delta a model emits and still finite, so a stream that
// never sends a newline cannot exhaust memory.
const DefaultMaxLineBytes = 8 << 20

// LineReader reads newline-delimited lines without bufio.Scanner's 1MB limit.
//
// Both streaming providers need this: OpenAI-compatible over SSE, and Ollama over
// NDJSON. Sharing it matters more than the duplication would suggest, because the
// two bugs it fixes were the same bug in the same shape - each provider had its
// own Scanner, its own limit, and its own slightly different failure.
//
// The buffer is small on purpose. ReadLine returns isPrefix when the line
// continues and grows the caller's slice anyway, so a large bufio buffer is most
// of the allocation for a two-line answer.
type LineReader struct {
	rd  *bufio.Reader
	buf []byte
	max int
}

// NewLineReader wraps r. A max of zero uses DefaultMaxLineBytes.
func NewLineReader(r io.Reader, max int) *LineReader {
	if max <= 0 {
		max = DefaultMaxLineBytes
	}
	return &LineReader{rd: bufio.NewReaderSize(r, 8<<10), max: max}
}

// Next returns the next line without its trailing newline.
//
// It returns io.EOF when the stream ends cleanly, including with a non-empty
// final line that has no trailing newline - a stream that ends mid-frame is
// normal, not an error. A wrapped ErrLineTooLong means the bound was hit, and the
// partial line is returned alongside it so the caller can still report what
// arrived.
func (l *LineReader) Next() (string, error) {
	b := l.buf[:0]
	for {
		chunk, isPrefix, err := l.rd.ReadLine()
		if err != nil {
			l.buf = b[:0]
			// Whatever arrived before the error is still a line, and for SSE it
			// is usually the last one.
			return string(b), err
		}
		b = append(b, chunk...)
		// Checked on every append, not only when the line continues. Checking
		// only on isPrefix leaves a hole: the final chunk of a long line carries
		// the newline, so isPrefix is false and the bound is never consulted.
		if len(b) > l.max {
			l.buf = b[:0]
			return string(b[:0]), fmt.Errorf("response line exceeded %d bytes: %w", l.max, ErrLineTooLong)
		}
		if !isPrefix {
			l.buf = b[:0]
			return string(b), nil
		}
	}
}
