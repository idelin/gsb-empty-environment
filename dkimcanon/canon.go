// Package dkimcanon implements the DKIM body canonicalization algorithms
// defined in RFC 6376, section 3.4 (simple and relaxed).
//
// The canonicalizer is a streaming io.Writer: it does not buffer the whole
// message body. Trailing empty lines are held back as a counter rather than
// retained bytes, so memory stays constant no matter how many trailing CRLFs
// the body contains.
package dkimcanon

import (
	"bytes"
	"errors"
	"fmt"
	"io"
)

// Algorithm selects one of the two DKIM body canonicalization algorithms.
type Algorithm int

const (
	// Simple is the "simple" body canonicalization algorithm (RFC 6376,
	// section 3.4.5).
	Simple Algorithm = iota
	// Relaxed is the "relaxed" body canonicalization algorithm (RFC 6376,
	// section 3.4.6).
	Relaxed
)

// Option customizes a Canonicalizer.
type Option func(*Canonicalizer)

// WithBodyLength sets the optional "l=" body length limit: at most n bytes of
// canonicalized body are emitted.
func WithBodyLength(n int64) Option {
	return func(c *Canonicalizer) { c.limit = n }
}

var (
	// ErrInvalidNewline is reported when the body contains a bare CR or bare
	// LF. DKIM canonicalization is defined over CRLF-terminated lines only, so
	// such input cannot be canonicalized or verified.
	ErrInvalidNewline = errors.New("dkimcanon: body contains bare CR or bare LF; only CRLF line endings are valid")

	// ErrDigestMismatch is reported by Digester.Verify when the computed body
	// hash differs from the signature's bh= value.
	ErrDigestMismatch = errors.New("dkimcanon: body digest mismatch")
)

// InvalidNewlineError wraps ErrInvalidNewline with the byte offset of the
// offending bare CR or LF within the Write call that detected it.
type InvalidNewlineError struct {
	// Offset is the index, within the offending Write's input slice, of the
	// bare CR or LF byte. A dangling CR at the end of the whole body is
	// reported from Close wrapped in ErrInvalidNewline.
	Offset int
}

func (e *InvalidNewlineError) Error() string {
	return fmt.Sprintf("%s (at write offset %d)", ErrInvalidNewline, e.Offset)
}

func (e *InvalidNewlineError) Unwrap() error { return ErrInvalidNewline }

// Canonicalizer canonicalizes a mail body written through Write and forwards
// the canonical bytes to its sink.
//
// Trailing empty lines are delayed: they are only flushed once more body
// content proves they were interior blank lines. Pending lines are counted,
// not stored, so an arbitrarily long run of trailing CRLFs costs O(1) memory.
type Canonicalizer struct {
	sink   io.Writer
	algo   Algorithm
	limit  int64 // remaining canonical bytes allowed; -1 when unlimited
	closed bool
	err    error

	emitted int64

	// pendingCRLF counts empty lines (CRLFs) whose delivery is delayed.
	pendingCRLF int

	// crPending means the previous Write ended with a CR, so the next byte
	// must be LF to form a valid line ending.
	crPending bool

	// Relaxed-only state for the line currently being filled.
	relaxSeenContent bool // the current line already contains non-WSP content
	relaxHoldSpace   bool // one or more WSP bytes are pending before the next content byte
	relaxSeenLine    bool // at least one non-empty line has been canonicalized
}

// NewCanonicalizer builds a streaming body canonicalizer.
func NewCanonicalizer(sink io.Writer, algo Algorithm, opts ...Option) *Canonicalizer {
	c := &Canonicalizer{sink: sink, algo: algo, limit: -1}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// NewSimple returns a simple-algorithm canonicalizer writing to sink.
func NewSimple(sink io.Writer, opts ...Option) *Canonicalizer {
	return NewCanonicalizer(sink, Simple, opts...)
}

// NewRelaxed returns a relaxed-algorithm canonicalizer writing to sink.
func NewRelaxed(sink io.Writer, opts ...Option) *Canonicalizer {
	return NewCanonicalizer(sink, Relaxed, opts...)
}

// Write feeds a chunk of the raw body. It validates line endings: every CR
// must be immediately followed by LF and every LF must be preceded by CR.
// On an invalid newline it returns an InvalidNewlineError, records a sticky
// error, and delivers nothing further; a relay must treat the message as
// unverified and must not forward it as DKIM-verified.
//
// Canonicalization is independent of where chunk boundaries fall: splitting
// one body differently across Write calls yields identical output.
func (c *Canonicalizer) Write(p []byte) (int, error) {
	if c.err != nil {
		return 0, c.err
	}
	if c.closed {
		return 0, io.ErrClosedPipe
	}

	consumed := 0
	fail := func(offset int) (int, error) {
		err := &InvalidNewlineError{Offset: offset}
		c.err = err
		return consumed, err
	}

	for len(p) > 0 {
		if c.crPending {
			// The previous chunk ended with CR; only LF completes the line.
			if p[0] != '\n' {
				return fail(0)
			}
			c.crPending = false
			if err := c.lineEnd(); err != nil {
				c.err = err
				return consumed, err
			}
			p = p[1:]
			consumed++
			continue
		}

		nextCR := indexByte(p, '\r')
		nextLF := indexByte(p, '\n')
		if nextLF >= 0 && (nextCR < 0 || nextLF < nextCR) {
			return fail(consumed + nextLF)
		}
		if nextCR < 0 {
			// No line ending inside this chunk.
			if err := c.emitSegment(p); err != nil {
				c.err = err
				return consumed, err
			}
			consumed += len(p)
			p = nil
			break
		}

		// Emit the segment before the CR.
		if err := c.emitSegment(p[:nextCR]); err != nil {
			c.err = err
			return consumed, err
		}
		consumed += nextCR
		p = p[nextCR:]

		if len(p) == 1 {
			// CR is the last byte; its LF must arrive in the next Write.
			c.crPending = true
			consumed++
			p = nil
			break
		}
		if p[1] != '\n' {
			return fail(consumed)
		}
		if err := c.lineEnd(); err != nil {
			c.err = err
			return consumed, err
		}
		// Consume CRLF.
		p = p[2:]
		consumed += 2
	}

	return consumed, nil
}

func indexByte(p []byte, b byte) int {
	for i, x := range p {
		if x == b {
			return i
		}
	}
	return -1
}

// Reused scratch buffers keep per-byte processing allocation-free.
var (
	spaceByte = []byte{' '}
	crlfBytes = []byte{'\r', '\n'}
	oneByte   = make([]byte, 1)
	oneByteCR = []byte{'\r'}
)

// emitSegment feeds a chunk that contains no CR or LF bytes.
func (c *Canonicalizer) emitSegment(seg []byte) error {
	if len(seg) == 0 {
		return nil
	}
	if c.algo == Simple {
		return c.writeCanon(seg)
	}

	for _, b := range seg {
		switch {
		case b == ' ' || b == '\t':
			// Fold runs of WSP and hold the single replacement space until
			// it is known to be interior (not leading or trailing).
			c.relaxHoldSpace = true
		default:
			if c.relaxSeenContent {
				if c.relaxHoldSpace {
					if err := c.writeCanon(spaceByte); err != nil {
						return err
					}
				}
			} else {
				// Leading WSP of the line is dropped.
				c.relaxSeenContent = true
			}
			c.relaxHoldSpace = false
			oneByte[0] = b
			if err := c.writeCanon(oneByte); err != nil {
				return err
			}
		}
	}
	return nil
}

// lineEnd processes one complete CRLF.
func (c *Canonicalizer) lineEnd() error {
	// A CRLF at the limit boundary: if only one byte remains, emit the CR
	// now so the byte-prefix accounting stays exact; the pending path only
	// ever deals with whole (two-byte) CRLFs.
	if c.limit == 1 {
		if c.algo == Relaxed {
			c.relaxHoldSpace = false
			c.relaxSeenContent = false
		}
		if err := c.emit(oneByteCR); err != nil {
			return err
		}
		// Limit is now zero; the LF and everything after it is suppressed.
		return nil
	}

	if c.algo == Relaxed {
		// Trailing WSP is dropped: the held space is simply discarded.
		c.relaxHoldSpace = false
		if c.relaxSeenContent {
			c.relaxSeenContent = false
			c.relaxSeenLine = true
		}
		// Both content lines and empty (or WSP-only) lines keep their CRLF
		// pending until more content arrives or Close reduces the tail.
		c.pendingCRLF++
		return nil
	}

	// Simple: every line, including blank ones, is preserved; trailing blank
	// lines are held back as a count and replayed when more body follows.
	c.pendingCRLF++
	return nil
}

// writeCanon enforces the optional body length limit and forwards bytes.
func (c *Canonicalizer) writeCanon(b []byte) error {
	if len(b) == 0 || c.limit == 0 {
		return nil
	}
	if err := c.flushPending(); err != nil {
		return err
	}
	if c.limit == 0 {
		return nil
	}
	if c.limit > 0 && int64(len(b)) > c.limit {
		// Copy the permitted prefix into a local slice so package-level
		// scratch buffers are never permanently resliced.
		prefix := make([]byte, c.limit)
		copy(prefix, b[:c.limit])
		b = prefix
	}
	return c.emit(b)
}

// emit writes b to the sink and accounts for the body length limit. Callers
// must have applied the limit to b; emit never slices shared scratch buffers.
func (c *Canonicalizer) emit(b []byte) error {
	n, err := c.sink.Write(b)
	if err != nil {
		return err
	}
	c.emitted += int64(n)
	if c.limit > 0 {
		c.limit -= int64(n)
	}
	return nil
}

// flushPending replays delayed empty-line CRLFs before new canonical content.
// The length limit applies to the replayed bytes as well.
func (c *Canonicalizer) flushPending() error {
	if c.pendingCRLF == 0 {
		return nil
	}

	for c.pendingCRLF > 0 {
		if c.limit == 0 {
			c.pendingCRLF = 0
			return nil
		}
		if err := c.emit(crlfBytes); err != nil {
			return err
		}
		c.pendingCRLF--
	}
	return nil
}

// Close finishes canonicalization: trailing empty lines are reduced per the
// algorithm and the mandated final CRLF (if any) is emitted. It is safe to
// call after a failed Write; the sticky error is returned.
func (c *Canonicalizer) Close() error {
	if c.closed {
		return c.err
	}
	c.closed = true
	if c.err != nil {
		return c.err
	}
	if c.crPending {
		// A body ending with a dangling CR never had its LF.
		err := &InvalidNewlineError{Offset: 0}
		c.err = err
		return err
	}

	switch c.algo {
	case Simple:
		// Reduce all trailing empty lines to a single CRLF (RFC 6376 3.4.5).
		c.pendingCRLF = 0
		if err := c.writeCanon(crlfBytes); err != nil {
			c.err = err
			return err
		}
	case Relaxed:
		// Reduce all trailing empty lines; append one CRLF only when the body
		// had a non-empty line (RFC 6376 3.4.6). An empty body canonicalizes
		// to the empty string.
		if c.relaxSeenLine || c.relaxSeenContent {
			c.pendingCRLF = 0
			if err := c.writeCanon(crlfBytes); err != nil {
				c.err = err
				return err
			}
		} else {
			c.pendingCRLF = 0
		}
	}
	return nil
}

// Truncated reports whether the body length limit was reached, i.e. whether
// only a prefix of the canonical body was delivered to the sink.
func (c *Canonicalizer) Truncated() bool { return c.limit == 0 }

// Emitted returns the number of canonical bytes delivered to the sink so far.
func (c *Canonicalizer) Emitted() int64 { return c.emitted }

// Err returns the first sticky error encountered while canonicalizing, or nil.
func (c *Canonicalizer) Err() error { return c.err }

// Canonicalize canonicalizes a complete in-memory body. It is a convenience
// wrapper around the streaming Canonicalizer.
func Canonicalize(algo Algorithm, body []byte, opts ...Option) ([]byte, error) {
	var buf bytes.Buffer
	c := NewCanonicalizer(&buf, algo, opts...)
	if _, err := c.Write(body); err != nil {
		return nil, err
	}
	if err := c.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
