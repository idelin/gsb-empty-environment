package rangecoder

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
)

// Sentinel errors for format verification.
var (
	ErrBadMagic           = errors.New("rangecoder: bad magic")
	ErrUnsupportedVersion = errors.New("rangecoder: unsupported format version")
	ErrBadLength          = errors.New("rangecoder: bad record length")
	ErrBadChecksum        = errors.New("rangecoder: checksum mismatch")
	ErrCorruptStream      = errors.New("rangecoder: corrupt code stream")
	ErrBadOffset          = errors.New("rangecoder: checkpoint offset out of range")
)

// A Checkpoint captures everything needed to resume encoding or to start
// decoding at a segment boundary: the byte offset into the code stream, the
// number of symbols coded so far, and a snapshot of the adaptive model.
//
// Because the range coder is flushed and reset at each checkpoint, segments
// are independent: corruption in one segment cannot affect the decoder state
// (model or bit alignment) of any later segment.
type Checkpoint struct {
	Offset      uint64 // byte offset into the stream where the next segment begins
	SymbolIndex uint64 // number of symbols coded before this checkpoint
	Model       *Model // model snapshot at this point
}

// Checkpoint record format (all integers big-endian):
//
//	magic   4 bytes  "RCKP"
//	version uint16   = CheckpointVersion
//	flags   uint16   = 0 (reserved)
//	length  uint32   payload length in bytes
//	payload length bytes:
//	    offset      uint64  stream offset
//	    symbolIndex uint64  symbols coded so far
//	    model       serialized Model (see model.go)
//	crc32   uint32   IEEE checksum over everything before this field
const (
	CheckpointMagic   = "RCKP"
	CheckpointVersion = 1
	checkpointHdrLen  = 12 // magic + version + flags + length
)

// Marshal serializes the checkpoint record.
func (c Checkpoint) Marshal() []byte {
	mb := c.Model.Marshal()
	payload := make([]byte, 0, 16+len(mb))
	payload = binary.BigEndian.AppendUint64(payload, c.Offset)
	payload = binary.BigEndian.AppendUint64(payload, c.SymbolIndex)
	payload = append(payload, mb...)

	out := make([]byte, 0, checkpointHdrLen+len(payload)+4)
	out = append(out, CheckpointMagic...)
	out = binary.BigEndian.AppendUint16(out, CheckpointVersion)
	out = binary.BigEndian.AppendUint16(out, 0) // flags
	out = binary.BigEndian.AppendUint32(out, uint32(len(payload)))
	out = append(out, payload...)
	return binary.BigEndian.AppendUint32(out, crc32.ChecksumIEEE(out))
}

// ParseCheckpoint parses and verifies a checkpoint record. A record with a
// different format version is rejected with ErrUnsupportedVersion.
func ParseCheckpoint(b []byte) (Checkpoint, error) {
	if len(b) < checkpointHdrLen+4 {
		return Checkpoint{}, ErrBadLength
	}
	if string(b[:4]) != CheckpointMagic {
		return Checkpoint{}, ErrBadMagic
	}
	if v := binary.BigEndian.Uint16(b[4:6]); v != CheckpointVersion {
		return Checkpoint{}, fmt.Errorf("%w: checkpoint version %d, want %d",
			ErrUnsupportedVersion, v, CheckpointVersion)
	}
	if n := binary.BigEndian.Uint32(b[8:12]); uint64(n) != uint64(len(b))-checkpointHdrLen-4 {
		return Checkpoint{}, fmt.Errorf("%w: payload length %d, record holds %d",
			ErrBadLength, n, len(b)-checkpointHdrLen-4)
	}
	want := binary.BigEndian.Uint32(b[len(b)-4:])
	if got := crc32.ChecksumIEEE(b[:len(b)-4]); got != want {
		return Checkpoint{}, ErrBadChecksum
	}
	p := b[checkpointHdrLen : len(b)-4]
	m, err := UnmarshalModel(p[16:])
	if err != nil {
		return Checkpoint{}, err
	}
	return Checkpoint{
		Offset:      binary.BigEndian.Uint64(p[0:8]),
		SymbolIndex: binary.BigEndian.Uint64(p[8:16]),
		Model:       m,
	}, nil
}

// Checkpoint table format (all integers big-endian):
//
//	magic   4 bytes  "RCKT"
//	version uint16   = TableVersion
//	count   uint32   number of records
//	records count times:
//	    reclen  uint32  length of the checkpoint record that follows
//	    record  checkpoint record as produced by Checkpoint.Marshal
//	crc32   uint32   IEEE checksum over everything before this field
const (
	TableMagic   = "RCKT"
	TableVersion = 1
	tableHdrLen  = 10
)

// MarshalCheckpointTable serializes a list of checkpoints into one blob.
func MarshalCheckpointTable(cps []Checkpoint) []byte {
	out := make([]byte, 0, tableHdrLen+len(cps)*(checkpointHdrLen+64)+4)
	out = append(out, TableMagic...)
	out = binary.BigEndian.AppendUint16(out, TableVersion)
	out = binary.BigEndian.AppendUint32(out, uint32(len(cps)))
	for _, c := range cps {
		rec := c.Marshal()
		out = binary.BigEndian.AppendUint32(out, uint32(len(rec)))
		out = append(out, rec...)
	}
	return binary.BigEndian.AppendUint32(out, crc32.ChecksumIEEE(out))
}

// ParseCheckpointTable parses and verifies a checkpoint table blob.
func ParseCheckpointTable(b []byte) ([]Checkpoint, error) {
	if len(b) < tableHdrLen+4 {
		return nil, ErrBadLength
	}
	if string(b[:4]) != TableMagic {
		return nil, ErrBadMagic
	}
	if v := binary.BigEndian.Uint16(b[4:6]); v != TableVersion {
		return nil, fmt.Errorf("%w: table version %d, want %d",
			ErrUnsupportedVersion, v, TableVersion)
	}
	want := binary.BigEndian.Uint32(b[len(b)-4:])
	if got := crc32.ChecksumIEEE(b[:len(b)-4]); got != want {
		return nil, ErrBadChecksum
	}
	count := binary.BigEndian.Uint32(b[6:10])
	cps := make([]Checkpoint, 0, count)
	pos := tableHdrLen
	end := len(b) - 4
	for i := uint32(0); i < count; i++ {
		if pos+4 > end {
			return nil, ErrBadLength
		}
		n := int(binary.BigEndian.Uint32(b[pos:]))
		pos += 4
		if n < checkpointHdrLen+4 || pos+n > end {
			return nil, ErrBadLength
		}
		cp, err := ParseCheckpoint(b[pos : pos+n])
		if err != nil {
			return nil, fmt.Errorf("record %d: %w", i, err)
		}
		cps = append(cps, cp)
		pos += n
	}
	if pos != end {
		return nil, ErrBadLength
	}
	return cps, nil
}
