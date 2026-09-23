package rangecoder

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
)

// Symbol alphabet: 256 byte values plus one end-of-segment symbol.
const (
	NumSymbols = 257
	EOFSymbol  = 256
)

const (
	maxTotalFreq = 1 << 14 // rescale threshold for the adaptive model
	updateStep   = 16      // frequency increment per observed symbol
)

// Checkpoint wire format (all integers big-endian unless noted):
//
//	magic     4 bytes  "RGCP"
//	version   uint16    checkpoint format version (checkpointVersion)
//	length    uint32    payload length in bytes
//	checksum  uint32    CRC32-IEEE over version||length||payload
//	payload   length bytes:
//	    numSymbols  uvarint  (must equal NumSymbols)
//	    total       uvarint  (must equal sum of freqs)
//	    freq[0..]   uvarint x numSymbols (each >= 1)
const (
	checkpointMagic   = "RGCP"
	checkpointVersion = 1
	checkpointHdrLen  = 4 + 2 + 4 + 4
	maxCheckpointSize = 1 << 20
)

var (
	ErrBadMagic    = errors.New("rangecoder: bad checkpoint magic")
	ErrBadVersion  = errors.New("rangecoder: unsupported checkpoint version")
	ErrBadLength   = errors.New("rangecoder: checkpoint length mismatch")
	ErrBadChecksum = errors.New("rangecoder: checkpoint checksum mismatch")
	ErrBadPayload  = errors.New("rangecoder: corrupt checkpoint payload")
)

// Model is an adaptive order-0 frequency model. It is the only state that
// carries across a segment boundary, so snapshotting/restoring it is enough
// to resume coding deterministically.
type Model struct {
	freq  [NumSymbols]uint32
	total uint32
}

func NewModel() *Model {
	m := &Model{}
	for i := range m.freq {
		m.freq[i] = 1
	}
	m.total = NumSymbols
	return m
}

func (m *Model) cumFreq(sym int) uint32 {
	var c uint32
	for i := 0; i < sym; i++ {
		c += m.freq[i]
	}
	return c
}

func (m *Model) update(sym int) {
	m.freq[sym] += updateStep
	m.total += updateStep
	if m.total > maxTotalFreq {
		var t uint32
		for i := range m.freq {
			m.freq[i] = (m.freq[i] + 1) >> 1
			t += m.freq[i]
		}
		m.total = t
	}
}

// symbolFor maps a cumulative value v < total back to its symbol.
func (m *Model) symbolFor(v uint32) (sym int, cum uint32) {
	var c uint32
	for i := 0; i < NumSymbols; i++ {
		if v < c+m.freq[i] {
			return i, c
		}
		c += m.freq[i]
	}
	return -1, 0 // unreachable when v < total
}

// Snapshot serializes the model into a self-verifying checkpoint.
func (m *Model) Snapshot() []byte {
	payload := make([]byte, 0, 3*NumSymbols)
	payload = binary.AppendUvarint(payload, uint64(NumSymbols))
	payload = binary.AppendUvarint(payload, uint64(m.total))
	for _, f := range m.freq {
		payload = binary.AppendUvarint(payload, uint64(f))
	}

	out := make([]byte, 0, checkpointHdrLen+len(payload))
	out = append(out, checkpointMagic...)
	out = binary.BigEndian.AppendUint16(out, checkpointVersion)
	out = binary.BigEndian.AppendUint32(out, uint32(len(payload)))
	crc := crc32.NewIEEE()
	crc.Write(out[4 : checkpointHdrLen-4]) // version || length
	crc.Write(payload)
	out = binary.BigEndian.AppendUint32(out, crc.Sum32())
	return append(out, payload...)
}

// ModelFromCheckpoint parses and verifies a checkpoint produced by Snapshot.
func ModelFromCheckpoint(cp []byte) (*Model, error) {
	if len(cp) < checkpointHdrLen {
		return nil, ErrBadLength
	}
	if string(cp[:4]) != checkpointMagic {
		return nil, ErrBadMagic
	}
	if v := binary.BigEndian.Uint16(cp[4:6]); v != checkpointVersion {
		return nil, ErrBadVersion
	}
	payloadLen := binary.BigEndian.Uint32(cp[6:10])
	if payloadLen > maxCheckpointSize {
		return nil, ErrBadLength
	}
	if uint64(len(cp)) != uint64(checkpointHdrLen)+uint64(payloadLen) {
		return nil, ErrBadLength
	}
	payload := cp[checkpointHdrLen:]
	crc := crc32.NewIEEE()
	crc.Write(cp[4 : checkpointHdrLen-4])
	crc.Write(payload)
	if crc.Sum32() != binary.BigEndian.Uint32(cp[10:14]) {
		return nil, ErrBadChecksum
	}

	m := &Model{}
	rest := payload
	n, rest, ok := takeUvarint(rest)
	if !ok || n != NumSymbols {
		return nil, ErrBadPayload
	}
	total, rest, ok := takeUvarint(rest)
	if !ok || total == 0 || total > maxTotalFreq {
		return nil, ErrBadPayload
	}
	var sum uint64
	for i := 0; i < NumSymbols; i++ {
		var f uint64
		f, rest, ok = takeUvarint(rest)
		if !ok || f == 0 || f > maxTotalFreq {
			return nil, ErrBadPayload
		}
		m.freq[i] = uint32(f)
		sum += f
	}
	if len(rest) != 0 {
		return nil, ErrBadPayload
	}
	if sum != total {
		return nil, ErrBadPayload
	}
	m.total = uint32(total)
	return m, nil
}

func takeUvarint(b []byte) (uint64, []byte, bool) {
	v, n := binary.Uvarint(b)
	if n <= 0 {
		return 0, nil, false
	}
	return v, b[n:], true
}
