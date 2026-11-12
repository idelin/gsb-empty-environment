// Package capture provides bounded collection of output produced by
// untrusted trial processes.
//
// A Capture acts as an io.Writer (suitable for exec.Cmd.Stdout/Stderr or
// io.Copy) that never blocks the producer and never grows without bound:
//
//   - MaxLineBytes caps the retained head of every single line; bytes beyond
//     the cap are drained but discarded, and the line is marked truncated.
//   - MaxTotalBytes caps retained evidence by evicting the oldest completed
//     lines, so the snapshot always represents the tail of the stream.
//   - All bytes are still read from the underlying pipe until EOF, therefore
//     a chatty process cannot deadlock on a full pipe during cleanup.
package capture

import (
	"bytes"
	"errors"
	"strings"
	"sync"
)

const (
	defaultMaxTotalBytes = 1 << 20
	defaultMaxLineBytes  = 64 << 10
)

// ErrClosed is returned by Write after the Capture has been closed.
var ErrClosed = errors.New("capture: writer already closed")

// Config configures a Capture. Zero values are replaced by sane defaults.
type Config struct {
	// MaxTotalBytes bounds the retained evidence. Completed lines are evicted
	// oldest-first once the retained size exceeds the cap. A single in-flight
	// line is bounded by MaxLineBytes instead, so the hard memory bound is
	// MaxTotalBytes + MaxLineBytes.
	MaxTotalBytes int

	// MaxLineBytes bounds the retained head of one line. Overflow bytes are
	// drained and counted, and the resulting Line is flagged Truncated.
	MaxLineBytes int
}

func (c Config) normalized() Config {
	if c.MaxTotalBytes <= 0 {
		c.MaxTotalBytes = defaultMaxTotalBytes
	}
	if c.MaxLineBytes <= 0 {
		c.MaxLineBytes = defaultMaxLineBytes
	}
	return c
}

// Line is one retained logical line. A trailing '\r' (CRLF) is stripped.
type Line struct {
	// Data holds at most MaxLineBytes, the head of the original line.
	Data []byte
	// Truncated is true when the original line exceeded MaxLineBytes.
	Truncated bool
}

// Snapshot is an immutable, point-in-time copy of the captured evidence.
// Counters cover every byte ever observed, including evicted content.
type Snapshot struct {
	// Lines are the retained lines, oldest first; the slice is a tail view
	// once TotalTruncated is set.
	Lines []Line

	// ReceivedBytes is the total number of bytes ever written.
	ReceivedBytes int64
	// RetainedBytes is the sum of len(Line.Data) currently held.
	RetainedBytes int

	// TruncatedLines counts lines that exceeded MaxLineBytes (including
	// lines later evicted by the total-byte cap).
	TruncatedLines int
	// DroppedLineBytes counts bytes discarded by the per-line cap.
	DroppedLineBytes int64
	// DroppedTotalBytes counts bytes discarded by the total-byte cap.
	DroppedTotalBytes int64

	// TotalTruncated is true once at least one completed line was evicted.
	TotalTruncated bool
}

// String joins the retained lines with newlines. Truncation markers are not
// rendered; inspect Lines directly for forensic details.
func (s Snapshot) String() string {
	parts := make([]string, len(s.Lines))
	for i, line := range s.Lines {
		parts[i] = string(line.Data)
	}
	return strings.Join(parts, "\n")
}

// Capture is a goroutine-safe, bounded io.WriteCloser.
type Capture struct {
	mu sync.Mutex

	maxTotal int
	maxLine  int

	lines       []Line // completed, retained lines
	cur         []byte // in-flight line head, cap <= maxLine
	curTrunc    bool
	retained    int
	closed      bool
	once        sync.Once
	done        chan struct{}
	received    int64
	truncLines  int
	droppedLine int64
	droppedTot  int64
	totalTrunc  bool
}

// New creates a Capture with the given config.
func New(cfg Config) *Capture {
	cfg = cfg.normalized()
	return &Capture{
		maxTotal: cfg.MaxTotalBytes,
		maxLine:  cfg.MaxLineBytes,
		done:     make(chan struct{}),
	}
}

// Write consumes p fully so the producer is never blocked. It always returns
// len(p), nil unless the Capture is closed.
func (c *Capture) Write(p []byte) (int, error) {
	n := len(p)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return 0, ErrClosed
	}
	c.received += int64(n)

	for len(p) > 0 {
		idx := bytes.IndexByte(p, '\n')
		if idx < 0 {
			c.appendChunk(p)
			break
		}
		c.appendChunk(p[:idx])
		p = p[idx+1:]
		c.finishLine()
	}
	return n, nil
}

// appendChunk stores the head of the current line up to maxLine and accounts
// for the overflow. The caller holds c.mu.
func (c *Capture) appendChunk(chunk []byte) {
	if len(chunk) == 0 {
		return
	}
	if room := c.maxLine - len(c.cur); room > 0 {
		if len(chunk) <= room {
			c.cur = append(c.cur, chunk...)
			return
		}
		c.cur = append(c.cur, chunk[:room]...)
		chunk = chunk[room:]
	}
	c.curTrunc = true
	c.droppedLine += int64(len(chunk))
}

// finishLine seals the in-flight line and enforces the total-byte cap by
// evicting oldest completed lines. The caller holds c.mu.
func (c *Capture) finishLine() {
	if len(c.cur) > 0 && c.cur[len(c.cur)-1] == '\r' {
		c.cur = c.cur[:len(c.cur)-1]
	}

	data := append([]byte(nil), c.cur...)
	line := Line{Data: data, Truncated: c.curTrunc}
	if c.curTrunc {
		c.truncLines++
	}
	c.cur = c.cur[:0]
	c.curTrunc = false

	c.lines = append(c.lines, line)
	c.retained += len(data)

	// A single line larger than maxTotal is retained as-is; the overall hard
	// bound remains maxTotal + maxLine by construction.
	for c.retained > c.maxTotal && len(c.lines) > 1 {
		head := c.lines[0]
		c.retained -= len(head.Data)
		c.droppedTot += int64(len(head.Data))
		c.totalTrunc = true
		c.lines = c.lines[1:]
	}
}

// Close flushes any trailing partial line and marks the Capture done. It is
// safe to call multiple times and from multiple goroutines.
func (c *Capture) Close() error {
	c.mu.Lock()
	if !c.closed {
		c.closed = true
		if len(c.cur) > 0 {
			c.finishLine()
		}
	}
	c.mu.Unlock()

	c.once.Do(func() { close(c.done) })
	return nil
}

// Done is closed once Close has completed.
func (c *Capture) Done() <-chan struct{} {
	return c.done
}

// Snapshot returns a deep copy of the current evidence and counters.
func (c *Capture) Snapshot() Snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()

	lines := make([]Line, len(c.lines))
	for i, line := range c.lines {
		lines[i] = Line{
			Data:      append([]byte(nil), line.Data...),
			Truncated: line.Truncated,
		}
	}
	return Snapshot{
		Lines:             lines,
		ReceivedBytes:     c.received,
		RetainedBytes:     c.retained,
		TruncatedLines:    c.truncLines,
		DroppedLineBytes:  c.droppedLine,
		DroppedTotalBytes: c.droppedTot,
		TotalTruncated:    c.totalTrunc,
	}
}
