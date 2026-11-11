package ttfglyph

import "encoding/binary"

// glyphRange returns the [start, end) byte range of glyph gid inside the glyf
// table. A zero-length range denotes an empty glyph.
func (f *Font) glyphRange(gid uint16) (int64, int64, error) {
	n, err := f.NumGlyphs()
	if err != nil {
		return 0, 0, err
	}
	if int(gid) >= int(n) {
		return 0, 0, corruptf("glyph index %d out of range (numGlyphs=%d)", gid, n)
	}
	loca, err := f.locaTable()
	if err != nil {
		return 0, 0, err
	}
	head, err := f.headTable()
	if err != nil {
		return 0, 0, err
	}

	var start, end uint32
	if head.indexToLocFormat == 0 {
		// Offsets are stored as uint16 units of 2 bytes.
		start = uint32(binary.BigEndian.Uint16(loca[2*gid:])) * 2
		end = uint32(binary.BigEndian.Uint16(loca[2*(gid+1):])) * 2
	} else {
		start = binary.BigEndian.Uint32(loca[4*gid:])
		end = binary.BigEndian.Uint32(loca[4*(gid+1):])
	}
	if end < start {
		return 0, 0, corruptf("loca entry for glyph %d is inverted (%d>%d)", gid, start, end)
	}
	return int64(start), int64(end), nil
}
