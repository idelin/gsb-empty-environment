package sctpauth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
)

// KeySize is the size in bytes of every rotating HMAC key.
const KeySize = 32

// hmacTag computes HMAC-SHA256(key, data).
func hmacTag(key, data []byte) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write(data)
	return mac.Sum(nil)
}

// KeyRing holds two cookie-signing key generations. Cookies are signed with
// the current generation; verification accepts either the current or the
// immediately previous generation so that in-flight cookies survive a
// rotation. Rotation is constant time with respect to the key values.
type KeyRing struct {
	current  []byte
	previous []byte
	drbg     *hmacDRBG
}

// NewKeyRing derives generation 0 deterministically from seed using
// HMAC-SHA256. The seed must carry at least 32 bytes of server entropy.
func NewKeyRing(seed []byte) *KeyRing {
	ring := &KeyRing{
		current:  deriveKey(seed, []byte("sctpauth cookie key gen 0")),
		previous: nil,
	}
	ring.drbg = newHMACDRBG(deriveKey(seed, []byte("sctpauth nonce drbg seed")))
	return ring
}

// deriveKey expands seed into a fixed-length HMAC key under a domain label.
func deriveKey(seed, label []byte) []byte {
	mac := hmac.New(sha256.New, seed)
	mac.Write(label)
	return mac.Sum(nil)
}

// Rotate derives a fresh generation from the supplied rotation secret and
// demotes the current generation to previous. The DRBG is reseeded from the
// same secret so nonces can never be predicted across rotations.
func (r *KeyRing) Rotate(rotationSecret []byte) {
	r.previous = r.current
	r.current = deriveKey(rotationSecret, []byte("sctpauth cookie key rotation"))
	r.drbg.reseed(deriveKey(rotationSecret, []byte("sctpauth nonce drbg reseed")))
}

// sign authenticates data under the current generation.
func (r *KeyRing) sign(data []byte) []byte {
	return hmacTag(r.current, data)
}

// authenticate reports whether tag is valid under either retained generation.
// Both candidates are compared in constant time regardless of which matches.
func (r *KeyRing) authenticate(data, tag []byte) bool {
	if hmac.Equal(hmacTag(r.current, data), tag) {
		return true
	}
	if r.previous != nil && hmac.Equal(hmacTag(r.previous, data), tag) {
		return true
	}
	return false
}

// Nonce returns 32 fresh, unpredictable bytes from the HMAC DRBG.
func (r *KeyRing) Nonce() [32]byte {
	var out [32]byte
	copy(out[:], r.drbg.generate(32))
	return out
}

// hmacDRBG is a minimal HMAC-SHA256 deterministic random bit generator
// following the update/generate structure of NIST SP 800-90A. It uses only
// HMAC and SHA-256, so no platform randomness or network is required.
type hmacDRBG struct {
	key []byte
	v   []byte
}

func newHMACDRBG(seed []byte) *hmacDRBG {
	d := &hmacDRBG{
		key: make([]byte, KeySize),
		v:   make([]byte, KeySize),
	}
	for i := range d.v {
		d.v[i] = 0x01
	}
	d.update(seed)
	return d
}

// update implements the HMAC_DRBG_Update routine with provided data.
func (d *hmacDRBG) update(providedData []byte) {
	mac := hmac.New(sha256.New, d.key)
	mac.Write(d.v)
	mac.Write([]byte{0x00})
	mac.Write(providedData)
	d.key = mac.Sum(nil)

	mac = hmac.New(sha256.New, d.key)
	mac.Write(d.v)
	d.v = mac.Sum(nil)

	if len(providedData) > 0 {
		mac = hmac.New(sha256.New, d.key)
		mac.Write(d.v)
		mac.Write([]byte{0x01})
		mac.Write(providedData)
		d.key = mac.Sum(nil)

		mac = hmac.New(sha256.New, d.key)
		mac.Write(d.v)
		d.v = mac.Sum(nil)
	}
}

// reseed mixes fresh material into the generator state.
func (d *hmacDRBG) reseed(entropy []byte) {
	d.update(entropy)
}

// generate returns n pseudo-random bytes, refreshing after each block.
func (d *hmacDRBG) generate(n int) []byte {
	out := make([]byte, 0, n)
	for len(out) < n {
		mac := hmac.New(sha256.New, d.key)
		mac.Write(d.v)
		d.v = mac.Sum(nil)
		out = append(out, d.v...)
	}
	d.update(nil)
	return out[:n]
}

// randUint32 draws one uniformly distributed 32-bit value from the DRBG.
func (r *KeyRing) randUint32() uint32 {
	return binary.BigEndian.Uint32(r.drbg.generate(4))
}
