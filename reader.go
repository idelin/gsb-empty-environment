package chunkaead

import (
	"crypto/cipher"
	"encoding/binary"
	"io"
)

// Reader provides random access to an encrypted object written by Writer.
// Each ReadAt decrypts only the segments overlapping the requested range.
// Segments are authenticated before any plaintext is copied to the caller:
// if any touched segment fails authentication, ReadAt returns an error and
// zero bytes of plaintext are handed to the caller.
type Reader struct {
	backend   io.ReaderAt
	aead      cipher.AEAD
	objectID  string
	chunkSize uint32
	plainSize uint64
	baseNonce [nonceSize]byte
	numSegs   uint64
	frameSize uint32 // full segment frame length (chunkSize + tag)
}

var _ io.ReaderAt = (*Reader)(nil)

// NewReader opens an encrypted object. r must support random access and be
// able to report its size: *os.File, *bytes.Reader and *bytes.Buffer-backed
// wrappers work directly; otherwise wrap it in a type implementing
// Size() int64. The objectID is verified against the authenticated header.
func NewReader(r io.ReaderAt, key []byte, objectID string) (*Reader, error) {
	if len(objectID) == 0 || len(objectID) > maxObjectIDLen {
		return nil, ErrConfig
	}
	aead, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	size, ok := backendSize(r)
	if !ok || size < int64(nonceSize+footerSuffix) {
		return nil, ErrFormat
	}

	suffix := make([]byte, footerSuffix)
	if _, err := r.ReadAt(suffix, size-int64(footerSuffix)); err != nil {
		return nil, err
	}
	if string(suffix[4:]) != magic {
		return nil, ErrFormat
	}
	sealedLen := binary.BigEndian.Uint32(suffix[:4])
	headerStart := size - int64(footerSuffix) - int64(sealedLen) - nonceSize
	if headerStart < 0 || sealedLen == 0 || sealedLen > uint32(size) {
		return nil, ErrFormat
	}

	headerBlock := make([]byte, nonceSize+sealedLen)
	if _, err := r.ReadAt(headerBlock, headerStart); err != nil {
		return nil, err
	}
	var baseNonce [nonceSize]byte
	copy(baseNonce[:], headerBlock[:nonceSize])
	sealedHeader := headerBlock[nonceSize:]

	nonce, err := deriveNonce(baseNonce, 0)
	if err != nil {
		return nil, err
	}
	header, err := aead.Open(nil, nonce[:], sealedHeader, headerAAD())
	if err != nil {
		return nil, ErrAuthentication
	}
	chunkSize, plainSize, storedID, err := decodeHeader(header)
	if err != nil {
		return nil, err
	}
	if storedID != objectID {
		return nil, ErrObjectID
	}

	numSegs, frameSize, framesSize, err := expectedLayout(plainSize, chunkSize, sealedLen)
	if err != nil {
		return nil, err
	}
	if framesSize != uint64(headerStart) {
		return nil, ErrTruncated
	}

	return &Reader{
		backend:   r,
		aead:      aead,
		objectID:  objectID,
		chunkSize: chunkSize,
		plainSize: plainSize,
		baseNonce: baseNonce,
		numSegs:   numSegs,
		frameSize: frameSize,
	}, nil
}

// expectedLayout computes the expected segment counts and frame size from the
// authenticated header values, rejecting overflow and invalid combinations.
func expectedLayout(plainSize uint64, chunkSize uint32, sealedHeaderLen uint32) (numSegs uint64, frameSize uint32, framesSize uint64, err error) {
	frameSize = chunkSize + tagSize
	if uint64(frameSize) < uint64(chunkSize) {
		return 0, 0, 0, ErrFormat
	}
	numSegs = plainSize / uint64(chunkSize)
	lastPlain := plainSize % uint64(chunkSize)
	if lastPlain != 0 {
		numSegs++
	} else if plainSize > 0 {
		lastPlain = uint64(chunkSize)
	}
	// Empty objects have no segment frames; the footer must start at 0.
	if numSegs == 0 && plainSize != 0 {
		return 0, 0, 0, ErrFormat
	}
	// Sanity bound: the sealed header must fit the fixed overhead plus one
	// version byte, the object ID and a GCM tag.
	minSealed := uint32(1 + 4 + 8 + 2 + tagSize)
	if sealedHeaderLen < minSealed {
		return 0, 0, 0, ErrFormat
	}
	if numSegs > 0 {
		framesSize = (numSegs-1)*uint64(frameSize) + lastPlain + tagSize
	}
	return numSegs, frameSize, framesSize, nil
}

// Size returns the authenticated plaintext length of the object.
func (r *Reader) Size() int64 {
	return int64(r.plainSize)
}

// ReadAt implements io.ReaderAt. Only segments overlapping [off, off+len(p))
// are read and decrypted. All touched segments are authenticated before any
// plaintext reaches p, so a corrupted or reordered segment never leaks
// partial plaintext to the caller.
func (r *Reader) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 {
		return 0, ErrConfig
	}
	if uint64(off) >= r.plainSize {
		if len(p) == 0 {
			return 0, nil
		}
		return 0, io.EOF
	}

	maxN := r.plainSize - uint64(off)
	want := uint64(len(p))
	n := want
	if n > maxN {
		n = maxN
	}

	firstSeg := uint64(off) / uint64(r.chunkSize)
	endOff := uint64(off) + n
	lastSeg := (endOff - 1) / uint64(r.chunkSize)

	// Phase 1: authenticate every touched segment into a private buffer.
	scratch := make([]byte, 0, (lastSeg-firstSeg+1)*uint64(r.chunkSize))
	frameBuf := make([]byte, r.frameSize)
	for seg := firstSeg; seg <= lastSeg; seg++ {
		isFinal := seg == r.numSegs-1
		plainLen := r.chunkSize
		if isFinal && r.plainSize%uint64(r.chunkSize) != 0 {
			plainLen = uint32(r.plainSize % uint64(r.chunkSize))
		}
		frameLen := plainLen + tagSize
		frameOff := int64(seg) * int64(r.frameSize)
		if _, err := r.backend.ReadAt(frameBuf[:frameLen], frameOff); err != nil {
			return 0, err
		}
		nonce, err := deriveNonce(r.baseNonce, seg+1)
		if err != nil {
			return 0, err
		}
		plain, err := r.aead.Open(nil, nonce[:], frameBuf[:frameLen],
			segmentAAD(r.objectID, seg, plainLen, isFinal))
		if err != nil {
			return 0, ErrAuthentication
		}
		scratch = append(scratch, plain...)
	}

	// Phase 2: all segments verified; copy only the requested window.
	startInSeg := uint64(off) % uint64(r.chunkSize)
	copy(p[:n], scratch[startInSeg:startInSeg+n])

	if n < want {
		return int(n), io.EOF
	}
	return int(n), nil
}
