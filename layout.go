package cidx

import (
	"bytes"
	"encoding/binary"
	"math"
	"sort"
)

// Region is a half-open byte range [Start, End) inside the file.
type Region struct {
	Start uint64
	End   uint64
}

// Layout is the result of a full structural validation of an index file.
// It is produced only by Check, the independent byte-layout checker, and is
// the sole source of truth the query path is allowed to rely on.
type Layout struct {
	EntryCount uint32
	DataOffset uint64
	// Offsets has EntryCount+1 entries, relative to DataOffset.
	Offsets []uint32
	// Regions lists every byte range that Check validated, merged and
	// sorted. Bytes outside these ranges were never inspected and must
	// never be read by the query path.
	Regions  []Region
	FileSize uint64
}

// Check fully validates the byte layout of an index file. It is independent
// of the query path: it reads raw bytes, verifies every structural
// invariant (bounds, overlap, offset-graph cycles, integer overflow, record
// framing, global key ordering), and records exactly which regions it
// validated. A record that parses locally is not trusted until every global
// invariant also holds.
func Check(data []byte) (*Layout, error) {
	if len(data) == 0 {
		return nil, ErrEmptyFile
	}
	if len(data) < HeaderSize {
		return nil, ErrTooShort
	}
	h, err := decodeHeader(data[:HeaderSize])
	if err != nil {
		return nil, err
	}
	fileSize := uint64(len(data))

	// Offset table geometry, with explicit overflow checks.
	if h.entryCount == math.MaxUint32 {
		return nil, ErrOverflow // entryCount+1 would wrap
	}
	tableEntries := uint64(h.entryCount) + 1
	tableBytes := tableEntries * offsetEntrySize // <= 2^34, cannot overflow
	if h.tableOffset > math.MaxUint64-tableBytes {
		return nil, ErrOverflow
	}
	tableEnd := h.tableOffset + tableBytes
	if h.tableOffset < HeaderSize {
		return nil, ErrRegionOverlap // table overlaps header
	}
	if tableEnd > fileSize {
		return nil, ErrTruncated
	}

	// Data region geometry.
	if h.dataOffset > math.MaxUint64-h.dataLength {
		return nil, ErrOverflow
	}
	dataEnd := h.dataOffset + h.dataLength
	if dataEnd > fileSize {
		return nil, ErrTruncated
	}
	if dataEnd < fileSize {
		return nil, ErrTrailingData
	}
	if h.dataOffset < tableEnd {
		return nil, ErrRegionOverlap // data overlaps offset table
	}

	// Offset table is known to fit in the file, so this allocation is
	// bounded by the file size and cannot be used as a memory bomb.
	offsets := make([]uint32, int(tableEntries))
	for i := range offsets {
		o := h.tableOffset + uint64(i)*offsetEntrySize
		offsets[i] = binary.LittleEndian.Uint32(data[o : o+offsetEntrySize])
	}

	// Offset graph validation: bounds, back-edges (cycles), anchoring.
	if offsets[0] != 0 {
		return nil, ErrOffsetOutOfRange
	}
	prev := uint64(0)
	for _, off := range offsets {
		cur := uint64(off)
		if cur > h.dataLength {
			return nil, ErrOffsetOutOfRange
		}
		if cur < prev {
			return nil, ErrOffsetCycle
		}
		prev = cur
	}
	if uint64(offsets[len(offsets)-1]) != h.dataLength {
		return nil, ErrOffsetOutOfRange
	}

	// Record-level and global validation. Every record must parse exactly
	// within its extent, and keys must be strictly increasing across the
	// whole file: local validity never implies global trust.
	regions := []Region{
		{Start: 0, End: HeaderSize},
		{Start: h.tableOffset, End: tableEnd},
	}
	var prevKey []byte
	for i := 0; i < int(h.entryCount); i++ {
		start := h.dataOffset + uint64(offsets[i])
		end := h.dataOffset + uint64(offsets[i+1])
		key, _, err := parseRecord(data[start:end])
		if err != nil {
			return nil, err
		}
		if prevKey != nil && bytes.Compare(prevKey, key) >= 0 {
			return nil, ErrKeyOrder
		}
		prevKey = append(prevKey[:0], key...)
		regions = append(regions, Region{Start: start, End: end})
	}

	return &Layout{
		EntryCount: h.entryCount,
		DataOffset: h.dataOffset,
		Offsets:    offsets,
		Regions:    mergeRegions(regions),
		FileSize:   fileSize,
	}, nil
}

func mergeRegions(rs []Region) []Region {
	sort.Slice(rs, func(i, j int) bool { return rs[i].Start < rs[j].Start })
	out := rs[:0]
	for _, r := range rs {
		if len(out) == 0 || r.Start > out[len(out)-1].End {
			out = append(out, r)
			continue
		}
		if r.End > out[len(out)-1].End {
			out[len(out)-1].End = r.End
		}
	}
	return out
}
