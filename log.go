package manifestmgr

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash/crc32"
)

// Commit log framing (see FORMAT.md):
//
//	frame := len(4, big-endian) | payload(len bytes, JSON Record) | crc32(4, IEEE, of payload)
//
// Recovery rule: a trailing partial frame (short length, short payload, or
// short CRC) is an incomplete last record and is tolerated by truncating the
// log. A complete frame whose CRC does not match is a corrupt record and is
// a hard error.

const maxRecordSize = 32 << 20 // 32 MiB sanity bound on a single record

// encodeFrame serializes a record into a framed log entry.
func encodeFrame(r *Record) ([]byte, error) {
	payload, err := json.Marshal(r)
	if err != nil {
		return nil, err
	}
	frame := make([]byte, 4+len(payload)+4)
	binary.BigEndian.PutUint32(frame[:4], uint32(len(payload)))
	copy(frame[4:], payload)
	binary.BigEndian.PutUint32(frame[4+len(payload):], crc32.ChecksumIEEE(payload))
	return frame, nil
}

// payloadDigest is the chain digest stored in the next record's PrevDigest.
func payloadDigest(frame []byte) string {
	payload := frame[4 : len(frame)-4]
	return DigestOf(payload)
}

// replayLog parses the raw log bytes into records. It returns the records,
// the offset of the first byte not covered by a valid complete frame
// (logLen), and whether a torn tail was found and must be truncated.
func replayLog(data []byte) (records []*Record, logLen int, tornTail bool, err error) {
	off := 0
	for off < len(data) {
		rem := data[off:]
		if len(rem) < 4 {
			return records, off, true, nil // incomplete length field
		}
		n := binary.BigEndian.Uint32(rem[:4])
		if n == 0 || n > maxRecordSize {
			return nil, 0, false, fmt.Errorf("corrupt record at offset %d: implausible length %d", off, n)
		}
		frameLen := 4 + int(n) + 4
		if len(rem) < frameLen {
			return records, off, true, nil // incomplete payload/crc: torn tail
		}
		payload := rem[4 : 4+int(n)]
		wantCRC := binary.BigEndian.Uint32(rem[4+int(n) : frameLen])
		if crc32.ChecksumIEEE(payload) != wantCRC {
			return nil, 0, false, fmt.Errorf("corrupt record at offset %d: CRC mismatch on complete frame", off)
		}
		var rec Record
		if jerr := json.Unmarshal(payload, &rec); jerr != nil {
			return nil, 0, false, fmt.Errorf("corrupt record at offset %d: %v", off, jerr)
		}
		records = append(records, &rec)
		off += frameLen
	}
	return records, off, false, nil
}

// validateChain checks the increasing version chain, the prev-digest links,
// and that each record's full segment table follows from its added/removed
// deltas applied to the previous record.
func validateChain(records []*Record, rawLog []byte) error {
	var prev *Record
	prevDigest := ""
	off := 0
	for i, rec := range records {
		wantVersion := uint64(i + 1)
		if rec.Version != wantVersion {
			return fmt.Errorf("broken version chain: record %d has version %d, want %d", i, rec.Version, wantVersion)
		}
		if rec.PrevDigest != prevDigest {
			return fmt.Errorf("broken digest chain at version %d", rec.Version)
		}
		rebuilt, err := buildRecord(prev, rec.Added, rec.Removed, prevDigest)
		if err != nil {
			return fmt.Errorf("version %d: invalid delta: %v", rec.Version, err)
		}
		if !segmentsEqual(rebuilt.Segments, rec.Segments) {
			return fmt.Errorf("version %d: segment table does not match added/removed deltas", rec.Version)
		}
		// Advance the digest chain using the exact frame bytes on disk.
		n := binary.BigEndian.Uint32(rawLog[off : off+4])
		frame := rawLog[off : off+4+int(n)+4]
		prevDigest = payloadDigest(frame)
		off += len(frame)
		prev = rec
	}
	return nil
}

func segmentsEqual(a, b []Segment) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
