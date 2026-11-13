package netsec

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
)

// infoLabel is the fixed domain-separation label prepended to the KDF info.
// It is scoped to this hybrid construction so keys derived here cannot be
// confused with outputs of other HMAC uses.
const infoLabel = "netsec-hybrid-kem-v1"

// zeroKey is the all-zero 32-byte HMAC key used for the first extraction.
var zeroKey [32]byte

// extractPRK computes the pseudorandom key:
//
//	PRK = HMAC-SHA256(0x00*32, ss1 || ss2)
//
// Both component shared secrets are always concatenated in a fixed order,
// including implicit-rejection secrets, so no component is ever dropped.
func extractPRK(ss1, ss2 [32]byte) [32]byte {
	mac := hmac.New(sha256.New, zeroKey[:])
	mac.Write(ss1[:])
	mac.Write(ss2[:])
	var prk [32]byte
	copy(prk[:], mac.Sum(nil))
	return prk
}

// buildInfo assembles the bound KDF info string:
//
//	label || suiteID ||
//	uint16(len(ct1)) || ct1 ||
//	uint16(len(ct2)) || ct2 ||
//	clientPubHash || serverPubHash || contextHash
//
// The length prefixes make every field unambiguous and bind the exact
// ciphertext bytes, preventing component substitution or treating a failed
// component as an empty string.
func buildInfo(req Request) []byte {
	out := make([]byte, 0, len(infoLabel)+suiteIDSize+
		lengthSize+len(req.Ciphertext1)+
		lengthSize+len(req.Ciphertext2)+
		3*digestSize)

	out = append(out, infoLabel...)
	out = append(out, req.SuiteID[:]...)

	out = binary.BigEndian.AppendUint16(out, uint16(len(req.Ciphertext1)))
	out = append(out, req.Ciphertext1...)

	out = binary.BigEndian.AppendUint16(out, uint16(len(req.Ciphertext2)))
	out = append(out, req.Ciphertext2...)

	out = append(out, req.ClientPublicKeyHash[:]...)
	out = append(out, req.ServerPublicKeyHash[:]...)
	out = append(out, req.HandshakeContextHash[:]...)
	return out
}

// deriveKey computes the final key:
//
//	key = HMAC-SHA256(PRK, info)
func deriveKey(prk [32]byte, req Request) [32]byte {
	mac := hmac.New(sha256.New, prk[:])
	mac.Write(buildInfo(req))
	var key [32]byte
	copy(key[:], mac.Sum(nil))
	return key
}
