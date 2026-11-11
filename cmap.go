package ttfglyph

import (
	"encoding/binary"
	"fmt"
)

// cmapSubtable is the resolved subtable used for rune-to-glyph lookups.
type cmapSubtable interface {
	// lookup maps a rune to a glyph index. A zero result means "no glyph".
	lookup(rune) (uint16, error)
}

type cmapFormat4 struct {
	segCount      int
	endCode       []uint16
	startCode     []uint16
	idDelta       []int16
	idRangeOffset []uint16
	// glyphArray is the glyphIdArray area, relative to the start of the subtable.
	glyphArray []byte
	arrayBase  int
}

type cmapGroup struct {
	startCharCode uint32
	endCharCode   uint32
	startGlyphID  uint32
}

type cmapFormat12 struct {
	groups []cmapGroup
}

// loadCmap selects a Unicode subtable, preferring format 12 over format 4.
func (f *Font) loadCmap() {
	b, err := f.table("cmap", 4)
	if err != nil {
		f.cmapErr = err
		return
	}
	numTables := int(binary.BigEndian.Uint16(b[2:4]))
	if len(b) < 4+numTables*8 {
		f.cmapErr = corruptf("cmap encoding records exceed table length")
		return
	}

	var f4off, f12off int = -1, -1
	for i := 0; i < numTables; i++ {
		rec := b[4+i*8 : 4+(i+1)*8]
		platformID := binary.BigEndian.Uint16(rec[0:2])
		encodingID := binary.BigEndian.Uint16(rec[2:4])
		off := int(binary.BigEndian.Uint32(rec[4:8]))
		if off+2 > len(b) {
			f.cmapErr = corruptf("cmap subtable offset %d outside table", off)
			return
		}
		format := binary.BigEndian.Uint16(b[off : off+2])
		// Prefer Unicode (platform 0) and Windows Unicode (3/1, 3/10).
		unicode := platformID == 0 || (platformID == 3 && (encodingID == 1 || encodingID == 10))
		if !unicode {
			continue
		}
		switch format {
		case 12:
			if f12off < 0 {
				f12off = off
			}
		case 4:
			if f4off < 0 {
				f4off = off
			}
		}
	}

	switch {
	case f12off >= 0:
		f.cmap, f.cmapErr = parseCmapFormat12(b, f12off)
	case f4off >= 0:
		f.cmap, f.cmapErr = parseCmapFormat4(b, f4off)
	default:
		f.cmapErr = fmt.Errorf("%w: no format 4 or 12 Unicode subtable", ErrUnsupported)
	}
}

func parseCmapFormat4(b []byte, off int) (cmapSubtable, error) {
	if off+14 > len(b) {
		return nil, corruptf("cmap format 4 header truncated")
	}
	length := int(binary.BigEndian.Uint16(b[off+2 : off+4]))
	if length < 16 || off+length > len(b) {
		return nil, corruptf("cmap format 4 length %d out of bounds", length)
	}
	segX2 := int(binary.BigEndian.Uint16(b[off+6 : off+8]))
	if segX2 == 0 || segX2%2 != 0 {
		return nil, corruptf("cmap format 4 invalid segCountX2 %d", segX2)
	}
	segCount := segX2 / 2
	// Layout: 14 header bytes, endCode, 2 reserved, startCode, idDelta,
	// idRangeOffset, then glyphIdArray.
	need := 14 + segX2 + 2 + segX2 + segX2 + segX2
	if need > length {
		return nil, corruptf("cmap format 4 segments exceed declared length")
	}
	s := &cmapFormat4{segCount: segCount}
	p := off + 14
	s.endCode = make([]uint16, segCount)
	for i := range s.endCode {
		s.endCode[i] = binary.BigEndian.Uint16(b[p+2*i : p+2*i+2])
	}
	p += segX2 + 2 // skip reservedPad
	s.startCode = make([]uint16, segCount)
	for i := range s.startCode {
		s.startCode[i] = binary.BigEndian.Uint16(b[p+2*i : p+2*i+2])
	}
	p += segX2
	s.idDelta = make([]int16, segCount)
	for i := range s.idDelta {
		s.idDelta[i] = int16(binary.BigEndian.Uint16(b[p+2*i : p+2*i+2]))
	}
	p += segX2
	s.idRangeOffset = make([]uint16, segCount)
	for i := range s.idRangeOffset {
		s.idRangeOffset[i] = binary.BigEndian.Uint16(b[p+2*i : p+2*i+2])
	}
	// The glyph array index math is defined relative to the idRangeOffset field.
	s.arrayBase = p
	s.glyphArray = b
	return s, nil
}

func (s *cmapFormat4) lookup(r rune) (uint16, error) {
	if r > 0xFFFF {
		return 0, nil
	}
	ch := uint16(r)
	// Binary search for the first segment whose endCode >= ch.
	lo, hi := 0, s.segCount
	for lo < hi {
		mid := (lo + hi) / 2
		if s.endCode[mid] < ch {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo == s.segCount || ch < s.startCode[lo] {
		return 0, nil
	}
	if s.idRangeOffset[lo] == 0 {
		return uint16(int(ch) + int(s.idDelta[lo])), nil
	}
	// Address: address of idRangeOffset field + the offset value +
	// 2 * (charCode - startCode).
	fieldOff := s.arrayBase + 2*lo
	glyphOff := fieldOff + int(s.idRangeOffset[lo]) + 2*int(ch-s.startCode[lo])
	if glyphOff+2 > len(s.glyphArray) {
		return 0, corruptf("cmap format 4 glyph index outside table")
	}
	gid := binary.BigEndian.Uint16(s.glyphArray[glyphOff : glyphOff+2])
	if gid == 0 {
		return 0, nil
	}
	return uint16(int(gid) + int(s.idDelta[lo])), nil
}

func parseCmapFormat12(b []byte, off int) (cmapSubtable, error) {
	if off+16 > len(b) {
		return nil, corruptf("cmap format 12 header truncated")
	}
	length := int(binary.BigEndian.Uint32(b[off+4 : off+8]))
	if length < 16 || off+length > len(b) {
		return nil, corruptf("cmap format 12 length %d out of bounds", length)
	}
	numGroups := int(binary.BigEndian.Uint32(b[off+12 : off+16]))
	if numGroups == 0 || 16+numGroups*12 > length {
		return nil, corruptf("cmap format 12 group count %d out of bounds", numGroups)
	}
	s := &cmapFormat12{groups: make([]cmapGroup, numGroups)}
	p := off + 16
	for i := 0; i < numGroups; i++ {
		g := cmapGroup{
			startCharCode: binary.BigEndian.Uint32(b[p : p+4]),
			endCharCode:   binary.BigEndian.Uint32(b[p+4 : p+8]),
			startGlyphID:  binary.BigEndian.Uint32(b[p+8 : p+12]),
		}
		if g.endCharCode < g.startCharCode {
			return nil, corruptf("cmap format 12 group %d has inverted range", i)
		}
		s.groups[i] = g
		p += 12
	}
	return s, nil
}

func (s *cmapFormat12) lookup(r rune) (uint16, error) {
	ch := uint32(r)
	lo, hi := 0, len(s.groups)
	for lo < hi {
		mid := (lo + hi) / 2
		if s.groups[mid].endCharCode < ch {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo == len(s.groups) || ch < s.groups[lo].startCharCode {
		return 0, nil
	}
	gid := s.groups[lo].startGlyphID + ch - s.groups[lo].startCharCode
	if gid > 0xFFFF {
		return 0, corruptf("cmap format 12 glyph id %d exceeds uint16", gid)
	}
	return uint16(gid), nil
}

// GlyphIndex maps a rune to its glyph index. A zero index with a nil error
// means the font has no glyph for the rune.
func (f *Font) GlyphIndex(r rune) (uint16, error) {
	f.cmapOnce.Do(f.loadCmap)
	if f.cmapErr != nil {
		return 0, f.cmapErr
	}
	return f.cmap.lookup(r)
}
