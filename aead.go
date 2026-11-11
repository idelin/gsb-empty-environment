// Package netsec provides a failure-isolating adapter around the standard
// library's cipher.AEAD implementations.
//
// The encrypted channel's upper layer reuses destination buffers across
// tenants, so an authentication failure must never expose partial plaintext
// or stale contents through the destination. Every operation therefore runs
// through a bounded scratch buffer: the destination reference is only
// updated after the AEAD operation has fully succeeded, and on any error
// the destination's logical length and contents are exactly what they were
// before the call.
package netsec

import (
	"crypto/cipher"
	"errors"
	"sync"
)

var (
	// ErrInvalidNonce is returned when the nonce length does not match the
	// wrapped AEAD's nonce size.
	ErrInvalidNonce = errors.New("netsec: nonce length does not match AEAD nonce size")

	// ErrPlaintextTooLarge is returned by Seal when the plaintext exceeds
	// Config.MaxPlaintext.
	ErrPlaintextTooLarge = errors.New("netsec: plaintext exceeds configured maximum")

	// ErrCiphertextTooLarge is returned by Open when the ciphertext exceeds
	// Config.MaxCiphertext.
	ErrCiphertextTooLarge = errors.New("netsec: ciphertext exceeds configured maximum")

	// ErrTempLimitExceeded is returned when a call would require more
	// scratch memory than Config.MaxTempBytes allows. The request is
	// rejected before any allocation or cryptographic work happens.
	ErrTempLimitExceeded = errors.New("netsec: required scratch memory exceeds temporary memory limit")

	// ErrAuthentication is returned by Open when the ciphertext is
	// malformed or fails authentication. The destination is left untouched.
	ErrAuthentication = errors.New("netsec: ciphertext authentication failed")
)

// Config bounds the work a single Adapter call may perform.
type Config struct {
	// MaxPlaintext is the maximum plaintext size Seal accepts.
	MaxPlaintext int
	// MaxCiphertext is the maximum ciphertext size Open accepts.
	MaxCiphertext int
	// MaxTempBytes is the maximum scratch memory a single call may use.
	// Calls needing more are rejected with ErrTempLimitExceeded.
	MaxTempBytes int
}

// Adapter wraps a cipher.AEAD with failure-isolated, buffer-reusing
// Seal/Open operations. It is safe for concurrent use.
type Adapter struct {
	aead cipher.AEAD
	cfg  Config
	pool sync.Pool // scratch buffers, always zeroed before reuse
}

// NewAdapter builds an Adapter around aead. All limits must be >= 0.
func NewAdapter(aead cipher.AEAD, cfg Config) (*Adapter, error) {
	if aead == nil {
		return nil, errors.New("netsec: nil AEAD")
	}
	if cfg.MaxPlaintext < 0 || cfg.MaxCiphertext < 0 || cfg.MaxTempBytes < 0 {
		return nil, errors.New("netsec: negative limit in config")
	}
	return &Adapter{aead: aead, cfg: cfg}, nil
}

// NonceSize returns the nonce size of the wrapped AEAD.
func (a *Adapter) NonceSize() int { return a.aead.NonceSize() }

// Overhead returns the authentication tag overhead of the wrapped AEAD.
func (a *Adapter) Overhead() int { return a.aead.Overhead() }

// Seal encrypts and authenticates plaintext and, on success, replaces *dst
// with the ciphertext, reusing dst's backing capacity when possible.
//
// plaintext and *dst may be fully overlapping, partially overlapping, or
// independent buffers; the plaintext is staged in scratch memory first, so
// no aliasing layout can corrupt the result. On any error *dst is left
// exactly as it was before the call.
func (a *Adapter) Seal(dst *[]byte, nonce, plaintext, additionalData []byte) error {
	if len(nonce) != a.aead.NonceSize() {
		return ErrInvalidNonce
	}
	if len(plaintext) > a.cfg.MaxPlaintext {
		return ErrPlaintextTooLarge
	}
	tmp, err := a.scratch(len(plaintext) + a.aead.Overhead())
	if err != nil {
		return err
	}
	defer a.release(tmp)

	sealed := a.aead.Seal(tmp[:0], nonce, plaintext, additionalData)
	*dst = commit(*dst, sealed)
	return nil
}

// Open authenticates and decrypts ciphertext and, on success, replaces *dst
// with the plaintext, reusing dst's backing capacity when possible.
//
// ciphertext and *dst may be fully overlapping, partially overlapping, or
// independent buffers. Decryption runs entirely in scratch memory, so an
// authentication failure can never leak partial plaintext or stale tenant
// data through *dst: on any error *dst keeps its previous logical length
// and contents.
func (a *Adapter) Open(dst *[]byte, nonce, ciphertext, additionalData []byte) error {
	if len(nonce) != a.aead.NonceSize() {
		return ErrInvalidNonce
	}
	if len(ciphertext) > a.cfg.MaxCiphertext {
		return ErrCiphertextTooLarge
	}
	if len(ciphertext) < a.aead.Overhead() {
		return ErrAuthentication
	}
	tmp, err := a.scratch(len(ciphertext) - a.aead.Overhead())
	if err != nil {
		return err
	}
	defer a.release(tmp)

	plain, err := a.aead.Open(tmp[:0], nonce, ciphertext, additionalData)
	if err != nil {
		return ErrAuthentication
	}
	*dst = commit(*dst, plain)
	return nil
}

// scratch returns a temporary buffer of length n, honoring MaxTempBytes.
func (a *Adapter) scratch(n int) ([]byte, error) {
	if n > a.cfg.MaxTempBytes {
		return nil, ErrTempLimitExceeded
	}
	if n == 0 {
		return nil, nil
	}
	if v := a.pool.Get(); v != nil {
		if b := v.([]byte); cap(b) >= n {
			return b[:n], nil
		}
		// Too small; drop it and allocate. It stays eligible for GC.
	}
	return make([]byte, n), nil
}

// release zeroes b (erasing any partial plaintext) and returns it to the
// pool when it is within the configured limit.
func (a *Adapter) release(b []byte) {
	if b == nil {
		return
	}
	for i := range b {
		b[i] = 0
	}
	if cap(b) <= a.cfg.MaxTempBytes {
		a.pool.Put(b[:cap(b)])
	}
}

// commit copies src into dst's backing array, growing it if needed, and
// returns the updated slice. It is only called after a successful AEAD
// operation, so callers never observe a partially updated destination.
func commit(dst, src []byte) []byte {
	if cap(dst) < len(src) {
		dst = make([]byte, len(src))
	} else {
		dst = dst[:len(src)]
	}
	copy(dst, src)
	return dst
}
