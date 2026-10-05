package segvm

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
)

// genesisHash is the Prev value of the first record.
const genesisHash = "GENESIS"

// maxRecordSize bounds a single record payload; anything larger is treated
// as a torn/corrupt length prefix.
const maxRecordSize = 4 << 20

// ErrCorruptRecord is returned (wrapped) when a *complete* log record fails
// validation. Only an incomplete trailing record is tolerated.
var ErrCorruptRecord = errors.New("segvm: corrupt complete record")

// SegmentMeta describes one segment as recorded in the manifest.
type SegmentMeta struct {
	Name   string `json:"name"`
	Digest string `json:"digest"` // hex sha256 of content; also the file name
	Size   int64  `json:"size"`
}

// Record is one version entry in manifest.log.
type Record struct {
	Version uint64        `json:"version"`
	Prev    string        `json:"prev"` // hex sha256 of previous record's payload
	Adds    []SegmentMeta `json:"adds,omitempty"`
	Removes []string      `json:"removes,omitempty"`
}

// On-disk frame: uint32 BE payload length | JSON payload | uint32 BE CRC32(payload).
func encodeRecord(rec *Record) []byte {
	payload, err := json.Marshal(rec)
	if err != nil {
		panic(err) // Record is always marshalable
	}
	frame := make([]byte, 8+len(payload))
	binary.BigEndian.PutUint32(frame[0:4], uint32(len(payload)))
	copy(frame[4:], payload)
	binary.BigEndian.PutUint32(frame[4+len(payload):], crc32.ChecksumIEEE(payload))
	return frame
}

// recordHash is the chain hash of a record: sha256 of its canonical payload.
// encoding/json marshals struct fields in declaration order, so this is
// deterministic for a given Record value.
func recordHash(rec *Record) string {
	payload, err := json.Marshal(rec)
	if err != nil {
		panic(err)
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

// scanLog parses and validates the whole log image.
//
// It returns the validated records and the number of bytes they occupy.
// Bytes beyond that form an incomplete trailing record (torn write): the
// caller may truncate them. A *complete* record that fails CRC, JSON,
// version-sequence or hash-chain validation is a hard error.
func scanLog(data []byte) (recs []*Record, validLen int, err error) {
	prev := genesisHash
	wantVersion := uint64(1)
	pos := 0
	for {
		remaining := len(data) - pos
		if remaining == 0 {
			break
		}
		if remaining < 4 {
			break // torn length prefix -> incomplete tail
		}
		l := int(binary.BigEndian.Uint32(data[pos : pos+4]))
		if l <= 0 || l > maxRecordSize {
			if remaining < 8+l || l < 0 {
				break // implausible length that does not fit -> torn tail
			}
			return nil, 0, fmt.Errorf("%w: bad length %d at offset %d", ErrCorruptRecord, l, pos)
		}
		if remaining < 8+l {
			break // torn payload/crc -> incomplete tail
		}
		payload := data[pos+4 : pos+4+l]
		crc := binary.BigEndian.Uint32(data[pos+4+l : pos+8+l])
		if crc32.ChecksumIEEE(payload) != crc {
			return nil, 0, fmt.Errorf("%w: checksum mismatch at offset %d", ErrCorruptRecord, pos)
		}
		var rec Record
		if uerr := json.Unmarshal(payload, &rec); uerr != nil {
			return nil, 0, fmt.Errorf("%w: invalid JSON at offset %d: %v", ErrCorruptRecord, pos, uerr)
		}
		if rec.Version != wantVersion {
			return nil, 0, fmt.Errorf("segvm: version chain broken at offset %d: got version %d, want %d", pos, rec.Version, wantVersion)
		}
		if rec.Prev != prev {
			return nil, 0, fmt.Errorf("segvm: hash chain broken at version %d", rec.Version)
		}
		prev = recordHash(&rec)
		wantVersion++
		recs = append(recs, &rec)
		pos += 8 + l
	}
	return recs, pos, nil
}
