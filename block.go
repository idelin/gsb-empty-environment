package tsblock

import (
	"encoding/binary"
	"errors"
	"math"
	"math/bits"
)

// Frame layout:
//
//	magic        4 bytes  "TSB1"
//	numSamples   uvarint
//	firstT       8 bytes  big-endian uint64
//	firstDelta   8 bytes  big-endian uint64 (only when numSamples >= 2)
//	firstV       8 bytes  big-endian float64 bits
//	bitLen       uvarint number of meaningful bits in the compressed body
//	body         ceil(bitLen/8) bytes, MSB-first packed samples 2..N

var magic = [4]byte{'T', 'S', 'B', '1'}

var (
	// ErrEmpty is returned when sealing or decoding a block with no samples.
	ErrEmpty = errors.New("tsblock: block contains no samples")
	// ErrOutOfOrder is returned when a timestamp is not strictly greater than
	// the previous one.
	ErrOutOfOrder = errors.New("tsblock: out-of-order or duplicate timestamp")
	// ErrBlockSealed is returned when appending to an encoder already sealed.
	ErrBlockSealed = errors.New("tsblock: encoder already sealed")
	// ErrCorrupt is returned when a block fails to decode.
	ErrCorrupt = errors.New("tsblock: corrupt block")
)

// DefaultMaxBlockSize is the default soft cap on compressed frame size.
const DefaultMaxBlockSize = 4 * 1024

// Encoder compresses a strictly ordered stream of samples into sealed blocks.
//
// An Encoder is not safe for concurrent use.
type Encoder struct {
	maxSize int
	sealed  bool

	samples   uint64
	firstT    uint64
	lastT     uint64
	lastDelta uint64
	firstV    uint64
	lastV     uint64

	haveWindow bool
	lead       uint
	trail      uint

	body   bitWriter
	frames [][]byte
}

// NewEncoder creates an encoder that seals a block once the pending compressed
// frame reaches roughly maxSize bytes. A non-positive value selects
// DefaultMaxBlockSize.
func NewEncoder(maxSize int) *Encoder {
	if maxSize <= 0 {
		maxSize = DefaultMaxBlockSize
	}
	return &Encoder{maxSize: maxSize}
}

// fitsSigned reports whether v fits into nbits two's-complement bits.
func fitsSigned(v int64, nbits uint) bool {
	if nbits >= 64 {
		return true
	}
	limit := int64(1) << (nbits - 1)
	return v >= -limit && v < limit
}

// uvarintLen returns the encoded length of v as a binary.AppendUvarint value.
func uvarintLen(v uint64) int {
	n := 1
	for v >= 0x80 {
		v >>= 7
		n++
	}
	return n
}

// Append adds a sample. It returns ErrOutOfOrder for non-monotonic timestamps
// and ErrBlockSealed after Seal has been called.
func (e *Encoder) Append(s Sample) error {
	if e.sealed {
		return ErrBlockSealed
	}
	if e.samples > 0 && s.T <= e.lastT {
		return ErrOutOfOrder
	}

	bitsV := math.Float64bits(s.V)

	if e.samples == 0 {
		e.firstT = s.T
		e.firstV = bitsV
	} else {
		if e.samples == 1 {
			e.lastDelta = s.T - e.firstT
			e.body.writeBits(e.lastDelta, 64)
			e.encodeValue(bitsV)
		} else {
			delta := s.T - e.lastT
			e.encodeTimestamp(int64(delta) - int64(e.lastDelta))
			e.lastDelta = delta
			e.encodeValue(bitsV)
		}
	}

	e.lastT = s.T
	e.lastV = bitsV
	e.samples++

	if e.pendingSize() >= e.maxSize && e.samples >= 2 {
		e.sealCurrent()
	}
	return nil
}

// encodeTimestamp writes a delta-of-delta with the Gorilla variable scheme:
//
//	0                          -> dod == 0
//	10 + 7-bit signed          -> [-64, 63]
//	110 + 9-bit signed         -> [-256, 255]
//	1110 + 12-bit signed       -> [-2048, 2047]
//	1111 + 64-bit signed       -> any delta-of-delta
func (e *Encoder) encodeTimestamp(dod int64) {
	switch {
	case dod == 0:
		e.body.writeBits(0b0, 1)
	case fitsSigned(dod, 7):
		e.body.writeBits(0b10, 2)
		e.body.writeBits(uint64(dod), 7)
	case fitsSigned(dod, 9):
		e.body.writeBits(0b110, 3)
		e.body.writeBits(uint64(dod), 9)
	case fitsSigned(dod, 12):
		e.body.writeBits(0b1110, 4)
		e.body.writeBits(uint64(dod), 12)
	default:
		e.body.writeBits(0b1111, 4)
		e.body.writeBits(uint64(dod), 64)
	}
}

// encodeValue writes a float64 with XOR compression against the previous value.
func (e *Encoder) encodeValue(v uint64) {
	xor := v ^ e.lastV
	if xor == 0 {
		e.body.writeBits(0b0, 1)
		return
	}
	e.body.writeBits(0b1, 1)

	lead := uint(bits.LeadingZeros64(xor))
	trail := uint(bits.TrailingZeros64(xor))

	if !e.haveWindow {
		// First XOR: store the full descriptor plus meaningful bits.
		e.body.writeBits(0b1, 1)
		e.storeNewWindow(xor, min(lead, 31), trail)
		return
	}

	if lead >= e.lead && trail >= e.trail {
		// Meaningful bits fit entirely inside the previous window.
		e.body.writeBits(0b0, 1)
		meaningful := 64 - e.lead - e.trail
		e.body.writeBits(xor>>e.trail, meaningful)
		return
	}

	// Store a fresh descriptor. Clamp the leading-zero count to 5 bits.
	e.body.writeBits(0b1, 1)
	if lead > 31 {
		lead = 31
	}
	e.storeNewWindow(xor, lead, trail)
}

// storeNewWindow writes a fresh leading/meaningful descriptor and the XOR's
// meaningful bits, then makes it the active window.
func (e *Encoder) storeNewWindow(xor uint64, lead, trail uint) {
	meaningful := 64 - lead - trail
	if meaningful > 63 {
		// A full-word XOR (no leading or trailing zero) needs 64 meaningful
		// bits, but the length field only holds 6 bits (0..63). The descriptor
		// (lead=0, len=0) is otherwise unreachable for a nonzero XOR, so use it
		// as an escape code followed by the raw 64-bit XOR.
		e.body.writeBits(0, 5) // lead = 0
		e.body.writeBits(0, 6) // meaningful = 0 -> escape marker
		e.body.writeBits(xor, 64)
		e.lead = lead
		e.trail = trail
		e.haveWindow = true
		return
	}
	e.body.writeBits(uint64(lead), 5)
	e.body.writeBits(uint64(meaningful), 6)
	e.body.writeBits(xor>>trail, meaningful)
	e.lead = lead
	e.trail = trail
	e.haveWindow = true
}

// pendingSize estimates the sealed frame size of the current block in bytes.
func (e *Encoder) pendingSize() int {
	return e.frameHeaderLen() + (int(e.body.bitLen())+7)/8
}

// frameHeaderLen returns the serialized header length for the current block.
func (e *Encoder) frameHeaderLen() int {
	n := 4 + uvarintLen(e.samples) + 8 + 8
	if e.samples >= 2 {
		n += 8 // firstDelta
	}
	n += uvarintLen(uint64(e.body.bitLen()))
	return n
}

// sealCurrent appends the pending block as one frame and resets block state.
func (e *Encoder) sealCurrent() {
	if e.samples == 0 {
		return
	}

	var frame []byte
	frame = append(frame, magic[:]...)
	frame = binary.AppendUvarint(frame, e.samples)

	var tmp [8]byte
	binary.BigEndian.PutUint64(tmp[:], e.firstT)
	frame = append(frame, tmp[:]...)
	if e.samples >= 2 {
		binary.BigEndian.PutUint64(tmp[:], e.lastDelta)
		frame = append(frame, tmp[:]...)
	}
	binary.BigEndian.PutUint64(tmp[:], e.firstV)
	frame = append(frame, tmp[:]...)
	frame = binary.AppendUvarint(frame, uint64(e.body.bitLen()))
	frame = append(frame, e.body.bytes()...)

	e.frames = append(e.frames, frame)
	e.resetBlock()
}

func (e *Encoder) resetBlock() {
	e.samples = 0
	e.firstT = 0
	e.lastT = 0
	e.lastDelta = 0
	e.firstV = 0
	e.lastV = 0
	e.haveWindow = false
	e.lead = 0
	e.trail = 0
	e.body = bitWriter{}
}

// Seal closes the current block and returns all sealed frames. The encoder must
// not be used afterwards. At least one sample must have been appended.
func (e *Encoder) Seal() ([][]byte, error) {
	if e.sealed {
		return nil, ErrBlockSealed
	}
	e.sealCurrent()
	if len(e.frames) == 0 {
		return nil, ErrEmpty
	}
	e.sealed = true
	return e.frames, nil
}

// Blocks returns the number of fully sealed frames.
func (e *Encoder) Blocks() int { return len(e.frames) }
