package netsec

import (
	"crypto/sha1"
	"encoding/binary"
	"hash"
	"math/big"
)

// checksumConstant builds the RFC 3961/RFC 8009 derivation constant
// usage | 0x99. Key usage numbers are encoded big-endian.
func checksumConstant(usage uint32) []byte {
	out := make([]byte, 5)
	binary.BigEndian.PutUint32(out[:4], usage)
	out[4] = 0x99
	return out
}

func deriveHMACSHA1Key(key, constant []byte) []byte {
	return hmacBytes(sha1.New, key, constant)
}

func deriveHMACSHA1AES128(key, constant []byte) []byte {
	return truncate(deriveHMACSHA1Key(key, constant), 16)
}

func deriveHMACSHA1AES256(key, constant []byte) []byte {
	return nfold(deriveHMACSHA1Key(key, constant), 256)
}

// deriveRFC8009Key implements KDF-HMAC-SHA2 for the single-block cases used
// by checksum types 19 and 20.
func deriveRFC8009Key(newHash func() hash.Hash, key, constant []byte, outBits int) []byte {
	input := make([]byte, 0, 4+len(constant)+1+4)
	input = append(input, 0x00, 0x00, 0x00, 0x01)
	input = append(input, constant...)
	input = append(input, 0x00)
	k := make([]byte, 4)
	binary.BigEndian.PutUint32(k, uint32(outBits))
	input = append(input, k...)

	digest := hmacBytes(newHash, key, input)
	return truncate(digest, outBits/8)
}

func checksumWithDerivedKey(newHash func() hash.Hash, derivedKey, data []byte, size int) []byte {
	return truncate(hmacBytes(newHash, derivedKey, data), size)
}

// nfold implements the n-fold operation from RFC 3961 section 5.1.
func nfold(input []byte, outBits int) []byte {
	if outBits <= 0 || outBits%8 != 0 || len(input) == 0 {
		return nil
	}
	inBits := len(input) * 8
	l := lcm(outBits, inBits)
	if l > 1<<20 {
		return nil
	}
	repetitions := l / inBits
	chunks := l / outBits

	bits := make([]bool, l)
	for rep := 0; rep < repetitions; rep++ {
		shift := (13 * rep) % inBits
		for i := 0; i < inBits; i++ {
			src := i - shift
			if src < 0 {
				src += inBits
			}
			bits[rep*inBits+i] = inputBit(input, src)
		}
	}

	// 1's-complement addition of the n-bit chunks.
	mask := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), uint(outBits)), big.NewInt(1))
	sum := new(big.Int)
	for c := 0; c < chunks; c++ {
		chunk := new(big.Int)
		for i := 0; i < outBits; i++ {
			chunk.Lsh(chunk, 1)
			if bits[c*outBits+i] {
				chunk.Or(chunk, big.NewInt(1))
			}
		}
		sum = new(big.Int).Add(sum, chunk)
		for sum.BitLen() > outBits {
			high := new(big.Int).Rsh(new(big.Int).Set(sum), uint(outBits))
			low := new(big.Int).And(new(big.Int).Set(sum), mask)
			sum = new(big.Int).Add(low, high)
		}
	}

	out := make([]byte, outBits/8)
	sumBytes := sum.Bytes()
	copy(out[len(out)-len(sumBytes):], sumBytes)
	return out
}

func inputBit(input []byte, bit int) bool {
	return input[bit/8]&(0x80>>uint(bit%8)) != 0
}

func lcm(a, b int) int {
	g := gcd(a, b)
	return a / g * b
}

func gcd(a, b int) int {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}
