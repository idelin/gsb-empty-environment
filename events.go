package netsec

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"hash/crc32"
)

// EventType identifies the lifecycle stage recorded in the log.
type EventType uint8

const (
	// EventProofReceived records that an authorization proof arrived.
	EventProofReceived EventType = 1 + iota
	// EventVerified records that the proof finished validation.
	EventVerified
	// EventCommit records a committed authorization upgrade. Only a
	// complete, checksum-valid commit may restore a permission.
	EventCommit
	// EventRevoke records revocation of a previously granted permission.
	EventRevoke
)

func (t EventType) String() string {
	switch t {
	case EventProofReceived:
		return "proof-received"
	case EventVerified:
		return "verified"
	case EventCommit:
		return "commit"
	case EventRevoke:
		return "revoke"
	default:
		return "unknown"
	}
}

// Event is a single durable log entry.
type Event struct {
	Type       EventType `json:"type"`
	Subject    string    `json:"subject"`
	Permission string    `json:"permission"`
	Proof      string    `json:"proof,omitempty"`
}

// ErrCorrupt is returned when the log is damaged in the middle: a
// checksum-invalid record is followed by further valid records, so the
// damage cannot be a torn tail and recovery must be refused.
var ErrCorrupt = errors.New("netsec: log corrupt, recovery refused")

var errBadRecord = errors.New("netsec: invalid record")

const (
	recordMagic   = 0x4E534C31 // "NSL1"
	recordHdrSize = 4 + 8 + 1 + 2
	recordCRCSize = 4
)

// Record layout:
//
//	magic   uint32
//	seq     uint64
//	type    uint8
//	paylen  uint16
//	payload paylen bytes (JSON Event)
//	crc32   uint32 (IEEE, over magic..payload)
func encodeRecord(seq uint64, e Event) ([]byte, error) {
	payload, err := json.Marshal(e)
	if err != nil {
		return nil, err
	}
	buf := make([]byte, recordHdrSize+len(payload)+recordCRCSize)
	binary.LittleEndian.PutUint32(buf[0:4], recordMagic)
	binary.LittleEndian.PutUint64(buf[4:12], seq)
	buf[12] = byte(e.Type)
	binary.LittleEndian.PutUint16(buf[13:15], uint16(len(payload)))
	copy(buf[recordHdrSize:], payload)
	crc := crc32.ChecksumIEEE(buf[:recordHdrSize+len(payload)])
	binary.LittleEndian.PutUint32(buf[recordHdrSize+len(payload):], crc)
	return buf, nil
}

// decodeRecordAt decodes the record starting exactly at off.
func decodeRecordAt(buf []byte, off int) (Event, uint64, int, error) {
	var e Event
	if off < 0 || len(buf)-off < recordHdrSize+recordCRCSize {
		return e, 0, 0, errBadRecord
	}
	if binary.LittleEndian.Uint32(buf[off:off+4]) != recordMagic {
		return e, 0, 0, errBadRecord
	}
	seq := binary.LittleEndian.Uint64(buf[off+4 : off+12])
	plen := int(binary.LittleEndian.Uint16(buf[off+13 : off+15]))
	total := recordHdrSize + plen + recordCRCSize
	if len(buf)-off < total {
		return e, 0, 0, errBadRecord
	}
	body := buf[off : off+recordHdrSize+plen]
	want := binary.LittleEndian.Uint32(buf[off+recordHdrSize+plen : off+total])
	if crc32.ChecksumIEEE(body) != want {
		return e, 0, 0, errBadRecord
	}
	if err := json.Unmarshal(buf[off+recordHdrSize:off+recordHdrSize+plen], &e); err != nil {
		return e, 0, 0, errBadRecord
	}
	switch e.Type {
	case EventProofReceived, EventVerified, EventCommit, EventRevoke:
	default:
		return e, 0, 0, errBadRecord
	}
	return e, seq, total, nil
}
