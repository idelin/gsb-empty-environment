package twopc

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"hash/crc32"
)

// Persistent log record kinds.
//
// Coordinator (per transaction id):
//
//	RecBegin    — participants list + writeset are durable before phase 1
//	RecCommit   — the commit decision is durable before any COMMIT is sent
//	RecAbort    — the abort decision is durable
//
// Participant (per transaction id):
//
//	RecPrepared — vote YES and all locks are durable before PREPARED is sent
//	RecCommitted — local commit applied after COMMIT
//	RecAborted   — local abort applied (vote NO, timeout, or ABORT)
const (
	RecBegin = iota + 1
	RecCommit
	RecAbort
	RecPrepared
	RecCommitted
)

// Rec is one durable log record. Kind selects which fields are meaningful.
type Rec struct {
	Kind         int      `json:"k"`
	Txn          string   `json:"t"`
	Coordinator  string   `json:"c,omitempty"`
	Participants []string `json:"p,omitempty"`
	Keys         []string `json:"keys,omitempty"`
	Values       []string `json:"vals,omitempty"` // parallel to Keys; nil = delete
	Reason       string   `json:"r,omitempty"`
}

// ErrTruncatedTail reports that the last log sector on disk is torn. Such a
// record must never be acted upon. In this simulator disk appends are
// atomic, so a torn tail can only be injected by a test.
var ErrTruncatedTail = errors.New("twopc: truncated or corrupt trailing WAL record")

func putUvarint(b []byte, v uint64) []byte {
	var buf [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(buf[:], v)
	return append(b, buf[:n]...)
}

// Encode serialises a record as: uvarint length | JSON payload | uint32 CRC
// (IEEE CRC of the JSON payload, little-endian).
func (r Rec) Encode() ([]byte, error) {
	payload, err := json.Marshal(r)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 0, len(payload)+10)
	out = putUvarint(out, uint64(len(payload)))
	out = append(out, payload...)
	var crc [4]byte
	binary.LittleEndian.PutUint32(crc[:], crc32.ChecksumIEEE(payload))
	out = append(out, crc[:]...)
	return out, nil
}

// DecodeRecord parses one record from the front of b and returns the number
// of bytes consumed.
func DecodeRecord(b []byte) (Rec, int, error) {
	ln, n := binary.Uvarint(b)
	if n <= 0 {
		return Rec{}, 0, ErrTruncatedTail
	}
	total := n + int(ln) + 4
	if uint64(len(b)-n) < ln+4 {
		return Rec{}, 0, ErrTruncatedTail
	}
	payload := b[n : n+int(ln)]
	want := binary.LittleEndian.Uint32(b[n+int(ln) : total])
	if crc32.ChecksumIEEE(payload) != want {
		return Rec{}, 0, ErrTruncatedTail
	}
	var r Rec
	if err := json.Unmarshal(payload, &r); err != nil {
		return Rec{}, 0, err
	}
	return r, total, nil
}

// DecodeAll parses every record. A torn/corrupt trailing record yields the
// intact prefix plus ErrTruncatedTail.
func DecodeAll(raw [][]byte) ([]Rec, error) {
	var recs []Rec
	var firstErr error
	for _, chunk := range raw {
		b := chunk
		for len(b) > 0 {
			r, n, err := DecodeRecord(b)
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				b = nil // stop this chunk
				break
			}
			recs = append(recs, r)
			b = b[n:]
		}
	}
	return recs, firstErr
}
