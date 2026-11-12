package netsec

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/asn1"
	"fmt"
	"math/big"
)

// ecdsaSigValue is the ECDSA-Sig-Value ASN.1 structure (RFC 3279).
type ecdsaSigValue struct {
	R, S *big.Int
}

// verifySignature performs the cryptographic check over signedInput.
// signedInput is the re-tagged DER of signedAttrs when present, otherwise
// the raw encapsulated content octets.
func verifySignature(cert *x509.Certificate, signer *SignerInfo, digestOIDHash crypto.Hash, signedInput []byte) error {
	oid := signer.SignatureAlgorithm.Algorithm
	switch {
	case oid.Equal(oidRSAEncryption):
		rsaKey, ok := cert.PublicKey.(*rsa.PublicKey)
		if !ok {
			return fmt.Errorf("%w: certificate key is not RSA", ErrSignature)
		}
		hashed := hashBytes(digestOIDHash, signedInput)
		if err := rsa.VerifyPKCS1v15(rsaKey, digestOIDHash, hashed, signer.Signature); err != nil {
			return fmt.Errorf("%w: %v", ErrSignature, err)
		}
		return nil

	case oid.Equal(oidRSASSA_PSS):
		rsaKey, ok := cert.PublicKey.(*rsa.PublicKey)
		if !ok {
			return fmt.Errorf("%w: certificate key is not RSA", ErrSignature)
		}
		opts, err := pssOptions(signer.SignatureAlgorithm.Parameters, digestOIDHash)
		if err != nil {
			return err
		}
		hashed := hashBytes(opts.Hash, signedInput)
		if err := rsa.VerifyPSS(rsaKey, opts.Hash, hashed, signer.Signature, opts); err != nil {
			return fmt.Errorf("%w: %v", ErrSignature, err)
		}
		return nil

	case oid.Equal(oidECDSAWithSHA256), oid.Equal(oidECDSAWithSHA384), oid.Equal(oidECDSAWithSHA512):
		ecKey, ok := cert.PublicKey.(*ecdsa.PublicKey)
		if !ok {
			return fmt.Errorf("%w: certificate key is not ECDSA", ErrSignature)
		}
		var esig ecdsaSigValue
		if rest, err := asn1.Unmarshal(signer.Signature, &esig); err != nil {
			return fmt.Errorf("%w: ECDSA signature is not DER: %v", ErrSignature, err)
		} else if len(rest) > 0 {
			return fmt.Errorf("%w: trailing data in ECDSA signature", ErrSignature)
		}
		hashed := hashBytes(digestOIDHash, signedInput)
		if !ecdsa.Verify(ecKey, hashed, esig.R, esig.S) {
			return ErrSignature
		}
		return nil

	case oid.Equal(oidEd25519):
		edKey, ok := cert.PublicKey.(ed25519.PublicKey)
		if !ok {
			return fmt.Errorf("%w: certificate key is not Ed25519", ErrSignature)
		}
		if !ed25519.Verify(edKey, signedInput, signer.Signature) {
			return ErrSignature
		}
		return nil

	default:
		return fmt.Errorf("%w: signature algorithm %s", ErrUnsupportedAlgorithm, oid.String())
	}
}

// pssOptions parses id-RSASSA-PSS parameters. Parameters MUST be present in
// DER; missing parameters are rejected rather than defaulted.
func pssOptions(raw asn1.RawValue, fallback crypto.Hash) (*rsa.PSSOptions, error) {
	if len(raw.FullBytes) == 0 {
		return nil, fmt.Errorf("%w: RSASSA-PSS requires explicit parameters", ErrUnsupportedAlgorithm)
	}
	var params rsaPSSParams
	if rest, err := asn1.Unmarshal(raw.FullBytes, &params); err != nil {
		return nil, fmt.Errorf("%w: invalid PSS parameters: %v", ErrUnsupportedAlgorithm, err)
	} else if len(rest) > 0 {
		return nil, fmt.Errorf("%w: trailing data in PSS parameters", ErrUnsupportedAlgorithm)
	}

	hashFunc := fallback
	if params.HashAlg.Algorithm != nil {
		h, err := digestForOID(params.HashAlg.Algorithm)
		if err != nil {
			return nil, err
		}
		hashFunc = h
	}
	if params.MaskAlg.Algorithm != nil && !params.MaskAlg.Algorithm.Equal(oidMGF1) {
		return nil, fmt.Errorf("%w: PSS mask algorithm %s", ErrUnsupportedAlgorithm, params.MaskAlg.Algorithm.String())
	}
	if params.MaskAlg.Parameters.Algorithm != nil {
		mgfHash, err := digestForOID(params.MaskAlg.Parameters.Algorithm)
		if err != nil {
			return nil, err
		}
		if mgfHash != hashFunc {
			return nil, fmt.Errorf("%w: PSS MGF-1 hash differs from signature hash", ErrUnsupportedAlgorithm)
		}
	}
	if params.TrailerField != 0 && params.TrailerField != 1 {
		return nil, fmt.Errorf("%w: PSS trailerField %d", ErrUnsupportedAlgorithm, params.TrailerField)
	}
	saltLength := hashFunc.Size()
	if pssFieldPresent(raw.FullBytes, 2) {
		saltLength = params.SaltLength
	}
	return &rsa.PSSOptions{
		SaltLength: saltLength,
		Hash:       hashFunc,
	}, nil
}

// pssFieldPresent reports whether the given IMPLICIT context-specific
// parameter tag occurs in an RSASSA-PSS parameter block. It is needed
// because an omitted saltLength unmarshals to the Go zero value 0, which is
// a valid explicit salt length and must not be conflated with "absent".
func pssFieldPresent(der []byte, tag int) bool {
	want := byte(0x80 | tag)
	// Skip the outer SEQUENCE header and scan the inner TLVs.
	var outer asn1.RawValue
	if rest, err := asn1.Unmarshal(der, &outer); err != nil || len(rest) > 0 {
		return false
	}
	remain := outer.Bytes
	for len(remain) > 0 {
		var field asn1.RawValue
		var err error
		remain, err = asn1.Unmarshal(remain, &field)
		if err != nil {
			return false
		}
		if field.Class == asn1.ClassContextSpecific && field.Tag == tag && field.FullBytes[0] == want {
			return true
		}
	}
	return false
}

// sign is the test-facing primitive used by the in-package test helper to
// produce a signature with the given algorithm OID.
func sign(priv any, sigOID asn1.ObjectIdentifier, hashFunc crypto.Hash, input []byte) ([]byte, error) {
	switch key := priv.(type) {
	case *rsa.PrivateKey:
		hashed := hashBytes(hashFunc, input)
		if sigOID.Equal(oidRSASSA_PSS) {
			return rsa.SignPSS(rand.Reader, key, hashFunc, hashed, &rsa.PSSOptions{SaltLength: hashFunc.Size(), Hash: hashFunc})
		}
		return rsa.SignPKCS1v15(rand.Reader, key, hashFunc, hashed)
	case *ecdsa.PrivateKey:
		hashed := hashBytes(hashFunc, input)
		r, s, err := ecdsa.Sign(rand.Reader, key, hashed)
		if err != nil {
			return nil, err
		}
		return asn1.Marshal(ecdsaSigValue{R: r, S: s})
	case ed25519.PrivateKey:
		return ed25519.Sign(key, input), nil
	default:
		return nil, fmt.Errorf("%w: unsupported private key type", ErrUnsupportedAlgorithm)
	}
}
