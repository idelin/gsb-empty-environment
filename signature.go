package netsec

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	_ "crypto/sha1"
	_ "crypto/sha256"
	_ "crypto/sha512"
	"crypto/x509"
	"fmt"
	"strings"
)

type signatureMode int

const (
	signatureModePKCS1 signatureMode = iota
	signatureModePSS
	signatureModeECDSA
	signatureModeEd25519
)

type signatureScheme struct {
	hash crypto.Hash
	mode signatureMode
}

func schemeForSecurityPolicy(uri string) (signatureScheme, bool) {
	switch CanonicalSecurityPolicyURI(uri) {
	case SecurityPolicyBasic128Rsa15, SecurityPolicyBasic256:
		return signatureScheme{hash: crypto.SHA1, mode: signatureModePKCS1}, true
	case SecurityPolicyBasic256Sha256, SecurityPolicyAes128Sha256RsaOaep:
		return signatureScheme{hash: crypto.SHA256, mode: signatureModePKCS1}, true
	case SecurityPolicyAes256Sha256RsaPss:
		return signatureScheme{hash: crypto.SHA256, mode: signatureModePSS}, true
	case SecurityPolicyECCNistP256, SecurityPolicyECCBrainpoolP256R1:
		return signatureScheme{hash: crypto.SHA256, mode: signatureModeECDSA}, true
	case SecurityPolicyECCNistP384, SecurityPolicyECCBrainpoolP384R1:
		return signatureScheme{hash: crypto.SHA384, mode: signatureModeECDSA}, true
	case SecurityPolicyECCCurve25519:
		return signatureScheme{mode: signatureModeEd25519}, true
	default:
		return signatureScheme{}, false
	}
}

func parseSignatureScheme(uri string) (signatureScheme, bool) {
	lower := strings.ToLower(strings.TrimSpace(uri))
	if lower == "" {
		return signatureScheme{}, false
	}

	scheme := signatureScheme{mode: signatureModePKCS1}
	switch {
	case strings.Contains(lower, "ed25519"):
		scheme.mode = signatureModeEd25519
		return scheme, true
	case strings.Contains(lower, "pss"):
		scheme.mode = signatureModePSS
	case strings.Contains(lower, "ecdsa"):
		scheme.mode = signatureModeECDSA
	}

	switch {
	case strings.Contains(lower, "sha512") || strings.Contains(lower, "sha-512"):
		scheme.hash = crypto.SHA512
	case strings.Contains(lower, "sha384") || strings.Contains(lower, "sha-384"):
		scheme.hash = crypto.SHA384
	case strings.Contains(lower, "sha256") || strings.Contains(lower, "sha-256"):
		scheme.hash = crypto.SHA256
	case strings.Contains(lower, "sha1") || strings.Contains(lower, "sha-1"):
		scheme.hash = crypto.SHA1
	default:
		return signatureScheme{}, false
	}
	return scheme, true
}

func compatibleSchemes(a, b signatureScheme) bool {
	if a.mode != b.mode {
		return false
	}
	if a.mode == signatureModeEd25519 {
		return true
	}
	return a.hash == b.hash
}

func signatureSchemeForRequest(policyURI, requestedAlgorithm string, cert *x509.Certificate) (signatureScheme, error) {
	expected, policyKnown := schemeForSecurityPolicy(policyURI)
	if strings.TrimSpace(requestedAlgorithm) == "" {
		if policyKnown {
			return expected, nil
		}
		// SecurityPolicyNone and custom policy URIs do not name a signature
		// algorithm. Infer a secure default from the token certificate.
		switch cert.PublicKey.(type) {
		case *rsa.PublicKey:
			return signatureScheme{hash: crypto.SHA256, mode: signatureModePKCS1}, nil
		case *ecdsa.PublicKey:
			return signatureScheme{hash: crypto.SHA256, mode: signatureModeECDSA}, nil
		case ed25519.PublicKey:
			return signatureScheme{mode: signatureModeEd25519}, nil
		default:
			return signatureScheme{}, fmt.Errorf(
				"%w: policy %q and key type %T",
				ErrSignatureAlgorithmRequired, policyURI, cert.PublicKey,
			)
		}
	}

	requested, ok := parseSignatureScheme(requestedAlgorithm)
	if !ok {
		return signatureScheme{}, fmt.Errorf(
			"%w: %q", ErrSignatureAlgorithmUnsupported, requestedAlgorithm,
		)
	}
	if policyKnown && policyURI != SecurityPolicyNone && !compatibleSchemes(requested, expected) {
		return signatureScheme{}, fmt.Errorf(
			"%w: policy %q does not allow %q",
			ErrSignatureAlgorithmMismatch, policyURI, requestedAlgorithm,
		)
	}
	return requested, nil
}

// SignedData returns the exact bytes a client certificate token must sign:
// the server certificate followed by the server nonce.
func SignedData(serverCertificate, serverNonce []byte) []byte {
	data := make([]byte, 0, len(serverCertificate)+len(serverNonce))
	data = append(data, serverCertificate...)
	data = append(data, serverNonce...)
	return data
}

// VerifyCertificateToken checks the token certificate, its optional binding to
// the CreateSession client certificate, and the client signature over
// serverCertificate||serverNonce. It does not consume the nonce; callers
// should consume it only after this function succeeds.
func VerifyCertificateToken(endpoint Endpoint, request ActivateSessionRequest) error {
	policy, err := endpoint.LookupUserTokenPolicy(request.PolicyID, TokenTypeCertificate)
	if err != nil {
		return err
	}
	if len(request.Certificate) == 0 {
		return ErrCertificateRequired
	}
	if len(request.Signature) == 0 {
		return ErrSignatureRequired
	}
	if len(request.ServerNonce) == 0 {
		return ErrNonceMissing
	}
	if len(endpoint.ServerCertificate) == 0 {
		return ErrServerCertificateMissing
	}
	if len(request.ClientCertificate) > 0 && !bytes.Equal(request.ClientCertificate, request.Certificate) {
		return ErrCertificateMismatch
	}

	cert, err := x509.ParseCertificate(request.Certificate)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrCertificateInvalid, err)
	}

	policyURI := endpoint.EffectiveSecurityPolicyURI(policy)
	scheme, err := signatureSchemeForRequest(policyURI, request.SignatureAlgorithm, cert)
	if err != nil {
		return err
	}

	data := SignedData(endpoint.ServerCertificate, request.ServerNonce)
	if err := verifyCertificateSignature(cert, data, scheme, request.Signature); err != nil {
		return err
	}
	return nil
}

func verifyCertificateSignature(cert *x509.Certificate, data []byte, scheme signatureScheme, signature []byte) error {
	switch publicKey := cert.PublicKey.(type) {
	case *rsa.PublicKey:
		if scheme.mode != signatureModePKCS1 && scheme.mode != signatureModePSS {
			return fmt.Errorf("%w: RSA certificate with mode %d", ErrSignatureAlgorithmUnsupported, scheme.mode)
		}
		digest, err := hashData(scheme.hash, data)
		if err != nil {
			return err
		}
		if scheme.mode == signatureModePSS {
			if err := rsa.VerifyPSS(publicKey, scheme.hash, digest, signature, &rsa.PSSOptions{
				SaltLength: rsa.PSSSaltLengthAuto,
				Hash:       scheme.hash,
			}); err != nil {
				return fmt.Errorf("%w: %v", ErrSignatureInvalid, err)
			}
			return nil
		}
		if err := rsa.VerifyPKCS1v15(publicKey, scheme.hash, digest, signature); err != nil {
			return fmt.Errorf("%w: %v", ErrSignatureInvalid, err)
		}
		return nil

	case *ecdsa.PublicKey:
		if scheme.mode != signatureModeECDSA {
			return fmt.Errorf("%w: ECDSA certificate with non-ECDSA mode", ErrSignatureAlgorithmUnsupported)
		}
		digest, err := hashData(scheme.hash, data)
		if err != nil {
			return err
		}
		if !ecdsa.VerifyASN1(publicKey, digest, signature) {
			return ErrSignatureInvalid
		}
		return nil

	case ed25519.PublicKey:
		if scheme.mode != signatureModeEd25519 {
			return fmt.Errorf("%w: Ed25519 certificate with non-Ed25519 mode", ErrSignatureAlgorithmUnsupported)
		}
		if !ed25519.Verify(publicKey, data, signature) {
			return ErrSignatureInvalid
		}
		return nil

	default:
		return fmt.Errorf("%w: public key type %T", ErrSignatureAlgorithmUnsupported, cert.PublicKey)
	}
}

func hashData(hash crypto.Hash, data []byte) ([]byte, error) {
	if !hash.Available() {
		return nil, fmt.Errorf("%w: hash %v is unavailable", ErrSignatureAlgorithmUnsupported, hash)
	}
	h := hash.New()
	if _, err := h.Write(data); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrSignatureAlgorithmUnsupported, err)
	}
	return h.Sum(nil), nil
}
