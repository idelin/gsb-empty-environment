package ackset

import (
	"bytes"
	"encoding/binary"
	"fmt"
)

type reader struct{ b []byte }

func (r *reader) byte() (byte, error) {
	if len(r.b) == 0 {
		return 0, ErrTruncated
	}
	c := r.b[0]
	r.b = r.b[1:]
	return c, nil
}

func (r *reader) uint32() (uint32, error) {
	if len(r.b) < 4 {
		return 0, ErrTruncated
	}
	v := binary.BigEndian.Uint32(r.b)
	r.b = r.b[4:]
	return v, nil
}

func (r *reader) uvarint() (uint64, error) {
	v, n := binary.Uvarint(r.b)
	if n == 0 {
		return 0, ErrTruncated
	}
	if n < 0 {
		return 0, fmt.Errorf("%w: varint overflow", ErrMalformed)
	}
	r.b = r.b[n:]
	return v, nil
}

func (r *reader) bytes(n uint64) ([]byte, error) {
	if uint64(len(r.b)) < n {
		return nil, ErrTruncated
	}
	s := r.b[:n]
	r.b = r.b[n:]
	return s, nil
}

// Decode parses a canonical encoding and returns its elements in
// ascending modular order starting from the first segment (i.e. sorted
// up to a rotation at sequence number wraparound). It rejects
// overlapping segments and any non-canonical form.
func Decode(data []byte) ([]uint32, error) {
	if len(data) == 0 {
		return nil, ErrTruncated
	}
	switch data[0] {
	case modeEmpty:
		if len(data) != 1 {
			return nil, ErrTrailing
		}
		return nil, nil
	case modeSegments:
	default:
		return nil, fmt.Errorf("%w: unknown mode %d", ErrMalformed, data[0])
	}

	r := &reader{b: data[1:]}
	nseg, err := r.uvarint()
	if err != nil {
		return nil, err
	}
	if nseg == 0 {
		return nil, fmt.Errorf("%w: zero segments", ErrNotCanonical)
	}

	var out []uint32
	var prevLast uint32
	var span uint64
	total := 0

	for k := uint64(0); k < nseg; k++ {
		typ, err := r.byte()
		if err != nil {
			return nil, err
		}
		if typ != segRange && typ != segBitmap {
			return nil, fmt.Errorf("%w: unknown segment type %d", ErrMalformed, typ)
		}
		start, err := r.uint32()
		if err != nil {
			return nil, err
		}

		var segLen uint64
		switch typ {
		case segRange:
			c, err := r.uvarint()
			if err != nil {
				return nil, err
			}
			if c == 0 {
				return nil, fmt.Errorf("%w: empty range", ErrNotCanonical)
			}
			if c > MaxWindow {
				return nil, fmt.Errorf("%w: range too large", ErrMalformed)
			}
			segLen = c
			if c > MaxElements || total > MaxElements-int(c) {
				return nil, ErrTooLarge
			}
			for d := uint64(0); d < c; d++ {
				out = append(out, start+uint32(d))
			}
			total += int(c)
		case segBitmap:
			nb, err := r.uvarint()
			if err != nil {
				return nil, err
			}
			if nb < 2 {
				return nil, fmt.Errorf("%w: bitmap shorter than 2 bits", ErrNotCanonical)
			}
			if nb > MaxBitmapBits {
				return nil, fmt.Errorf("%w: bitmap longer than %d bits", ErrNotCanonical, MaxBitmapBits)
			}
			buf, err := r.bytes((nb + 7) / 8)
			if err != nil {
				return nil, err
			}
			if rem := nb % 8; rem != 0 && buf[len(buf)-1]>>rem != 0 {
				return nil, fmt.Errorf("%w: nonzero padding bits", ErrNotCanonical)
			}
			if buf[0]&1 == 0 {
				return nil, fmt.Errorf("%w: bitmap starts with a zero bit", ErrNotCanonical)
			}
			if buf[(nb-1)/8]>>(uint((nb-1)%8))&1 == 0 {
				return nil, fmt.Errorf("%w: bitmap ends with a zero bit", ErrNotCanonical)
			}
			segLen = nb
			ones := 0
			for d := uint64(0); d < nb; d++ {
				if buf[d/8]>>(uint(d)%8)&1 != 0 {
					if total == MaxElements {
						return nil, ErrTooLarge
					}
					out = append(out, start+uint32(d))
					total++
					ones++
				}
			}
			if uint64(ones) == nb {
				return nil, fmt.Errorf("%w: all-ones bitmap, use a range", ErrNotCanonical)
			}
		}

		if k == 0 {
			span = segLen - 1
		} else {
			d := start - prevLast // mod 2^32
			switch {
			case d == 0:
				return nil, ErrOverlap
			case uint64(d) >= MaxWindow:
				return nil, fmt.Errorf("%w: segment out of order", ErrOverlap)
			}
			span += uint64(d) + segLen - 1
			if span >= MaxWindow {
				return nil, ErrTooSparse
			}
		}
		prevLast = start + uint32(segLen) - 1 // mod 2^32
	}

	if len(r.b) != 0 {
		return nil, ErrTrailing
	}

	// Canonicality: the encoding must be the byte-minimal one.
	enc, err := Encode(out)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNotCanonical, err)
	}
	if !bytes.Equal(enc, data) {
		return nil, fmt.Errorf("%w: not the byte-minimal encoding", ErrNotCanonical)
	}
	return out, nil
}
