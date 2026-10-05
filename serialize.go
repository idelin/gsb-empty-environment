package slottedpage

import (
	"encoding/binary"
	"errors"
	"fmt"
)

const magic uint32 = 0x534c5054 // "SLPT"

var ErrCorrupt = errors.New("slottedpage: corrupt page image")

// Serialized page layout (all integers little-endian):
//
//	[0:4]   magic
//	[4:12]  page ID (uint64)
//	[12:16] page size in bytes (uint32)
//	[16:20] slot count (uint32)
//	[20:24] freeEnd offset (uint32)
//	[24:28] fragmented byte count (uint32)
//	[28:32] reserved
//	[32:]   slot directory, slotCount * 12 bytes:
//	          offset uint16 | length uint16 | gen uint32 | flags uint32
//	[freeEnd:] record heap, copied verbatim
//
// The directory grows forward from the header while the record heap grows
// backward from the end of the page; they meet at freeEnd.
func (p *Page) Marshal() []byte {
	img := make([]byte, len(p.buf))
	binary.LittleEndian.PutUint32(img[0:4], magic)
	binary.LittleEndian.PutUint64(img[4:12], p.id)
	binary.LittleEndian.PutUint32(img[12:16], uint32(len(p.buf)))
	binary.LittleEndian.PutUint32(img[16:20], uint32(len(p.slots)))
	binary.LittleEndian.PutUint32(img[20:24], uint32(p.freeEnd))
	binary.LittleEndian.PutUint32(img[24:28], p.fragmented)
	for i, s := range p.slots {
		off := headerSize + i*slotSize
		binary.LittleEndian.PutUint16(img[off:off+2], s.offset)
		binary.LittleEndian.PutUint16(img[off+2:off+4], s.length)
		binary.LittleEndian.PutUint32(img[off+4:off+8], s.gen)
		binary.LittleEndian.PutUint32(img[off+8:off+12], s.flags)
	}
	copy(img[p.freeEnd:], p.buf[p.freeEnd:])
	return img
}

// Unmarshal decodes a page image produced by Marshal. It is an independent
// decoder: it re-derives and cross-checks every structural invariant instead
// of trusting the image.
func Unmarshal(img []byte) (*Page, error) {
	if len(img) < headerSize {
		return nil, fmt.Errorf("%w: image smaller than header", ErrCorrupt)
	}
	if binary.LittleEndian.Uint32(img[0:4]) != magic {
		return nil, fmt.Errorf("%w: bad magic", ErrCorrupt)
	}
	id := binary.LittleEndian.Uint64(img[4:12])
	size := int(binary.LittleEndian.Uint32(img[12:16]))
	slotCount := int(binary.LittleEndian.Uint32(img[16:20]))
	freeEnd := int(binary.LittleEndian.Uint32(img[20:24]))
	fragmented := binary.LittleEndian.Uint32(img[24:28])

	if size != len(img) || size < headerSize+slotSize || size > MaxPageSize {
		return nil, fmt.Errorf("%w: page size %d vs image %d", ErrCorrupt, size, len(img))
	}
	dirEnd := headerSize + slotCount*slotSize
	if dirEnd > size {
		return nil, fmt.Errorf("%w: directory overruns page", ErrCorrupt)
	}
	if freeEnd < dirEnd || freeEnd > size {
		return nil, fmt.Errorf("%w: freeEnd %d outside [%d, %d]", ErrCorrupt, freeEnd, dirEnd, size)
	}

	p := &Page{
		id:         id,
		buf:        make([]byte, size),
		slots:      make([]slot, slotCount),
		freeEnd:    uint16(freeEnd),
		fragmented: fragmented,
	}
	copy(p.buf[freeEnd:], img[freeEnd:])

	// Decode slots and validate that live records lie inside the heap and
	// never overlap one another.
	type interval struct{ lo, hi int }
	var live []interval
	recordBytes := 0
	for i := range p.slots {
		off := headerSize + i*slotSize
		s := slot{
			offset: binary.LittleEndian.Uint16(img[off : off+2]),
			length: binary.LittleEndian.Uint16(img[off+2 : off+4]),
			gen:    binary.LittleEndian.Uint32(img[off+4 : off+8]),
			flags:  binary.LittleEndian.Uint32(img[off+8 : off+12]),
		}
		if s.gen == 0 {
			return nil, fmt.Errorf("%w: slot %d has zero generation", ErrCorrupt, i)
		}
		if !s.tombstone() {
			lo, hi := int(s.offset), int(s.offset)+int(s.length)
			if lo < freeEnd || hi > size {
				return nil, fmt.Errorf("%w: slot %d record outside heap", ErrCorrupt, i)
			}
			live = append(live, interval{lo, hi})
			recordBytes += int(s.length)
		}
		p.slots[i] = s
	}
	for i := range live {
		for j := i + 1; j < len(live); j++ {
			if live[i].lo < live[j].hi && live[j].lo < live[i].hi {
				return nil, fmt.Errorf("%w: overlapping records", ErrCorrupt)
			}
		}
	}

	// Cross-check accounting: heap bytes must equal live records + fragments.
	if size-freeEnd != recordBytes+int(fragmented) {
		return nil, fmt.Errorf("%w: heap accounting mismatch", ErrCorrupt)
	}
	return p, nil
}
