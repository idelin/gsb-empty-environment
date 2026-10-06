package kvlog

import (
	"encoding/binary"
	"hash/crc32"
	"sort"
)

const dirMagic = 0x4B564C44 // "KVLD"

// dirEntry points at one record in the data log. It is only a hint: the
// data log is the source of truth, so every hit must be re-validated
// against the record it points at.
type dirEntry struct {
	offset  int64
	version uint64
}

// encodeDirFile serializes a directory snapshot:
// magic(4) gen(8) count(4) [ keyLen(4) key offset(8) version(8) ]... crc32(4)
func encodeDirFile(gen uint64, dir map[string]dirEntry) []byte {
	keys := make([]string, 0, len(dir))
	for k := range dir {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	size := 4 + 8 + 4
	for _, k := range keys {
		size += 4 + len(k) + 8 + 8
	}
	buf := make([]byte, size+4)
	binary.LittleEndian.PutUint32(buf[0:4], dirMagic)
	binary.LittleEndian.PutUint64(buf[4:12], gen)
	binary.LittleEndian.PutUint32(buf[12:16], uint32(len(keys)))
	pos := 16
	for _, k := range keys {
		e := dir[k]
		binary.LittleEndian.PutUint32(buf[pos:pos+4], uint32(len(k)))
		pos += 4
		copy(buf[pos:], k)
		pos += len(k)
		binary.LittleEndian.PutUint64(buf[pos:pos+8], uint64(e.offset))
		pos += 8
		binary.LittleEndian.PutUint64(buf[pos:pos+8], e.version)
		pos += 8
	}
	crc := crc32.ChecksumIEEE(buf[:pos])
	binary.LittleEndian.PutUint32(buf[pos:pos+4], crc)
	return buf
}

// decodeDirFile parses and validates a directory snapshot. Any corruption
// (bad magic, bad crc, truncation) yields ErrCorrupt and the caller falls
// back to rebuilding from the data log.
func decodeDirFile(f File) (map[string]dirEntry, uint64, error) {
	size := f.Size()
	if size < 4+8+4+4 || size > 1<<30 {
		return nil, 0, ErrCorrupt
	}
	buf := make([]byte, size)
	if err := readFullAt(f, buf, 0); err != nil {
		return nil, 0, ErrCorrupt
	}
	if binary.LittleEndian.Uint32(buf[0:4]) != dirMagic {
		return nil, 0, ErrCorrupt
	}
	want := binary.LittleEndian.Uint32(buf[len(buf)-4:])
	if crc32.ChecksumIEEE(buf[:len(buf)-4]) != want {
		return nil, 0, ErrCorrupt
	}
	gen := binary.LittleEndian.Uint64(buf[4:12])
	count := binary.LittleEndian.Uint32(buf[12:16])
	entries := make(map[string]dirEntry, count)
	pos := 16
	for i := uint32(0); i < count; i++ {
		if pos+4 > len(buf)-4 {
			return nil, 0, ErrCorrupt
		}
		keyLen := int(binary.LittleEndian.Uint32(buf[pos : pos+4]))
		pos += 4
		if keyLen > maxFieldLen || pos+keyLen+16 > len(buf)-4 {
			return nil, 0, ErrCorrupt
		}
		key := string(buf[pos : pos+keyLen])
		pos += keyLen
		offset := binary.LittleEndian.Uint64(buf[pos : pos+8])
		pos += 8
		version := binary.LittleEndian.Uint64(buf[pos : pos+8])
		pos += 8
		entries[key] = dirEntry{offset: int64(offset), version: version}
	}
	if pos != len(buf)-4 {
		return nil, 0, ErrCorrupt
	}
	return entries, gen, nil
}
