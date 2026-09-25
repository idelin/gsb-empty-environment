package main

import (
	"encoding/binary"
	"hash/crc32"
	"os"
)

const (
	recordHeaderLen  = 8
	recordTrailerLen = 4
	maxKeyLen        = 1 << 20
	maxValLen        = 1 << 30
)

// Record layout: u32le keyLen | u32le valLen | key | value | u32le crc32(key|value)
func encodeRecord(key, value []byte) []byte {
	buf := make([]byte, 0, recordHeaderLen+len(key)+len(value)+recordTrailerLen)
	var hdr [recordHeaderLen]byte
	binary.LittleEndian.PutUint32(hdr[0:4], uint32(len(key)))
	binary.LittleEndian.PutUint32(hdr[4:8], uint32(len(value)))
	buf = append(buf, hdr[:]...)
	bodyStart := len(buf)
	buf = append(buf, key...)
	buf = append(buf, value...)
	crc := crc32.ChecksumIEEE(buf[bodyStart:])
	var tr [recordTrailerLen]byte
	binary.LittleEndian.PutUint32(tr[:], crc)
	buf = append(buf, tr[:]...)
	return buf
}

type scannedRecord struct {
	Key    string
	Offset int64
	Length int64
}

// scanSegment decodes records sequentially and stops at the first malformed
// one, reporting tailCorrupt so callers can distinguish a clean end from
// truncation or bit rot.
func scanSegment(data []byte) (recs []scannedRecord, tailCorrupt bool) {
	var off int64
	for off < int64(len(data)) {
		if int64(len(data))-off < recordHeaderLen {
			return recs, true
		}
		keyLen := binary.LittleEndian.Uint32(data[off:])
		valLen := binary.LittleEndian.Uint32(data[off+4:])
		if keyLen > maxKeyLen || valLen > maxValLen {
			return recs, true
		}
		recLen := int64(recordHeaderLen) + int64(keyLen) + int64(valLen) + recordTrailerLen
		if off+recLen > int64(len(data)) {
			return recs, true
		}
		body := data[off+recordHeaderLen : off+recLen-recordTrailerLen]
		wantCRC := binary.LittleEndian.Uint32(data[off+recLen-recordTrailerLen:])
		if crc32.ChecksumIEEE(body) != wantCRC {
			return recs, true
		}
		recs = append(recs, scannedRecord{
			Key:    string(body[:keyLen]),
			Offset: off,
			Length: recLen,
		})
		off += recLen
	}
	return recs, false
}

// buildIndex scans the named segments in order; later records override
// earlier ones for the same key (append-only log semantics).
func buildIndex(segData map[string][]byte, names []string) *Index {
	idx := &Index{Version: 1, Entries: map[string]IndexEntry{}}
	for _, name := range names {
		recs, _ := scanSegment(segData[name])
		for _, r := range recs {
			idx.Entries[r.Key] = IndexEntry{Segment: name, Offset: r.Offset, Length: r.Length}
		}
	}
	return idx
}

func loadSegments(dir string, names []string) (map[string][]byte, error) {
	out := make(map[string][]byte, len(names))
	for _, name := range names {
		data, err := os.ReadFile(segmentPath(dir, name))
		if err != nil {
			return nil, err
		}
		out[name] = data
	}
	return out, nil
}
