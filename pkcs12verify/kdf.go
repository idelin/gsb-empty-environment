package pkcs12verify

import (
	"crypto/hmac"
	"hash"
	"unicode/utf16"
)

// bmpStringZeroTerminated encodes a UTF-8 password as the PKCS#12 BMPString
// form: UTF-16-BE code units followed by a two-byte U+0000 terminator, exactly
// as required by RFC 7292 Appendix B.1. An empty password yields the two zero
// bytes on their own.
func bmpStringZeroTerminated(password string) []byte {
	runes := []rune(password)
	units := utf16.Encode(runes)
	out := make([]byte, 0, 2*(len(units)+1))
	for _, unit := range units {
		out = append(out, byte(unit>>8), byte(unit))
	}
	out = append(out, 0, 0)
	return out
}

// pkcs12KDF implements the key derivation function from RFC 7292 Appendix B.
// id is 1 for key material, 2 for IV material and 3 for MAC key material.
// newHash must produce the digest selected by the MacData algorithm.
func pkcs12KDF(password string, salt []byte, id byte, iterations int, n int, newHash func() hash.Hash) []byte {
	h := newHash()
	v := h.BlockSize()

	d := make([]byte, v)
	for i := range d {
		d[i] = id
	}

	passwordBytes := bmpStringZeroTerminated(password)
	extendedSalt := repeatToLength(salt, v)
	extendedPassword := repeatToLength(passwordBytes, v)

	iBuf := make([]byte, v)
	copy(iBuf, extendedSalt)
	for j := 0; j < v; j++ {
		iBuf[j] ^= extendedPassword[j]
	}

	out := make([]byte, 0, n)
	for len(out) < n {
		a := h
		a.Reset()
		a.Write(d)
		a.Write(iBuf)
		buf := a.Sum(nil)

		for iter := 1; iter < iterations; iter++ {
			a.Reset()
			a.Write(buf)
			buf = a.Sum(buf[:0])
		}

		out = append(out, buf...)

		// B = copies of the most recent A, then I = (I + B + 1) mod 2^(v*8),
		// performed independently on each v/8-byte block.
		b := repeatToLength(buf, v)
		blockSize := v / 8
		for offset := 0; offset < v; offset += blockSize {
			var carry uint32 = 1
			for j := blockSize - 1; j >= 0; j-- {
				sum := uint32(iBuf[offset+j]) + uint32(b[j]) + carry
				iBuf[offset+j] = byte(sum)
				carry = sum >> 8
			}
		}
	}
	return out[:n]
}

// repeatToLength expands data by concatenating whole copies until it fills a
// blockSize-length buffer, then truncates. Empty input yields a zero-filled
// block, matching the PKCS#12 expansion definition for absent salt/password.
func repeatToLength(data []byte, blockSize int) []byte {
	if blockSize <= 0 {
		return nil
	}
	out := make([]byte, blockSize)
	if len(data) == 0 {
		return out
	}
	for i := range out {
		out[i] = data[i%len(data)]
	}
	return out
}

// pbkdf2HMAC derives a key with PBKDF2 (RFC 8018 Section 5.2). The PRF is
// HMAC parameterised through newHash; only SHA-2 family constructors are wired
// in by the policy layer.
func pbkdf2HMAC(password, salt []byte, iterations, keyLength int, newHash func() hash.Hash) []byte {
	prf := hmac.New(newHash, password)
	hashLen := prf.Size()
	numBlocks := (keyLength + hashLen - 1) / hashLen

	dk := make([]byte, 0, numBlocks*hashLen)
	var block [4]byte
	for blockIndex := 1; blockIndex <= numBlocks; blockIndex++ {
		prf.Reset()
		prf.Write(salt)
		block[0] = byte(blockIndex >> 24)
		block[1] = byte(blockIndex >> 16)
		block[2] = byte(blockIndex >> 8)
		block[3] = byte(blockIndex)
		prf.Write(block[:])
		u := prf.Sum(nil)
		t := append([]byte(nil), u...)

		for iter := 1; iter < iterations; iter++ {
			prf.Reset()
			prf.Write(u)
			u = prf.Sum(u[:0])
			for i := range t {
				t[i] ^= u[i]
			}
		}
		dk = append(dk, t...)
	}
	return dk[:keyLength]
}
