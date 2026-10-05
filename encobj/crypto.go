package encobj

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
)

// schemaVersion is the on-disk schema version bound into every AEAD
// additional-data string.
const schemaVersion uint32 = 1

// ErrCorrupt is returned when any authenticated or digest-pinned data fails
// verification (tampered, stale, swapped or truncated ciphertext/manifest).
var ErrCorrupt = errors.New("encobj: corrupt or tampered data")

// ---------------------------------------------------------------------------
// Canonical, unambiguous binary encoding helpers.
// All integers are fixed-width big-endian; all strings/byte blobs are
// uint32-length-prefixed. No encoding is a prefix of another.
// ---------------------------------------------------------------------------

type encbuf struct{ b []byte }

func (e *encbuf) u8(v byte)  { e.b = append(e.b, v) }
func (e *encbuf) u32(v uint32) {
	var t [4]byte
	binary.BigEndian.PutUint32(t[:], v)
	e.b = append(e.b, t[:]...)
}
func (e *encbuf) u64(v uint64) {
	var t [8]byte
	binary.BigEndian.PutUint64(t[:], v)
	e.b = append(e.b, t[:]...)
}
func (e *encbuf) str(s string) {
	e.u32(uint32(len(s)))
	e.b = append(e.b, s...)
}
func (e *encbuf) bytes(b []byte) {
	e.u32(uint32(len(b)))
	e.b = append(e.b, b...)
}
func (e *encbuf) fixed(b []byte) { e.b = append(e.b, b...) }

type decbuf struct {
	b   []byte
	bad bool
}

func (d *decbuf) take(n int) ([]byte, bool) {
	if d.bad || n < 0 || len(d.b) < n {
		d.bad = true
		return nil, false
	}
	r := d.b[:n]
	d.b = d.b[n:]
	return r, true
}
func (d *decbuf) u8() (byte, bool) {
	t, ok := d.take(1)
	if !ok {
		return 0, false
	}
	return t[0], true
}
func (d *decbuf) u32() (uint32, bool) {
	t, ok := d.take(4)
	if !ok {
		return 0, false
	}
	return binary.BigEndian.Uint32(t), true
}
func (d *decbuf) u64() (uint64, bool) {
	t, ok := d.take(8)
	if !ok {
		return 0, false
	}
	return binary.BigEndian.Uint64(t), true
}
func (d *decbuf) str() (string, bool) {
	n, ok := d.u32()
	if !ok {
		return "", false
	}
	t, ok := d.take(int(n))
	if !ok {
		return "", false
	}
	return string(t), true
}
func (d *decbuf) fixed(n int) ([]byte, bool) { return d.take(n) }
func (d *decbuf) done() bool                 { return !d.bad && len(d.b) == 0 }

// ---------------------------------------------------------------------------
// AEAD additional data. Every ciphertext is bound to its full identity:
// purpose, schema version, object ID, and the field/commit coordinates plus
// the key ID that encrypted it.
// ---------------------------------------------------------------------------

func aadField(objID string, fieldID, revision uint64, keyID string) []byte {
	var e encbuf
	e.str("encobj/aad/v1")
	e.u8(1) // purpose: field value
	e.u32(schemaVersion)
	e.str(objID)
	e.u64(fieldID)
	e.u64(revision)
	e.str(keyID)
	return e.b
}

func aadManifest(objID string, version uint64, keyID string) []byte {
	var e encbuf
	e.str("encobj/aad/v1")
	e.u8(2) // purpose: manifest
	e.u32(schemaVersion)
	e.str(objID)
	e.u64(version)
	e.str(keyID)
	return e.b
}

// ---------------------------------------------------------------------------
// AES-256-GCM helpers. No cryptographic primitives are implemented here; all
// cryptography is delegated to crypto/aes and crypto/cipher.
// ---------------------------------------------------------------------------

const (
	keyLen   = 32 // AES-256
	nonceLen = 12 // 96-bit GCM nonce
	tagLen   = 16
)

func gcm(key []byte) (cipher.AEAD, error) {
	if len(key) != keyLen {
		return nil, fmt.Errorf("encobj: key must be %d bytes", keyLen)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func sealGCM(key, nonce, plain, aad []byte) ([]byte, error) {
	g, err := gcm(key)
	if err != nil {
		return nil, err
	}
	return g.Seal(nil, nonce, plain, aad), nil
}

func openGCM(key, nonce, ct, aad []byte) ([]byte, error) {
	g, err := gcm(key)
	if err != nil {
		return nil, err
	}
	plain, err := g.Open(nil, nonce, ct, aad)
	if err != nil {
		return nil, fmt.Errorf("%w: AEAD authentication failed", ErrCorrupt)
	}
	return plain, nil
}

func sha256Sum(b []byte) [32]byte { return sha256.Sum256(b) }
