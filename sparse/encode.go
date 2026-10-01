package sparse

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// On-disk encoding layout. Unsigned integers are uvarint, signed integers
// are zigzag varint. Positions are delta encoded against the previous
// position, so a run of nearby exceptions costs ~1-2 bytes of position each.
//
//	header:
//	  uvarint  logical length n
//	  zigzag   default value
//	  byte     nonNeg flag (0 or 1)
//	  uvarint  current layer sequence number
//	  uvarint  number of layers L
//	layers, L times:
//	  uvarint  lo
//	  uvarint  hi - lo
//	  zigzag   delta
//	  uvarint  seq
//	exceptions:
//	  uvarint  number of exceptions E
//	  per exception, sorted by pos:
//	    uvarint  pos - prevPos   (prevPos starts at 0)
//	    zigzag   val
//	    uvarint  wm
//
// Physical size is O(E + L) bytes, independent of the logical length n.

func appendUvarint(dst []byte, x uint64) []byte {
	var buf [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(buf[:], x)
	return append(dst, buf[:n]...)
}

func appendVarint(dst []byte, x int64) []byte {
	var buf [binary.MaxVarintLen64]byte
	n := binary.PutVarint(buf[:], x)
	return append(dst, buf[:n]...)
}

// Encode serializes the view into the byte layout documented above.
func (v *view) Encode() []byte {
	out := make([]byte, 0, 32+8*len(v.exc))
	out = appendUvarint(out, v.n)
	out = appendVarint(out, v.def)
	if v.nonNeg {
		out = append(out, 1)
	} else {
		out = append(out, 0)
	}
	seq := uint64(0)
	for _, l := range v.layers {
		if l.seq > seq {
			seq = l.seq
		}
	}
	out = appendUvarint(out, seq)
	out = appendUvarint(out, uint64(len(v.layers)))
	for _, l := range v.layers {
		out = appendUvarint(out, l.lo)
		out = appendUvarint(out, l.hi-l.lo)
		out = appendVarint(out, l.delta)
		out = appendUvarint(out, l.seq)
	}
	out = appendUvarint(out, uint64(len(v.exc)))
	prev := uint64(0)
	for _, e := range v.exc {
		out = appendUvarint(out, e.pos-prev)
		out = appendVarint(out, e.val)
		out = appendUvarint(out, e.wm)
		prev = e.pos
	}
	return out
}

// PhysicalBytes returns the encoded size in bytes.
func (v *view) PhysicalBytes() int { return len(v.Encode()) }

type decoder struct {
	b   []byte
	off int
}

func (d *decoder) uvarint() (uint64, error) {
	x, n := binary.Uvarint(d.b[d.off:])
	if n <= 0 {
		return 0, errors.New("sparse: truncated uvarint")
	}
	d.off += n
	return x, nil
}

func (d *decoder) varint() (int64, error) {
	x, n := binary.Varint(d.b[d.off:])
	if n <= 0 {
		return 0, errors.New("sparse: truncated varint")
	}
	d.off += n
	return x, nil
}

// Decode parses bytes produced by Encode back into a mutable Array.
func Decode(b []byte) (*Array, error) {
	d := &decoder{b: b}
	n, err := d.uvarint()
	if err != nil {
		return nil, err
	}
	def, err := d.varint()
	if err != nil {
		return nil, err
	}
	if d.off >= len(d.b) {
		return nil, errors.New("sparse: truncated nonNeg flag")
	}
	nonNeg := d.b[d.off] == 1
	d.off++
	seq, err := d.uvarint()
	if err != nil {
		return nil, err
	}
	nl, err := d.uvarint()
	if err != nil {
		return nil, err
	}
	layers := make([]layer, 0, nl)
	for i := uint64(0); i < nl; i++ {
		lo, err := d.uvarint()
		if err != nil {
			return nil, err
		}
		span, err := d.uvarint()
		if err != nil {
			return nil, err
		}
		delta, err := d.varint()
		if err != nil {
			return nil, err
		}
		lseq, err := d.uvarint()
		if err != nil {
			return nil, err
		}
		layers = append(layers, layer{lo: lo, hi: lo + span, delta: delta, seq: lseq})
	}
	ne, err := d.uvarint()
	if err != nil {
		return nil, err
	}
	exc := make([]entry, 0, ne)
	prev := uint64(0)
	for i := uint64(0); i < ne; i++ {
		delta, err := d.uvarint()
		if err != nil {
			return nil, err
		}
		val, err := d.varint()
		if err != nil {
			return nil, err
		}
		wm, err := d.uvarint()
		if err != nil {
			return nil, err
		}
		pos := prev + delta
		if pos >= n {
			return nil, fmt.Errorf("sparse: exception position %d out of range [0,%d)", pos, n)
		}
		exc = append(exc, entry{pos: pos, val: val, wm: wm})
		prev = pos
	}
	if d.off != len(d.b) {
		return nil, errors.New("sparse: trailing bytes after encoding")
	}
	return &Array{
		view: view{n: n, def: def, exc: exc, layers: layers, nonNeg: nonNeg},
		seq:  seq,
	}, nil
}
