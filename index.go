package sparseidx

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"sort"
)

// Sparse index file layout:
//
//	"SIX1" | u32 formatVersion | u32 stride | u64 totalRecords |
//	u32 entryCount | entryCount * (u64 recordIndex | u64 blockID | u64 offset) |
//	u32 crc32(everything before this field)
const indexMagic = "SIX1"

// indexEntry locates one record exactly: its global index and its position.
type indexEntry struct {
	recordIndex uint64
	blockID     uint64
	offset      uint64
}

// sparseIndex maps every stride-th record to its exact position, so locating
// record k requires decoding at most stride records.
type sparseIndex struct {
	stride  int
	total   uint64
	entries []indexEntry
}

// buildIndex constructs a sparse index over blocks in manifest order.
func buildIndex(blocks []*blockInfo, stride int) *sparseIndex {
	idx := &sparseIndex{stride: stride}
	var g uint64
	for _, b := range blocks {
		for _, off := range b.offsets {
			if g%uint64(stride) == 0 {
				idx.entries = append(idx.entries, indexEntry{recordIndex: g, blockID: b.id, offset: uint64(off)})
			}
			g++
		}
	}
	idx.total = g
	return idx
}

// floor returns the entry with the largest recordIndex <= k.
func (idx *sparseIndex) floor(k uint64) indexEntry {
	i := sort.Search(len(idx.entries), func(i int) bool {
		return idx.entries[i].recordIndex > k
	})
	return idx.entries[i-1]
}

func (idx *sparseIndex) marshal() []byte {
	buf := make([]byte, 0, 24+24*len(idx.entries)+4)
	buf = append(buf, indexMagic...)
	buf = binary.BigEndian.AppendUint32(buf, FormatVersion)
	buf = binary.BigEndian.AppendUint32(buf, uint32(idx.stride))
	buf = binary.BigEndian.AppendUint64(buf, idx.total)
	buf = binary.BigEndian.AppendUint32(buf, uint32(len(idx.entries)))
	for _, e := range idx.entries {
		buf = binary.BigEndian.AppendUint64(buf, e.recordIndex)
		buf = binary.BigEndian.AppendUint64(buf, e.blockID)
		buf = binary.BigEndian.AppendUint64(buf, e.offset)
	}
	return binary.BigEndian.AppendUint32(buf, crc32.ChecksumIEEE(buf))
}

// parseIndex validates the structural integrity (magic, version, lengths,
// checksum) of an index file. Entry offsets are deliberately NOT trusted
// here; they are validated against record boundaries at lookup time.
func parseIndex(data []byte) (*sparseIndex, error) {
	if len(data) < 24+4 {
		return nil, fmt.Errorf("index too short: %w", ErrCorrupt)
	}
	if string(data[:4]) != indexMagic {
		return nil, fmt.Errorf("bad index magic: %w", ErrCorrupt)
	}
	if v := binary.BigEndian.Uint32(data[4:]); v != FormatVersion {
		return nil, fmt.Errorf("unsupported index version %d: %w", v, ErrCorrupt)
	}
	if crc32.ChecksumIEEE(data[:len(data)-4]) != binary.BigEndian.Uint32(data[len(data)-4:]) {
		return nil, fmt.Errorf("index checksum: %w", ErrCorrupt)
	}
	count := int(binary.BigEndian.Uint32(data[20:]))
	if len(data) != 24+24*count+4 {
		return nil, fmt.Errorf("index length mismatch: %w", ErrCorrupt)
	}
	idx := &sparseIndex{
		stride: int(binary.BigEndian.Uint32(data[8:])),
		total:  binary.BigEndian.Uint64(data[12:]),
	}
	off := 24
	for i := 0; i < count; i++ {
		idx.entries = append(idx.entries, indexEntry{
			recordIndex: binary.BigEndian.Uint64(data[off:]),
			blockID:     binary.BigEndian.Uint64(data[off+8:]),
			offset:      binary.BigEndian.Uint64(data[off+16:]),
		})
		off += 24
	}
	return idx, nil
}

// Manifest file layout:
//
//	"SIM1" | u32 formatVersion | u32 blockCount | blockCount * u64 blockID |
//	u32 crc32(everything before this field)
const manifestMagic = "SIM1"

func marshalManifest(ids []uint64) []byte {
	buf := make([]byte, 0, 16+8*len(ids)+4)
	buf = append(buf, manifestMagic...)
	buf = binary.BigEndian.AppendUint32(buf, FormatVersion)
	buf = binary.BigEndian.AppendUint32(buf, uint32(len(ids)))
	for _, id := range ids {
		buf = binary.BigEndian.AppendUint64(buf, id)
	}
	return binary.BigEndian.AppendUint32(buf, crc32.ChecksumIEEE(buf))
}

func parseManifest(data []byte) ([]uint64, error) {
	if len(data) < 12+4 {
		return nil, fmt.Errorf("manifest too short: %w", ErrCorrupt)
	}
	if string(data[:4]) != manifestMagic {
		return nil, fmt.Errorf("bad manifest magic: %w", ErrCorrupt)
	}
	if v := binary.BigEndian.Uint32(data[4:]); v != FormatVersion {
		return nil, fmt.Errorf("unsupported manifest version %d: %w", v, ErrCorrupt)
	}
	if crc32.ChecksumIEEE(data[:len(data)-4]) != binary.BigEndian.Uint32(data[len(data)-4:]) {
		return nil, fmt.Errorf("manifest checksum: %w", ErrCorrupt)
	}
	count := int(binary.BigEndian.Uint32(data[8:]))
	if len(data) != 12+8*count+4 {
		return nil, fmt.Errorf("manifest length mismatch: %w", ErrCorrupt)
	}
	ids := make([]uint64, 0, count)
	off := 12
	for i := 0; i < count; i++ {
		ids = append(ids, binary.BigEndian.Uint64(data[off:]))
		off += 8
	}
	return ids, nil
}
