package batchlease

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"
)

// Frame layout (all integers little-endian):
//
//	magic   [2]byte  frameMagic
//	recType uint8    one of rec* constants
//	length  uint32   payload length (<= maxPayload)
//	payload [length] byte
//	crc     uint32   IEEE CRC-32 over magic..payload (everything before crc)
//	trailer uint32   frameTrailer
//
// The fixed overhead per record is headerSize + crcSize + trailerSize.
const (
	frameMagic0  byte = 0xB1
	frameMagic1  byte = 0x5E
	frameTrailer      = 0x1E5EA5E1

	headerSize    = 2 + 1 + 4
	crcSize       = 4
	trailerSize   = 4
	frameOverhead = headerSize + crcSize + trailerSize
	maxPayload    = 8 << 20 // 8 MiB, guards against absurd length fields
)

// Record types.
const (
	recEpoch    byte = 1 // payload: version + uint64 epoch
	recLease    byte = 2 // payload: version + lease fields
	recConfirm  byte = 3 // payload: version + uint64 sequence
	recSnapshot byte = 4 // payload: version + snapshot fields
)

const payloadVersion byte = 1

var (
	crcTable = crc32.MakeTable(crc32.IEEE)

	errFrameMagic   = errors.New("batchlease: invalid frame magic (torn or truncated log tail)")
	errFrameSize    = errors.New("batchlease: invalid frame length")
	errFrameCRC     = errors.New("batchlease: frame checksum mismatch (torn log tail)")
	errFrameTrailer = errors.New("batchlease: invalid frame trailer (torn log tail)")
)

// buildFrame encodes a single self-checksumming log frame.
func buildFrame(recType byte, payload []byte) []byte {
	if len(payload) > maxPayload {
		panic("batchlease: payload exceeds max frame size")
	}
	buf := make([]byte, frameOverhead+len(payload))
	buf[0] = frameMagic0
	buf[1] = frameMagic1
	buf[2] = recType
	binary.LittleEndian.PutUint32(buf[3:7], uint32(len(payload)))
	copy(buf[headerSize:], payload)
	sum := crc32.Checksum(buf[:headerSize+len(payload)], crcTable)
	binary.LittleEndian.PutUint32(buf[headerSize+len(payload):], sum)
	binary.LittleEndian.PutUint32(buf[headerSize+len(payload)+crcSize:], frameTrailer)
	return buf
}

// readFrame reads one frame from r. A torn tail (short read, bad magic, bad
// checksum or bad trailer) is reported with errTornTail so the caller can
// safely truncate the log at frameOffset. A structurally impossible length
// field is treated as hard corruption.
func readFrame(r io.Reader) (recType byte, payload []byte, err error) {
	var header [headerSize]byte
	n, err := io.ReadFull(r, header[:])
	if err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			if n == 0 && errors.Is(err, io.EOF) {
				return 0, nil, err
			}
			return 0, nil, errTornTail
		}
		return 0, nil, err
	}
	if header[0] != frameMagic0 || header[1] != frameMagic1 {
		return 0, nil, errFrameMagic
	}
	recType = header[2]
	length := binary.LittleEndian.Uint32(header[3:7])
	if length > maxPayload {
		return 0, nil, errFrameSize
	}

	body := make([]byte, length+crcSize+trailerSize)
	if _, err := io.ReadFull(r, body); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return 0, nil, errTornTail
		}
		return 0, nil, err
	}
	payload = body[:length]
	storedCRC := binary.LittleEndian.Uint32(body[length : length+crcSize])
	if binary.LittleEndian.Uint32(body[length+crcSize:]) != frameTrailer {
		return 0, nil, errFrameTrailer
	}

	h := crc32.New(crcTable)
	h.Write(header[:])
	h.Write(payload)
	if h.Sum32() != storedCRC {
		return 0, nil, errFrameCRC
	}
	return recType, payload, nil
}

// --- payload codecs ---------------------------------------------------------
//
// Every payload starts with a version byte so future SDK releases can evolve
// the on-disk format while older builds reject unknown versions explicitly.

func putU64(b []byte, v uint64) { binary.LittleEndian.PutUint64(b, v) }
func u64(b []byte) uint64       { return binary.LittleEndian.Uint64(b) }

func encodeEpoch(epoch uint64) []byte {
	p := make([]byte, 1+8)
	p[0] = payloadVersion
	putU64(p[1:], epoch)
	return p
}

func decodeEpoch(p []byte) (uint64, error) {
	if len(p) != 9 || p[0] != payloadVersion {
		return 0, errCorruptRecord
	}
	return u64(p[1:]), nil
}

func encodeLease(tok Token, firstSeq, lastSeq uint64, hash []byte) []byte {
	p := make([]byte, 1+8+1+len(tok.BatchID)+8+8+4+len(hash))
	i := 0
	p[i] = payloadVersion
	i++
	putU64(p[i:], tok.Epoch)
	i += 8
	p[i] = byte(len(tok.BatchID))
	i++
	copy(p[i:], tok.BatchID)
	i += len(tok.BatchID)
	putU64(p[i:], firstSeq)
	i += 8
	putU64(p[i:], lastSeq)
	i += 8
	binary.LittleEndian.PutUint32(p[i:], uint32(len(hash)))
	i += 4
	copy(p[i:], hash)
	return p
}

func decodeLease(p []byte) (Token, uint64, uint64, []byte, error) {
	// version + epoch + id-length byte, then at least one ID byte, two
	// uint64 ranges and a uint32 hash length; the hash itself and its exact
	// size are validated after decoding the length field.
	if len(p) < 1+8+1+1+8+8+4 || p[0] != payloadVersion {
		return Token{}, 0, 0, nil, errCorruptRecord
	}
	i := 1
	epoch := u64(p[i:])
	i += 8
	idLen := int(p[i])
	i++
	if idLen == 0 || len(p) < i+idLen+8+8+4 {
		return Token{}, 0, 0, nil, errCorruptRecord
	}
	batchID := string(p[i : i+idLen])
	i += idLen
	firstSeq := u64(p[i:])
	i += 8
	lastSeq := u64(p[i:])
	i += 8
	hashLen := int(binary.LittleEndian.Uint32(p[i:]))
	i += 4
	if hashLen == 0 || len(p) != i+hashLen {
		return Token{}, 0, 0, nil, errCorruptRecord
	}
	hash := append([]byte(nil), p[i:]...)
	return Token{BatchID: batchID, Epoch: epoch}, firstSeq, lastSeq, hash, nil
}

func encodeConfirm(seq uint64) []byte {
	p := make([]byte, 1+8)
	p[0] = payloadVersion
	putU64(p[1:], seq)
	return p
}

func decodeConfirm(p []byte) (uint64, error) {
	if len(p) != 9 || p[0] != payloadVersion {
		return 0, errCorruptRecord
	}
	return u64(p[1:]), nil
}

// Snapshot payload: version, epoch, firstSeq, confirmed, then
// repeat( batchID len uint16, batchID, epoch, firstSeq, lastSeq, hashLen, hash ).
func encodeSnapshot(epoch, firstSeq, confirmed uint64, leases []leaseEntry) []byte {
	size := 1 + 8 + 8 + 8
	for _, le := range leases {
		size += 2 + len(le.token.BatchID) + 8 + 8 + 8 + 4 + len(le.hash)
	}
	p := make([]byte, size)
	i := 0
	p[i] = payloadVersion
	i++
	putU64(p[i:], epoch)
	i += 8
	putU64(p[i:], firstSeq)
	i += 8
	putU64(p[i:], confirmed)
	i += 8
	for _, le := range leases {
		binary.LittleEndian.PutUint16(p[i:], uint16(len(le.token.BatchID)))
		i += 2
		copy(p[i:], le.token.BatchID)
		i += len(le.token.BatchID)
		putU64(p[i:], le.token.Epoch)
		i += 8
		putU64(p[i:], le.firstSeq)
		i += 8
		putU64(p[i:], le.lastSeq)
		i += 8
		binary.LittleEndian.PutUint32(p[i:], uint32(len(le.hash)))
		i += 4
		copy(p[i:], le.hash)
		i += len(le.hash)
	}
	return p
}

func decodeSnapshot(p []byte) (epoch, firstSeq, confirmed uint64, leases []leaseEntry, err error) {
	if len(p) < 1+8+8+8 || p[0] != payloadVersion {
		return 0, 0, 0, nil, errCorruptRecord
	}
	i := 1
	epoch = u64(p[i:])
	i += 8
	firstSeq = u64(p[i:])
	i += 8
	confirmed = u64(p[i:])
	i += 8
	leases = nil
	for i < len(p) {
		if len(p)-i < 2 {
			return 0, 0, 0, nil, errCorruptRecord
		}
		idLen := int(binary.LittleEndian.Uint16(p[i:]))
		i += 2
		if idLen == 0 || len(p)-i < idLen+8+8+8+4 {
			return 0, 0, 0, nil, errCorruptRecord
		}
		le := leaseEntry{}
		le.token.BatchID = string(p[i : i+idLen])
		i += idLen
		le.token.Epoch = u64(p[i:])
		i += 8
		le.firstSeq = u64(p[i:])
		i += 8
		le.lastSeq = u64(p[i:])
		i += 8
		hashLen := int(binary.LittleEndian.Uint32(p[i:]))
		i += 4
		if i+hashLen > len(p) {
			return 0, 0, 0, nil, errCorruptRecord
		}
		le.hash = append([]byte(nil), p[i:i+hashLen]...)
		i += hashLen
		leases = append(leases, le)
	}
	return epoch, firstSeq, confirmed, leases, nil
}
