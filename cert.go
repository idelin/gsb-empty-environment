package netsec

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

type verifiedClientChain struct {
	leaf             *x509.Certificate
	certs            []*x509.Certificate
	leafFingerprint  string
	chainFingerprint string
	identity         string
	subject          string
}

func verifyClientChain(rawChain [][]byte, roots *x509.CertPool, at time.Time) (*verifiedClientChain, error) {
	if roots == nil {
		return nil, ErrNoTrustRoots
	}
	if len(rawChain) == 0 {
		return nil, fmt.Errorf("%w: empty client certificate chain", ErrCertificateInvalid)
	}

	certs := make([]*x509.Certificate, 0, len(rawChain))
	for i, der := range rawChain {
		cert, err := x509.ParseCertificate(der)
		if err != nil {
			return nil, fmt.Errorf("%w: parse certificate %d: %v", ErrCertificateInvalid, i, err)
		}
		certs = append(certs, cert)
	}
	leaf := certs[0]
	if leaf.IsCA {
		return nil, fmt.Errorf("%w: client leaf certificate is a CA", ErrCertificateInvalid)
	}

	intermediates := x509.NewCertPool()
	for _, cert := range certs[1:] {
		intermediates.AddCert(cert)
	}
	_, err := leaf.Verify(x509.VerifyOptions{
		Roots:         roots,
		Intermediates: intermediates,
		CurrentTime:   at,
		KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	})
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCertificateInvalid, err)
	}

	leafSum := sha256.Sum256(leaf.Raw)
	chainSum := chainFingerprint(rawChain)
	return &verifiedClientChain{
		leaf:             leaf,
		certs:            certs,
		leafFingerprint:  hex.EncodeToString(leafSum[:]),
		chainFingerprint: hex.EncodeToString(chainSum[:]),
		identity:         certificateIdentity(leaf),
		subject:          leaf.Subject.String(),
	}, nil
}

func chainFingerprint(rawChain [][]byte) [sha256.Size]byte {
	h := sha256.New()
	var size [4]byte
	for _, der := range rawChain {
		putUint32(size[:], uint32(len(der)))
		h.Write(size[:])
		h.Write(der)
	}
	var out [sha256.Size]byte
	copy(out[:], h.Sum(nil))
	return out
}

func putUint32(dst []byte, v uint32) {
	dst[0] = byte(v >> 24)
	dst[1] = byte(v >> 16)
	dst[2] = byte(v >> 8)
	dst[3] = byte(v)
}

// certificateIdentity derives the authorization principal from the verified
// certificate. The inner EAP identity must match this value. Subject
// Alternative Names take precedence over CommonName.
func certificateIdentity(cert *x509.Certificate) string {
	if len(cert.EmailAddresses) > 0 {
		return strings.TrimSpace(cert.EmailAddresses[0])
	}
	if len(cert.URIs) > 0 {
		return strings.TrimSpace(cert.URIs[0].String())
	}
	if len(cert.DNSNames) > 0 {
		return strings.TrimSpace(cert.DNSNames[0])
	}
	if cert.Subject.CommonName != "" {
		return strings.TrimSpace(cert.Subject.CommonName)
	}
	return strings.TrimSpace(cert.Subject.String())
}
