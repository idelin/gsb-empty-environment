package netsec

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/sha256"
	"errors"
	"io"
)

// STREAM construction from age v1 section 6: chunked AES-256-GCM with a fixed
// 64 KiB chunk size, an 11-byte nonce carrying the 64-bit counter and a final
// bit in the last nonce byte.

const (
	streamKeySize  = 32
	streamNonceLen = 12
	chunkSize      = 64 * 1024
	overhead       = 16
)

var (
	errChunkSize = errors.New("netsec: encrypted chunk has wrong size")
)

func newStreamGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func streamNonce(counter uint64, final bool) []byte {
	nonce := make([]byte, streamNonceLen)
	for i := 0; i < 8; i++ {
		nonce[7-i] = byte(counter >> (8 * i))
	}
	if final {
		nonce[11] = 1
	}
	return nonce
}

// streamReader decrypts the authenticated payload. It is only reachable after
// the header HMAC has been verified because PayloadReader gates construction.
type streamReader struct {
	source    io.Reader
	aead      cipher.AEAD
	initErr   error
	counter   uint64
	buf       []byte // decrypted, not-yet-consumed bytes of the current chunk
	seenFinal bool
}

func newStreamReader(source io.Reader, key []byte) *streamReader {
	aead, err := newStreamGCM(key)
	if err != nil {
		return &streamReader{source: source, initErr: err}
	}
	return &streamReader{source: source, aead: aead}
}

func (r *streamReader) Read(p []byte) (int, error) {
	if r.initErr != nil {
		return 0, r.initErr
	}
	if len(r.buf) > 0 {
		n := copy(p, r.buf)
		r.buf = r.buf[n:]
		return n, nil
	}
	if r.seenFinal {
		return 0, io.EOF
	}

	// Read exactly one chunk. A full-size chunk may be non-final or the final
	// chunk if it holds exactly chunkSize plaintext bytes; the final-bit in the
	// GCM nonce disambiguates during Open.
	enc := make([]byte, chunkSize+overhead)
	n, err := io.ReadFull(r.source, enc)
	if err == io.ErrUnexpectedEOF {
		// Short remainder: necessarily the final chunk.
		enc = enc[:n]
		final := true
		return r.openChunk(p, enc, final)
	}
	if err == io.EOF {
		return 0, io.EOF
	}
	if err != nil {
		return 0, err
	}

	// A full chunk: try the non-final nonce first, then the final nonce. The
	// GCM tag binds the final bit, so exactly one of the two can authenticate.
	if plaintext, openErr := r.aead.Open(nil, streamNonce(r.counter, false), enc, nil); openErr == nil {
		return r.deliver(p, plaintext, false)
	}
	plaintext, openErr := r.aead.Open(nil, streamNonce(r.counter, true), enc, nil)
	if openErr != nil {
		return 0, openErr
	}
	if len(plaintext) != chunkSize {
		return 0, errChunkSize
	}
	return r.deliver(p, plaintext, true)
}

func (r *streamReader) openChunk(p, enc []byte, final bool) (int, error) {
	plaintext, err := r.aead.Open(nil, streamNonce(r.counter, final), enc, nil)
	if err != nil {
		return 0, err
	}
	return r.deliver(p, plaintext, final)
}

func (r *streamReader) deliver(p, plaintext []byte, final bool) (int, error) {
	r.counter++
	r.seenFinal = final
	n := copy(p, plaintext)
	if n < len(plaintext) {
		r.buf = plaintext[n:]
	}
	if final {
		// Reject any data appended after the authenticated final chunk.
		extra := make([]byte, 1)
		if _, err := r.source.Read(extra); err != io.EOF {
			return n, errors.New("netsec: trailing data after final payload chunk")
		}
	}
	return n, nil
}

// streamWriter encrypts plaintext into STREAM chunks.
type streamWriter struct {
	dest    io.Writer
	aead    cipher.AEAD
	counter uint64
	pending []byte
}

func newStreamWriter(dest io.Writer, key []byte) (*streamWriter, error) {
	aead, err := newStreamGCM(key)
	if err != nil {
		return nil, err
	}
	return &streamWriter{dest: dest, aead: aead}, nil
}

func (w *streamWriter) Write(p []byte) (int, error) {
	w.pending = append(w.pending, p...)
	written := len(p)
	for len(w.pending) >= chunkSize {
		chunk := w.pending[:chunkSize]
		if err := w.writeChunk(chunk, false); err != nil {
			return 0, err
		}
		w.pending = w.pending[chunkSize:]
	}
	return written, nil
}

// Close flushes the final chunk, always emitting one (even when empty).
func (w *streamWriter) Close() error {
	if err := w.writeChunk(w.pending, true); err != nil {
		return err
	}
	w.pending = nil
	return nil
}

func (w *streamWriter) writeChunk(chunk []byte, final bool) error {
	sealed := w.aead.Seal(nil, streamNonce(w.counter, final), chunk, nil)
	w.counter++
	_, err := w.dest.Write(sealed)
	return err
}

func payloadKeyFromFileKey(fileKey []byte) ([]byte, error) {
	return hkdf.Key(sha256.New, fileKey, nil, "payload", streamKeySize)
}
