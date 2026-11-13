package netsec

import (
	"bytes"
	"crypto/dsa"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/x509"
	"encoding/asn1"
	"fmt"
	"hash"
	"math/big"
)

type keyState struct {
	dsa    bool
	y      *big.Int
	params *dsa.Parameters
	parsed *x509.Certificate
}

type dsaSignature struct {
	R, S *big.Int
}

func cloneDSAParameters(params *dsa.Parameters) *dsa.Parameters {
	if params == nil {
		return nil
	}
	return &dsa.Parameters{
		P: new(big.Int).Set(params.P),
		Q: new(big.Int).Set(params.Q),
		G: new(big.Int).Set(params.G),
	}
}

func resolveKeyState(cert *rawCertificate, inherited *dsa.Parameters) (*keyState, error) {
	if cert.keyAlg.Algorithm.Equal(oidPublicKeyDSA) {
		declared, err := dsaParametersFromAlgorithmIdentifier(cert.keyAlg)
		if err != nil {
			return nil, err
		}

		var y *big.Int
		rest, err := asn1.Unmarshal(cert.keyBits, &y)
		if err != nil || len(rest) != 0 || y == nil || y.Sign() <= 0 {
			return nil, fmt.Errorf("%w: malformed DSA public key", ErrInvalidCertificate)
		}

		switch {
		case inherited == nil && declared == nil:
			return nil, ErrDSAParametersMissing
		case inherited != nil && declared != nil && !ParametersEqual(declared, inherited):
			return nil, fmt.Errorf("%w: child public key parameters cannot replace the issuer's parameters", ErrDSAParameterMismatch)
		case declared == nil:
			declared = cloneDSAParameters(inherited)
		}

		return &keyState{
			dsa:    true,
			y:      y,
			params: declared,
			parsed: cert.parsed,
		}, nil
	}

	if cert.parsed == nil {
		return nil, fmt.Errorf("%w: cannot parse %s public key", ErrUnsupportedAlgorithm, cert.keyAlg.Algorithm)
	}
	return &keyState{parsed: cert.parsed}, nil
}

func dsaHashForAlgorithm(oid asn1.ObjectIdentifier) (hash.Hash, error) {
	switch {
	case oid.Equal(oidDSAWithSHA1):
		return sha1.New(), nil
	case oid.Equal(oidDSAWithSHA224):
		return sha256.New224(), nil
	case oid.Equal(oidDSAWithSHA256):
		return sha256.New(), nil
	case oid.Equal(oidDSAWithSHA384):
		return sha512.New384(), nil
	case oid.Equal(oidDSAWithSHA512):
		return sha512.New(), nil
	default:
		return nil, fmt.Errorf("%w: signature algorithm %s", ErrUnsupportedAlgorithm, oid)
	}
}

func verifyLink(child *rawCertificate, issuer *keyState) error {
	if isDSASignatureAlgorithm(child.sigAlg.Algorithm) {
		if !issuer.dsa || issuer.y == nil || issuer.params == nil {
			return fmt.Errorf("%w: DSA signature with a non-DSA issuer key", ErrUnsupportedAlgorithm)
		}

		sigParams, err := dsaParametersFromAlgorithmIdentifier(child.sigAlg)
		if err != nil {
			return err
		}
		if sigParams != nil && !ParametersEqual(sigParams, issuer.params) {
			return fmt.Errorf("%w: signature parameters cannot replace the issuer's parameters", ErrDSAParameterMismatch)
		}

		var sig dsaSignature
		rest, err := asn1.Unmarshal(child.sigValue, &sig)
		if err != nil || len(rest) != 0 || sig.R == nil || sig.S == nil {
			return fmt.Errorf("%w: malformed DSA signature", ErrInvalidSignature)
		}

		h, err := dsaHashForAlgorithm(child.sigAlg.Algorithm)
		if err != nil {
			return err
		}
		h.Write(child.tbs)
		digest := h.Sum(nil)

		pub := &dsa.PublicKey{
			Parameters: *issuer.params,
			Y:          issuer.y,
		}
		if !dsa.Verify(pub, digest, sig.R, sig.S) {
			return fmt.Errorf("%w: DSA verification failed under the issuer's parameters", ErrInvalidSignature)
		}
		return nil
	}

	if child.parsed == nil || issuer.parsed == nil {
		return fmt.Errorf("%w: cannot verify signature algorithm %s", ErrUnsupportedAlgorithm, child.sigAlg.Algorithm)
	}
	if err := issuer.parsed.CheckSignature(child.parsed.SignatureAlgorithm, child.tbs, child.sigValue); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidSignature, err)
	}
	return nil
}

func orientChain(certs []*rawCertificate) ([]*rawCertificate, bool) {
	if len(certs) < 2 {
		return certs, false
	}

	leafFirst, rootFirst := 0, 0
	for i := 0; i+1 < len(certs); i++ {
		if bytes.Equal(certs[i].issuer, certs[i+1].subject) {
			leafFirst++
		}
		if bytes.Equal(certs[i+1].issuer, certs[i].subject) {
			rootFirst++
		}
	}
	if certs[0].isSelfIssued() && !certs[len(certs)-1].isSelfIssued() {
		rootFirst += 2
	}
	if certs[len(certs)-1].isSelfIssued() && !certs[0].isSelfIssued() {
		leafFirst += 2
	}

	if leafFirst > rootFirst {
		reversed := make([]*rawCertificate, len(certs))
		for i := range certs {
			reversed[len(certs)-1-i] = certs[i]
		}
		return reversed, false
	}
	return certs, rootFirst == leafFirst
}

func verifyOrderedChain(ordered []*rawCertificate) error {
	anchor, err := resolveKeyState(ordered[0], nil)
	if err != nil {
		return err
	}

	state := anchor
	for i := 1; i < len(ordered); i++ {
		if err := verifyLink(ordered[i], state); err != nil {
			return err
		}
		next, err := resolveKeyState(ordered[i], state.params)
		if err != nil {
			return err
		}
		state = next
	}
	return nil
}

func reverseRawChain(certs []*rawCertificate) []*rawCertificate {
	reversed := make([]*rawCertificate, len(certs))
	for i := range certs {
		reversed[len(certs)-1-i] = certs[i]
	}
	return reversed
}

func verifyRawChain(certs []*rawCertificate) error {
	if len(certs) == 0 {
		return ErrEmptyChain
	}

	ordered, ambiguous := orientChain(certs)
	err := verifyOrderedChain(ordered)
	if err == nil || !ambiguous {
		return err
	}

	if reverseErr := verifyOrderedChain(reverseRawChain(ordered)); reverseErr == nil {
		return nil
	}
	return err
}

func rawCertificatesFromX509(certs []*x509.Certificate) ([]*rawCertificate, error) {
	if len(certs) == 0 {
		return nil, ErrEmptyChain
	}
	out := make([]*rawCertificate, 0, len(certs))
	for i, cert := range certs {
		if cert == nil {
			return nil, fmt.Errorf("%w: certificate %d is nil", ErrInvalidCertificate, i)
		}
		if len(cert.Raw) == 0 {
			return nil, fmt.Errorf("%w: certificate %d has no raw DER", ErrInvalidCertificate, i)
		}
		raw, err := parseRawCertificate(cert.Raw)
		if err != nil {
			return nil, fmt.Errorf("certificate %d: %w", i, err)
		}
		out = append(out, raw)
	}
	return out, nil
}

// verifyCertificates checks the signatures along a certificate path. The
// certificates may be supplied in leaf-to-root or root-to-leaf order.
//
// The trust anchor is not checked for a self-signature. For every other
// certificate, its signature is checked with the issuing certificate's key.
// DSA parameters are inherited when omitted. If a child certificate carries
// DSA parameters, they must be identical to the parameters in use by its
// issuer; a mid-chain parameter replacement is rejected.
func verifyCertificates(certs []*x509.Certificate) error {
	raw, err := rawCertificatesFromX509(certs)
	if err != nil {
		return err
	}
	return verifyRawChain(raw)
}

// VerifyChain verifies the signatures in a certificate path. It accepts
// either a slice of certificates or individual certificate arguments.
func VerifyChain[T chainArgument](args ...T) error {
	certs, err := collectChain(args...)
	if err != nil {
		return err
	}
	return verifyCertificates(certs)
}

// VerifyDSAChain is an alias for VerifyChain, provided for callers that want
// to make the DSA parameter-inheritance semantics explicit.
func VerifyDSAChain[T chainArgument](args ...T) error {
	return VerifyChain(args...)
}

// VerifyChainSlice verifies a certificate path supplied as a slice.
func VerifyChainSlice(certs []*x509.Certificate) error {
	return verifyCertificates(certs)
}

// VerifyCertificateChain verifies a certificate path supplied as a slice.
func VerifyCertificateChain(certs []*x509.Certificate) error {
	return verifyCertificates(certs)
}

// VerifyPath verifies a certificate path supplied as a slice.
func VerifyPath(certs []*x509.Certificate) error {
	return verifyCertificates(certs)
}

// VerifyCertificatePath verifies a certificate path supplied as a slice.
func VerifyCertificatePath(certs []*x509.Certificate) error {
	return verifyCertificates(certs)
}

// ValidateChain verifies a certificate path supplied as a slice.
func ValidateChain(certs []*x509.Certificate) error {
	return verifyCertificates(certs)
}

// VerifyCertChain verifies a certificate path supplied as a slice.
func VerifyCertChain(certs []*x509.Certificate) error {
	return verifyCertificates(certs)
}

// VerifyCertPath verifies a certificate path supplied as a slice.
func VerifyCertPath(certs []*x509.Certificate) error {
	return verifyCertificates(certs)
}

// CheckChain verifies a certificate path supplied as a slice.
func CheckChain(certs []*x509.Certificate) error {
	return verifyCertificates(certs)
}

// CheckCertificatePath verifies a certificate path supplied as a slice.
func CheckCertificatePath(certs []*x509.Certificate) error {
	return verifyCertificates(certs)
}

// VerifyDERChain verifies a path of DER-encoded certificates. This entry point
// also supports DSA SubjectPublicKeyInfo structures that omit parameters and
// inherit them from the issuer, which crypto/x509 cannot parse on its own.
func VerifyDERChain(chain [][]byte) error {
	if len(chain) == 0 {
		return ErrEmptyChain
	}
	raw := make([]*rawCertificate, 0, len(chain))
	for i, der := range chain {
		cert, err := parseRawCertificate(der)
		if err != nil {
			return fmt.Errorf("certificate %d: %w", i, err)
		}
		raw = append(raw, cert)
	}
	return verifyRawChain(raw)
}

type chainArgument interface {
	*x509.Certificate | []*x509.Certificate
}

func collectChain[T chainArgument](args ...T) ([]*x509.Certificate, error) {
	switch values := any(args).(type) {
	case []*x509.Certificate:
		return values, nil
	case [][]*x509.Certificate:
		var certs []*x509.Certificate
		for _, value := range values {
			certs = append(certs, value...)
		}
		return certs, nil
	default:
		return nil, fmt.Errorf("%w: unsupported certificate argument type", ErrInvalidCertificate)
	}
}

// Chain is a convenience wrapper around a certificate path.
type Chain struct {
	Certificates []*x509.Certificate
}

// NewChain builds a Chain from a slice or from individual certificates.
func NewChain[T chainArgument](args ...T) *Chain {
	certs, _ := collectChain(args...)
	return &Chain{Certificates: certs}
}

// Verify verifies the chain.
func (c *Chain) Verify() error {
	if c == nil {
		return ErrEmptyChain
	}
	return verifyCertificates(c.Certificates)
}

// Path is an alias for Chain.
type Path = Chain

// NewPath builds a Path from a slice or from individual certificates.
func NewPath[T chainArgument](args ...T) *Path {
	return NewChain(args...)
}
