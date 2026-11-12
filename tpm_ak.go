package netsec

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/rsa"
	"math/big"
)

// parseAKPublic decodes a TPM2B_PUBLIC holding an RSA or ECC attestation key.
func parseAKPublic(raw []byte) (crypto.PublicKey, error) {
	outer := newByteReader(raw)
	body, err := outer.readSized16()
	if err != nil {
		return nil, ErrMalformedInput
	}
	if outer.remaining() != 0 {
		return nil, ErrMalformedInput
	}

	r := newByteReader(body)
	typeAlgRaw, err := r.readU16()
	if err != nil {
		return nil, ErrMalformedInput
	}
	typeAlg := AlgorithmID(typeAlgRaw)
	if _, err := r.readU16(); err != nil { // nameAlg
		return nil, ErrMalformedInput
	}
	attributes, err := r.readU32()
	if err != nil {
		return nil, ErrMalformedInput
	}
	if attributes&objectAttrSign == 0 {
		return nil, ErrMalformedInput
	}
	if _, err := r.readSized16(); err != nil { // authPolicy
		return nil, ErrMalformedInput
	}

	// TPMT_SYM_DEF_OBJECT: algorithm, plus keyBits/mode when non-null.
	symAlg, err := r.readU16()
	if err != nil {
		return nil, ErrMalformedInput
	}
	if AlgorithmID(symAlg) != AlgNull {
		if _, err := r.readU16(); err != nil { // keyBits
			return nil, ErrMalformedInput
		}
		if _, err := r.readU16(); err != nil { // mode
			return nil, ErrMalformedInput
		}
	}

	var pub crypto.PublicKey
	switch typeAlg {
	case AlgRSA:
		keyBits, err := r.readU16()
		if err != nil {
			return nil, ErrMalformedInput
		}
		exponent, err := r.readU32()
		if err != nil {
			return nil, ErrMalformedInput
		}
		modulus, err := r.readSized16()
		if err != nil {
			return nil, ErrMalformedInput
		}
		if len(modulus) == 0 || int(keyBits) != len(modulus)*8 {
			return nil, ErrMalformedInput
		}
		if exponent == 0 {
			exponent = 65537
		}
		pub = &rsa.PublicKey{
			N: new(big.Int).SetBytes(modulus),
			E: int(exponent),
		}
	case AlgECC:
		curveID, err := r.readU16()
		if err != nil {
			return nil, ErrMalformedInput
		}
		curve, ok := eccCurves[AlgorithmID(curveID)]
		if !ok {
			return nil, ErrUnknownAlgorithm
		}
		// TPMT_KDF_SCHEME: scheme, plus a hash algorithm when non-null.
		kdfAlg, err := r.readU16()
		if err != nil {
			return nil, ErrMalformedInput
		}
		if AlgorithmID(kdfAlg) != AlgNull {
			if _, err := r.readU16(); err != nil {
				return nil, ErrMalformedInput
			}
		}
		xBytes, err := r.readSized16()
		if err != nil {
			return nil, ErrMalformedInput
		}
		yBytes, err := r.readSized16()
		if err != nil {
			return nil, ErrMalformedInput
		}
		x := new(big.Int).SetBytes(xBytes)
		y := new(big.Int).SetBytes(yBytes)
		if !curve.IsOnCurve(x, y) {
			return nil, ErrMalformedInput
		}
		pub = &ecdsa.PublicKey{Curve: curve, X: x, Y: y}
	default:
		return nil, ErrUnknownAlgorithm
	}

	if r.remaining() != 0 {
		return nil, ErrMalformedInput
	}
	return pub, nil
}
