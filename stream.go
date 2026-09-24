package llex

import (
	"errors"
	"fmt"
)

// StreamDecoder decodes a chunked file from arbitrarily fragmented input.
// Bytes may be fed in any size pieces and the emit callback may pause the
// decoder at any symbol (by returning false); neither changes the result,
// which depends only on the byte content. After a pause, call Write with
// a nil slice to resume.
type StreamDecoder struct {
	maxBlocks int
	maxOutput int

	buf     []byte
	entries []BlockEntry
	hdrLen  int
	parsed  bool
	next    int
	pending []string
	pendPos int
	emitted int
	done    bool
	err     error
}

// NewStreamDecoder creates a streaming decoder. maxBlocks caps the index
// size and maxOutput caps the total decoded symbols (both are hard limits
// that reject tampered lengths instead of over-allocating).
func NewStreamDecoder(maxBlocks, maxOutput int) *StreamDecoder {
	return &StreamDecoder{maxBlocks: maxBlocks, maxOutput: maxOutput}
}

// Write feeds p (possibly empty, e.g. to resume after a pause) and emits
// decoded symbols through emit. If emit returns false the symbol is not
// consumed: decoding pauses and the same symbol is offered again on the next
// call, so no symbol is lost or duplicated.
func (d *StreamDecoder) Write(p []byte, emit func(string) bool) error {
	if d.err != nil {
		return d.err
	}
	if emit == nil {
		return d.setErr(errors.New("llex: nil emit callback"))
	}
	d.buf = append(d.buf, p...)
	return d.pump(emit)
}

func (d *StreamDecoder) setErr(err error) error {
	d.err = err
	return err
}

func (d *StreamDecoder) pump(emit func(string) bool) error {
	for {
		// Drain any decoded-but-paused symbols first.
		for d.pendPos < len(d.pending) {
			if !emit(d.pending[d.pendPos]) {
				return nil // paused; resume with a later Write
			}
			d.pendPos++
			d.emitted++
		}
		d.pending = nil

		if d.done {
			if len(d.buf) > 0 {
				return d.setErr(fmt.Errorf("llex: %d trailing bytes after final block", len(d.buf)))
			}
			return nil
		}

		if !d.parsed {
			if len(d.buf) < fileFixedLen {
				return nil // need more bytes
			}
			nb := int(binaryLE32(d.buf[6:10]))
			if d.maxBlocks >= 0 && nb > d.maxBlocks {
				return d.setErr(fmt.Errorf("llex: block count %d exceeds limit %d", nb, d.maxBlocks))
			}
			need := fileFixedLen + indexEntryLen*nb
			if len(d.buf) < need {
				return nil // need more bytes
			}
			entries, hdrLen, err := parseIndex(d.buf[:need], d.maxBlocks)
			if err != nil {
				return d.setErr(err)
			}
			d.entries = entries
			d.hdrLen = hdrLen
			d.parsed = true
			d.buf = d.buf[need:]
		}

		if d.next == len(d.entries) {
			d.done = true
			continue
		}

		e := d.entries[d.next]
		expectOff := uint64(d.hdrLen)
		if d.next > 0 {
			prev := d.entries[d.next-1]
			expectOff = prev.Offset + uint64(prev.ByteLen)
		}
		if e.Offset != expectOff {
			return d.setErr(fmt.Errorf("llex: block %d starts at %d, expected contiguous offset %d", d.next, e.Offset, expectOff))
		}
		if uint64(len(d.buf)) < uint64(e.ByteLen) {
			return nil // need more bytes
		}
		remaining := d.maxOutput - d.emitted
		if d.maxOutput < 0 {
			remaining = -1
		}
		syms, consumed, err := DecodeBlockData(d.buf[:e.ByteLen], remaining)
		if err != nil {
			return d.setErr(fmt.Errorf("llex: block %d: %w", d.next, err))
		}
		if consumed != int(e.ByteLen) {
			return d.setErr(fmt.Errorf("llex: block %d consumed %d bytes, index declares %d", d.next, consumed, e.ByteLen))
		}
		if len(syms) != int(e.Symbols) {
			return d.setErr(fmt.Errorf("llex: block %d decoded %d symbols, index declares %d", d.next, len(syms), e.Symbols))
		}
		d.buf = d.buf[e.ByteLen:]
		d.next++
		d.pending = syms
		d.pendPos = 0
	}
}

func binaryLE32(b []byte) uint32 {
	return uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24
}
