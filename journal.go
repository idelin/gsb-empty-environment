package publisher

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"hash/crc32"
)

// Record types in the write-ahead journal.
const (
	recBegin  = "begin"  // starts a transaction
	recPut    = "put"    // carries the full new content of one virtual file
	recCommit = "commit" // the commit decision; once durable the txn must complete
)

type journalRecord struct {
	Type string `json:"type"`
	Txn  uint64 `json:"txn"`
	Name string `json:"name,omitempty"`
	Data []byte `json:"data,omitempty"` // put records carry the new content
}

// encodeRecords serializes records as [len][crc32][payload] frames so a torn
// tail write is detected and ignored on recovery.
func encodeRecords(recs []journalRecord) ([]byte, error) {
	var buf bytes.Buffer
	for _, r := range recs {
		payload, err := json.Marshal(r)
		if err != nil {
			return nil, err
		}
		var hdr [8]byte
		binary.BigEndian.PutUint32(hdr[0:4], uint32(len(payload)))
		binary.BigEndian.PutUint32(hdr[4:8], crc32.ChecksumIEEE(payload))
		buf.Write(hdr[:])
		buf.Write(payload)
	}
	return buf.Bytes(), nil
}

// decodeRecords parses frames, silently dropping a torn or corrupt tail.
func decodeRecords(b []byte) []journalRecord {
	var recs []journalRecord
	for len(b) >= 8 {
		n := binary.BigEndian.Uint32(b[0:4])
		crc := binary.BigEndian.Uint32(b[4:8])
		if uint64(n) > uint64(len(b)-8) {
			break // torn length
		}
		payload := b[8 : 8+n]
		if crc32.ChecksumIEEE(payload) != crc {
			break // torn or corrupt payload
		}
		var r journalRecord
		if err := json.Unmarshal(payload, &r); err != nil {
			break
		}
		recs = append(recs, r)
		b = b[8+n:]
	}
	return recs
}
