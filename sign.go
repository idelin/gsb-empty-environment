package netsec

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"encoding/asn1"
	"errors"
)

// Sign produces an RRSIG Signature octet string for the given RRset, signing
// exactly the same canonical byte sequence that Verify reconstructs. It exists
// so callers (and this package's tests) can create conforming signatures
// offline; validators normally only consume signatures.
//
// sigFields supplies every RRSIG field except Signature. Its Labels value must
// already reflect the signing owner name (the wildcard name for an expansion).
func Sign(priv crypto.PrivateKey, rrset SignedRRset, signedName Name) ([]byte, error) {
	alg := rrset.Signature.Algorithm
	signed, err := buildSignedData(rrset, signedName)
	if err != nil {
		return nil, err
	}

	switch alg {
	case AlgRSASHA256, AlgRSASHA512:
		rsaKey, ok := priv.(*rsa.PrivateKey)
		if !ok {
			return nil, errors.New("netsec: RSA signing requires *rsa.PrivateKey")
		}
		hashID, newHash, err := hashForAlgorithm(alg)
		if err != nil {
			return nil, err
		}
		h := newHash()
		h.Write(signed)
		digest := h.Sum(nil)
		return rsa.SignPKCS1v15(rand.Reader, rsaKey, hashID, digest)

	case AlgECDSAP256, AlgECDSAP384:
		ecKey, ok := priv.(*ecdsa.PrivateKey)
		if !ok {
			return nil, errors.New("netsec: ECDSA signing requires *ecdsa.PrivateKey")
		}
		_, newHash, err := hashForAlgorithm(alg)
		if err != nil {
			return nil, err
		}
		h := newHash()
		h.Write(signed)
		r, s, err := ecdsa.Sign(rand.Reader, ecKey, h.Sum(nil))
		if err != nil {
			return nil, err
		}
		return asn1.Marshal(ecdsaSignature{R: r, S: s})

	case AlgEd25519:
		edKey, ok := priv.(ed25519.PrivateKey)
		if !ok {
			return nil, errors.New("netsec: Ed25519 signing requires ed25519.PrivateKey")
		}
		return ed25519.Sign(edKey, signed), nil

	default:
		return nil, ErrUnsupportedAlgorithm
	}
}
