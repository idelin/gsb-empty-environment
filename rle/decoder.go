// Package rle decodes the archive importer's run-length encoding.
//
// The stream is a sequence of records. Each record is an unsigned
// varint (LEB128) count followed by a single value byte, and expands
// to count copies of the value. A zero count expands to nothing but
// still consumes the value byte. Non-minimal varint encodings (padded
// with zero continuation groups) are accepted; encodings that overflow
// a uint64 are rejected.
//
// The Decoder streams: it never buffers more than a fixed-size input
// window, and it never allocates memory proportional to a count, no
// matter how large the count is.
package rle

import (
	"errors"
	"fmt"
	"io"
)

// Sentinel errors. Use errors.Is against the error returned by Read;
// data errors are wrapped in *Error which carries the input offset.
var (
	// ErrCountOverflow reports a count varint wider than 64 bits.
	ErrCountOverflow = errors.New("count varint overflows uint64")
	// ErrOutputLimit reports that decoding would exceed
	// Options.MaxTotalBytes of cumulative output.
	ErrOutputLimit = errors.New("cumulative output limit exceeded")
	// ErrCanceled is returned by every Read after Cancel.
	ErrCanceled = errors.New("decoder canceled")
	// errNoProgress reports an underlying reader that keeps
	// returning (0, nil).
	errNoProgress = errors.New("underlying reader made no progress")
)

// Error describes a decode failure at a specific input position.
type Error struct {
	// Offset is the number of input bytes consumed when the error
	// was detected.
	Offset int64
	Err    error
}

func (e *Error) Error() string {
	return fmt.Sprintf("rle: input offset %d: %v", e.Offset, e.Err)
}

func (e *Error) Unwrap() error { return e.Err }

const (
	defaultMaxBuffer = 32 << 10
	inputBufSize     = 512
	maxEmptyReads    = 100
)

// Options configures a Decoder. Zero values pick defaults.
type Options struct {
	// MaxTotalBytes caps the cumulative decoded output across all
	// Read calls. Once the limit is reached and more output remains,
	// Read fails with ErrOutputLimit. Zero means unlimited.
	MaxTotalBytes int64
	// MaxBufferBytes caps how many bytes a single Read call may
	// produce; caller buffers are clamped to this size. Zero means
	// 32 KiB.
	MaxBufferBytes int
}

// Decoder is a streaming RLE decoder. It is not safe for concurrent
// use, except that Cancel may be called from any goroutine.
type Decoder struct {
	r        io.Reader
	maxTotal int64
	maxBuf   int

	in    []byte // fixed-size input window; nil after release
	off   int    // read position within in
	n     int    // valid bytes in in
	inOff int64  // total input bytes consumed
	eof   bool   // underlying reader returned EOF
	empty int    // consecutive (0, nil) reads from r

	// Incremental record parser state. Survives across input chunk
	// boundaries, so a count varint may span any number of reads
	// from the underlying reader.
	count     uint64 // accumulated count value
	shift     uint   // bit shift for the next varint group
	countN    int    // varint bytes consumed for the pending count
	haveCount bool   // count complete; next byte is the value
	value     byte   // value byte of the current record
	remain    uint64 // copies of value not yet emitted

	emitted int64 // cumulative output bytes

	canceled bool
	err      error // sticky terminal error
}

// NewDecoder returns a Decoder reading encoded data from r.
func NewDecoder(r io.Reader, opts Options) *Decoder {
	maxBuf := opts.MaxBufferBytes
	if maxBuf <= 0 {
		maxBuf = defaultMaxBuffer
	}
	return &Decoder{
		r:        r,
		maxTotal: opts.MaxTotalBytes,
		maxBuf:   maxBuf,
		in:       make([]byte, inputBufSize),
	}
}

// Cancel aborts decoding and immediately releases all internal state.
// Every subsequent Read returns ErrCanceled.
func (d *Decoder) Cancel() {
	d.canceled = true
	d.release()
}

// TotalEmitted reports how many output bytes have been produced so far.
func (d *Decoder) TotalEmitted() int64 { return d.emitted }

// Read decodes into p, which is clamped to Options.MaxBufferBytes.
// It returns io.EOF only at a clean record boundary; truncation
// mid-record yields io.ErrUnexpectedEOF. Data errors are sticky and
// wrapped in *Error.
func (d *Decoder) Read(p []byte) (int, error) {
	if d.canceled {
		return 0, ErrCanceled
	}
	if d.err != nil {
		return 0, d.err
	}
	if len(p) > d.maxBuf {
		p = p[:d.maxBuf]
	}
	if len(p) == 0 {
		return 0, nil
	}
	n := 0
	for n < len(p) {
		if d.remain > 0 {
			if d.maxTotal > 0 && d.emitted >= d.maxTotal {
				d.fail(ErrOutputLimit)
				break
			}
			m := uint64(len(p) - n)
			if d.remain < m {
				m = d.remain
			}
			if d.maxTotal > 0 {
				if left := uint64(d.maxTotal - d.emitted); left < m {
					m = left
				}
			}
			fill(p[n:n+int(m)], d.value)
			n += int(m)
			d.emitted += int64(m)
			d.remain -= m
			continue
		}
		b, err := d.next()
		if err != nil {
			if err == io.EOF {
				if d.countN > 0 || d.haveCount {
					// Ran out of input in the middle of
					// a count varint or before the value
					// byte of a complete count.
					d.fail(io.ErrUnexpectedEOF)
				} else {
					d.err = io.EOF
				}
			} else {
				d.fail(err)
			}
			break
		}
		if d.step(b) != nil {
			break
		}
	}
	if n > 0 {
		// Any error just recorded is sticky and surfaces on the
		// next call, so the caller never loses produced bytes.
		return n, nil
	}
	return 0, d.err
}

// step consumes one input byte of the current record. It returns a
// sticky error if the count varint overflows.
func (d *Decoder) step(b byte) error {
	if !d.haveCount {
		// The 10th varint byte (shift 63) may only be 0 or 1;
		// anything larger, or a continuation bit here, overflows
		// a uint64.
		if d.countN == 9 && b > 1 {
			return d.fail(ErrCountOverflow)
		}
		d.count |= uint64(b&0x7f) << d.shift
		d.countN++
		if b&0x80 == 0 {
			d.haveCount = true
		} else {
			d.shift += 7
		}
		return nil
	}
	d.value = b
	d.remain = d.count
	d.count = 0
	d.shift = 0
	d.countN = 0
	d.haveCount = false
	return nil
}

// next returns the next input byte, refilling the fixed-size window
// from the underlying reader as needed.
func (d *Decoder) next() (byte, error) {
	for d.off >= d.n {
		if d.eof {
			return 0, io.EOF
		}
		d.off, d.n = 0, 0
		nr, err := d.r.Read(d.in)
		d.n = nr
		if err == io.EOF {
			d.eof = true
		} else if err != nil {
			return 0, err
		}
		if d.n == 0 {
			if d.eof {
				return 0, io.EOF
			}
			d.empty++
			if d.empty > maxEmptyReads {
				return 0, errNoProgress
			}
			continue
		}
		d.empty = 0
	}
	b := d.in[d.off]
	d.off++
	d.inOff++
	return b, nil
}

// fail records a sticky, positioned error and releases internal state
// so a rejected stream never holds memory hostage.
func (d *Decoder) fail(err error) error {
	d.err = &Error{Offset: d.inOff, Err: err}
	d.release()
	return d.err
}

func (d *Decoder) release() {
	d.in = nil
	d.remain = 0
}

func fill(p []byte, v byte) {
	for i := range p {
		p[i] = v
	}
}
