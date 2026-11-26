package sigverify

import (
	"crypto/ecdsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/asn1"
	"time"
)

// Fulcio OIDC-related extension OIDs.
var (
	// OIDIssuerV1 is the legacy Fulcio OIDC issuer extension carrying the
	// issuer URL as a raw OCTET STRING.
	OIDIssuerV1 = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 57264, 1, 1}

	// OIDIssuerV2 is the newer issuer extension whose extnValue is an
	// ASN.1 SEQUENCE { UTF8String issuerURL }.
	OIDIssuerV2 = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 57264, 1, 8}
)

// issuerV2Extension mirrors the ASN.1 structure of the 1.3.6.1.4.1.57264.1.8
// extension value.
type issuerV2Extension struct {
	IssuerURL string `asn1:"utf8"`
}

// ExtractOIDCIssuer reads the OIDC issuer URL from the leaf certificate. The
// V1 extension (OCTET STRING) and the V2 extension (SEQUENCE containing a
// UTF8String) are both supported; V1 is consulted first.
func ExtractOIDCIssuer(cert *x509.Certificate) (string, error) {
	if cert == nil {
		return "", ErrIssuer
	}

	for _, ext := range cert.Extensions {
		if ext.Id.Equal(OIDIssuerV1) {
			var url string
			if _, err := asn1.Unmarshal(ext.Value, &url); err == nil && url != "" {
				return url, nil
			}
			// Some encoders store the URL as a bare string without ASN.1
			// wrapping; accept it as a defensive fallback.
			if len(ext.Value) > 0 && !isASN1Structured(ext.Value) {
				return string(ext.Value), nil
			}
		}
	}

	for _, ext := range cert.Extensions {
		if ext.Id.Equal(OIDIssuerV2) {
			var v2 issuerV2Extension
			if _, err := asn1.Unmarshal(ext.Value, &v2); err == nil && v2.IssuerURL != "" {
				return v2.IssuerURL, nil
			}
		}
	}

	return "", ErrIssuer
}

func isASN1Structured(b []byte) bool {
	return len(b) >= 2 && b[0]&0x20 != 0
}

// ExtractSANIdentities returns the Email and URI subjectAltName identities of
// the leaf certificate.
func ExtractSANIdentities(cert *x509.Certificate) ([]string, error) {
	if cert == nil {
		return nil, ErrSubject
	}
	var identities []string
	identities = append(identities, cert.EmailAddresses...)
	for _, u := range cert.URIs {
		if u != nil && u.String() != "" {
			identities = append(identities, u.String())
		}
	}
	if len(identities) == 0 {
		return nil, ErrSubject
	}
	return identities, nil
}

// verifyChain builds and validates the leaf-to-trusted-root path. Verification
// time is supplied by the caller: it must be the attested log integrated
// time, never the local clock.
func verifyChain(leaf *x509.Certificate, intermediates [][]byte, roots *x509.CertPool, at time.Time) error {
	intermediatePool := x509.NewCertPool()
	for _, der := range intermediates {
		cert, err := x509.ParseCertificate(der)
		if err != nil {
			return ErrCertificateChain
		}
		intermediatePool.AddCert(cert)
	}

	opts := x509.VerifyOptions{
		Roots:         roots,
		Intermediates: intermediatePool,
		CurrentTime:   at,
		KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageCodeSigning},
	}
	if _, err := leaf.Verify(opts); err != nil {
		return ErrCertificateChain
	}
	return nil
}

// hasCodeSigningEKU reports whether the leaf explicitly carries the
// CodeSigning extended key usage. A certificate without any EKU extension is
// rejected.
func hasCodeSigningEKU(cert *x509.Certificate) bool {
	for _, usage := range cert.ExtKeyUsage {
		if usage == x509.ExtKeyUsageCodeSigning {
			return true
		}
	}
	return false
}

// verifyArtifactSignature checks the ECDSA signature of the artifact digest
// under the leaf public key. The signature is over the raw artifact bytes;
// hashing is performed inside ecdsa.VerifyASN1.
func verifyArtifactSignature(leaf *x509.Certificate, artifact, signature []byte) error {
	publicKey, ok := leaf.PublicKey.(*ecdsa.PublicKey)
	if !ok {
		return ErrArtifactSignature
	}
	digest := sha256.Sum256(artifact)
	if !ecdsa.VerifyASN1(publicKey, digest[:], signature) {
		return ErrArtifactSignature
	}
	return nil
}
