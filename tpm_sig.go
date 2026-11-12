package netsec

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/rsa"
	"math/big"
)

type parsedSignature struct {
	scheme AlgorithmID
	hash   crypto.Hash
	sig    []byte
}

// parseTPMTSignature decodes a TPMT_SIGNATURE produced by TPM2_Quote.
func parseTPMTSignature(raw []byte) (parsedSignature, error) {
	r := newByteReader(raw)
	schemeRaw, err := r.readU16()
	if err != nil {
		return parsedSignature{}, ErrMalformedInput
	}
	scheme := AlgorithmID(schemeRaw)

	out := parsedSignature{scheme: scheme}
	switch scheme {
	case AlgRSASSA, AlgRSAPSS:
		hashAlgRaw, err := r.readU16()
		if err != nil {
			return parsedSignature{}, ErrMalformedInput
		}
		spec, err := hashSpecFor(AlgorithmID(hashAlgRaw))
		if err != nil {
			return parsedSignature{}, err
		}
		sig, err := r.readSized16()
		if err != nil {
			return parsedSignature{}, ErrMalformedInput
		}
		out.hash = spec.crypto
		out.sig = sig
	case AlgECDSA:
		hashAlgRaw, err := r.readU16()
		if err != nil {
			return parsedSignature{}, ErrMalformedInput
		}
		spec, err := hashSpecFor(AlgorithmID(hashAlgRaw))
		if err != nil {
			return parsedSignature{}, err
		}
		rBytes, err := r.readSized16()
		if err != nil {
			return parsedSignature{}, ErrMalformedInput
		}
		sBytes, err := r.readSized16()
		if err != nil {
			return parsedSignature{}, ErrMalformedInput
		}
		out.hash = spec.crypto
		out.sig = append(append([]byte{}, rBytes...), sBytes...)
	default:
		return parsedSignature{}, ErrUnknownAlgorithm
	}

	if r.remaining() != 0 {
		return parsedSignature{}, ErrMalformedInput
	}
	return out, nil
}

// verifyQuoteSignature checks the Quote signature against the AK public key.
// The signed message is the full TPM2B_ATTEST blob, including its size prefix.
func verifyQuoteSignature(ak crypto.PublicKey, sig parsedSignature, attestBlob []byte) error {
	if !sig.hash.Available() {
		return ErrUnknownAlgorithm
	}
	hasher := sig.hash.New()
	hasher.Write(attestBlob)
	digest := hasher.Sum(nil)

	switch key := ak.(type) {
	case *rsa.PublicKey:
		switch sig.scheme {
		case AlgRSASSA:
			if err := rsa.VerifyPKCS1v15(key, sig.hash, digest, sig.sig); err != nil {
				return ErrSignature
			}
		case AlgRSAPSS:
			opts := &rsa.PSSOptions{SaltLength: sig.hash.Size()}
			if err := rsa.VerifyPSS(key, sig.hash, digest, sig.sig, opts); err != nil {
				return ErrSignature
			}
		default:
			return ErrSignature
		}
	case *ecdsa.PublicKey:
		if sig.scheme != AlgECDSA {
			return ErrSignature
		}
		half := len(sig.sig) / 2
		if half == 0 || len(sig.sig) != half*2 {
			return ErrSignature
		}
		r := new(big.Int).SetBytes(sig.sig[:half])
		s := new(big.Int).SetBytes(sig.sig[half:])
		if !ecdsa.Verify(key, digest, r, s) {
			return ErrSignature
		}
	default:
		return ErrSignature
	}
	return nil
}

// signQuote is a helper used by tests to produce a TPMT_SIGNATURE over the
// encoded attestation blob.
func signQuote(priv crypto.Signer, scheme AlgorithmID, hashAlg AlgorithmID, attestBlob []byte) ([]byte, error) {
	spec, err := hashSpecFor(hashAlg)
	if err != nil {
		return nil, err
	}
	hasher := spec.crypto.New()
	hasher.Write(attestBlob)
	digest := hasher.Sum(nil)

	switch key := priv.(type) {
	case *rsa.PrivateKey:
		var raw []byte
		var err error
		switch scheme {
		case AlgRSASSA:
			raw, err = rsa.SignPKCS1v15(rand.Reader, key, spec.crypto, digest)
		case AlgRSAPSS:
			raw, err = rsa.SignPSS(rand.Reader, key, spec.crypto, digest,
				&rsa.PSSOptions{SaltLength: spec.crypto.Size()})
		default:
			return nil, ErrUnknownAlgorithm
		}
		if err != nil {
			return nil, err
		}
		out := u16(scheme)
		out = append(out, u16(hashAlg)...)
		out = append(out, sized16(raw)...)
		return out, nil
	case *ecdsa.PrivateKey:
		if scheme != AlgECDSA {
			return nil, ErrUnknownAlgorithm
		}
		r, s, err := ecdsa.Sign(rand.Reader, key, digest)
		if err != nil {
			return nil, err
		}
		size := (key.Curve.Params().BitSize + 7) / 8
		rb := padLeft(r.Bytes(), size)
		sb := padLeft(s.Bytes(), size)
		out := u16(scheme)
		out = append(out, u16(hashAlg)...)
		out = append(out, sized16(rb)...)
		out = append(out, sized16(sb)...)
		return out, nil
	default:
		return nil, ErrUnknownAlgorithm
	}
}

func padLeft(b []byte, size int) []byte {
	if len(b) >= size {
		return b
	}
	out := make([]byte, size)
	copy(out[size-len(b):], b)
	return out
}
