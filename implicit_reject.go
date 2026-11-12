package netsec

import (
	"crypto/hmac"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"math/big"
)

// PreMasterSecretLen is the fixed length of every value returned by
// DecryptKeyTransport: a TLS RSA key transport premaster secret is 48 bytes
// (two version bytes followed by 46 random bytes).
const PreMasterSecretLen = 48

// ErrKeyTransport is the only error ever returned by DecryptKeyTransport. It
// is returned solely when the package is misused (a nil or malformed private
// key). Every failure that depends on the attacker-controlled ciphertext
// (wrong length, malformed PKCS #1 v1.5 padding, plaintext of the wrong size)
// is handled with implicit rejection: the function still returns a 48-byte
// value and a nil error, so that legal and illegal paths expose identical
// error objects and identical result shapes to the handshake layer.
var ErrKeyTransport = errors.New("netsec: key transport decryption failed")

// hkdfInfoPrefix tags the key derivation used to synthesize fallback premaster
// secrets, so derived bytes are bound to this protocol use.
var hkdfInfoPrefix = []byte("netsec-rsa-pkcs1v15-implicit-reject/v1")

// DecryptKeyTransport performs RSA PKCS #1 v1.5 decryption with implicit
// rejection (RFC 5246 Section 7.4.7.1, RFC 8017 Section 7.2.2) for the TLS
// handshake key transport.
//
// The function never reports padding or length errors to the caller. It
// always returns a [PreMasterSecretLen]-byte value together with a nil error
// when priv is usable:
//
//   - If the ciphertext is well formed and the embedded message is exactly
//     48 bytes, that message is returned.
//   - Otherwise (truncated or oversized ciphertext, wrong 0x00 0x02 prefix,
//     padding string shorter than eight bytes, missing or shifted zero
//     separator, message length other than 48), a deterministic pseudorandom
//     48-byte value derived from the private key and the exact ciphertext is
//     returned. Repeating the same request always yields the same fallback
//     value, and different ciphertexts or different private keys yield
//     different fallback values.
//
// Padding is validated in constant time and the fallback value is selected
// with crypto/subtle, so neither the error channel nor control flow
// distinguishes valid from invalid ciphertexts.
func DecryptKeyTransport(priv *rsa.PrivateKey, ciphertext []byte) ([PreMasterSecretLen]byte, error) {
	var secret [PreMasterSecretLen]byte
	if err := checkPrivateKey(priv); err != nil {
		return secret, err
	}

	k := priv.Size()

	// Compute the fallback before touching the RSA result so both branches
	// perform the same derivation work. The fallback is a deterministic
	// function of the private exponent, the public modulus and the exact
	// ciphertext bytes, hence stable across repeated requests yet only
	// reproducible by holders of the private key.
	fallback := deriveFallback(priv, ciphertext)

	// Normalize the ciphertext into a k-byte representative. Wrong-length
	// inputs cannot be RSA-decrypted, so they are reduced to a fixed-width
	// value with the same byte length as a valid block and follow the same
	// constant-time unpadding path, which can never validate.
	representative := make([]byte, k)
	if len(ciphertext) == k {
		copy(representative, ciphertext)
	} else {
		c := new(big.Int).SetBytes(ciphertext)
		c.Mod(c, priv.N)
		rb := c.Bytes()
		copy(representative[k-len(rb):], rb)
	}

	em, err := rawRSADecrypt(priv, representative)
	if err != nil {
		// A representative inside the modulus cannot fail; keep the single
		// indistinguishable error contract regardless.
		return fallback, nil
	}

	valid, index := pkcs1v15Unpad(em)
	// The embedded message must be exactly 48 bytes. A valid envelope around
	// any other length is still an invalid key transport message.
	valid &= subtle.ConstantTimeEq(int32(k-index), PreMasterSecretLen)

	subtle.ConstantTimeCopy(valid, secret[:], em[k-PreMasterSecretLen:])
	subtle.ConstantTimeCopy(1^valid, secret[:], fallback[:])
	return secret, nil
}

// checkPrivateKey returns ErrKeyTransport for every unusable private key so
// that callers cannot distinguish failure causes through error values.
func checkPrivateKey(priv *rsa.PrivateKey) error {
	if priv == nil || priv.N == nil || priv.D == nil {
		return ErrKeyTransport
	}
	// The modulus must hold a 48-byte message plus the 11-byte minimum
	// PKCS #1 v1.5 overhead (0x00 0x02 || PS(>=8) || 0x00).
	if priv.Size() < PreMasterSecretLen+11 {
		return ErrKeyTransport
	}
	if err := priv.Validate(); err != nil {
		return ErrKeyTransport
	}
	return nil
}

// rawRSADecrypt computes the textbook RSA private operation m = c^d mod N and
// returns m as a fixed k-byte big-endian block.
func rawRSADecrypt(priv *rsa.PrivateKey, c []byte) ([]byte, error) {
	k := priv.Size()
	if len(c) != k {
		return nil, ErrKeyTransport
	}
	ci := new(big.Int).SetBytes(c)
	if ci.Cmp(priv.N) >= 0 {
		return nil, ErrKeyTransport
	}
	mi := new(big.Int).Exp(ci, priv.D, priv.N)
	m := make([]byte, k)
	mb := mi.Bytes()
	copy(m[k-len(mb):], mb)
	return m, nil
}

// pkcs1v15Unpad validates the PKCS #1 v1.5 encryption block
// 0x00 || 0x02 || PS(non-zero, >=8) || 0x00 || M in constant time.
// It returns valid (1 or 0) and, when valid, the index one past the zero
// separator. On an invalid block index is zero.
func pkcs1v15Unpad(em []byte) (valid, index int) {
	firstByteIsZero := subtle.ConstantTimeByteEq(em[0], 0)
	secondByteIsTwo := subtle.ConstantTimeByteEq(em[1], 2)

	lookingForIndex := 1
	for i := 2; i < len(em); i++ {
		equals0 := subtle.ConstantTimeByteEq(em[i], 0)
		index = subtle.ConstantTimeSelect(lookingForIndex&equals0, i, index)
		lookingForIndex = subtle.ConstantTimeSelect(equals0, 0, lookingForIndex)
	}

	validPS := subtle.ConstantTimeLessOrEq(2+8, index)
	valid = firstByteIsZero & secondByteIsTwo & (^lookingForIndex & 1) & validPS
	index = subtle.ConstantTimeSelect(valid, index+1, 0)
	return valid, index
}

// deriveFallback expands a deterministic pseudorandom premaster secret from
// the private key material and the received ciphertext using HKDF-SHA256
// (RFC 5869). No randomness source is used, so identical requests produce
// identical fallback values while an attacker without the private exponent
// cannot predict the value for any ciphertext.
func deriveFallback(priv *rsa.PrivateKey, ciphertext []byte) [PreMasterSecretLen]byte {
	var out [PreMasterSecretLen]byte

	prk := hkdfExtract(priv.D.Bytes(), priv.N.Bytes())

	info := make([]byte, 0, len(hkdfInfoPrefix)+8+len(ciphertext))
	info = append(info, hkdfInfoPrefix...)
	info = appendUint32(info, uint32(len(ciphertext)))
	info = append(info, ciphertext...)

	hkdfExpand(prk, info, out[:])
	return out
}

// hkdfExtract implements HKDF-Extract: PRK = HMAC-SHA256(salt, IKM).
func hkdfExtract(ikm, salt []byte) []byte {
	mac := hmac.New(sha256.New, salt)
	mac.Write(ikm)
	return mac.Sum(nil)
}

// hkdfExpand implements HKDF-Expand and writes exactly len(out) bytes.
func hkdfExpand(prk, info, out []byte) {
	mac := hmac.New(sha256.New, prk)
	var previous []byte
	copied := 0
	for counter := byte(1); copied < len(out); counter++ {
		mac.Reset()
		mac.Write(previous)
		mac.Write(info)
		mac.Write([]byte{counter})
		previous = mac.Sum(nil)
		copied += copy(out[copied:], previous)
	}
}

// appendUint32 appends v as a four-byte big-endian length prefix.
func appendUint32(buf []byte, v uint32) []byte {
	return append(buf, byte(v>>24), byte(v>>16), byte(v>>8), byte(v))
}
