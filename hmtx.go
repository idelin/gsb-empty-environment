package ttfglyph

import "encoding/binary"

// HMetrics returns the horizontal metrics of glyph gid. Glyphs past the last
// long horizontal metric reuse the final advanceWidth and read their own
// left side bearing from the trailing array.
func (f *Font) HMetrics(gid uint16) (Metrics, error) {
	numGlyphs, err := f.NumGlyphs()
	if err != nil {
		return Metrics{}, err
	}
	if int(gid) >= int(numGlyphs) {
		return Metrics{}, corruptf("glyph index %d out of range (numGlyphs=%d)", gid, numGlyphs)
	}

	hhea, err := f.table("hhea", 36)
	if err != nil {
		return Metrics{}, err
	}
	numberOfHMetrics := int(binary.BigEndian.Uint16(hhea[34:36]))
	if numberOfHMetrics == 0 || numberOfHMetrics > int(numGlyphs) {
		return Metrics{}, corruptf("hhea.numberOfHMetrics %d invalid", numberOfHMetrics)
	}

	e, ok := f.tables["hmtx"]
	if !ok {
		return Metrics{}, ErrNotFound
	}
	need := 4*numberOfHMetrics + 2*(int(numGlyphs)-numberOfHMetrics)
	if int(e.length) < need {
		return Metrics{}, corruptf("hmtx table too short: have %d need %d", e.length, need)
	}

	var m Metrics
	metricIndex := int(gid)
	if metricIndex >= numberOfHMetrics {
		metricIndex = numberOfHMetrics - 1
	}
	buf := make([]byte, 4)
	if err := f.readFull(buf, int64(e.offset)+int64(4*metricIndex)); err != nil {
		return Metrics{}, err
	}
	m.AdvanceWidth = binary.BigEndian.Uint16(buf[0:2])

	if int(gid) < numberOfHMetrics {
		m.LeftSideBearing = int16(binary.BigEndian.Uint16(buf[2:4]))
		return m, nil
	}
	lsbIndex := 4*numberOfHMetrics + 2*(int(gid)-numberOfHMetrics)
	if err := f.readFull(buf[0:2], int64(e.offset)+int64(lsbIndex)); err != nil {
		return Metrics{}, err
	}
	m.LeftSideBearing = int16(binary.BigEndian.Uint16(buf[0:2]))
	return m, nil
}
