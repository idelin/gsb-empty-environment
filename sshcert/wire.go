// Package sshcert implements native, standard-library-only parsing and
// verification of OpenSSH ssh-ed25519-cert-v01@openssh.com user/host
// certificates and binary KRL (SSHKRL) revocation lists.
//
// No golang.org/x/crypto dependency is used anywhere in this package: Ed25519
// verification goes through crypto/ed25519 and fingerprints through
// crypto/sha256. All wire structures are decoded directly from the raw SSH wire
// format described in PROTOCOL.certkeys and PROTOCOL.krl.
package sshcert

import "errors"

// wireReader is a strict, bounds-checked reader for SSH wire primitives.
// Every read fails once the underlying buffer is exhausted or a declared
// length exceeds the remaining data; callers must additionally check that the
// reader is fully consumed where the format requires it.
type wireReader struct {
	data []byte
	pos  int
}

func newWireReader(b []byte) *wireReader {
	return &wireReader{data: b}
}

func (r *wireReader) remaining() int {
	return len(r.data) - r.pos
}

func (r *wireReader) done() bool {
	return r.pos == len(r.data)
}
func (r *wireReader) byte() (byte, error) {
	if r.pos >= len(r.data) {
		return 0, ErrTruncated
	}
	v := r.data[r.pos]
	r.pos++
	return v, nil
}

func (r *wireReader) bytes(n int) ([]byte, error) {
	if n < 0 || n > r.remaining() {
		return nil, ErrTruncated
	}
	out := r.data[r.pos : r.pos+n]
	r.pos += n
	return out, nil
}

// uint32 reads a big-endian uint32.
func (r *wireReader) uint32() (uint32, error) {
	b, err := r.bytes(4)
	if err != nil {
		return 0, err
	}
	return uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3]), nil
}

// uint64 reads a big-endian uint64.
func (r *wireReader) uint64() (uint64, error) {
	b, err := r.bytes(8)
	if err != nil {
		return 0, err
	}
	var v uint64
	for _, c := range b {
		v = v<<8 | uint64(c)
	}
	return v, nil
}

// string reads an SSH string: uint32 length prefix followed by that many
// bytes. A zero-length string returns a non-nil empty slice.
func (r *wireReader) string() ([]byte, error) {
	n, err := r.uint32()
	if err != nil {
		return nil, err
	}
	out, err := r.bytes(int(n))
	if err != nil {
		return nil, err
	}
	return out, nil
}

// subReader returns a new reader over a length-prefixed blob and advances the
// parent reader past it, enforcing that the declared length is in bounds.
func (r *wireReader) subReader() (*wireReader, error) {
	blob, err := r.string()
	if err != nil {
		return nil, err
	}
	return newWireReader(blob), nil
}

// mpint reads an SSH mpint (RFC 4251): a length-prefixed signed big-endian
// integer. It returns the raw magnitude bytes after enforcing the canonical
// encoding rules (minimal length, leading zero only to mark a positive sign).
func (r *wireReader) mpint() ([]byte, error) {
	blob, err := r.string()
	if err != nil {
		return nil, err
	}
	if len(blob) == 0 {
		return blob, nil
	}
	// Negative mpints are never valid for a bitmap payload.
	if blob[0]&0x80 != 0 {
		return nil, ErrKRLBadBitmap
	}
	// A leading zero byte is only legal when needed to keep the sign bit
	// clear on the following magnitude byte.
	if blob[0] == 0 {
		if len(blob) == 1 || blob[1]&0x80 == 0 {
			return nil, ErrKRLBadBitmap
		}
	}
	return blob, nil
}

// wireWriter emits the same SSH wire primitives. It is used by the package's
// test vectors to hand-craft certificates and KRLs byte by byte.
type wireWriter struct {
	buf []byte
}

func (w *wireWriter) byte(v byte) {
	w.buf = append(w.buf, v)
}

func (w *wireWriter) bytes(b []byte) {
	w.buf = append(w.buf, b...)
}

func (w *wireWriter) uint32(v uint32) {
	w.buf = append(w.buf, byte(v>>24), byte(v>>16), byte(v>>8), byte(v))
}

func (w *wireWriter) uint64(v uint64) {
	for shift := 56; shift >= 0; shift -= 8 {
		w.buf = append(w.buf, byte(v>>shift))
	}
}

func (w *wireWriter) string(b []byte) {
	w.uint32(uint32(len(b)))
	w.buf = append(w.buf, b...)
}

// stringPrefixed wraps the current buffer in a length-prefixed string.
func (w *wireWriter) stringPrefixed() []byte {
	out := make([]byte, 4+len(w.buf))
	putUint32(out, uint32(len(w.buf)))
	copy(out[4:], w.buf)
	return out
}

func putUint32(b []byte, v uint32) {
	b[0] = byte(v >> 24)
	b[1] = byte(v >> 16)
	b[2] = byte(v >> 8)
	b[3] = byte(v)
}

// wrapString returns the SSH wire encoding (length prefix + content) of b.
func wrapString(b []byte) []byte {
	out := make([]byte, 4+len(b))
	putUint32(out, uint32(len(b)))
	copy(out[4:], b)
	return out
}

// Common structural errors.
var (
	// ErrTruncated means a record was cut short or a declared length ran past
	// the end of the containing buffer.
	ErrTruncated = errors.New("sshcert: truncated or length-overflowing record")
	// ErrTrailingData means bytes remain after a record that must fully
	// consume its buffer.
	ErrTrailingData = errors.New("sshcert: trailing bytes after record")
)

// bytesLess reports the strict lexicographic ordering of two byte strings.
func bytesLess(a, b []byte) bool {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return len(a) < len(b)
}

// bytesEqual is a constant-time-free structural comparison used for ordering
// and exact-match lookups on non-secret identifiers.
func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// uint64Sorted reports whether s is strictly ascending.
func uint64Sorted(s []uint64) bool {
	for i := 1; i < len(s); i++ {
		if s[i] <= s[i-1] {
			return false
		}
	}
	return true
}
