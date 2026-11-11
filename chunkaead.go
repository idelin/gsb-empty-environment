// Package chunkaead encrypts large objects as independently authenticated
// fixed-size segments so that random reads only decrypt the segments they
// touch. Every segment is bound, via AEAD additional data, to the object
// identifier, its segment number, its plaintext length and a final-segment
// marker. An authenticated header trailer records the object identity, chunk
// size and total plaintext length, which makes truncation, extension,
// reordering and cross-object splicing detectable.
package chunkaead

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"io"
	"io/fs"
)

const (
	magic          = "CHNKAED1"
	version        = 1
	nonceSize      = 12 // AES-GCM standard nonce
	tagSize        = 16 // AES-GCM standard tag
	footerSuffix   = 4 + len(magic)
	maxObjectIDLen = 1<<16 - 1

	headerAADDomain  = "CHNKAED1-hdr-v1"
	segmentAADDomain = "CHNKAED1-seg-v1"
)

var (
	// ErrKeySize is returned when the key is not 32 bytes (AES-256).
	ErrKeySize = errors.New("chunkaead: key must be 32 bytes")
	// ErrConfig is returned for invalid writer/reader configuration.
	ErrConfig = errors.New("chunkaead: invalid configuration")
	// ErrFormat is returned when the stored object is malformed.
	ErrFormat = errors.New("chunkaead: malformed encrypted object")
	// ErrTruncated is returned when the stored size does not match the
	// authenticated header.
	ErrTruncated = errors.New("chunkaead: encrypted object size mismatch (truncated or extended)")
	// ErrAuthentication is returned when a segment or the header fails
	// AEAD authentication.
	ErrAuthentication = errors.New("chunkaead: authentication failure")
	// ErrObjectID is returned when the authenticated object identifier
	// does not match the identifier the reader was opened with.
	ErrObjectID = errors.New("chunkaead: object identifier mismatch")
	// ErrClosed is returned when writing to a closed writer.
	ErrClosed = errors.New("chunkaead: writer is closed")
)

func newGCM(key []byte) (cipher.AEAD, error) {
	if len(key) != 32 {
		return nil, ErrKeySize
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// deriveNonce adds counter to the 96-bit big-endian base nonce. Counter 0 is
// reserved for the header; data segment i uses counter i+1.
func deriveNonce(base [nonceSize]byte, counter uint64) ([nonceSize]byte, error) {
	var nonce [nonceSize]byte
	copy(nonce[:], base[:])
	carry := counter
	for i := nonceSize - 1; i >= 0 && carry > 0; i-- {
		sum := uint64(nonce[i]) + (carry & 0xff)
		nonce[i] = byte(sum)
		carry = (carry >> 8) + (sum >> 8)
	}
	if carry != 0 {
		return nonce, ErrConfig
	}
	return nonce, nil
}

// segmentAAD builds the additional authenticated data for one segment.
// Layout: domain || u16(objectIDLen) || objectID || u64(segmentIndex) ||
// u32(plaintextChunkLen) || finalFlag. All numeric fields have fixed width,
// so the encoding is unambiguous.
func segmentAAD(objectID string, index uint64, chunkLen uint32, final bool) []byte {
	aad := make([]byte, 0, len(segmentAADDomain)+2+len(objectID)+8+4+1)
	aad = append(aad, segmentAADDomain...)
	var num [8]byte
	binary.BigEndian.PutUint16(num[:2], uint16(len(objectID)))
	aad = append(aad, num[:2]...)
	aad = append(aad, objectID...)
	binary.BigEndian.PutUint64(num[:], index)
	aad = append(aad, num[:]...)
	binary.BigEndian.PutUint32(num[:4], chunkLen)
	aad = append(aad, num[:4]...)
	if final {
		aad = append(aad, 1)
	} else {
		aad = append(aad, 0)
	}
	return aad
}

func headerAAD() []byte {
	return []byte(headerAADDomain)
}

// encodeHeader serializes the authenticated header fields.
func encodeHeader(chunkSize uint32, plainSize uint64, objectID string) []byte {
	buf := make([]byte, 0, 1+4+8+2+len(objectID))
	buf = append(buf, version)
	var num [8]byte
	binary.BigEndian.PutUint32(num[:4], chunkSize)
	buf = append(buf, num[:4]...)
	binary.BigEndian.PutUint64(num[:], plainSize)
	buf = append(buf, num[:]...)
	binary.BigEndian.PutUint16(num[:2], uint16(len(objectID)))
	buf = append(buf, num[:2]...)
	buf = append(buf, objectID...)
	return buf
}

// decodeHeader parses and validates the authenticated header fields.
func decodeHeader(buf []byte) (chunkSize uint32, plainSize uint64, objectID string, err error) {
	if len(buf) < 1+4+8+2 {
		return 0, 0, "", ErrFormat
	}
	pos := 0
	if buf[pos] != version {
		return 0, 0, "", ErrFormat
	}
	pos++
	chunkSize = binary.BigEndian.Uint32(buf[pos : pos+4])
	pos += 4
	plainSize = binary.BigEndian.Uint64(buf[pos : pos+8])
	pos += 8
	idLen := int(binary.BigEndian.Uint16(buf[pos : pos+2]))
	pos += 2
	if len(buf) != pos+idLen {
		return 0, 0, "", ErrFormat
	}
	if chunkSize == 0 {
		return 0, 0, "", ErrFormat
	}
	objectID = string(buf[pos : pos+idLen])
	return chunkSize, plainSize, objectID, nil
}

func randomNonce() ([nonceSize]byte, error) {
	var nonce [nonceSize]byte
	if _, err := io.ReadFull(rand.Reader, nonce[:]); err != nil {
		return nonce, err
	}
	return nonce, nil
}

// sizedReaderAt is the storage backend expected by Reader. Adapters are
// provided for common types that expose their size through other methods.
type sizedReaderAt interface {
	io.ReaderAt
	Size() int64
}

func backendSize(r io.ReaderAt) (int64, bool) {
	switch v := r.(type) {
	case sizedReaderAt:
		return v.Size(), true
	case interface{ Len() int }:
		return int64(v.Len()), true
	case interface{ Stat() (fs.FileInfo, error) }:
		info, err := v.Stat()
		if err != nil {
			return 0, false
		}
		return info.Size(), true
	}
	return 0, false
}

// Sized pairs an io.ReaderAt with its known object size so it can be opened
// by NewReader when the backend type does not expose Size, Len or Stat.
type Sized struct {
	io.ReaderAt
	objectSize int64
}

// NewSized wraps r together with the underlying object's byte length.
func NewSized(r io.ReaderAt, objectSize int64) *Sized {
	return &Sized{ReaderAt: r, objectSize: objectSize}
}

// Size returns the object length supplied to NewSized.
func (s *Sized) Size() int64 { return s.objectSize }
