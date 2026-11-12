package netsec

import (
	"bytes"
	"crypto/x509"
	"fmt"
	"io"
)

// Policy selects how strictly a gem is verified.
type Policy int

const (
	// MediumSecurity verifies every signature that is present and accepts gems
	// with no signatures at all. A gem where only some members are signed is
	// always rejected.
	MediumSecurity Policy = iota

	// HighSecurity requires every signed member to carry a valid RSA-SHA256
	// signature and requires the signer certificate chain to terminate at a
	// root in the configured TrustStore. Unsigned gems are rejected.
	HighSecurity
)

// TrustStore holds the trusted certificate roots for HighSecurity.
type TrustStore struct {
	Roots *x509.CertPool
}

// Options controls a verification run.
type Options struct {
	// Policy selects MediumSecurity or HighSecurity.
	Policy Policy

	// Trust is required for HighSecurity and ignored by MediumSecurity.
	Trust *TrustStore

	// MaxMemberBytes caps the size of a single outer tar member. Zero means
	// the built-in default.
	MaxMemberBytes int64
}

// Result reports the outcome of a successful verification.
type Result struct {
	// Signed is false when the gem carried no signatures at all (only
	// possible under MediumSecurity).
	Signed bool

	// Signer is the leaf certificate that signed every signed member. It is
	// nil for unsigned gems.
	Signer *x509.Certificate
}

var signedMembers = []string{MemberMetadata, MemberData, MemberChecksums}

// Verify reads a gem from r and enforces the requested policy:
//
//   - duplicate outer tar member names, unknown members and non-regular
//     entries are rejected;
//   - every SHA512 digest in checksums.yaml must match the actual member and
//     metadata.gz plus data.tar.gz must be covered;
//   - either all of metadata.gz, data.tar.gz and checksums.yaml.gz carry a
//     .sig counterpart or none of them do, so stripping a single .sig fails;
//   - present signatures must be valid detached RSA-SHA256 PKCS#7 signatures
//     made by one signer certificate;
//   - HighSecurity additionally requires a chain terminating at a trusted
//     root, while MediumSecurity validates the chain structurally but does
//     not require a configured trust root.
func Verify(r io.Reader, opts Options) (*Result, error) {
	members, err := ReadMembers(r, opts.MaxMemberBytes)
	if err != nil {
		return nil, err
	}

	if err := VerifyChecksums(members); err != nil {
		return nil, err
	}

	signed, err := signatureCoverage(members)
	if err != nil {
		return nil, err
	}
	if !signed {
		if opts.Policy == HighSecurity {
			return nil, ErrUnsigned
		}
		return &Result{Signed: false}, nil
	}

	var signer *x509.Certificate
	for _, name := range signedMembers {
		verified, err := verifyPKCS7Detached(members[name+".sig"], members[name])
		if err != nil {
			return nil, fmt.Errorf("verify %q signature: %w", name, err)
		}
		if signer == nil {
			signer = verified.Signer
		} else if !bytes.Equal(signer.Raw, verified.Signer.Raw) {
			return nil, fmt.Errorf("%w: %q", ErrSignerMismatch, name)
		}

		if err := verifyChain(verified, opts); err != nil {
			return nil, err
		}
	}

	return &Result{Signed: true, Signer: signer}, nil
}

// signatureCoverage enforces the all-or-nothing rule for .sig members.
func signatureCoverage(members map[string][]byte) (bool, error) {
	present := 0
	for _, name := range signedMembers {
		if _, ok := members[name+".sig"]; ok {
			present++
		}
	}
	switch present {
	case 0:
		return false, nil
	case len(signedMembers):
		return true, nil
	default:
		return false, ErrPartialSignature
	}
}

func verifyChain(verified *verifiedSignature, opts Options) error {
	intermediates := x509.NewCertPool()
	embeddedRoots := x509.NewCertPool()
	for _, cert := range verified.CertChain {
		if bytes.Equal(cert.Raw, verified.Signer.Raw) {
			continue
		}
		intermediates.AddCert(cert)
		if cert.IsCA && bytes.Equal(cert.RawIssuer, cert.RawSubject) {
			embeddedRoots.AddCert(cert)
		}
	}

	verifyOpts := x509.VerifyOptions{
		Intermediates: intermediates,
		KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageCodeSigning, x509.ExtKeyUsageAny},
	}

	if opts.Policy == HighSecurity {
		if opts.Trust == nil || opts.Trust.Roots == nil || len(opts.Trust.Roots.Subjects()) == 0 {
			return ErrNoTrustRoots
		}
		verifyOpts.Roots = opts.Trust.Roots
	} else {
		// MediumSecurity validates signatures and the embedded chain's
		// structure, but does not require the root to live in a configured
		// trust store. Pinning Roots to the embedded certificates prevents Go
		// from silently falling back to the operating system pool.
		verifyOpts.Roots = embeddedRoots
	}

	if _, err := verified.Signer.Verify(verifyOpts); err != nil {
		return fmt.Errorf("%w: %v", ErrUntrustedChain, err)
	}
	return nil
}
