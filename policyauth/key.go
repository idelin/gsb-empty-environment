package policyauth

import (
	"crypto/ecdsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/binary"
	"errors"
	"fmt"
)

// tpmAlgSHA256 is the TPM2 algorithm identifier for SHA-256 (TPM_ALG_SHA256).
const tpmAlgSHA256 uint16 = 0x000B

// PublicKeyName derives the TPM-style name of an ECDSA public key.
//
// On a real TPM the name is the nameAlg digest of the marshaled TPMT_PUBLIC
// area. This software stand-in digests the SHA-256 algorithm tag together
// with the DER-encoded SubjectPublicKeyInfo, which gives every key a fixed,
// collision-resistant name that the object policy can pin against.
func PublicKeyName(pub *ecdsa.PublicKey) (Name, error) {
	if pub == nil {
		return Name{}, errors.New("policyauth: nil public key")
	}
	derBytes, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return Name{}, fmt.Errorf("policyauth: marshal public key: %w", err)
	}
	var algorithmTag [2]byte
	binary.BigEndian.PutUint16(algorithmTag[:], tpmAlgSHA256)

	h := sha256.New()
	h.Write(algorithmTag[:])
	h.Write(derBytes)
	var name Name
	copy(name[:], h.Sum(nil))
	return name, nil
}
