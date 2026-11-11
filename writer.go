package chunkaead

import (
	"encoding/binary"
	"io"
)

// Writer encrypts a plaintext stream into independently authenticated
// fixed-size segments written to an underlying io.Writer. Segment frames are
// emitted as they fill; on Close the authenticated header trailer is written,
// which marks the final segment and binds the object's total length.
type Writer struct {
	w         io.Writer
	aead      aeadSealer
	objectID  string
	chunkSize int
	baseNonce [nonceSize]byte

	pending     []byte // buffered plaintext not yet sealed
	index       uint64 // number of sealed segments
	total       uint64 // total plaintext bytes written
	closed      bool
	closeFailed bool
}

// aeadSealer is the subset of cipher.AEAD used by the writer.
type aeadSealer interface {
	NonceSize() int
	Overhead() int
	Seal(dst, nonce, plaintext, additionalData []byte) []byte
}

// NewWriter returns an encryption writer for objectID. Each object should use
// a fresh writer so that a fresh random base nonce is generated; segment
// nonces are derived from it.
func NewWriter(w io.Writer, key []byte, objectID string, chunkSize int) (*Writer, error) {
	aead, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	if chunkSize <= 0 {
		return nil, ErrConfig
	}
	if len(objectID) == 0 || len(objectID) > maxObjectIDLen {
		return nil, ErrConfig
	}
	if uint64(chunkSize) > uint64(^uint32(0)) {
		return nil, ErrConfig
	}
	base, err := randomNonce()
	if err != nil {
		return nil, err
	}
	return &Writer{
		w:         w,
		aead:      aead,
		objectID:  objectID,
		chunkSize: chunkSize,
		baseNonce: base,
		pending:   make([]byte, 0, chunkSize),
	}, nil
}

// Write buffers plaintext, sealing and emitting every full segment except the
// last. A segment is only marked final once Close knows no more data follows.
func (w *Writer) Write(p []byte) (int, error) {
	if w.closed {
		return 0, ErrClosed
	}
	written := len(p)
	for len(p) > 0 {
		space := w.chunkSize - len(w.pending)
		if len(p) < space {
			space = len(p)
		}
		w.pending = append(w.pending, p[:space]...)
		p = p[space:]
		if len(w.pending) == w.chunkSize && len(p) > 0 {
			if err := w.sealSegment(false); err != nil {
				return 0, err
			}
		}
	}
	w.total += uint64(written)
	return written, nil
}

// sealSegment encrypts the pending buffer as one segment and flushes it to
// the underlying writer. The AAD binds objectID, segment index, plaintext
// length and the final flag.
func (w *Writer) sealSegment(final bool) error {
	if len(w.pending) == 0 {
		return nil
	}
	if w.index == ^uint64(0) {
		return ErrConfig
	}
	index := w.index
	nonce, err := deriveNonce(w.baseNonce, index+1)
	if err != nil {
		return err
	}
	chunkLen := len(w.pending)
	frame := w.aead.Seal(nil, nonce[:], w.pending, segmentAAD(w.objectID, index, uint32(chunkLen), final))
	if _, err := w.w.Write(frame); err != nil {
		w.closeFailed = true
		return err
	}
	w.pending = w.pending[:0]
	w.index++
	return nil
}

// Close flushes the final segment (if any) and writes the authenticated
// header trailer. It is safe to call multiple times; subsequent calls return
// ErrClosed.
func (w *Writer) Close() error {
	if w.closed {
		return ErrClosed
	}
	w.closed = true

	if len(w.pending) > 0 {
		if err := w.sealSegment(true); err != nil {
			return err
		}
	}

	header := encodeHeader(uint32(w.chunkSize), w.total, w.objectID)
	headerNonce, err := deriveNonce(w.baseNonce, 0)
	if err != nil {
		return err
	}
	sealedHeader := w.aead.Seal(nil, headerNonce[:], header, headerAAD())

	// Footer layout: baseNonce || sealedHeader || u32BE(sealedHeaderLen) || magic
	footer := make([]byte, 0, nonceSize+len(sealedHeader)+footerSuffix)
	footer = append(footer, w.baseNonce[:]...)
	footer = append(footer, sealedHeader...)
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(sealedHeader)))
	footer = append(footer, lenBuf[:]...)
	footer = append(footer, magic...)

	if _, err := w.w.Write(footer); err != nil {
		w.closeFailed = true
		return err
	}
	return nil
}

// CloseFailed reports whether an underlying write error previously occurred.
func (w *Writer) CloseFailed() bool { return w.closeFailed }
