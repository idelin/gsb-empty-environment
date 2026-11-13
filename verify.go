package netsec

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
)

var (
	// ErrSignatureSchemeMismatch is returned when a signature scheme cannot be
	// used with the algorithm or parameters of the supplied public key. No
	// signature verification is attempted when this error is returned.
	ErrSignatureSchemeMismatch = errors.New("netsec: signature scheme mismatch")

	// ErrUnsupportedSignatureScheme is returned for an unknown or unavailable
	// signature scheme.
	ErrUnsupportedSignatureScheme = errors.New("netsec: unsupported signature scheme")

	// ErrUnsupportedPublicKey is returned when the supplied value does not
	// contain a supported public key.
	ErrUnsupportedPublicKey = errors.New("netsec: unsupported public key")

	// ErrInvalidPublicKey is returned for a malformed public key, including a
	// certificate whose declared key algorithm contradicts its key material.
	ErrInvalidPublicKey = errors.New("netsec: invalid public key")

	// ErrInvalidSignature is returned when a compatible scheme has been
	// selected but the signature does not verify.
	ErrInvalidSignature = errors.New("netsec: invalid signature")

	// ErrNoPublicKey is returned when no leaf public key is available.
	ErrNoPublicKey = errors.New("netsec: no public key")

	// ErrInvalidCertificate is returned when a certificate cannot be parsed.
	ErrInvalidCertificate = errors.New("netsec: invalid certificate")
)

// Common aliases for ErrSignatureSchemeMismatch.
var (
	ErrSchemeMismatch              = ErrSignatureSchemeMismatch
	ErrIncompatibleSignatureScheme = ErrSignatureSchemeMismatch
)

// CheckSignatureScheme reports whether scheme is compatible with the public
// key contained in pub.
//
// pub may be a *x509.Certificate (the end-entity certificate is used), a
// tls.Certificate, a DER or PEM certificate, a crypto.PublicKey, or a
// crypto.Signer. A nil error means the scheme and key algorithm/parameters
// are compatible; it does not by itself authenticate any signature.
func CheckSignatureScheme(pub any, scheme any) error {
	key, id, err := resolveKeyAndScheme(pub, scheme)
	if err != nil {
		return err
	}
	return checkSignatureScheme(key, id)
}

// SignatureSchemeMatches reports whether scheme is compatible with the
// algorithm and parameters of the supplied public key.
func SignatureSchemeMatches(pub any, scheme any) bool {
	return CheckSignatureScheme(pub, scheme) == nil
}

// SupportsSignatureScheme is an alias for SignatureSchemeMatches.
func SupportsSignatureScheme(pub any, scheme any) bool {
	return SignatureSchemeMatches(pub, scheme)
}

// VerifySignature verifies signature over message using scheme and the
// end-entity public key contained in pub.
//
// The scheme is checked against the public key's algorithm and parameters
// first. If they are incompatible, ErrSignatureSchemeMismatch is returned and
// the cryptographic verifier is never invoked.
func VerifySignature(pub any, scheme any, message, signature []byte) error {
	key, id, err := resolveKeyAndScheme(pub, scheme)
	if err != nil {
		return err
	}
	return verifySignature(key, id, message, signature)
}

// VerifyCertificateSignature verifies a signature made by the holder of the
// end-entity certificate cert. It is an alias for VerifySignature.
func VerifyCertificateSignature(cert any, scheme any, message, signature []byte) error {
	return VerifySignature(cert, scheme, message, signature)
}

// VerifyCertificateVerify verifies a TLS CertificateVerify signature. The
// caller supplies the already-constructed signed context as message.
func VerifyCertificateVerify(cert any, scheme any, message, signature []byte) error {
	return VerifySignature(cert, scheme, message, signature)
}

// VerifyLeafSignature is an alias for VerifySignature.
func VerifyLeafSignature(cert any, scheme any, message, signature []byte) error {
	return VerifySignature(cert, scheme, message, signature)
}

// VerifyWithSignatureScheme is an alias for VerifySignature.
func VerifyWithSignatureScheme(pub any, scheme any, message, signature []byte) error {
	return VerifySignature(pub, scheme, message, signature)
}

// VerifySignedMessage is an alias for VerifySignature.
func VerifySignedMessage(pub any, scheme any, message, signature []byte) error {
	return VerifySignature(pub, scheme, message, signature)
}

// VerifyMessage is an alias for VerifySignature.
func VerifyMessage(pub any, scheme any, message, signature []byte) error {
	return VerifySignature(pub, scheme, message, signature)
}

// Verify is an alias for VerifySignature.
func Verify(pub any, scheme any, message, signature []byte) error {
	return VerifySignature(pub, scheme, message, signature)
}

// ValidateSignatureScheme is an alias for CheckSignatureScheme.
func ValidateSignatureScheme(pub any, scheme any) error {
	return CheckSignatureScheme(pub, scheme)
}

// CheckCertificateSignatureScheme is an alias for CheckSignatureScheme.
func CheckCertificateSignatureScheme(cert any, scheme any) error {
	return CheckSignatureScheme(cert, scheme)
}

// CheckSignatureSchemeCompatibility is an alias for CheckSignatureScheme.
func CheckSignatureSchemeCompatibility(pub any, scheme any) error {
	return CheckSignatureScheme(pub, scheme)
}

// IsSignatureSchemeCompatible is an alias for SignatureSchemeMatches.
func IsSignatureSchemeCompatible(pub any, scheme any) bool {
	return SignatureSchemeMatches(pub, scheme)
}

// SignatureSchemeCompatible is an alias for SignatureSchemeMatches.
func SignatureSchemeCompatible(pub any, scheme any) bool {
	return SignatureSchemeMatches(pub, scheme)
}

// SchemeMatchesKey is an alias for SignatureSchemeMatches with the scheme
// argument first.
func SchemeMatchesKey(scheme any, pub any) bool {
	return SignatureSchemeMatches(pub, scheme)
}

// PublicKeyMatchesSignatureScheme is an alias for SignatureSchemeMatches.
func PublicKeyMatchesSignatureScheme(pub any, scheme any) bool {
	return SignatureSchemeMatches(pub, scheme)
}

// SignatureSchemeMatchesKey is an alias for SignatureSchemeMatches.
func SignatureSchemeMatchesKey(pub any, scheme any) bool {
	return SignatureSchemeMatches(pub, scheme)
}

// KeySupportsSignatureScheme is an alias for SignatureSchemeMatches.
func KeySupportsSignatureScheme(pub any, scheme any) bool {
	return SignatureSchemeMatches(pub, scheme)
}

// ValidateCertificateSignature is an alias for VerifySignature.
func ValidateCertificateSignature(cert any, scheme any, message, signature []byte) error {
	return VerifySignature(cert, scheme, message, signature)
}

// VerifyCertificate is an alias for VerifySignature.
func VerifyCertificate(cert any, scheme any, message, signature []byte) error {
	return VerifySignature(cert, scheme, message, signature)
}

// CheckEndEntitySignatureScheme is an alias for CheckSignatureScheme.
func CheckEndEntitySignatureScheme(cert any, scheme any) error {
	return CheckSignatureScheme(cert, scheme)
}

// VerifySignatureScheme checks scheme/key compatibility. If message and
// signature are also supplied, it verifies the signature after the
// compatibility check.
func VerifySignatureScheme(pub any, scheme any, messageAndSignature ...[]byte) error {
	switch len(messageAndSignature) {
	case 0:
		return CheckSignatureScheme(pub, scheme)
	case 2:
		return VerifySignature(pub, scheme, messageAndSignature[0], messageAndSignature[1])
	default:
		return fmt.Errorf("netsec: VerifySignatureScheme expects zero or two byte slices, got %d", len(messageAndSignature))
	}
}

func resolveKeyAndScheme(pub any, scheme any) (crypto.PublicKey, SignatureScheme, error) {
	key, keyErr := publicKeyOf(pub)
	id, schemeErr := parseSignatureScheme(scheme)
	if keyErr == nil && schemeErr == nil {
		return key, id, nil
	}

	// Also accept the (scheme, key) argument order used by some callers.
	swappedKey, swappedKeyErr := publicKeyOf(scheme)
	swappedScheme, swappedSchemeErr := parseSignatureScheme(pub)
	if swappedKeyErr == nil && swappedSchemeErr == nil {
		return swappedKey, swappedScheme, nil
	}

	if keyErr != nil && swappedSchemeErr != nil {
		return nil, 0, keyErr
	}
	if schemeErr != nil {
		return nil, 0, schemeErr
	}
	return nil, 0, keyErr
}

func publicKeyOf(v any) (crypto.PublicKey, error) {
	switch k := v.(type) {
	case nil:
		return nil, ErrNoPublicKey
	case *x509.Certificate:
		return certificatePublicKey(k)
	case x509.Certificate:
		return certificatePublicKey(&k)
	case *tls.Certificate:
		if k == nil {
			return nil, ErrNoPublicKey
		}
		if k.Leaf != nil {
			return certificatePublicKey(k.Leaf)
		}
		if len(k.Certificate) == 0 {
			return nil, ErrNoPublicKey
		}
		cert, err := x509.ParseCertificate(k.Certificate[0])
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidCertificate, err)
		}
		return certificatePublicKey(cert)
	case tls.Certificate:
		return publicKeyOf(&k)
	case []*x509.Certificate:
		if len(k) == 0 {
			return nil, ErrNoPublicKey
		}
		return certificatePublicKey(k[0])
	case []x509.Certificate:
		if len(k) == 0 {
			return nil, ErrNoPublicKey
		}
		return certificatePublicKey(&k[0])
	case [][]byte:
		if len(k) == 0 {
			return nil, ErrNoPublicKey
		}
		return publicKeyFromBytes(k[0])
	case *rsa.PublicKey:
		if k == nil {
			return nil, ErrInvalidPublicKey
		}
		return k, nil
	case rsa.PublicKey:
		return &k, nil
	case *ecdsa.PublicKey:
		if k == nil {
			return nil, ErrInvalidPublicKey
		}
		return k, nil
	case ecdsa.PublicKey:
		return &k, nil
	case ed25519.PublicKey:
		return k, nil
	case *ed25519.PublicKey:
		if k == nil {
			return nil, ErrInvalidPublicKey
		}
		return ed25519.PublicKey(*k), nil
	case []byte:
		return publicKeyFromBytes(k)
	case *pem.Block:
		if k == nil {
			return nil, ErrInvalidCertificate
		}
		return publicKeyFromBytes(pem.EncodeToMemory(k))
	case crypto.Signer:
		pub := k.Public()
		if pub == nil {
			return nil, ErrNoPublicKey
		}
		return publicKeyOf(pub)
	case interface{ Public() crypto.PublicKey }:
		pub := k.Public()
		if pub == nil {
			return nil, ErrNoPublicKey
		}
		return publicKeyOf(pub)
	default:
		return nil, fmt.Errorf("%w: %T", ErrUnsupportedPublicKey, v)
	}
}

func certificatePublicKey(cert *x509.Certificate) (crypto.PublicKey, error) {
	if cert == nil {
		return nil, ErrNoPublicKey
	}
	if cert.PublicKey == nil {
		if len(cert.Raw) > 0 {
			parsed, err := x509.ParseCertificate(cert.Raw)
			if err != nil {
				return nil, fmt.Errorf("%w: %v", ErrInvalidCertificate, err)
			}
			cert = parsed
		} else {
			return nil, ErrNoPublicKey
		}
	}

	declared := cert.PublicKeyAlgorithm
	if declared != x509.UnknownPublicKeyAlgorithm {
		family, err := keyFamilyOf(cert.PublicKey)
		if err != nil {
			return nil, err
		}
		var declaredFamily keyFamily
		switch declared {
		case x509.RSA:
			declaredFamily = familyRSAKey
		case x509.ECDSA:
			declaredFamily = familyECDSAKey
		case x509.Ed25519:
			declaredFamily = familyEd25519Key
		default:
			declaredFamily = familyUnsupportedKey
		}
		if declaredFamily != family {
			return nil, fmt.Errorf("%w: certificate declares %s but contains %T",
				ErrInvalidPublicKey, declared, cert.PublicKey)
		}
	}
	return cert.PublicKey, nil
}

func publicKeyFromBytes(derOrPEM []byte) (crypto.PublicKey, error) {
	data := bytes.TrimSpace(derOrPEM)
	if len(data) == 0 {
		return nil, ErrNoPublicKey
	}
	if bytes.HasPrefix(data, []byte("-----BEGIN")) {
		block, _ := pem.Decode(data)
		if block == nil {
			return nil, fmt.Errorf("%w: malformed PEM", ErrInvalidCertificate)
		}
		data = block.Bytes
	}
	cert, err := x509.ParseCertificate(data)
	if err == nil {
		return certificatePublicKey(cert)
	}
	if pub, err := x509.ParsePKIXPublicKey(data); err == nil {
		return publicKeyOf(pub)
	}
	if pub, err := x509.ParsePKCS1PublicKey(data); err == nil {
		return publicKeyOf(pub)
	}
	return nil, fmt.Errorf("%w: %v", ErrInvalidCertificate, err)
}

func verifySignature(pub crypto.PublicKey, scheme SignatureScheme, message, signature []byte) error {
	info, err := schemeInfoFor(scheme)
	if err != nil {
		return err
	}
	// The compatibility check deliberately precedes all signature operations.
	if err := checkSignatureScheme(pub, scheme); err != nil {
		return err
	}
	if len(signature) == 0 {
		return fmt.Errorf("%w: empty signature", ErrInvalidSignature)
	}

	if info.kind == kindEd25519 {
		key, ok := pub.(ed25519.PublicKey)
		if !ok {
			return fmt.Errorf("%w: %T", ErrInvalidPublicKey, pub)
		}
		if !ed25519.Verify(key, message, signature) {
			return ErrInvalidSignature
		}
		return nil
	}

	digest, err := digestFor(info, message)
	if err != nil {
		return err
	}

	switch key := pub.(type) {
	case *rsa.PublicKey:
		if info.kind == kindRSAPKCS1 {
			if err := rsa.VerifyPKCS1v15(key, info.hash, digest, signature); err != nil {
				return fmt.Errorf("%w: %v", ErrInvalidSignature, err)
			}
			return nil
		}
		err := rsa.VerifyPSS(key, info.hash, digest, signature, &rsa.PSSOptions{
			SaltLength: rsa.PSSSaltLengthEqualsHash,
			Hash:       info.hash,
		})
		if err != nil {
			// Some TLS 1.2 implementations emit a larger PSS salt. The
			// signature is still an RSA-PSS signature over this digest, so
			// allow the salt length to be recovered from the signature while
			// keeping the scheme/key binding strict.
			err = rsa.VerifyPSS(key, info.hash, digest, signature, &rsa.PSSOptions{
				SaltLength: rsa.PSSSaltLengthAuto,
				Hash:       info.hash,
			})
		}
		if err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidSignature, err)
		}
		return nil
	case *ecdsa.PublicKey:
		if info.kind != kindECDSA {
			return mismatchError(pub, info)
		}
		if !ecdsa.VerifyASN1(key, digest, signature) {
			return ErrInvalidSignature
		}
		return nil
	default:
		return fmt.Errorf("%w: %T", ErrUnsupportedPublicKey, pub)
	}
}

func digestFor(info schemeInfo, message []byte) ([]byte, error) {
	h := info.hash.New()
	if h == nil {
		return nil, fmt.Errorf("%w: hash unavailable for %s", ErrUnsupportedSignatureScheme, info.name)
	}
	_, _ = h.Write(message)
	return h.Sum(nil), nil
}

// CertificateVerifyMessage builds the TLS 1.3 CertificateVerify signed input
// for the supplied context string (for example
// "TLS 1.3, server CertificateVerify") and transcript hash.
func CertificateVerifyMessage(context string, transcriptHash []byte) []byte {
	var b bytes.Buffer
	b.Write(bytes.Repeat([]byte{' '}, 64))
	b.WriteString(context)
	b.WriteByte(0)
	b.Write(transcriptHash)
	return b.Bytes()
}
