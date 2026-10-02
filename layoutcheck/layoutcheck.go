// Package layoutcheck is an independent byte-layout checker. It re-derives
// the validated regions of a compact index image from the raw bytes with
// its own parser (it does not share code with package compactidx), and can
// then verify that a set of observed read accesses never touched bytes
// outside those regions.
package layoutcheck

import (
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
)

// Range is a half-open byte range [Start, End).
type Range struct {
	Start uint64
	End   uint64
}

const (
	headerLen      = 48
	formatVersion  = 1
	tableEntrySize = 8
	linkNone       = 0x00
	linkMore       = 0x01
)

var magic = [4]byte{'C', 'I', 'D', 'X'}

var ErrInvalid = errors.New("layoutcheck: image is not a valid compact index")

// Checker holds the independently derived validated layout of one image.
type Checker struct {
	regions []Range
}

// New parses data and derives the set of byte ranges a reader is allowed
// to touch: the header, the offset table, and every record extent reached
// by walking the offset graph. It fails on any structural problem.
func New(data []byte) (*Checker, error) {
	if len(data) < headerLen {
		return nil, fmt.Errorf("%w: shorter than %d-byte header", ErrInvalid, headerLen)
	}
	if string(data[0:4]) != string(magic[:]) {
		return nil, fmt.Errorf("%w: bad magic", ErrInvalid)
	}
	if binary.LittleEndian.Uint16(data[4:6]) != formatVersion {
		return nil, fmt.Errorf("%w: bad version", ErrInvalid)
	}
	count := binary.LittleEndian.Uint32(data[12:16])
	tableOff := binary.LittleEndian.Uint64(data[16:24])
	dataOff := binary.LittleEndian.Uint64(data[24:32])
	dataLen := binary.LittleEndian.Uint64(data[32:40])
	fileLen := uint64(len(data))

	tableBytes := uint64(count) * tableEntrySize
	if count != 0 && tableBytes/uint64(count) != tableEntrySize {
		return nil, fmt.Errorf("%w: table size overflow", ErrInvalid)
	}
	tableEnd := tableOff + tableBytes
	if tableEnd < tableOff || tableEnd > fileLen {
		return nil, fmt.Errorf("%w: table out of file", ErrInvalid)
	}
	dataEnd := dataOff + dataLen
	if dataEnd < dataOff || dataEnd > fileLen {
		return nil, fmt.Errorf("%w: data region out of file", ErrInvalid)
	}

	c := &Checker{}
	c.add(Range{0, headerLen})
	c.add(Range{tableOff, tableEnd})
	// The declared data region must not intersect header or table.
	if dataEnd > 0 && dataOff < headerLen {
		return nil, fmt.Errorf("%w: data region overlaps header", ErrInvalid)
	}
	if dataOff < tableEnd && tableOff < dataEnd {
		return nil, fmt.Errorf("%w: data region overlaps table", ErrInvalid)
	}

	seen := make(map[uint64]bool)
	for i := uint32(0); i < count; i++ {
		off := binary.LittleEndian.Uint64(data[tableOff+uint64(i)*tableEntrySize:])
		if off < dataOff || off >= dataEnd {
			return nil, fmt.Errorf("%w: table[%d] out of data region", ErrInvalid, i)
		}
		head := true
		for {
			if seen[off] {
				return nil, fmt.Errorf("%w: cycle at offset %d", ErrInvalid, off)
			}
			seen[off] = true
			end, keyLen, link, err := scanRecord(data, off, dataEnd)
			if err != nil {
				return nil, err
			}
			if head && keyLen == 0 {
				return nil, fmt.Errorf("%w: table[%d] is a continuation", ErrInvalid, i)
			}
			if !head && keyLen != 0 {
				return nil, fmt.Errorf("%w: continuation with key", ErrInvalid)
			}
			if err := c.add(Range{off, end}); err != nil {
				return nil, err
			}
			if link == 0 {
				break
			}
			if link < dataOff || link >= dataEnd {
				return nil, fmt.Errorf("%w: link out of data region", ErrInvalid)
			}
			off = link
			head = false
		}
	}
	return c, nil
}

// scanRecord returns the end offset, key length, and link target of the
// record at off, reading only bytes below dataEnd.
func scanRecord(data []byte, off, dataEnd uint64) (end, keyLen, link uint64, err error) {
	pos := off
	readVar := func() (uint64, error) {
		if pos >= dataEnd {
			return 0, fmt.Errorf("%w: varint past data end", ErrInvalid)
		}
		limit := pos + 10
		if limit > dataEnd {
			limit = dataEnd
		}
		v, n := binary.Uvarint(data[pos:limit])
		if n <= 0 {
			return 0, fmt.Errorf("%w: bad varint at %d", ErrInvalid, pos)
		}
		pos += uint64(n)
		return v, nil
	}
	keyLen, err = readVar()
	if err != nil {
		return 0, 0, 0, err
	}
	pos += keyLen
	if pos < off || pos > dataEnd {
		return 0, 0, 0, fmt.Errorf("%w: key overruns data region", ErrInvalid)
	}
	valLen, err := readVar()
	if err != nil {
		return 0, 0, 0, err
	}
	pos += valLen
	if pos > dataEnd {
		return 0, 0, 0, fmt.Errorf("%w: value overruns data region", ErrInvalid)
	}
	if pos >= dataEnd {
		return 0, 0, 0, fmt.Errorf("%w: missing link tag", ErrInvalid)
	}
	switch data[pos] {
	case linkNone:
		pos++
	case linkMore:
		pos++
		if pos+8 > dataEnd {
			return 0, 0, 0, fmt.Errorf("%w: link target overruns data region", ErrInvalid)
		}
		link = binary.LittleEndian.Uint64(data[pos : pos+8])
		pos += 8
	default:
		return 0, 0, 0, fmt.Errorf("%w: bad link tag", ErrInvalid)
	}
	return pos, keyLen, link, nil
}

func (c *Checker) add(r Range) error {
	if r.Start == r.End {
		return nil
	}
	if r.End < r.Start {
		return fmt.Errorf("%w: inverted range", ErrInvalid)
	}
	i := sort.Search(len(c.regions), func(i int) bool { return c.regions[i].Start >= r.Start })
	if i > 0 && c.regions[i-1].End > r.Start {
		return fmt.Errorf("%w: overlapping regions", ErrInvalid)
	}
	if i < len(c.regions) && r.End > c.regions[i].Start {
		return fmt.Errorf("%w: overlapping regions", ErrInvalid)
	}
	c.regions = append(c.regions, Range{})
	copy(c.regions[i+1:], c.regions[i:])
	c.regions[i] = r
	return nil
}

// Regions returns the validated byte ranges, sorted and non-overlapping.
func (c *Checker) Regions() []Range {
	return append([]Range(nil), c.regions...)
}

// Contains reports whether [off, off+n) lies inside one validated region.
func (c *Checker) Contains(off, n uint64) bool {
	end := off + n
	if end < off {
		return false
	}
	i := sort.Search(len(c.regions), func(i int) bool { return c.regions[i].Start > off }) - 1
	if i < 0 {
		return false
	}
	r := c.regions[i]
	return r.Start <= off && end <= r.End
}

// Verify fails if any observed access touched bytes outside the validated
// layout.
func (c *Checker) Verify(accesses []Range) error {
	for _, a := range accesses {
		if a.End < a.Start || !c.Contains(a.Start, a.End-a.Start) {
			return fmt.Errorf("layoutcheck: read of [%d,%d) is outside the validated layout", a.Start, a.End)
		}
	}
	return nil
}
