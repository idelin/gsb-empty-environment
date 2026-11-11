// Package record implements the legacy session-layer record format.
//
// A legacy record protects a plaintext payload with encrypt-then-MAC:
// AES-CBC for confidentiality and HMAC-SHA-256 for authentication. The
// design treats any malformed or tampered input as an authentication
// failure so that callers cannot distinguish truncation, padding errors,
// bad MACs, or replayed sequence numbers from one another.
//
// Wire layout of a record:
//
//	iv   (aes.BlockSize bytes)
//	ct   (whole number of aes.BlockSize blocks, PKCS#7 padded plaintext)
//	tag  (sha256.Size bytes, HMAC over the authenticated data)
//
// Authenticated data (covered by the HMAC, transmitted implicitly via the
// session state rather than on the wire):
//
//	u64-be(sequence number) || u64-be(payload length) || iv || ct
//
// The MAC is always verified before any decryption is attempted, and the
// ciphertext is never decrypted on an unauthenticated path. Every failure
// returned by [Reader.Open] is the single sentinel [ErrAuth], which
// prevents an attacker from learning whether a chosen ciphertext has valid
// PKCS#7 padding (the Vaudenay padding-oracle side channel).
package record

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"hash"
)

// TagSize is the size in bytes of the per-record HMAC-SHA-256 tag.
const TagSize = sha256.Size

// minRecordSize is the smallest possible record: IV, one ciphertext block
// (the mandatory PKCS#7 padding block), and the tag.
const minRecordSize = aes.BlockSize + aes.BlockSize + TagSize

// ErrAuth is the only error observable through [Reader.Open]. It is
// returned for every failure mode (truncation, bad MAC, wrong sequence
// number, malformed padding, tampered ciphertext) so that externally
// visible behavior never reveals which internal check failed.
var ErrAuth = errors.New("record: authentication failed")

// Counter receives one observation per authentication failure. It is the
// countable failure interface for the failure path: implementations may
// increment metrics, log, or raise alarms. Callbacks must never panic and
// must not inspect record contents, since they run before any failure
// details are surfaced.
type Counter interface {
	AuthFailure()
}

// CounterFunc adapts a plain function to the [Counter] interface.
type CounterFunc func()

// AuthFailure implements [Counter].
func (f CounterFunc) AuthFailure() { f() }

// Reader decapsulates authenticated legacy records. It is not safe for
// concurrent use; a session owns a single ordered record stream.
type Reader struct {
	mac     func() hash.Hash
	block   cipher.Block
	counter Counter
	seq     uint64
	failed  uint64
}

// NewReader constructs a Reader from a 16, 24, or 32-byte AES key and a
// 32-byte HMAC-SHA-256 key. A nil counter disables failure notifications;
// failure counts remain available through [Reader.Failures].
func NewReader(aesKey, macKey []byte, counter Counter) (*Reader, error) {
	block, err := aes.NewCipher(aesKey)
	if err != nil {
		return nil, err
	}
	mac := func() hash.Hash {
		return hmac.New(sha256.New, macKey)
	}
	return &Reader{mac: mac, block: block, counter: counter}, nil
}

// Open authenticates and decapsulates one record. On success it returns
// the plaintext and advances the expected sequence number. On any failure
// it returns [ErrAuth]; the sequence number is unchanged and the failure
// counter is incremented exactly once.
//
// No plaintext is ever produced or returned on a failing path.
func (r *Reader) Open(record []byte) (plaintext []byte, err error) {
	// Convert unexpected internal failures (for example a panicking
	// counter or standard-library implementation) into the same external
	// authentication error so failure paths stay indistinguishable.
	defer func() {
		if recover() != nil {
			r.failed++
			plaintext = nil
			err = ErrAuth
		}
	}()

	// Length check first: too short to contain a frame, or a ciphertext
	// region that is not a whole number of blocks. Both are
	// indistinguishable from any other authentication failure.
	if len(record) < minRecordSize || (len(record)-aes.BlockSize-TagSize)%aes.BlockSize != 0 {
		r.fail()
		return nil, ErrAuth
	}

	iv := record[:aes.BlockSize]
	ciphertext := record[aes.BlockSize : len(record)-TagSize]
	tag := record[len(record)-TagSize:]

	// Authenticate before decrypting. The MAC covers the sequence number
	// (binding the record to its position in the stream), the payload
	// length, the IV, and the ciphertext.
	mac := r.mac()
	var header [16]byte
	binary.BigEndian.PutUint64(header[:8], r.seq)
	binary.BigEndian.PutUint64(header[8:], uint64(len(ciphertext)))
	mac.Write(header[:])
	mac.Write(iv)
	mac.Write(ciphertext)
	expected := mac.Sum(nil)

	if !hmac.Equal(tag, expected) {
		r.fail()
		return nil, ErrAuth
	}

	// The record is authentic: it is now safe to decrypt. The output
	// buffer is freshly allocated and never exposed on an error path.
	decrypted := make([]byte, len(ciphertext))
	cipher.NewCBCDecrypter(r.block, iv).CryptBlocks(decrypted, ciphertext)

	padded, err := pkcs7Unpad(decrypted, aes.BlockSize)
	if err != nil {
		// A valid MAC but structurally invalid padding can only arise
		// from a buggy peer; it is still an authentication failure to
		// the outside world.
		r.fail()
		return nil, ErrAuth
	}

	plaintext = padded
	r.seq++
	return plaintext, nil
}

// Sequence returns the next expected record sequence number. It advances by
// one only after a fully authenticated record.
func (r *Reader) Sequence() uint64 { return r.seq }

// Failures returns the number of times [Reader.Open] has failed.
func (r *Reader) Failures() uint64 { return r.failed }

// fail records one authentication failure, notifying an optional counter.
// A panicking counter is contained so it cannot change the externally
// observable error or corrupt the internal tally.
func (r *Reader) fail() {
	r.failed++
	if r.counter != nil {
		func() {
			defer func() { _ = recover() }()
			r.counter.AuthFailure()
		}()
	}
}

// Writer encapsulates legacy records. It is the counterpart of [Reader]
// and exists so tests and real sessions can produce well-formed frames.
// It is not safe for concurrent use.
type Writer struct {
	mac   func() hash.Hash
	block cipher.Block
	seq   uint64
}

// NewWriter constructs a Writer with the same key material as [NewReader].
func NewWriter(aesKey, macKey []byte) (*Writer, error) {
	block, err := aes.NewCipher(aesKey)
	if err != nil {
		return nil, err
	}
	return &Writer{
		mac:   func() hash.Hash { return hmac.New(sha256.New, macKey) },
		block: block,
	}, nil
}

// Seal encrypts and authenticates plaintext as the next record in the
// sequence. The returned slice is independent of plaintext and may be
// retained by the caller.
func (w *Writer) Seal(plaintext []byte) ([]byte, error) {
	padded := pkcs7Pad(plaintext, aes.BlockSize)

	record := make([]byte, aes.BlockSize+len(padded)+TagSize)
	iv := record[:aes.BlockSize]
	ciphertext := record[aes.BlockSize : aes.BlockSize+len(padded)]
	tag := record[aes.BlockSize+len(padded):]

	if _, err := readRandom(iv); err != nil {
		return nil, err
	}
	cipher.NewCBCEncrypter(w.block, iv).CryptBlocks(ciphertext, padded)

	mac := w.mac()
	var header [16]byte
	binary.BigEndian.PutUint64(header[:8], w.seq)
	binary.BigEndian.PutUint64(header[8:], uint64(len(ciphertext)))
	mac.Write(header[:])
	mac.Write(iv)
	mac.Write(ciphertext)
	copy(tag, mac.Sum(nil))

	w.seq++
	return record, nil
}

// Sequence returns the sequence number the next [Writer.Seal] will use.
func (w *Writer) Sequence() uint64 { return w.seq }

// pkcs7Pad returns a PKCS#7 padded copy of data. A full block of padding
// is always appended, so the output length is never zero.
func pkcs7Pad(data []byte, blockSize int) []byte {
	padding := blockSize - len(data)%blockSize
	out := make([]byte, len(data)+padding)
	copy(out, data)
	for i := len(data); i < len(out); i++ {
		out[i] = byte(padding)
	}
	return out
}

// pkcs7Unpad validates and removes PKCS#7 padding. It returns an error
// rather than panicking on any malformed padding.
func pkcs7Unpad(data []byte, blockSize int) ([]byte, error) {
	if len(data) == 0 || len(data)%blockSize != 0 {
		return nil, ErrAuth
	}
	padding := int(data[len(data)-1])
	if padding == 0 || padding > blockSize || padding > len(data) {
		return nil, ErrAuth
	}
	for _, b := range data[len(data)-padding:] {
		if int(b) != padding {
			return nil, ErrAuth
		}
	}
	return data[:len(data)-padding], nil
}
