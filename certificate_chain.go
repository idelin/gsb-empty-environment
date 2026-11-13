package netsec

import "crypto/x509"

// SignatureSchemeForCertificate maps the signature algorithm of cert to a TLS
// SignatureScheme value. It reports false for algorithms that have no TLS 1.3
// signature scheme encoding, such as MD5.
func SignatureSchemeForCertificate(cert *x509.Certificate) (SignatureScheme, bool) {
	if cert == nil {
		return 0, false
	}

	switch cert.SignatureAlgorithm {
	case x509.SHA1WithRSA:
		return PKCS1WithSHA1, true
	case x509.SHA256WithRSA:
		return PKCS1WithSHA256, true
	case x509.SHA384WithRSA:
		return PKCS1WithSHA384, true
	case x509.SHA512WithRSA:
		return PKCS1WithSHA512, true

	case x509.SHA256WithRSAPSS:
		return PSSWithSHA256, true
	case x509.SHA384WithRSAPSS:
		return PSSWithSHA384, true
	case x509.SHA512WithRSAPSS:
		return PSSWithSHA512, true

	case x509.ECDSAWithSHA1:
		return ECDSAWithSHA1, true
	case x509.ECDSAWithSHA256:
		return ECDSAWithP256AndSHA256, true
	case x509.ECDSAWithSHA384:
		return ECDSAWithP384AndSHA384, true
	case x509.ECDSAWithSHA512:
		return ECDSAWithP521AndSHA512, true

	case x509.DSAWithSHA1:
		return DSAWithSHA1, true
	case x509.DSAWithSHA256:
		return DSAWithSHA256, true

	case x509.PureEd25519:
		return Ed25519, true

	default:
		return 0, false
	}
}

// SignatureSchemesForCertificate returns the TLS signature schemes that can
// describe cert's signature. A certificate has one signature algorithm, so
// the result contains at most one entry. The slice form makes it convenient
// to use with the list-oriented selection helpers.
func SignatureSchemesForCertificate(cert *x509.Certificate) []SignatureScheme {
	scheme, ok := SignatureSchemeForCertificate(cert)
	if !ok {
		return nil
	}
	return []SignatureScheme{scheme}
}

// CertificateChainSignatureSchemes returns the signature scheme candidates for
// every certificate in chain. A nil entry is returned for a certificate whose
// signature algorithm has no TLS encoding.
func CertificateChainSignatureSchemes(chain []*x509.Certificate) [][]SignatureScheme {
	if len(chain) == 0 {
		return nil
	}
	out := make([][]SignatureScheme, len(chain))
	for i, cert := range chain {
		out[i] = SignatureSchemesForCertificate(cert)
	}
	return out
}

// CertificateChainSchemesAllowed reports whether every scheme in a chain
// represented directly as signature schemes is permitted by the applicable
// peer list. This is useful before x509 certificates have been parsed.
func CertificateChainSchemesAllowed(chainSchemes []SignatureScheme, signatureAlgorithms, signatureAlgorithmsCert []SignatureScheme, certExtensionPresent ...bool) bool {
	if len(chainSchemes) == 0 {
		return false
	}
	policy := NewCertificateSignatureAlgorithms(signatureAlgorithms, signatureAlgorithmsCert, certExtensionPresent...)
	for _, scheme := range chainSchemes {
		if scheme == 0 || !policy.AcceptsCertificateScheme(scheme) {
			return false
		}
	}
	return true
}

// FilterCertificateChainSchemes removes disallowed entries from a chain
// represented directly as signature schemes, preserving chain order.
func FilterCertificateChainSchemes(chainSchemes []SignatureScheme, signatureAlgorithms, signatureAlgorithmsCert []SignatureScheme, certExtensionPresent ...bool) []SignatureScheme {
	if len(chainSchemes) == 0 {
		return nil
	}
	policy := NewCertificateSignatureAlgorithms(signatureAlgorithms, signatureAlgorithmsCert, certExtensionPresent...)
	out := make([]SignatureScheme, 0, len(chainSchemes))
	for _, scheme := range chainSchemes {
		if scheme != 0 && policy.AcceptsCertificateScheme(scheme) {
			out = append(out, scheme)
		}
	}
	return out
}

// CertificateChainAllowed reports whether every certificate in chain has a
// signature scheme permitted by the applicable peer list. An empty chain, a
// nil certificate, or a certificate with an unknown signature algorithm is
// rejected.
func CertificateChainAllowed(chain []*x509.Certificate, signatureAlgorithms, signatureAlgorithmsCert []SignatureScheme, certExtensionPresent ...bool) bool {
	if len(chain) == 0 {
		return false
	}
	policy := NewCertificateSignatureAlgorithms(signatureAlgorithms, signatureAlgorithmsCert, certExtensionPresent...)
	for _, cert := range chain {
		schemes := SignatureSchemesForCertificate(cert)
		if len(schemes) == 0 {
			return false
		}
		allowed := false
		for _, scheme := range schemes {
			if policy.AcceptsCertificateScheme(scheme) {
				allowed = true
				break
			}
		}
		if !allowed {
			return false
		}
	}
	return true
}

// FilterCertificateChain removes certificates whose signature scheme is not
// permitted by the applicable peer list, preserving the chain order.
//
// A TLS stack will normally want to reject the whole candidate chain when any
// link is filtered out; CertificateChainAllowed implements that stricter
// check. This function is useful when inspecting or selecting individual
// chain elements.
func FilterCertificateChain(chain []*x509.Certificate, signatureAlgorithms, signatureAlgorithmsCert []SignatureScheme, certExtensionPresent ...bool) []*x509.Certificate {
	if len(chain) == 0 {
		return nil
	}
	policy := NewCertificateSignatureAlgorithms(signatureAlgorithms, signatureAlgorithmsCert, certExtensionPresent...)
	out := make([]*x509.Certificate, 0, len(chain))
	for _, cert := range chain {
		schemes := SignatureSchemesForCertificate(cert)
		for _, scheme := range schemes {
			if policy.AcceptsCertificateScheme(scheme) {
				out = append(out, cert)
				break
			}
		}
	}
	return out
}

// FilterCertificateChains returns the candidate chains for which every
// certificate is permitted, preserving the input order.
func FilterCertificateChains(chains [][]*x509.Certificate, signatureAlgorithms, signatureAlgorithmsCert []SignatureScheme, certExtensionPresent ...bool) [][]*x509.Certificate {
	if len(chains) == 0 {
		return nil
	}
	out := make([][]*x509.Certificate, 0, len(chains))
	for _, chain := range chains {
		if CertificateChainAllowed(chain, signatureAlgorithms, signatureAlgorithmsCert, certExtensionPresent...) {
			out = append(out, chain)
		}
	}
	return out
}
