package prefixcode

// Decoder incrementally decodes a stream of canonical prefix codes.
// Bits are read most-significant-bit first per byte. State (bit buffer,
// table level, remaining output quota, expected symbol count) persists
// across Decode calls, so input may be split at arbitrary byte boundaries
// without affecting the decoded output.
type Decoder struct {
	t         *Table
	buf       uint64 // pending bits, right-aligned, count in nbits
	nbits     int
	cur       int32 // current table level: -1 = root, else index into t.subs
	quota     int64 // remaining output budget, shared across calls; <0 = unlimited
	remaining int64 // expected symbols left; <0 = unknown (strict mode)
	done      bool
}

// DecoderOption configures a Decoder.
type DecoderOption func(*Decoder)

// WithQuota bounds the total number of symbols the decoder will ever emit
// across all Decode calls. A negative value means unlimited.
func WithQuota(q int64) DecoderOption {
	return func(d *Decoder) { d.quota = q }
}

// WithSymbolCount declares the exact number of symbols in the message.
// After the last symbol, only zero padding bits (fewer than 8) may remain.
// A negative value selects strict mode: the bit stream must end exactly on
// a codeword boundary.
func WithSymbolCount(n int64) DecoderOption {
	return func(d *Decoder) { d.remaining = n }
}

// NewDecoder creates a streaming decoder for table t.
func NewDecoder(t *Table, opts ...DecoderOption) *Decoder {
	d := &Decoder{t: t, cur: -1, quota: -1, remaining: -1}
	for _, o := range opts {
		o(d)
	}
	return d
}

// Quota returns the remaining output budget (negative means unlimited).
func (d *Decoder) Quota() int64 { return d.quota }

// AddQuota grants n more symbols of output budget.
func (d *Decoder) AddQuota(n int64) {
	if d.quota >= 0 {
		d.quota += n
	}
}

// SymbolsRemaining returns the expected symbols left (negative if unknown).
func (d *Decoder) SymbolsRemaining() int64 { return d.remaining }

// Done reports whether the message was fully and cleanly decoded.
func (d *Decoder) Done() bool { return d.done }

const (
	actNeedInput = iota
	actEmit
	actDescend
	actDone
)

// peek inspects the buffered bits without mutating state. final must be
// true when no more input bytes will ever arrive.
func (d *Decoder) peek(final bool) (act, sym, consume int, sub int32, err error) {
	if d.remaining == 0 {
		// Message complete: only short zero padding may remain.
		if d.nbits >= 8 || d.buf&mask64(d.nbits) != 0 {
			return 0, 0, 0, 0, ErrTrailingData
		}
		if final {
			return actDone, 0, 0, 0, nil
		}
		return actNeedInput, 0, 0, 0, nil
	}
	if d.nbits == 0 {
		if final {
			if d.remaining > 0 {
				return 0, 0, 0, 0, ErrTruncated
			}
			return actDone, 0, 0, 0, nil
		}
		return actNeedInput, 0, 0, 0, nil
	}
	tbl := d.t.root
	if d.cur >= 0 {
		tbl = d.t.subs[d.cur]
	}
	avail := d.nbits
	if avail > levelBits {
		avail = levelBits
	}
	idx := int(d.buf>>uint(d.nbits-avail)) << uint(levelBits-avail)
	e := tbl[idx]
	switch {
	case e.sym >= 0:
		if int(e.consume) > d.nbits {
			// The buffered bits are a proper prefix of a codeword.
			if final {
				return 0, 0, 0, 0, ErrTruncated
			}
			return actNeedInput, 0, 0, 0, nil
		}
		return actEmit, int(e.sym), int(e.consume), 0, nil
	case e.sub >= 0:
		if d.nbits < levelBits {
			// A long codeword started but its bits are missing.
			if final {
				return 0, 0, 0, 0, ErrTruncated
			}
			return actNeedInput, 0, 0, 0, nil
		}
		return actDescend, 0, levelBits, e.sub, nil
	default:
		// No codeword starts with these bits: unknown or overlong code.
		return 0, 0, 0, 0, ErrInvalidCode
	}
}

// Decode consumes bytes from src and writes decoded symbols to dst. It
// returns the number of bytes consumed and symbols produced. Pass finish =
// true with the final chunk; a truncated tail is then reported as
// ErrTruncated and never silently accepted. ErrQuota and ErrShortBuffer
// leave the decoder in a resumable state: grant quota or provide a larger
// dst and call again with the unconsumed input.
func (d *Decoder) Decode(src, dst []byte, finish bool) (consumed, produced int, err error) {
	for {
		for consumed < len(src) && d.nbits <= 56 {
			d.buf = d.buf<<8 | uint64(src[consumed])
			d.nbits += 8
			consumed++
		}
		if d.done {
			if consumed < len(src) {
				return consumed, produced, ErrTrailingData
			}
			return consumed, produced, nil
		}
		final := finish && consumed == len(src)
		act, sym, consume, sub, err := d.peek(final)
		if err != nil {
			return consumed, produced, err
		}
		switch act {
		case actDone:
			d.done = true
			d.buf, d.nbits = 0, 0
			return consumed, produced, nil
		case actNeedInput:
			return consumed, produced, nil
		case actDescend:
			d.nbits -= levelBits
			d.buf &= mask64(d.nbits)
			d.cur = sub
		case actEmit:
			if d.quota == 0 {
				return consumed, produced, ErrQuota
			}
			if produced == len(dst) {
				return consumed, produced, ErrShortBuffer
			}
			d.nbits -= consume
			d.buf &= mask64(d.nbits)
			d.cur = -1
			dst[produced] = byte(sym)
			produced++
			if d.quota > 0 {
				d.quota--
			}
			if d.remaining > 0 {
				d.remaining--
			}
		}
	}
}
