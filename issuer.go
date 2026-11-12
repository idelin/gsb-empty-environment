package netsec

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"fmt"
	"time"
)

// Issuer supplies the certificate and private key used to sign a generation.
// The certificate is embedded in the generation and its validity is checked
// against the generation's issuance time, not against verification time.
type Issuer struct {
	Certificate *x509.Certificate
	Signer      crypto.Signer
}

func (i Issuer) validate(at time.Time) (*x509.Certificate, error) {
	if i.Certificate == nil {
		return nil, fmt.Errorf("%w: nil certificate", ErrIssuerInvalid)
	}
	if i.Signer == nil {
		return nil, fmt.Errorf("%w: nil signer", ErrIssuerInvalid)
	}
	if len(i.Certificate.Raw) == 0 {
		return nil, fmt.Errorf("%w: certificate has no DER encoding", ErrIssuerInvalid)
	}
	if err := checkCertificateTime(i.Certificate, at); err != nil {
		return nil, err
	}

	certKey, err := x509.MarshalPKIXPublicKey(i.Certificate.PublicKey)
	if err != nil {
		return nil, fmt.Errorf("%w: certificate public key: %v", ErrIssuerInvalid, err)
	}
	signerKey, err := x509.MarshalPKIXPublicKey(i.Signer.Public())
	if err != nil {
		return nil, fmt.Errorf("%w: signer public key: %v", ErrIssuerInvalid, err)
	}
	if !bytes.Equal(certKey, signerKey) {
		return nil, fmt.Errorf("%w", ErrIssuerKeyMismatch)
	}
	return i.Certificate, nil
}

func checkCertificateTime(cert *x509.Certificate, at time.Time) error {
	at = at.UTC().Round(0)
	if at.Before(cert.NotBefore.UTC().Round(0)) {
		return fmt.Errorf("%w: issued at %s, certificate valid from %s",
			ErrIssuerNotYetValid, at.Format(time.RFC3339Nano), cert.NotBefore.UTC().Format(time.RFC3339Nano))
	}
	if at.After(cert.NotAfter.UTC().Round(0)) {
		return fmt.Errorf("%w: issued at %s, certificate expired at %s",
			ErrIssuerExpired, at.Format(time.RFC3339Nano), cert.NotAfter.UTC().Format(time.RFC3339Nano))
	}
	return nil
}

func signPayload(cert *x509.Certificate, signer crypto.Signer, payload []byte) ([]byte, error) {
	switch cert.PublicKey.(type) {
	case *ecdsa.PublicKey:
		digest := sha256.Sum256(payload)
		return signer.Sign(rand.Reader, digest[:], crypto.SHA256)
	case *rsa.PublicKey:
		digest := sha256.Sum256(payload)
		return signer.Sign(rand.Reader, digest[:], crypto.SHA256)
	case ed25519.PublicKey:
		return signer.Sign(rand.Reader, payload, crypto.Hash(0))
	default:
		return nil, fmt.Errorf("%w: unsupported public key type %T", ErrIssuerSignature, cert.PublicKey)
	}
}

func verifySignature(cert *x509.Certificate, payload, signature []byte) error {
	switch pub := cert.PublicKey.(type) {
	case *ecdsa.PublicKey:
		digest := sha256.Sum256(payload)
		if !ecdsa.VerifyASN1(pub, digest[:], signature) {
			return fmt.Errorf("%w", ErrIssuerSignature)
		}
	case *rsa.PublicKey:
		digest := sha256.Sum256(payload)
		if rsa.VerifyPKCS1v15(pub, crypto.SHA256, digest[:], signature) != nil &&
			rsa.VerifyPSS(pub, crypto.SHA256, digest[:], signature, nil) != nil {
			return fmt.Errorf("%w", ErrIssuerSignature)
		}
	case ed25519.PublicKey:
		if !ed25519.Verify(pub, payload, signature) {
			return fmt.Errorf("%w", ErrIssuerSignature)
		}
	default:
		return fmt.Errorf("%w: unsupported public key type %T", ErrIssuerSignature, cert.PublicKey)
	}
	return nil
}

func verifyCertificateTrust(cert *x509.Certificate, at time.Time, opts VerifyOptions) error {
	if !opts.RequireTrustedIssuer && opts.Roots == nil {
		return nil
	}
	if opts.Roots == nil {
		return fmt.Errorf("%w: no root pool configured", ErrUntrustedIssuer)
	}
	_, err := cert.Verify(x509.VerifyOptions{
		Roots:         opts.Roots,
		Intermediates: opts.Intermediates,
		CurrentTime:   at.UTC().Round(0),
		KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageAny},
	})
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUntrustedIssuer, err)
	}
	return nil
}
