package idset

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math/bits"
)

// Wire format (all integers little-endian):
//
//	uint32 numBlocks
//	per block, high keys strictly increasing:
//	  uint16 highKey
//	  uint8  containerType (0=array, 1=bitmap, 2=run)
//	  uint32 valueCount
//	  payload:
//	    array:  valueCount x uint16, strictly increasing
//	    bitmap: 8192 bytes, popcount == valueCount
//	    run:    uint32 numRuns, then numRuns x (uint16 lo, uint16 hi),
//	            lo <= hi, disjoint, non-adjacent, lengths sum to valueCount
//
// The encoding is canonical: every container must be the minimum-size
// representation (ties: array > run > bitmap) and empty blocks are absent.

// Encode returns the canonical encoding of s. Containers are canonicalized
// on the fly; s itself is not modified.
func (s *Set) Encode() []byte {
	n := 4
	canon := make([]*container, len(s.cons))
	for i, c := range s.cons {
		cc := c.canonical()
		canon[i] = cc
		n += 2 + 1 + 4 + cc.serializedSize()
		if cc.typ == cRun {
			n += 4
		}
	}
	buf := make([]byte, 0, n)
	buf = binary.LittleEndian.AppendUint32(buf, uint32(len(s.keys)))
	for i, hi := range s.keys {
		c := canon[i]
		buf = binary.LittleEndian.AppendUint16(buf, hi)
		buf = append(buf, byte(c.typ))
		buf = binary.LittleEndian.AppendUint32(buf, uint32(c.card))
		switch c.typ {
		case cArray:
			for _, v := range c.arr {
				buf = binary.LittleEndian.AppendUint16(buf, v)
			}
		case cBitmap:
			for _, w := range c.bm {
				buf = binary.LittleEndian.AppendUint64(buf, w)
			}
		default:
			buf = binary.LittleEndian.AppendUint32(buf, uint32(len(c.runs)))
			for _, r := range c.runs {
				buf = binary.LittleEndian.AppendUint16(buf, r.lo)
				buf = binary.LittleEndian.AppendUint16(buf, r.hi)
			}
		}
	}
	return buf
}

type decoder struct {
	b   []byte
	off int
}

func (d *decoder) u8() (uint8, error) {
	if d.off+1 > len(d.b) {
		return 0, errors.New("idset: truncated input")
	}
	v := d.b[d.off]
	d.off++
	return v, nil
}

func (d *decoder) u16() (uint16, error) {
	if d.off+2 > len(d.b) {
		return 0, errors.New("idset: truncated input")
	}
	v := binary.LittleEndian.Uint16(d.b[d.off:])
	d.off += 2
	return v, nil
}

func (d *decoder) u32() (uint32, error) {
	if d.off+4 > len(d.b) {
		return 0, errors.New("idset: truncated input")
	}
	v := binary.LittleEndian.Uint32(d.b[d.off:])
	d.off += 4
	return v, nil
}

func (d *decoder) u64() (uint64, error) {
	if d.off+8 > len(d.b) {
		return 0, errors.New("idset: truncated input")
	}
	v := binary.LittleEndian.Uint64(d.b[d.off:])
	d.off += 8
	return v, nil
}

// Decode parses and fully validates a canonical encoding: container value
// counts, array ordering, run overlap/adjacency, block key order and the
// canonical container choice are all checked.
func Decode(b []byte) (*Set, error) {
	d := &decoder{b: b}
	nblocks, err := d.u32()
	if err != nil {
		return nil, err
	}
	if nblocks > 65536 {
		return nil, fmt.Errorf("idset: implausible block count %d", nblocks)
	}
	s := &Set{}
	prevHi := -1
	for bi := 0; bi < int(nblocks); bi++ {
		hi, err := d.u16()
		if err != nil {
			return nil, err
		}
		if int(hi) <= prevHi {
			return nil, fmt.Errorf("idset: block keys not strictly increasing at block %d", bi)
		}
		prevHi = int(hi)
		typ, err := d.u8()
		if err != nil {
			return nil, err
		}
		if ctype(typ) != cArray && ctype(typ) != cBitmap && ctype(typ) != cRun {
			return nil, fmt.Errorf("idset: unknown container type %d", typ)
		}
		card, err := d.u32()
		if err != nil {
			return nil, err
		}
		if card == 0 || card > 65536 {
			return nil, fmt.Errorf("idset: invalid container cardinality %d", card)
		}
		var c *container
		var nruns int
		switch ctype(typ) {
		case cArray:
			arr := make([]uint16, card)
			for i := range arr {
				v, err := d.u16()
				if err != nil {
					return nil, err
				}
				if i > 0 && v <= arr[i-1] {
					return nil, fmt.Errorf("idset: array container not strictly increasing at block %d", bi)
				}
				arr[i] = v
				if i == 0 || uint32(v) != uint32(arr[i-1])+1 {
					nruns++
				}
			}
			c = &container{typ: cArray, card: int(card), arr: arr}
		case cBitmap:
			bm := new([bitmapWords]uint64)
			cnt := 0
			prevLast := false
			for w := 0; w < bitmapWords; w++ {
				word, err := d.u64()
				if err != nil {
					return nil, err
				}
				bm[w] = word
				cnt += bits.OnesCount64(word)
				if word != 0 {
					t := bits.OnesCount64(word &^ (word << 1))
					if prevLast && word&1 != 0 {
						t--
					}
					nruns += t
					prevLast = word>>63 != 0
				} else {
					prevLast = false
				}
			}
			if cnt != int(card) {
				return nil, fmt.Errorf("idset: bitmap popcount %d != cardinality %d at block %d", cnt, card, bi)
			}
			c = &container{typ: cBitmap, card: int(card), bm: bm}
		default: // cRun
			nr, err := d.u32()
			if err != nil {
				return nil, err
			}
			if nr == 0 || nr > 32768 {
				return nil, fmt.Errorf("idset: invalid run count %d at block %d", nr, bi)
			}
			runs := make([]ival, nr)
			total := 0
			for i := range runs {
				lo, err := d.u16()
				if err != nil {
					return nil, err
				}
				hi2, err := d.u16()
				if err != nil {
					return nil, err
				}
				if lo > hi2 {
					return nil, fmt.Errorf("idset: run with lo > hi at block %d", bi)
				}
				if i > 0 && uint32(lo) < uint32(runs[i-1].hi)+2 {
					return nil, fmt.Errorf("idset: runs overlap or are adjacent at block %d", bi)
				}
				runs[i] = ival{lo, hi2}
				total += int(hi2) - int(lo) + 1
			}
			if total != int(card) {
				return nil, fmt.Errorf("idset: run lengths sum %d != cardinality %d at block %d", total, card, bi)
			}
			nruns = int(nr)
			c = &container{typ: cRun, card: int(card), runs: runs}
		}
		if want := pickType(2*int(card), 4*nruns, bitmapBytes); want != ctype(typ) {
			return nil, fmt.Errorf("idset: non-canonical container type %d (want %d) at block %d", typ, want, bi)
		}
		s.keys = append(s.keys, hi)
		s.cons = append(s.cons, c)
		s.size += int(card)
	}
	if d.off != len(d.b) {
		return nil, fmt.Errorf("idset: %d trailing bytes", len(d.b)-d.off)
	}
	return s, nil
}
