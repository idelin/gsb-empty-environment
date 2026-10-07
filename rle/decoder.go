// Package rle implements a streaming decoder for run-length encoded
// archive payloads. Each record on the wire is an unsigned varint count
// followed by a single byte value; the count is the number of times the
// value repeats in the decoded output.
//
// The decoder never sizes allocations from the count field: output is
// written only into the caller's buffer, so a hostile or corrupt count
// cannot trigger a huge allocation. Counts may span arbitrary read
// blocks, and non-shortest varint encodings are accepted.
package rle

import (
	"context"
	"errors"
	"io"
)

var (
	// ErrCountOverflow reports a varint count that does not fit in uint64.
	ErrCountOverflow = errors.New("rle: varint count overflows uint64")
	// ErrOutputLimit reports that the cumulative output limit was reached.
	ErrOutputLimit = errors.New("rle: cumulative output limit exceeded")
)

// DefaultMaxBuffer is the per-Read emission cap used when Options.MaxBuffer
// is not set.
const DefaultMaxBuffer = 32 << 10

// Options configures a Decoder.
type Options struct {
	// MaxOutput caps the total number of bytes emitted across all Reads.
	// Zero means unlimited.
	MaxOutput uint64
	// MaxBuffer caps the number of bytes a single Read may emit. Values
	// <= 0 select DefaultMaxBuffer.
	MaxBuffer int
}

// Decoder decodes an RLE stream incrementally. It is not safe for
// concurrent use.
type Decoder struct {
	ctx context.Context
	src io.Reader

	maxOutput uint64
	emitted   uint64
	maxBuf    int

	// Current run being emitted.
	pending uint64
	value   byte
	haveRun bool

	// Partial varint state, preserved across underlying read blocks.
	accum uint64
	shift uint

	// Sticky terminal error (limit, overflow, truncation, EOF).
	err error
}

// NewDecoder returns a Decoder reading from src. Cancelling ctx makes
// Read return the context error and immediately releases decode state.
func NewDecoder(ctx context.Context, src io.Reader, opts Options) *Decoder {
	if ctx == nil {
		ctx = context.Background()
	}
	maxBuf := opts.MaxBuffer
	if maxBuf <= 0 {
		maxBuf = DefaultMaxBuffer
	}
	return &Decoder{
		ctx:       ctx,
		src:       src,
		maxOutput: opts.MaxOutput,
		maxBuf:    maxBuf,
	}
}

// Read decodes into p, emitting at most MaxBuffer bytes per call.
func (d *Decoder) Read(p []byte) (int, error) {
	if err := d.ctx.Err(); err != nil {
		d.release()
		return 0, err
	}
	if d.err != nil {
		return 0, d.err
	}
	if len(p) == 0 {
		return 0, nil
	}
	if len(p) > d.maxBuf {
		p = p[:d.maxBuf]
	}

	n := 0
	for n < len(p) {
		if !d.haveRun {
			if err := d.nextRun(); err != nil {
				d.err = err
				if n > 0 {
					return n, nil
				}
				return 0, err
			}
		}
		if d.maxOutput > 0 && d.emitted >= d.maxOutput {
			d.err = ErrOutputLimit
			if n > 0 {
				return n, nil
			}
			return 0, d.err
		}

		chunk := uint64(len(p) - n)
		if d.pending < chunk {
			chunk = d.pending
		}
		if d.maxOutput > 0 && d.maxOutput-d.emitted < chunk {
			chunk = d.maxOutput - d.emitted
		}
		for i := uint64(0); i < chunk; i++ {
			p[n+int(i)] = d.value
		}
		n += int(chunk)
		d.emitted += chunk
		d.pending -= chunk
		if d.pending == 0 {
			d.haveRun = false
		}
	}
	return n, nil
}

// nextRun consumes the next count+value record. Zero-count records are
// skipped; their value byte is still consumed.
func (d *Decoder) nextRun() error {
	for {
		count, err := d.readVarint()
		if err != nil {
			return err
		}
		value, err := d.readByte()
		if err != nil {
			if err == io.EOF {
				return io.ErrUnexpectedEOF
			}
			return err
		}
		if count == 0 {
			continue
		}
		d.pending = count
		d.value = value
		d.haveRun = true
		return nil
	}
}

// readVarint reads one unsigned varint, resuming across blocks via
// d.accum/d.shift. Non-shortest encodings are accepted.
func (d *Decoder) readVarint() (uint64, error) {
	for {
		b, err := d.readByte()
		if err != nil {
			if err == io.EOF && d.shift > 0 {
				d.resetVarint()
				return 0, io.ErrUnexpectedEOF
			}
			d.resetVarint()
			return 0, err
		}
		if d.shift == 63 && b > 1 {
			d.resetVarint()
			return 0, ErrCountOverflow
		}
		d.accum |= uint64(b&0x7f) << d.shift
		if b < 0x80 {
			v := d.accum
			d.resetVarint()
			return v, nil
		}
		d.shift += 7
	}
}

func (d *Decoder) readByte() (byte, error) {
	var one [1]byte
	if _, err := io.ReadFull(d.src, one[:]); err != nil {
		return 0, err
	}
	return one[0], nil
}

func (d *Decoder) resetVarint() {
	d.accum = 0
	d.shift = 0
}

// release drops all decode state, including the source reference, so a
// cancelled decoder retains nothing.
func (d *Decoder) release() {
	d.pending = 0
	d.haveRun = false
	d.resetVarint()
	d.src = nil
}
