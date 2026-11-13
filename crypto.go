package netsec

import (
	"crypto/aes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
)

const (
	nasKDFFunctionCode           byte = 0x69
	nasKDFAlgorithmTypeIntegrity byte = 0x01
)

// DeriveKNASint derives the 128-bit NAS integrity key from KAMF and the
// selected 5G integrity algorithm, as specified by the 5G key derivation
// function (TS 33.501, annex A).
//
// The KDF string is FC || P0 || L0 || P1 || L1 with FC = 0x69, P0 = 0x01 for
// NAS integrity, P1 = the algorithm identity. The 128 least significant bits
// of the HMAC-SHA-256 output are used.
func DeriveKNASint(kamf []byte, integrityAlgorithm byte) ([]byte, error) {
	if len(kamf) != 32 {
		return nil, fmt.Errorf("KAMF must be 32 octets, got %d", len(kamf))
	}
	if integrityAlgorithm > 0x0f {
		return nil, fmt.Errorf("invalid 5G integrity algorithm identity %d", integrityAlgorithm)
	}

	input := []byte{
		nasKDFFunctionCode,
		nasKDFAlgorithmTypeIntegrity, 0x00, 0x01,
		integrityAlgorithm, 0x00, 0x01,
	}
	mac := hmac.New(sha256.New, kamf)
	_, _ = mac.Write(input)
	output := mac.Sum(nil)

	// KAMF and the KDF output are 256 bits; NAS keys use the least significant
	// 128 bits.
	return append([]byte(nil), output[16:]...), nil
}

// DeriveNASIntegrityKey is an explicit alias for DeriveKNASint.
func DeriveNASIntegrityKey(kamf []byte, integrityAlgorithm byte) ([]byte, error) {
	return DeriveKNASint(kamf, integrityAlgorithm)
}

// ComputeNASMAC computes the 32-bit NAS MAC with the selected NIA algorithm.
//
// message must contain the NAS sequence number followed by the complete plain
// NAS message, i.e. the protected message octets after the four-octet MAC.
// For a downlink Security Mode Command, direction is 1 (downlink) and bearer
// is the NAS connection identifier (0 for 3GPP access).
//
// 5G-IA0 returns an all-zero MAC. 5G-IA2 (AES-CMAC) is implemented with the
// standard library. 5G-IA1 and 5G-IA3 require external algorithm support and
// return ErrUnsupportedIntegrityAlgorithm.
func ComputeNASMAC(algorithm byte, key []byte, count uint32, bearer, direction byte, message []byte) ([]byte, error) {
	if bearer > 0x1f {
		return nil, fmt.Errorf("bearer identity %d does not fit in 5 bits", bearer)
	}
	if direction > 1 {
		return nil, fmt.Errorf("direction must be 0 or 1, got %d", direction)
	}

	if algorithm == IntegrityAlgorithm5GIA0 {
		return []byte{0, 0, 0, 0}, nil
	}
	if algorithm != IntegrityAlgorithm5GIA2 {
		return nil, fmt.Errorf("%w: %d", ErrUnsupportedIntegrityAlgorithm, algorithm)
	}
	if len(key) != 16 {
		return nil, fmt.Errorf("NIA2 key must be 16 octets, got %d", len(key))
	}

	input := make([]byte, 0, 5+len(message))
	var countBytes [4]byte
	binary.BigEndian.PutUint32(countBytes[:], count)
	input = append(input, countBytes[:]...)
	input = append(input, bearer<<1|direction)
	input = append(input, message...)

	cmac, err := aesCMAC(key, input)
	if err != nil {
		return nil, err
	}
	// The NAS MAC is the most significant 32 bits of the 128-bit algorithm
	// output.
	return append([]byte(nil), cmac[:4]...), nil
}

// aesCMAC implements AES-128-CMAC as specified by RFC 4493.
func aesCMAC(key, message []byte) ([16]byte, error) {
	var result [16]byte
	if len(key) != aes.BlockSize {
		return result, fmt.Errorf("AES-CMAC key must be 16 octets, got %d", len(key))
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return result, err
	}

	var zero [16]byte
	var k1 [16]byte
	block.Encrypt(k1[:], zero[:])
	k1 = cmacDouble(k1)
	k2 := cmacDouble(k1)

	blockCount := (len(message) + aes.BlockSize - 1) / aes.BlockSize
	if blockCount == 0 {
		blockCount = 1
	}

	var chain [16]byte
	for i := 1; i < blockCount; i++ {
		offset := (i - 1) * aes.BlockSize
		for j := 0; j < aes.BlockSize; j++ {
			chain[j] ^= message[offset+j]
		}
		block.Encrypt(chain[:], chain[:])
	}

	var last [16]byte
	offset := (blockCount - 1) * aes.BlockSize
	remaining := len(message) - offset
	switch {
	case blockCount == 1 && len(message) == 0:
		last[0] = 0x80
		for j := range last {
			last[j] ^= k2[j]
		}
	case remaining == aes.BlockSize:
		copy(last[:], message[offset:offset+aes.BlockSize])
		for j := range last {
			last[j] ^= k1[j]
		}
	default:
		copy(last[:], message[offset:])
		last[remaining] = 0x80
		for j := range last {
			last[j] ^= k2[j]
		}
	}

	for j := 0; j < aes.BlockSize; j++ {
		chain[j] ^= last[j]
	}
	block.Encrypt(result[:], chain[:])
	return result, nil
}

// cmacDouble multiplies an AES-CMAC subkey by x in GF(2^128), using the
// constant polynomial from RFC 4493.
func cmacDouble(in [16]byte) (out [16]byte) {
	carry := in[0] >> 7
	for i := 0; i < 15; i++ {
		out[i] = in[i]<<1 | in[i+1]>>7
	}
	out[15] = in[15] << 1
	if carry != 0 {
		out[15] ^= 0x87
	}
	return out
}
