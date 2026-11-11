// Package netsec implements a bidirectional key-rotation state machine for a
// secure session channel.
//
// The two transport directions are independent: each direction keeps its own
// old and new key, its own generation and its own sequence number. Receiving a
// new key from the peer never changes the key used for sending.
//
// Every wire record is either an application record or a key-switch control
// marker. A direction commits to its new key only after the marker for that
// direction has been authenticated locally:
//
//   - On the sending side the marker is sealed under the new send key and the
//     direction commits once the fully sealed marker has been written to the
//     transport.
//   - On the receiving side the direction commits only after the inbound
//     marker has been authenticated under the provisioned new receive key.
//
// Once a key generation is deprecated it can never become the active key
// again. Application records keep their message boundaries; a control marker
// and the queued application records around it preserve their relative order,
// and every record is sealed and opened under exactly one determined key
// generation.
package netsec

import "errors"

// frameMagic identifies a framed channel record.
const frameMagic = uint32(0x4e534543) // "NSEC"

// Header size: magic (4) + type (1) + generation (8) + sequence (8) + length (4).
const frameHeaderSize = 25

// MaxFrameSize bounds the ciphertext of a single record and therefore the
// largest application message the session accepts.
const MaxFrameSize = 1 << 20

// RecordType discriminates control markers from application data.
type RecordType uint8

const (
	// RecordApplication carries one host application message and preserves
	// its boundary.
	RecordApplication RecordType = 1

	// RecordKeySwitch is the authenticated control marker that finalizes a key
	// rotation for one direction.
	RecordKeySwitch RecordType = 2
)

// Cipher is the cryptographic backend supplied by the embedding host (and by
// tests). One Cipher value represents one concrete key generation and one
// direction. Implementations must be safe for concurrent use.
//
// Seal produces ciphertext authenticating header together with plaintext;
// Open reverses it and returns an error wrapping ErrAuthenticationFailed when
// the authentication tag cannot be verified.
type Cipher interface {
	Seal(plaintext, header []byte) (ciphertext []byte, err error)
	Open(ciphertext, header []byte) (plaintext []byte, err error)
	// Overhead reports how many bytes Seal adds to the plaintext.
	Overhead() int
}

// Sentinel errors returned by the state machine.
var (
	// ErrClosed is returned after the session has been shut down.
	ErrClosed = errors.New("netsec: session closed")

	// ErrNoNewKey is returned when a rotation is requested before a new key
	// generation has been provisioned for that direction.
	ErrNoNewKey = errors.New("netsec: no new key provisioned")

	// ErrRekeyPending is returned when a rotation is already armed and has
	// not yet been committed.
	ErrRekeyPending = errors.New("netsec: rekey already in progress")

	// ErrAuthenticationFailed wraps every cipher-level authentication
	// failure, including failed key-switch markers.
	ErrAuthenticationFailed = errors.New("netsec: authentication failed")

	// ErrReplay is returned for a record whose sequence number has already
	// been observed in its generation.
	ErrReplay = errors.New("netsec: replayed record")

	// ErrSequenceGap is returned when a record arrives out of order.
	ErrSequenceGap = errors.New("netsec: sequence number gap")

	// ErrUnknownGeneration is returned for a generation that is neither
	// active nor still accepted as a late deprecated generation.
	ErrUnknownGeneration = errors.New("netsec: unknown key generation")

	// ErrLateWindowExceeded is returned for an old-generation record that
	// arrives after the late-message window was consumed.
	ErrLateWindowExceeded = errors.New("netsec: late message window exceeded")

	// ErrFrame is returned for malformed framing on the wire.
	ErrFrame = errors.New("netsec: malformed frame")

	// ErrMessageTooLarge is returned when an application message would
	// produce a frame larger than MaxFrameSize.
	ErrMessageTooLarge = errors.New("netsec: message too large")
)

// frameHeader is the authenticated, length-prefixed wire header. All integer
// fields use big-endian encoding on the wire.
type frameHeader struct {
	typ        RecordType
	generation uint64
	sequence   uint64
	length     uint32
}

// encode writes the fixed-size header into dst, which must be at least
// frameHeaderSize bytes. The magic prefix lets receivers reject junk before
// touching any key material.
func (h frameHeader) encode(dst []byte) {
	_ = dst[frameHeaderSize-1]
	magic := frameMagic
	dst[0] = byte(magic >> 24)
	dst[1] = byte(magic >> 16)
	dst[2] = byte(magic >> 8)
	dst[3] = byte(magic)
	dst[4] = byte(h.typ)
	putUint64(dst[5:13], h.generation)
	putUint64(dst[13:21], h.sequence)
	putUint32(dst[21:25], h.length)
}

// decodeHeader parses a header previously produced by encode.
func decodeHeader(src []byte) (frameHeader, error) {
	if len(src) < frameHeaderSize {
		return frameHeader{}, ErrFrame
	}
	magic := uint32(src[0])<<24 | uint32(src[1])<<16 | uint32(src[2])<<8 | uint32(src[3])
	if magic != frameMagic {
		return frameHeader{}, ErrFrame
	}
	typ := RecordType(src[4])
	if typ != RecordApplication && typ != RecordKeySwitch {
		return frameHeader{}, ErrFrame
	}
	length := uint32(src[21])<<24 | uint32(src[22])<<16 | uint32(src[23])<<8 | uint32(src[24])
	if length > MaxFrameSize {
		return frameHeader{}, ErrMessageTooLarge
	}
	return frameHeader{
		typ: typ,
		generation: uint64(src[5])<<56 | uint64(src[6])<<48 | uint64(src[7])<<40 |
			uint64(src[8])<<32 | uint64(src[9])<<24 | uint64(src[10])<<16 |
			uint64(src[11])<<8 | uint64(src[12]),
		sequence: uint64(src[13])<<56 | uint64(src[14])<<48 | uint64(src[15])<<40 |
			uint64(src[16])<<32 | uint64(src[17])<<24 | uint64(src[18])<<16 |
			uint64(src[19])<<8 | uint64(src[20]),
		length: length,
	}, nil
}

// encodeBytes returns the wire encoding of the header.
func (h frameHeader) encodeBytes() []byte {
	buf := make([]byte, frameHeaderSize)
	h.encode(buf)
	return buf
}

// headerFor builds the authenticated header of an outbound record.
func headerFor(record *outboundRecord) frameHeader {
	return frameHeader{
		typ:        record.typ,
		generation: record.gen,
		sequence:   record.sequence,
		length:     uint32(len(record.plaintext) + record.key.Overhead()),
	}
}

func putUint64(dst []byte, v uint64) {
	_ = dst[7]
	dst[0] = byte(v >> 56)
	dst[1] = byte(v >> 48)
	dst[2] = byte(v >> 40)
	dst[3] = byte(v >> 32)
	dst[4] = byte(v >> 24)
	dst[5] = byte(v >> 16)
	dst[6] = byte(v >> 8)
	dst[7] = byte(v)
}

func putUint32(dst []byte, v uint32) {
	_ = dst[3]
	dst[0] = byte(v >> 24)
	dst[1] = byte(v >> 16)
	dst[2] = byte(v >> 8)
	dst[3] = byte(v)
}
