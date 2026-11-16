package netsec

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"errors"
)

// Key derivation FC values for NAS keys (TS 33.501 Annex A.9, TS 33.220
// Annex B).
const (
	fcKnasInt byte = 0x69
	fcKnasEnc byte = 0x70
)

// NAS type distinguisher octet (TS 33.220 Annex B.2.2 / TS 33.501 A.9).
const nasTypeDistinguisher byte = 0x01

// genericKDF implements the 5G generic key derivation function
// (TS 33.220 Annex B.2):
//
//	K_out = HMAC-SHA-256(Key, FC || P1 || L1 || P2 || L2 || ... )
//
// where every Li is a 16-bit big-endian length of Pi in octets.
func genericKDF(key []byte, fc byte, params ...[]byte) ([]byte, error) {
	if len(key) != 32 {
		return nil, errors.New("netsec: 5G generic KDF expects a 256-bit key")
	}
	h := hmac.New(sha256.New, key)
	h.Write([]byte{fc})
	for _, p := range params {
		var lenBuf [2]byte
		binary.BigEndian.PutUint16(lenBuf[:], uint16(len(p)))
		h.Write(lenBuf[:])
		h.Write(p)
	}
	return h.Sum(nil), nil
}

// DeriveNASIntegrityKey derives K_nasint from K_AMF for the selected NAS
// integrity algorithm (TS 33.501 Annex A.9.2). The returned key is 256 bits;
// NIA1/NIA2/NIA3 use its leading 128 bits.
func DeriveNASIntegrityKey(kamf []byte, integrityAlgorithm byte) ([]byte, error) {
	if len(kamf) != 32 {
		return nil, errors.New("netsec: K_AMF must be 32 octets (256 bits)")
	}
	return genericKDF(kamf, fcKnasInt,
		[]byte{nasTypeDistinguisher, 0x00},     // P1: NAS type = "access node"
		[]byte{0x01, 0x00, integrityAlgorithm}, // P2: algorithm identity distinguisher
	)
}

// DeriveNASEncryptionKey derives K_nasenc from K_AMF for the selected NAS
// ciphering algorithm (TS 33.501 Annex A.9.1).
func DeriveNASEncryptionKey(kamf []byte, cipheringAlgorithm byte) ([]byte, error) {
	if len(kamf) != 32 {
		return nil, errors.New("netsec: K_AMF must be 32 octets (256 bits)")
	}
	return genericKDF(kamf, fcKnasEnc,
		[]byte{nasTypeDistinguisher, 0x00},     // P1: NAS type = "access node"
		[]byte{0x02, 0x00, cipheringAlgorithm}, // P2: algorithm identity distinguisher
	)
}
