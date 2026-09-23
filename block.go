package sparseidx

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"
)

// Block file layout (immutable once published):
//
//	header:  "SIB1" | u32 formatVersion | u64 blockID
//	body:    record[0] ... record[n-1]
//	footer:  u32 recordCount | u32 crc32(everything before this field)
const (
	blockMagic      = "SIB1"
	blockHeaderSize = 4 + 4 + 8
	blockFooterSize = 4 + 4
)

// blockInfo is a decoded in-memory view of one immutable block.
type blockInfo struct {
	id      uint64
	data    []byte // full file contents
	offsets []int  // start offset of every record
	dataEnd int    // offset just past the last valid record
}

func blockPath(dir string, id uint64) string {
	return filepath.Join(dir, "blocks", fmt.Sprintf("%016x.blk", id))
}

// encodeBlock serializes records into a full block image.
func encodeBlock(id uint64, records []Record) ([]byte, []int) {
	buf := make([]byte, 0, blockHeaderSize+blockFooterSize)
	buf = append(buf, blockMagic...)
	buf = binary.BigEndian.AppendUint32(buf, FormatVersion)
	buf = binary.BigEndian.AppendUint64(buf, id)
	offsets := make([]int, 0, len(records))
	for _, rec := range records {
		offsets = append(offsets, len(buf))
		buf = appendRecord(buf, rec)
	}
	buf = binary.BigEndian.AppendUint32(buf, uint32(len(records)))
	buf = binary.BigEndian.AppendUint32(buf, crc32.ChecksumIEEE(buf))
	return buf, offsets
}

// writeFileAtomic publishes data at path via tmp+fsync+rename+dir-fsync, so a
// crash leaves either the old or the new file, never a half-written one.
func writeFileAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	d, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

// writeBlockFile atomically publishes a new immutable block.
func writeBlockFile(dir string, id uint64, records []Record) (*blockInfo, error) {
	data, offsets := encodeBlock(id, records)
	if err := writeFileAtomic(blockPath(dir, id), data); err != nil {
		return nil, err
	}
	return &blockInfo{id: id, data: data, offsets: offsets, dataEnd: len(data) - blockFooterSize}, nil
}

// checkBlockHeader validates magic and format version, returning the block ID.
func checkBlockHeader(data []byte) (uint64, error) {
	if len(data) < blockHeaderSize+blockFooterSize {
		return 0, ErrTruncated
	}
	if string(data[:4]) != blockMagic {
		return 0, fmt.Errorf("bad block magic: %w", ErrCorrupt)
	}
	if v := binary.BigEndian.Uint32(data[4:]); v != FormatVersion {
		return 0, fmt.Errorf("unsupported format version %d: %w", v, ErrCorrupt)
	}
	return binary.BigEndian.Uint64(data[8:]), nil
}

// readBlockStrict fully validates a block: header, footer checksum, record
// count, and every record boundary and CRC.
func readBlockStrict(data []byte, maxBytes int) (*blockInfo, error) {
	id, err := checkBlockHeader(data)
	if err != nil {
		return nil, err
	}
	dataEnd := len(data) - blockFooterSize
	count := binary.BigEndian.Uint32(data[dataEnd:])
	if crc32.ChecksumIEEE(data[:len(data)-4]) != binary.BigEndian.Uint32(data[len(data)-4:]) {
		return nil, fmt.Errorf("block %d footer checksum: %w", id, ErrCorrupt)
	}
	offsets := make([]int, 0, count)
	off := blockHeaderSize
	for off < dataEnd {
		_, next, err := decodeRecordAt(data, off, maxBytes)
		if err != nil {
			return nil, fmt.Errorf("block %d record %d: %w", id, len(offsets), err)
		}
		offsets = append(offsets, off)
		off = next
	}
	if off != dataEnd || len(offsets) != int(count) {
		return nil, fmt.Errorf("block %d record count mismatch: %w", id, ErrCorrupt)
	}
	return &blockInfo{id: id, data: data, offsets: offsets, dataEnd: dataEnd}, nil
}

// scanBlockPrefix is the crash-recovery path: it decodes records one by one
// and keeps only the valid prefix, so a half-written record at the tail is
// discarded rather than accepted.
func scanBlockPrefix(data []byte, maxBytes int) (*blockInfo, error) {
	id, err := checkBlockHeader(data)
	if err != nil {
		return nil, err
	}
	b := &blockInfo{id: id, data: data, dataEnd: blockHeaderSize}
	off := blockHeaderSize
	for off < len(data) {
		_, next, err := decodeRecordAt(data, off, maxBytes)
		if err != nil {
			break // truncated tail or corrupt record: keep valid prefix only
		}
		b.offsets = append(b.offsets, off)
		off = next
		b.dataEnd = next
	}
	return b, nil
}
