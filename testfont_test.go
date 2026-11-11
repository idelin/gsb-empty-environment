package ttfglyph

import (
	"encoding/binary"
	"math"
)

// be16 is a big-endian byte buffer used by the test font builder.
type be16 struct {
	b []byte
}

func (w *be16) u16(v uint16) {
	w.b = binary.BigEndian.AppendUint16(w.b, v)
}

func (w *be16) i16(v int16) {
	w.b = binary.BigEndian.AppendUint16(w.b, uint16(v))
}

func (w *be16) u32(v uint32) {
	w.b = binary.BigEndian.AppendUint32(w.b, v)
}

func (w *be16) bytes(b ...byte) {
	w.b = append(w.b, b...)
}

func (w *be16) pad4() {
	for len(w.b)%4 != 0 {
		w.b = append(w.b, 0)
	}
}

// compDesc describes one composite glyph component for the builder.
type compDesc struct {
	glyphIndex uint16
	arg1, arg2 int16
	argsAreXY  bool
	words      bool
	scale      []int16 // 1 (uniform), 2 (xy) or 4 (2x2) F2Dot14 values
	instr      []byte  // composite instruction bytes (sets WE_HAVE_INSTRUCTIONS)
}

// buildSimpleGlyph encodes one simple glyph. All coordinate deltas use the
// signed 16-bit form, which every TrueType reader must accept.
func buildSimpleGlyph(points []Point, endPts []uint16) []byte {
	xMin, yMin := int32(math.MaxInt32), int32(math.MaxInt32)
	xMax, yMax := int32(math.MinInt32), int32(math.MinInt32)
	for _, p := range points {
		xMin = min(xMin, p.X)
		yMin = min(yMin, p.Y)
		xMax = max(xMax, p.X)
		yMax = max(yMax, p.Y)
	}

	var w be16
	w.i16(int16(len(endPts)))
	w.i16(int16(xMin))
	w.i16(int16(yMin))
	w.i16(int16(xMax))
	w.i16(int16(yMax))
	for _, e := range endPts {
		w.u16(e)
	}
	w.u16(0) // instructionLength
	for _, p := range points {
		if p.OnCurve {
			w.bytes(flagOnCurve)
		} else {
			w.bytes(0)
		}
	}
	var px, py int32
	for _, p := range points {
		w.i16(int16(p.X - px))
		px = p.X
	}
	for _, p := range points {
		w.i16(int16(p.Y - py))
		py = p.Y
	}
	return w.b
}

func buildCompositeGlyph(components []compDesc, bbox [4]int16) []byte {
	var w be16
	w.i16(-1)
	for _, v := range bbox {
		w.i16(v)
	}
	for i, comp := range components {
		var flags uint16
		if comp.words {
			flags |= compArg12Words
		}
		if comp.argsAreXY {
			flags |= compArgsAreXY
		}
		switch len(comp.scale) {
		case 0:
		case 1:
			flags |= compHasScale
		case 2:
			flags |= compHasXYScale
		case 4:
			flags |= compHas2x2
		default:
			panic("invalid component scale length")
		}
		if i != len(components)-1 {
			flags |= compMoreComponents
		}
		if len(comp.instr) > 0 {
			flags |= compHasInstructions
		}
		w.u16(flags)
		w.u16(comp.glyphIndex)
		if comp.words {
			w.i16(comp.arg1)
			w.i16(comp.arg2)
		} else {
			if comp.argsAreXY {
				w.bytes(byte(int8(comp.arg1)), byte(int8(comp.arg2)))
			} else {
				w.bytes(byte(comp.arg1), byte(comp.arg2))
			}
		}
		for _, s := range comp.scale {
			w.i16(s)
		}
		if len(comp.instr) > 0 {
			w.u16(uint16(len(comp.instr)))
			w.bytes(comp.instr...)
		}
	}
	return w.b
}

// buildTestFont assembles a minimal but valid TrueType font containing the
// supplied glyf blocks plus head/hhea/hmtx/loca/maxp/cmap tables. Checksums
// are left zero; they are not required for parsing.
// fontOptions toggles variants used by the tests.
type fontOptions struct {
	shortLoca    bool   // use indexToLocFormat 0 (uint16 half-offsets)
	format4Only  bool   // emit only the BMP cmap subtable
	headOverride []byte // replace the generated head table
	cmapOverride []byte // replace the generated cmap table
}

func buildTestFont(glyfBlocks [][]byte) []byte {
	return buildTestFontWith(glyfBlocks, fontOptions{})
}

func buildTestFontWith(glyfBlocks [][]byte, opt fontOptions) []byte {
	numGlyphs := len(glyfBlocks)

	// glyf + loca.
	var glyf be16
	offsets := make([]uint32, numGlyphs+1)
	for i, block := range glyfBlocks {
		offsets[i] = uint32(len(glyf.b))
		glyf.bytes(block...)
		if opt.shortLoca {
			for len(glyf.b)%2 != 0 {
				glyf.bytes(0)
			}
		} else {
			glyf.pad4()
		}
	}
	offsets[numGlyphs] = uint32(len(glyf.b))

	var loca be16
	if opt.shortLoca {
		// Short format stores half-offsets; glyphs are 4-byte aligned here.
		for _, off := range offsets {
			if off%2 != 0 {
				panic("short loca requires even glyph offsets")
			}
			loca.u16(uint16(off / 2))
		}
	} else {
		for _, off := range offsets {
			loca.u32(off)
		}
	}

	// head: 54 bytes, indexToLocFormat at offset 50.
	head := make([]byte, 54)
	binary.BigEndian.PutUint32(head[0:4], 0x00010000)   // version
	binary.BigEndian.PutUint32(head[12:16], 0x5F0F3CF5) // magicNumber
	if opt.shortLoca {
		binary.BigEndian.PutUint16(head[50:52], 0)
	} else {
		binary.BigEndian.PutUint16(head[50:52], 1)
	}
	if opt.headOverride != nil {
		head = opt.headOverride
	}

	// maxp: version 1.0, numGlyphs.
	var maxp be16
	maxp.u32(0x00010000)
	maxp.u16(uint16(numGlyphs))
	maxp.bytes(make([]byte, 26)...)

	// hhea: 36 bytes, numberOfHMetrics at offset 34.
	hhea := make([]byte, 36)
	binary.BigEndian.PutUint32(hhea[0:4], 0x00010000)
	nHMetrics := numGlyphs
	if nHMetrics > 6 {
		nHMetrics = 6
	}
	binary.BigEndian.PutUint16(hhea[34:36], uint16(nHMetrics))

	// hmtx: full long metrics for nHMetrics glyphs, then LSB-only entries.
	var hmtx be16
	for i := 0; i < nHMetrics; i++ {
		hmtx.u16(uint16(500 + 10*i))
		hmtx.i16(int16(10 + i))
	}
	for i := nHMetrics; i < numGlyphs; i++ {
		hmtx.i16(int16(-5 - i))
	}

	cmap := buildTestCmap()
	if opt.format4Only {
		cmap = buildTestCmapFormat4Only()
	}
	if opt.cmapOverride != nil {
		cmap = opt.cmapOverride
	}

	type tdir struct {
		tag  string
		data []byte
	}
	tables := []tdir{
		{"cmap", cmap},
		{"glyf", glyf.b},
		{"head", head},
		{"hhea", hhea},
		{"hmtx", hmtx.b},
		{"loca", loca.b},
		{"maxp", maxp.b},
	}

	var out be16
	// Offset table.
	out.u32(0x00010000)
	out.u16(uint16(len(tables)))
	// searchRange, entrySelector, rangeShift: not validated by the reader.
	searchRange := uint16(16)
	for searchRange*2 <= uint16(len(tables))*16 {
		searchRange *= 2
	}
	out.u16(searchRange)
	entrySelector := uint16(0)
	for 1<<(entrySelector+1) <= uint16(len(tables)) {
		entrySelector++
	}
	out.u16(entrySelector)
	out.u16(uint16(len(tables))*16 - searchRange)

	headerLen := 12 + 16*len(tables)
	dataOffset := headerLen
	type placed struct {
		tag  string
		off  uint32
		data []byte
	}
	placedTables := make([]placed, 0, len(tables))
	for _, t := range tables {
		placedTables = append(placedTables, placed{tag: t.tag, off: uint32(dataOffset), data: t.data})
		dataOffset += len(t.data)
		for dataOffset%4 != 0 {
			dataOffset++
		}
	}
	for _, p := range placedTables {
		out.bytes(p.tag[0], p.tag[1], p.tag[2], p.tag[3])
		out.u32(0) // checksum
		out.u32(p.off)
		out.u32(uint32(len(p.data)))
	}
	for _, p := range placedTables {
		out.bytes(p.data...)
		for len(out.b)%4 != 0 {
			out.bytes(0)
		}
	}
	return out.b
}

// buildTestCmap builds a cmap with both a format 4 (BMP) and a format 12
// (full repertoire) Unicode subtable.
func buildTestCmap() []byte {
	var f4 be16
	// Segments: [65..68], [69..69], [80..80], [32..32], sentinel.
	startCode := []uint16{65, 69, 80, 32, 0xFFFF}
	endCode := []uint16{68, 69, 80, 32, 0xFFFF}
	idDelta := []int16{-64, -64, 0, 0, 1}
	segCount := len(startCode)

	// First emit everything after the idRangeOffset array to know glyph array
	// positions; format 4 length is independent of content offsets.
	var glyphArray be16
	// Only segment 2 (P) uses idRangeOffset; it needs one glyph value for
	// index 0 of that segment.
	glyphArray.u16(7)

	length := 16 + 8*segCount + len(glyphArray.b)
	f4.u16(4)
	f4.u16(uint16(length))
	f4.u16(0) // language
	f4.u16(uint16(segCount * 2))
	f4.u16(uint16(2 * (1 << log2(segCount))))          // searchRange
	f4.u16(uint16(log2(segCount)))                     // entrySelector
	f4.u16(uint16(segCount*2 - 2*(1<<log2(segCount)))) // rangeShift
	for _, v := range endCode {
		f4.u16(v)
	}
	f4.u16(0) // reservedPad
	for _, v := range startCode {
		f4.u16(v)
	}
	for _, v := range idDelta {
		f4.i16(v)
	}
	// idRangeOffset: 0 except for segment 2. The glyph value must be at
	// (idRangeOffset field address) + offset + 2*(ch-start).
	idRangeOffsetPos := len(f4.b)
	rangeOffsets := make([]uint16, segCount)
	for range rangeOffsets {
		f4.u16(0)
	}
	glyphArrayStart := len(f4.b)
	f4.bytes(glyphArray.b...)
	fieldAddr := idRangeOffsetPos + 2*2
	rangeOffsets[2] = uint16(glyphArrayStart - fieldAddr)
	for i, v := range rangeOffsets {
		binary.BigEndian.PutUint16(f4.b[idRangeOffsetPos+2*i:], v)
	}

	// Format 12: groups A-D->1..4, E->5, P->7, space->0, U+1F600->6.
	var f12 be16
	groups := []struct {
		start, end, gid uint32
	}{
		{32, 32, 0},
		{65, 68, 1},
		{69, 69, 5},
		{80, 80, 7},
		{0x1F600, 0x1F600, 6},
	}
	f12.u16(12)
	f12.u16(0) // reserved
	f12.u32(uint32(16 + 12*len(groups)))
	f12.u32(0) // language
	f12.u32(uint32(len(groups)))
	for _, g := range groups {
		f12.u32(g.start)
		f12.u32(g.end)
		f12.u32(g.gid)
	}

	var cmap be16
	cmap.u16(0) // version
	cmap.u16(2) // num encoding records
	// Record 0: platform 3 / encoding 1 -> format 4.
	cmap.u16(3)
	cmap.u16(1)
	cmap.u32(4 + 2*8)
	// Record 1: platform 3 / encoding 10 -> format 12.
	cmap.u16(3)
	cmap.u16(10)
	cmap.u32(uint32(4 + 2*8 + len(f4.b)))
	cmap.bytes(f4.b...)
	cmap.bytes(f12.b...)
	return cmap.b
}

func log2(n int) int {
	e := 0
	for n > 1 {
		n >>= 1
		e++
	}
	return e
}

// buildTestCmapFormat4Only emits a single platform 3 / encoding 1 record
// pointing at the format-4 subtable, forcing the reader down that path.
func buildTestCmapFormat4Only() []byte {
	full := buildTestCmap()
	// full layout: 4-byte cmap header, 2 records of 8 bytes, then f4, then f12.
	const f4Start = 4 + 2*8
	f4Len := int(binary.BigEndian.Uint16(full[f4Start+2 : f4Start+4]))
	var out be16
	out.u16(0)
	out.u16(1)
	out.u16(3)
	out.u16(1)
	out.u32(4 + 8) // subtable offset relative to cmap start
	out.bytes(full[f4Start : f4Start+f4Len]...)
	return out.b
}
