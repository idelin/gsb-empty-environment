package remoteattest

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/x509"
	"fmt"
	"math/big"
)

// Signature is the decoded fixed ECDSA-P256 TPMT_SIGNATURE body.
type Signature struct {
	SigAlg  uint16
	HashAlg uint16
	R       *big.Int
	S       *big.Int
}

// DecodeECDSASignature parses the TPMT_SIGNATURE body of an ECDSA-P256 AK
// signature: sigAlg(2) hashAlg(2) r(32) s(32).
func DecodeECDSASignature(raw []byte) (*Signature, error) {
	r := newByteReader(raw)
	sigAlg, err := r.u16()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMalformedSignature, err)
	}
	if sigAlg != TPMAlgECDSA {
		return nil, fmt.Errorf("%w: sigAlg 0x%04X is not ECDSA", ErrMalformedSignature, sigAlg)
	}
	hashAlg, err := r.u16()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMalformedSignature, err)
	}
	if hashAlg != TPMAlgSHA256 {
		return nil, fmt.Errorf("%w: hashAlg 0x%04X is not SHA-256", ErrMalformedSignature, hashAlg)
	}
	rb, err := r.bytes(p256CoordinateSize)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMalformedSignature, err)
	}
	sb, err := r.bytes(p256CoordinateSize)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMalformedSignature, err)
	}
	if r.remaining() != 0 {
		return nil, fmt.Errorf("%w: %d trailing bytes", ErrMalformedSignature, r.remaining())
	}
	return &Signature{
		SigAlg:  sigAlg,
		HashAlg: hashAlg,
		R:       new(big.Int).SetBytes(rb),
		S:       new(big.Int).SetBytes(sb),
	}, nil
}

// VerifyAKChain validates that the AK leaf certificate chains to one of the
// configured trusted roots and carries an ECDSA-P256 public key. The
// returned key is the AK used for signature verification.
func VerifyAKChain(akCert *x509.Certificate, intermediates []*x509.Certificate, roots *x509.CertPool) (*ecdsa.PublicKey, error) {
	if akCert == nil {
		return nil, fmt.Errorf("%w: missing AK certificate", ErrAKNotTrusted)
	}
	if roots == nil {
		return nil, fmt.Errorf("%w: no trusted root pool configured", ErrAKNotTrusted)
	}
	pub, ok := akCert.PublicKey.(*ecdsa.PublicKey)
	if !ok || pub.Curve != elliptic.P256() {
		return nil, fmt.Errorf("%w: AK public key is not ECDSA-P256", ErrAKNotTrusted)
	}

	chainPool := x509.NewCertPool()
	for _, c := range intermediates {
		chainPool.AddCert(c)
	}
	opts := x509.VerifyOptions{
		Roots:         roots,
		Intermediates: chainPool,
		KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageAny},
	}
	if _, err := akCert.Verify(opts); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrAKNotTrusted, err)
	}
	return pub, nil
}

// akQualifiedName builds the TPM2B content that binds a quote to a specific
// AK: the SEC1 compressed encoding of the AK P-256 point.
func akQualifiedName(pub *ecdsa.PublicKey) []byte {
	return elliptic.MarshalCompressed(pub.Curve, pub.X, pub.Y)
}

// VerifyQuote performs the complete quote verification:
//   - decode the TPMS_ATTEST blob and validate magic/type,
//   - require extraData to equal expectedNonce exactly (constant time),
//   - verify the AK certificate against the trusted roots,
//   - require qualifiedSigner to bind the verified AK public key,
//   - verify the ECDSA-P256 signature over SHA-256 of the raw blob.
//
// It returns the decoded attestation on success.
func VerifyQuote(attestBlob, sigBlob []byte, expectedNonce []byte, akCert *x509.Certificate, intermediates []*x509.Certificate, roots *x509.CertPool) (*Attestation, error) {
	att, err := DecodeAttestation(attestBlob)
	if err != nil {
		return nil, err
	}

	if len(att.ExtraData) != len(expectedNonce) ||
		subtle.ConstantTimeCompare(att.ExtraData, expectedNonce) != 1 {
		return nil, fmt.Errorf("%w: nonce length %d, expected %d",
			ErrNonceMismatch, len(att.ExtraData), len(expectedNonce))
	}

	akPub, err := VerifyAKChain(akCert, intermediates, roots)
	if err != nil {
		return nil, err
	}

	qualified := akQualifiedName(akPub)
	if len(att.QualifiedSigner) != compressedPointSize ||
		subtle.ConstantTimeCompare(att.QualifiedSigner, qualified) != 1 {
		return nil, fmt.Errorf("%w: qualifiedSigner is not the compressed AK point", ErrAKBinding)
	}

	sig, err := DecodeECDSASignature(sigBlob)
	if err != nil {
		return nil, err
	}

	digest := sha256.Sum256(attestBlob)
	if !ecdsa.Verify(akPub, digest[:], sig.R, sig.S) {
		return nil, ErrBadSignature
	}
	return att, nil
}
