package strdict

import (
	"encoding/binary"
	"errors"
	"fmt"
)

var (
	// ErrBudgetExceeded is sticky: once a decode session exceeds its byte
	// budget it refuses any further expansion.
	ErrBudgetExceeded = errors.New("strdict: byte budget exceeded")
	// ErrRecordTooLarge reports an inline record whose declared length breaks
	// the per-record cap; detected before any allocation for the payload.
	ErrRecordTooLarge = errors.New("strdict: record length exceeds per-record cap")
	// ErrBadOpcode reports an unknown record opcode.
	ErrBadOpcode = errors.New("strdict: unknown record opcode")
	// ErrStreamEnded reports Decode calls after the END record.
	ErrStreamEnded = errors.New("strdict: stream already ended")
	// ErrCorruptLength reports an unparseable or overflowing length varint.
	ErrCorruptLength = errors.New("strdict: corrupt length prefix")
)

// DecoderOptions configures a streaming decode session.
type DecoderOptions struct {
	// Version pins the initial dictionary generation for the whole session.
	Version uint64
	// MaxBytes is the total byte budget: pinned dictionary payload + staging
	// buffer + all decoded output strings. Zero means unlimited.
	MaxBytes int64
	// MaxRecordBytes caps any single inline string. Zero means the budget is
	// the only limit.
	MaxRecordBytes int64
}

// Decoder incrementally decodes a record stream. Feed arbitrary chunks; record
// boundaries never have to align with chunk boundaries. Not safe for
// concurrent use.
type Decoder struct {
	reg       *Registry
	maxBytes  int64
	maxRecord int64

	lease     *Lease // pinned generation; replaced only at SWITCH records
	dictBytes int64  // bytes charged for the pinned dictionary
	buf       []byte // staging for incomplete records
	emitted   int64  // bytes of all decoded output so far
	ended     bool
	sticky    error
}

// NewDecoder pins opts.Version and charges its dictionary bytes to the budget.
func NewDecoder(reg *Registry, opts DecoderOptions) (*Decoder, error) {
	lease, err := reg.Acquire(opts.Version)
	if err != nil {
		return nil, err
	}
	d := &Decoder{reg: reg, maxBytes: opts.MaxBytes, maxRecord: opts.MaxRecordBytes, lease: lease}
	if dict, err := lease.Dict(); err == nil {
		d.dictBytes = dict.Bytes()
	}
	if d.over(0) {
		lease.Release()
		return nil, ErrBudgetExceeded
	}
	return d, nil
}

// used is the current charge: dictionary + staging + decoded output.
func (d *Decoder) used() int64 { return d.dictBytes + int64(len(d.buf)) + d.emitted }

// over reports whether charging extra bytes would exceed the budget.
func (d *Decoder) over(extra int64) bool {
	return d.maxBytes > 0 && d.used()+extra > d.maxBytes
}

func (d *Decoder) fail(err error) error {
	if d.sticky == nil {
		d.sticky = err
	}
	return d.sticky
}

// Decode feeds one chunk and returns the strings completed by it. A sticky
// error permanently fails the session.
func (d *Decoder) Decode(chunk []byte) ([]string, error) {
	if d.sticky != nil {
		return nil, d.sticky
	}
	if d.ended {
		return nil, d.fail(ErrStreamEnded)
	}
	if d.over(int64(len(chunk))) {
		return nil, d.fail(ErrBudgetExceeded)
	}
	d.buf = append(d.buf, chunk...)

	var out []string
	pos := 0
	for pos < len(d.buf) {
		n, err, ready := d.parseRecord(d.buf[pos:])
		if err != nil {
			return nil, d.fail(err)
		}
		if !ready {
			break // incomplete record; wait for more bytes
		}
		if err := d.execRecord(d.buf[pos:pos+n], &out); err != nil {
			return nil, d.fail(err)
		}
		pos += n
		if d.ended {
			break
		}
	}
	if pos > 0 {
		d.buf = append(d.buf[:0], d.buf[pos:]...) // release staging charge
	}
	return out, nil
}

// parseRecord inspects the head of b. It returns (recordLen, true) when a
// complete record is buffered, (0, false) when more bytes are needed, and a
// non-nil error for failures detectable BEFORE the payload arrives — in
// particular, declared inline lengths are validated against the per-record
// cap and the remaining byte budget before any allocation happens.
func (d *Decoder) parseRecord(b []byte) (n int, err error, ready bool) {
	switch b[0] {
	case opInline:
		l, hl := binary.Uvarint(b[1:])
		if hl == 0 {
			if len(b)-1 >= maxLenBytes {
				return 0, ErrCorruptLength, false
			}
			return 0, nil, false // length prefix still incomplete
		}
		if hl < 0 {
			return 0, ErrCorruptLength, false
		}
		if d.maxRecord > 0 && l > uint64(d.maxRecord) {
			return 0, ErrRecordTooLarge, false
		}
		if d.over(int64(l)) {
			return 0, ErrBudgetExceeded, false
		}
		total := 1 + hl + int(l)
		if len(b) < total {
			return 0, nil, false // payload still incomplete; staging is charged
		}
		return total, nil, true
	case opRef, opSwitch:
		_, hl := binary.Uvarint(b[1:])
		if hl == 0 {
			if len(b)-1 >= maxLenBytes {
				return 0, ErrCorruptLength, false
			}
			return 0, nil, false
		}
		if hl < 0 {
			return 0, ErrCorruptLength, false
		}
		return 1 + hl, nil, true
	case opEnd:
		return 1, nil, true
	default:
		return 0, fmt.Errorf("%w: 0x%02x", ErrBadOpcode, b[0]), false
	}
}

// execRecord applies one fully-buffered record.
func (d *Decoder) execRecord(rec []byte, out *[]string) error {
	switch rec[0] {
	case opInline:
		l, hl := binary.Uvarint(rec[1:])
		s := string(rec[1+hl : 1+hl+int(l)])
		d.emitted += int64(len(s))
		*out = append(*out, s)
		return nil
	case opRef:
		idx, _ := binary.Uvarint(rec[1:])
		s, err := d.lease.Lookup(int(idx))
		if err != nil {
			return err // unknown index is never silently an empty string
		}
		if d.over(int64(len(s))) {
			return ErrBudgetExceeded
		}
		d.emitted += int64(len(s))
		*out = append(*out, s)
		return nil
	case opSwitch:
		vers, _ := binary.Uvarint(rec[1:])
		lease, err := d.reg.Acquire(vers)
		if err != nil {
			return err
		}
		dict, err := lease.Dict()
		if err != nil {
			lease.Release()
			return err
		}
		old := d.dictBytes
		d.dictBytes = dict.Bytes()
		if d.over(0) {
			d.dictBytes = old
			lease.Release()
			return ErrBudgetExceeded
		}
		d.lease.Release()
		d.lease = lease
		return nil
	case opEnd:
		d.ended = true
		return nil
	default:
		return fmt.Errorf("%w: 0x%02x", ErrBadOpcode, rec[0])
	}
}

// Ended reports whether the END record has been consumed.
func (d *Decoder) Ended() bool { return d.ended }

// Close releases the pinned read lease.
func (d *Decoder) Close() {
	if d.lease != nil {
		d.lease.Release()
		d.lease = nil
	}
}
