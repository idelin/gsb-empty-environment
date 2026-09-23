package huffcodec

import "errors"

var (
	// ErrQuotaExceeded is returned when the decoder's shared output budget
	// is exhausted while more output is pending.
	ErrQuotaExceeded = errors.New("huffcodec: output budget exceeded")
	// ErrIncompleteCodeword is returned by Finish when the input ends in the
	// middle of a codeword (truncated tail).
	ErrIncompleteCodeword = errors.New("huffcodec: input ends mid-codeword")
	// ErrInvalidCode is returned when buffered bits match no codeword
	// (overlong or corrupted stream).
	ErrInvalidCode = errors.New("huffcodec: bits match no codeword")
)

type peekState int

const (
	stOK peekState = iota
	stNeedMore
	stInvalid
)

// StreamDecoder decodes a prefix-code bitstream incrementally. Bit state is
// kept across calls, so input may be chopped into arbitrary chunks. The
// output budget is shared across all Decode calls on the same decoder; a
// negative budget means unlimited.
type StreamDecoder struct {
	tab      *decodeTab
	acc      uint64 // buffered bits, right-aligned; oldest bits highest
	nbits    int
	budget   int64
	Produced int64 // total symbols emitted so far
}

// NewStreamDecoder validates the code lengths and builds a layered decode
// table. budget caps the total number of output bytes across all Decode
// calls; pass a negative value for unlimited output.
func NewStreamDecoder(lengths []uint8, budget int64) (*StreamDecoder, error) {
	codes, err := CanonicalCodes(lengths)
	if err != nil {
		return nil, err
	}
	tab, err := buildDecodeTable(codes)
	if err != nil {
		return nil, err
	}
	return &StreamDecoder{tab: tab, budget: budget}, nil
}

// peek inspects the buffered bits without consuming them. When fewer than
// levelBits bits are available the index is zero-padded, and only terminal
// entries whose full length is present are accepted.
func (d *StreamDecoder) peek() (sym int, length int, st peekState) {
	tab := d.tab
	off := 0
	for {
		avail := d.nbits - off
		if avail <= 0 {
			return 0, 0, stNeedMore
		}
		var idx uint64
		if avail >= levelBits {
			idx = (d.acc >> uint(avail-levelBits)) & (1<<levelBits - 1)
		} else {
			rem := d.acc & (1<<uint(avail) - 1)
			idx = rem << uint(levelBits-avail)
		}
		e := tab.entries[idx]
		switch {
		case e.term:
			if int(e.length) <= avail {
				return int(e.sym), off + int(e.length), stOK
			}
			return 0, 0, stNeedMore
		case e.sub != nil:
			if avail < levelBits {
				return 0, 0, stNeedMore
			}
			tab = e.sub
			off += levelBits
		default:
			return 0, 0, stInvalid
		}
	}
}

// drain emits as many symbols as the buffered bits, the out slice and the
// remaining budget allow.
func (d *StreamDecoder) drain(out []byte) (int, error) {
	produced := 0
	for {
		sym, n, st := d.peek()
		switch st {
		case stInvalid:
			return produced, ErrInvalidCode
		case stNeedMore:
			return produced, nil
		}
		if produced == len(out) {
			return produced, nil
		}
		if d.budget == 0 {
			return produced, ErrQuotaExceeded
		}
		out[produced] = byte(sym)
		produced++
		d.Produced++
		if d.budget > 0 {
			d.budget--
		}
		d.nbits -= n
		d.acc &= 1<<uint(d.nbits) - 1
	}
}

// Decode consumes src and writes decoded bytes into out, returning how many
// input bytes were consumed and output bytes were produced. Input may be
// delivered in arbitrary chunks; unconsumed bits remain buffered for the
// next call. If out fills while symbols are still pending, Decode returns
// with produced == len(out) and a nil error; call again with a fresh out.
func (d *StreamDecoder) Decode(src, out []byte) (consumed, produced int, err error) {
	for consumed < len(src) {
		p, err := d.drain(out[produced:])
		produced += p
		if err != nil {
			return consumed, produced, err
		}
		if produced == len(out) {
			return consumed, produced, nil
		}
		// peek() found no valid codeword in the buffered bits; if we
		// already hold a full maximum-length code's worth of bits, the
		// stream can never decode and is corrupt.
		if d.nbits >= MaxCodeLength {
			return consumed, produced, ErrInvalidCode
		}
		d.acc = d.acc<<8 | uint64(src[consumed])
		d.nbits += 8
		consumed++
	}
	p, err := d.drain(out[produced:])
	produced += p
	return consumed, produced, err
}

// DecodeBits feeds n bits (the low bits of v, MSB first) and drains into
// out. It allows bit-granular streaming on top of the byte-oriented Decode.
func (d *StreamDecoder) DecodeBits(v uint32, n uint, out []byte) (int, error) {
	if d.nbits+int(n) > 64 {
		return 0, ErrInvalidCode
	}
	d.acc = d.acc<<n | uint64(v)&(1<<n-1)
	d.nbits += int(n)
	return d.drain(out)
}

// Finish reports whether the buffered trailing bits are consistent with a
// clean stream end. It distinguishes a truncated tail (the remaining bits
// are a proper prefix of some codeword: ErrIncompleteCodeword) from bits
// that match no codeword at all (ErrInvalidCode). Callers should drain
// pending output with Decode before calling Finish.
func (d *StreamDecoder) Finish() error {
	if d.nbits == 0 {
		return nil
	}
	_, _, st := d.peek()
	if st == stInvalid {
		return ErrInvalidCode
	}
	return ErrIncompleteCodeword
}
