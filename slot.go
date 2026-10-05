// Package configstore implements a crash-safe configuration publication
// store built on two storage slots. A single write may tear, so every slot
// carries a generation, a payload length, a digest and a commit flag that
// is written strictly last. Readers independently validate both slots and
// select the newest fully committed one.
package configstore

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
)

// Slot image layout (all integers big-endian):
//
//	[0:4]                 magic "CFGS"
//	[4:12]                generation uint64
//	[12:16]               payload length uint32
//	[16:48]               SHA-256 over (generation || length || payload)
//	[48:48+MaxPayload]    payload, zero-padded to MaxPayload
//	[SlotSize-1]          commit flag (committedByte once committed)
//
// The commit flag sits at the very last byte so that any truncation of the
// image removes it, and it is always written after the rest of the image.
const (
	magic uint32 = 0x43464753 // "CFGS"

	magicSize  = 4
	genSize    = 8
	lenSize    = 4
	digestSize = sha256.Size

	// HeaderSize is the offset at which the payload begins.
	HeaderSize = magicSize + genSize + lenSize + digestSize

	// MaxPayload is the maximum configuration size in bytes.
	MaxPayload = 1 << 14 // 16 KiB

	// SlotSize is the fixed size of every slot image.
	SlotSize = HeaderSize + MaxPayload + 1

	// CommitOffset is the offset of the commit flag byte.
	CommitOffset = SlotSize - 1

	// committedByte marks a slot as fully written.
	committedByte = 0xA5
)

var (
	ErrSlotSize     = errors.New("configstore: slot image has wrong size")
	ErrBadMagic     = errors.New("configstore: bad magic")
	ErrNotCommitted = errors.New("configstore: slot is not committed")
	ErrBadLength    = errors.New("configstore: payload length out of range")
	ErrBadDigest    = errors.New("configstore: payload digest mismatch")
	ErrBadPadding   = errors.New("configstore: payload padding is not zeroed")
)

// Meta describes a validated slot.
type Meta struct {
	Generation uint64
	Length     uint32
}

// digestInput returns the byte ranges the digest covers: the generation and
// length header fields followed by the payload. The commit flag and the
// magic are deliberately excluded (the flag changes after the digest is
// computed; the magic is a constant).
func computeDigest(gen uint64, length uint32, payload []byte) []byte {
	var hdr [genSize + lenSize]byte
	binary.BigEndian.PutUint64(hdr[0:8], gen)
	binary.BigEndian.PutUint32(hdr[8:12], length)
	h := sha256.New()
	h.Write(hdr[:])
	h.Write(payload)
	return h.Sum(nil)
}

// encodeSlot builds a full slot image with the commit flag cleared.
func encodeSlot(gen uint64, payload []byte) []byte {
	img := make([]byte, SlotSize)
	binary.BigEndian.PutUint32(img[0:4], magic)
	binary.BigEndian.PutUint64(img[4:12], gen)
	binary.BigEndian.PutUint32(img[12:16], uint32(len(payload)))
	copy(img[HeaderSize:], payload)
	d := computeDigest(gen, uint32(len(payload)), payload)
	copy(img[16:48], d)
	// img[CommitOffset] stays 0 until the slot is committed.
	return img
}

// ValidateSlot independently validates a raw slot image. It returns the
// slot's metadata and payload on success, or a descriptive error if the
// image is torn, truncated, corrupted or uncommitted. It performs no I/O
// and shares no state with the store, so it can be used as a standalone
// verifier.
func ValidateSlot(data []byte) (Meta, []byte, error) {
	if len(data) != SlotSize {
		return Meta{}, nil, ErrSlotSize
	}
	if binary.BigEndian.Uint32(data[0:4]) != magic {
		return Meta{}, nil, ErrBadMagic
	}
	if data[CommitOffset] != committedByte {
		return Meta{}, nil, ErrNotCommitted
	}
	gen := binary.BigEndian.Uint64(data[4:12])
	length := binary.BigEndian.Uint32(data[12:16])
	if length > MaxPayload {
		return Meta{}, nil, ErrBadLength
	}
	payload := data[HeaderSize : HeaderSize+int(length)]
	want := computeDigest(gen, length, payload)
	if !bytes.Equal(data[16:48], want) {
		return Meta{}, nil, ErrBadDigest
	}
	for _, b := range data[HeaderSize+int(length) : CommitOffset] {
		if b != 0 {
			return Meta{}, nil, ErrBadPadding
		}
	}
	return Meta{Generation: gen, Length: length}, payload, nil
}
