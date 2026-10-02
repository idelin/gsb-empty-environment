package compactidx

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"sort"
)

// Range is a half-open byte range [Start, End) inside the image.
type Range struct {
	Start uint64
	End   uint64
}

// Len returns the number of bytes in the range.
func (r Range) Len() uint64 { return r.End - r.Start }

type recordInfo struct {
	off, end uint64
	head     bool
	key      string
}

// layout is the result of validating the whole offset graph of an image.
// Only bytes inside regions may be read at query time.
type layout struct {
	regions  []Range // sorted, non-overlapping, validated
	table    []uint64
	records  []recordInfo
	tableOff uint64
	tableEnd uint64
	dataOff  uint64
	dataEnd  uint64
}

// addRegion inserts r keeping regions sorted, rejecting overlaps.
func (l *layout) addRegion(r Range) error {
	if r.End < r.Start {
		return ErrOverflow
	}
	if r.Start == r.End {
		return nil
	}
	i := sort.Search(len(l.regions), func(i int) bool { return l.regions[i].Start >= r.Start })
	if i > 0 && l.regions[i-1].End > r.Start {
		return fmt.Errorf("%w: [%d,%d) collides with [%d,%d)", ErrRegionOverlap, r.Start, r.End, l.regions[i-1].Start, l.regions[i-1].End)
	}
	if i < len(l.regions) && r.End > l.regions[i].Start {
		return fmt.Errorf("%w: [%d,%d) collides with [%d,%d)", ErrRegionOverlap, r.Start, r.End, l.regions[i].Start, l.regions[i].End)
	}
	l.regions = append(l.regions, Range{})
	copy(l.regions[i+1:], l.regions[i:])
	l.regions[i] = r
	return nil
}

// contains reports whether [off, off+n) lies entirely inside one validated
// region. Zero-length reads at a region boundary are allowed.
func (l *layout) contains(off, n uint64) bool {
	end, err := add64(off, n)
	if err != nil {
		return false
	}
	i := sort.Search(len(l.regions), func(i int) bool { return l.regions[i].Start > off }) - 1
	if i < 0 {
		return false
	}
	r := l.regions[i]
	return r.Start <= off && end <= r.End
}

type rawRecord struct {
	start, end uint64
	key        []byte
	link       uint64
}

// parseRecord decodes the record starting at off, reading only bytes below
// dataEnd. Every field is bounds-checked before use.
func parseRecord(data []byte, off, dataEnd uint64) (rawRecord, error) {
	rec := rawRecord{start: off}
	pos := off

	keyLen, pos, err := readUvarintRaw(data, pos, dataEnd)
	if err != nil {
		return rec, err
	}
	keyEnd, err := add64(pos, keyLen)
	if err != nil {
		return rec, err
	}
	if keyEnd > dataEnd {
		return rec, fmt.Errorf("%w: key of %d bytes at offset %d overruns data region", ErrTruncated, keyLen, pos)
	}
	rec.key = data[pos:keyEnd]
	pos = keyEnd

	valLen, pos, err := readUvarintRaw(data, pos, dataEnd)
	if err != nil {
		return rec, err
	}
	valEnd, err := add64(pos, valLen)
	if err != nil {
		return rec, err
	}
	if valEnd > dataEnd {
		return rec, fmt.Errorf("%w: value of %d bytes at offset %d overruns data region", ErrTruncated, valLen, pos)
	}
	pos = valEnd

	if pos >= dataEnd {
		return rec, fmt.Errorf("%w: missing link tag at offset %d", ErrTruncated, pos)
	}
	switch tag := data[pos]; tag {
	case linkNone:
		pos++
	case linkMore:
		pos++
		linkEnd, err := add64(pos, 8)
		if err != nil {
			return rec, err
		}
		if linkEnd > dataEnd {
			return rec, fmt.Errorf("%w: continuation offset at %d overruns data region", ErrTruncated, pos)
		}
		rec.link = binary.LittleEndian.Uint64(data[pos:linkEnd])
		pos = linkEnd
	default:
		return rec, fmt.Errorf("%w: unknown link tag 0x%02x at offset %d", ErrBadRecord, tag, pos)
	}
	rec.end = pos
	return rec, nil
}

// validate fully walks the offset graph of data before any byte is trusted:
// header, offset table, and every record chain. It rejects out-of-file
// offsets, integer overflow, overlapping regions, and cycles. A locally
// valid record is never enough — the whole graph must check out.
func validate(data []byte) (*layout, error) {
	hdr, err := parseHeader(data)
	if err != nil {
		return nil, err
	}
	fileLen := uint64(len(data))

	tableBytes, err := mul64(uint64(hdr.entryCount), tableEntrySize)
	if err != nil {
		return nil, fmt.Errorf("offset table size: %w", err)
	}
	tableEnd, err := add64(hdr.tableOff, tableBytes)
	if err != nil {
		return nil, fmt.Errorf("offset table end: %w", err)
	}
	if tableEnd > fileLen {
		return nil, fmt.Errorf("%w: offset table [%d,%d) beyond file of %d bytes", ErrOffsetOutOfFile, hdr.tableOff, tableEnd, fileLen)
	}
	dataEnd, err := add64(hdr.dataOff, hdr.dataLen)
	if err != nil {
		return nil, fmt.Errorf("data region end: %w", err)
	}
	if dataEnd > fileLen {
		return nil, fmt.Errorf("%w: data region [%d,%d) beyond file of %d bytes", ErrOffsetOutOfFile, hdr.dataOff, dataEnd, fileLen)
	}

	lay := &layout{
		tableOff: hdr.tableOff,
		tableEnd: tableEnd,
		dataOff:  hdr.dataOff,
		dataEnd:  dataEnd,
	}
	if err := lay.addRegion(Range{0, HeaderLen}); err != nil {
		return nil, err
	}
	if err := lay.addRegion(Range{hdr.tableOff, tableEnd}); err != nil {
		return nil, err
	}
	// The data region is declared, not validated: only record extents inside
	// it become readable. It must still not collide with header or table.
	dataRegion := Range{hdr.dataOff, dataEnd}
	for _, r := range lay.regions {
		if dataRegion.Start < r.End && r.Start < dataRegion.End {
			return nil, fmt.Errorf("%w: data region [%d,%d) collides with [%d,%d)", ErrRegionOverlap, dataRegion.Start, dataRegion.End, r.Start, r.End)
		}
	}

	lay.table = make([]uint64, hdr.entryCount)
	for i := range lay.table {
		off := binary.LittleEndian.Uint64(data[hdr.tableOff+uint64(i)*tableEntrySize:])
		if off < hdr.dataOff || off >= dataEnd {
			return nil, fmt.Errorf("%w: table[%d]=%d not in data region [%d,%d)", ErrOffsetOutOfFile, i, off, hdr.dataOff, dataEnd)
		}
		lay.table[i] = off
	}

	visited := make(map[uint64]struct{})
	headKeys := make([][]byte, 0, hdr.entryCount)
	for i, start := range lay.table {
		off := start
		head := true
		for {
			if _, seen := visited[off]; seen {
				return nil, fmt.Errorf("%w: record at offset %d referenced more than once", ErrCycle, off)
			}
			rec, err := parseRecord(data, off, dataEnd)
			if err != nil {
				return nil, err
			}
			if err := lay.addRegion(Range{rec.start, rec.end}); err != nil {
				return nil, err
			}
			visited[off] = struct{}{}
			switch {
			case head && len(rec.key) == 0:
				return nil, fmt.Errorf("%w: table[%d] points at a continuation record", ErrBadRecord, i)
			case !head && len(rec.key) != 0:
				return nil, fmt.Errorf("%w: continuation record at offset %d carries a key", ErrBadRecord, off)
			}
			lay.records = append(lay.records, recordInfo{
				off:  rec.start,
				end:  rec.end,
				head: head,
				key:  string(rec.key),
			})
			if head {
				headKeys = append(headKeys, rec.key)
			}
			if rec.link == 0 {
				break
			}
			if rec.link < hdr.dataOff || rec.link >= dataEnd {
				return nil, fmt.Errorf("%w: continuation offset %d not in data region [%d,%d)", ErrOffsetOutOfFile, rec.link, hdr.dataOff, dataEnd)
			}
			off = rec.link
			head = false
		}
	}

	for i := 1; i < len(headKeys); i++ {
		if bytes.Compare(headKeys[i-1], headKeys[i]) >= 0 {
			return nil, fmt.Errorf("%w: table[%d]=%q not below table[%d]=%q", ErrUnsortedKeys, i-1, headKeys[i-1], i, headKeys[i])
		}
	}
	return lay, nil
}
