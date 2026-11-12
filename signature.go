package netsec

import (
	"crypto"
	"crypto/dsa"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	_ "crypto/sha256"
	_ "crypto/sha512"
	"crypto/x509"
	"fmt"
	"math/big"
)

var signatureHashes = []crypto.Hash{crypto.SHA256, crypto.SHA384, crypto.SHA512}

// VerifySignature verifies sig over data using cert's public key.
//
// hash may be zero to let this function try the common IKEv2 hashes
// (SHA-256, SHA-384, SHA-512), or may pin one hash. RSA PKCS#1 v1.5 and
// RSA-PSS, ECDSA (ASN.1 DER), DSA, and Ed25519 keys are supported.
func VerifySignature(cert *x509.Certificate, data, sig []byte, hash crypto.Hash) error {
	if cert == nil {
		return ErrCertificateRequired
	}
	if len(sig) == 0 {
		return ErrEmptyAuth
	}
	if hash != 0 && !hash.Available() {
		return fmt.Errorf("%w: hash %s unavailable", ErrUnsupportedSignature, hash)
	}

	hashes := signatureHashes
	if hash != 0 {
		hashes = []crypto.Hash{hash}
	}

	switch pub := cert.PublicKey.(type) {
	case *rsa.PublicKey:
		for _, h := range hashes {
			digest := digestFor(h, data)
			if digest == nil {
				continue
			}
			if rsa.VerifyPKCS1v15(pub, h, digest, sig) == nil {
				return nil
			}
			if rsa.VerifyPSS(pub, h, digest, sig, nil) == nil {
				return nil
			}
		}
		return fmt.Errorf("%w: RSA signature verification failed", ErrUnsupportedSignature)

	case *ecdsa.PublicKey:
		for _, h := range hashes {
			digest := digestFor(h, data)
			if digest == nil {
				continue
			}
			if ecdsa.VerifyASN1(pub, digest, sig) {
				return nil
			}
			// Some IKEv2 deployments emit r || s instead of the ASN.1
			// ECDSA-Sig-Value. Accept it only when the halves are unambiguous.
			if len(sig)%2 == 0 {
				n := len(sig) / 2
				r := new(big.Int).SetBytes(sig[:n])
				s := new(big.Int).SetBytes(sig[n:])
				if ecdsa.Verify(pub, digest, r, s) {
					return nil
				}
			}
		}
		return fmt.Errorf("%w: ECDSA signature verification failed", ErrUnsupportedSignature)

	case ed25519.PublicKey:
		if ed25519.Verify(pub, data, sig) {
			return nil
		}
		return fmt.Errorf("%w: Ed25519 signature verification failed", ErrUnsupportedSignature)

	case *dsa.PublicKey:
		if len(sig)%2 != 0 {
			return fmt.Errorf("%w: malformed DSA signature", ErrUnsupportedSignature)
		}
		n := len(sig) / 2
		for _, h := range hashes {
			digest := digestFor(h, data)
			if digest == nil {
				continue
			}
			r := new(big.Int).SetBytes(sig[:n])
			s := new(big.Int).SetBytes(sig[n:])
			if dsa.Verify(pub, digest, r, s) {
				return nil
			}
		}
		return fmt.Errorf("%w: DSA signature verification failed", ErrUnsupportedSignature)

	default:
		return fmt.Errorf("%w: public key type %T", ErrUnsupportedSignature, cert.PublicKey)
	}
}

func digestFor(h crypto.Hash, data []byte) []byte {
	if !h.Available() {
		return nil
	}
	hasher := h.New()
	_, _ = hasher.Write(data)
	return hasher.Sum(nil)
}
