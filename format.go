package streamcol

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
)

// Block wire layout:
//
//	magic      2 bytes   0xC1 0x0A (also used for resynchronisation)
//	verType    1 byte    version<<4 | encoding
//	flags      1 byte    bit0: independent baseline
//	baseline   zigzag    absolute anchor if independent, else delta from the
//	                     previous block's last value
//	count      uvarint   number of elements in the block
//	payloadLen uvarint   payload size in bytes
//	payload    payloadLen bytes
//	crc32      4 bytes   little-endian CRC-32C over verType..payload
const (
	magic0          = 0xC1
	magic1          = 0x0A
	formatVersion   = 1
	flagIndependent = 1
	crcLen          = 4
)

var castagnoli = crc32.MakeTable(crc32.Castagnoli)

// Encoding selects how a block payload represents its elements.
type Encoding uint8

const (
	// EncRaw stores every element as an absolute ZigZag varint.
	EncRaw Encoding = iota
	// EncDelta stores ZigZag first differences against the block baseline.
	EncDelta
	// EncDeltaOfDelta stores ZigZag second differences (delta of deltas,
	// with the previous delta initialised to 0).
	EncDeltaOfDelta
	// EncAuto asks the encoder to pick the smallest payload per block.
	EncAuto Encoding = 0xFF
)

func (e Encoding) String() string {
	switch e {
	case EncRaw:
		return "raw"
	case EncDelta:
		return "delta"
	case EncDeltaOfDelta:
		return "delta-of-delta"
	case EncAuto:
		return "auto"
	}
	return fmt.Sprintf("encoding(%d)", uint8(e))
}

var (
	ErrBadMagic            = errors.New("streamcol: bad block magic")
	ErrUnsupportedVersion  = errors.New("streamcol: unsupported format version")
	ErrUnknownEncoding     = errors.New("streamcol: unknown block encoding")
	ErrUnknownFlags        = errors.New("streamcol: unknown block flags")
	ErrChecksum            = errors.New("streamcol: block checksum mismatch")
	ErrLengthLimit         = errors.New("streamcol: declared length exceeds configured limit")
	ErrCorruptHeader       = errors.New("streamcol: inconsistent block header")
	ErrCorruptPayload      = errors.New("streamcol: malformed block payload")
	ErrBrokenChain         = errors.New("streamcol: block depends on a missing or corrupted predecessor")
	ErrIndexOutOfRange     = errors.New("streamcol: element index out of range")
	ErrIncompatibleEncoder = errors.New("streamcol: values not representable with the forced encoding")
)

// BlockError describes a block that could not be decoded. The decoder skips
// the bad bytes and stays usable; blocks depending on a failed block report
// ErrBrokenChain until the next independent block re-establishes the chain.
type BlockError struct {
	Offset int64 // absolute stream offset where the bad block started
	Err    error
}

func (e *BlockError) Error() string {
	return fmt.Sprintf("streamcol: block at offset %d: %v", e.Offset, e.Err)
}

func (e *BlockError) Unwrap() error { return e.Err }

type blockHeader struct {
	enc         Encoding
	independent bool
	baseline    int64 // stored form: absolute if independent, else delta
	count       uint64
	payloadLen  uint64
	headerLen   int // bytes from magic through the payloadLen field
}

// parseHeader parses a block header from the head of buf. ErrNeedMoreData
// means buf is a prefix of a potentially valid header; every other error is
// a hard rejection of bytes already present.
func parseHeader(buf []byte) (blockHeader, error) {
	var h blockHeader
	if len(buf) == 0 {
		return h, ErrNeedMoreData
	}
	if buf[0] != magic0 {
		return h, ErrBadMagic
	}
	if len(buf) == 1 {
		return h, ErrNeedMoreData
	}
	if buf[1] != magic1 {
		return h, ErrBadMagic
	}
	if len(buf) < 4 {
		return h, ErrNeedMoreData
	}
	vt := buf[2]
	if vt>>4 != formatVersion {
		return h, fmt.Errorf("%w: %d", ErrUnsupportedVersion, vt>>4)
	}
	enc := Encoding(vt & 0x0f)
	switch enc {
	case EncRaw, EncDelta, EncDeltaOfDelta:
	default:
		return h, fmt.Errorf("%w: %d", ErrUnknownEncoding, uint8(enc))
	}
	flags := buf[3]
	if flags&^flagIndependent != 0 {
		return h, fmt.Errorf("%w: 0x%02x", ErrUnknownFlags, flags)
	}
	h.enc = enc
	h.independent = flags&flagIndependent != 0
	off := 4
	base, n, err := decodeZigzag(buf[off:])
	if err != nil {
		return h, err
	}
	off += n
	count, n, err := decodeUvarint(buf[off:])
	if err != nil {
		return h, err
	}
	off += n
	plen, n, err := decodeUvarint(buf[off:])
	if err != nil {
		return h, err
	}
	off += n
	h.baseline = base
	h.count = count
	h.payloadLen = plen
	h.headerLen = off
	return h, nil
}

// appendBlock serialises one block onto dst.
func appendBlock(dst []byte, enc Encoding, independent bool, baselineStored int64, count uint64, payload []byte) []byte {
	start := len(dst)
	flags := byte(0)
	if independent {
		flags = flagIndependent
	}
	dst = append(dst, magic0, magic1, byte(formatVersion<<4)|byte(enc), flags)
	dst = appendZigzag(dst, baselineStored)
	dst = appendUvarint(dst, count)
	dst = appendUvarint(dst, uint64(len(payload)))
	dst = append(dst, payload...)
	var c [crcLen]byte
	binary.LittleEndian.PutUint32(c[:], crc32.Checksum(dst[start+2:], castagnoli))
	return append(dst, c[:]...)
}

// encodePayload encodes vals relative to anchor (the value logically
// preceding vals[0]). It returns ErrOverflow if a difference is not
// representable as an int64, letting the encoder fall back instead of
// silently wrapping.
func encodePayload(enc Encoding, anchor int64, vals []int64) ([]byte, error) {
	var out []byte
	switch enc {
	case EncRaw:
		for _, v := range vals {
			out = appendZigzag(out, v)
		}
	case EncDelta:
		prev := anchor
		for _, v := range vals {
			d, err := checkedSub(v, prev)
			if err != nil {
				return nil, err
			}
			out = appendZigzag(out, d)
			prev = v
		}
	case EncDeltaOfDelta:
		prevV, prevD := anchor, int64(0)
		for _, v := range vals {
			d, err := checkedSub(v, prevV)
			if err != nil {
				return nil, err
			}
			dd, err := checkedSub(d, prevD)
			if err != nil {
				return nil, err
			}
			out = appendZigzag(out, dd)
			prevV, prevD = v, d
		}
	}
	return out, nil
}

// decodePayload decodes exactly count elements from payload. The payload is
// trusted to be complete (the caller verified the checksum), so a truncated
// varint inside it is corruption, not a shortage of input.
func decodePayload(enc Encoding, baseline int64, count uint64, payload []byte) ([]int64, error) {
	if count > uint64(len(payload)) {
		// Every element needs at least one byte; reject before allocating.
		return nil, fmt.Errorf("%w: count %d exceeds payload %d bytes", ErrCorruptHeader, count, len(payload))
	}
	vals := make([]int64, 0, count)
	off := 0
	next := func() (int64, error) {
		v, n, err := decodeZigzag(payload[off:])
		if err != nil {
			if errors.Is(err, ErrNeedMoreData) {
				return 0, ErrCorruptPayload
			}
			return 0, err
		}
		off += n
		return v, nil
	}
	switch enc {
	case EncRaw:
		for i := uint64(0); i < count; i++ {
			v, err := next()
			if err != nil {
				return nil, err
			}
			vals = append(vals, v)
		}
	case EncDelta:
		prev := baseline
		for i := uint64(0); i < count; i++ {
			dv, err := next()
			if err != nil {
				return nil, err
			}
			v, err := checkedAdd(prev, dv)
			if err != nil {
				return nil, err
			}
			vals = append(vals, v)
			prev = v
		}
	case EncDeltaOfDelta:
		prevV, prevD := baseline, int64(0)
		for i := uint64(0); i < count; i++ {
			dd, err := next()
			if err != nil {
				return nil, err
			}
			d, err := checkedAdd(prevD, dd)
			if err != nil {
				return nil, err
			}
			v, err := checkedAdd(prevV, d)
			if err != nil {
				return nil, err
			}
			vals = append(vals, v)
			prevV, prevD = v, d
		}
	}
	if off != len(payload) {
		return nil, fmt.Errorf("%w: %d trailing bytes", ErrCorruptPayload, len(payload)-off)
	}
	return vals, nil
}
