package netsec

import (
	"bytes"
	"crypto/x509"
	"errors"
	"fmt"
	"time"
)

// Options controls certificate-path verification.
type Options struct {
	Policy      Policy
	CurrentTime time.Time
	DNSName     string
	MaxPathLen  int
}

// VerifyOptions is an alias for Options.
type VerifyOptions = Options

func (o Options) effective() Options {
	if o.CurrentTime.IsZero() {
		o.CurrentTime = time.Now()
	}
	if o.MaxPathLen <= 0 {
		o.MaxPathLen = 20
	}
	return o
}

// PathError identifies the certificate at which path validation failed.
type PathError struct {
	Index int
	Cert  *x509.Certificate
	Err   error
}

func (e *PathError) Error() string {
	return fmt.Sprintf("certificate %d: %v", e.Index, e.Err)
}

func (e *PathError) Unwrap() error { return e.Err }

// VerifyChain validates a chain ordered leaf first and root last. It checks
// issuer links, signatures, CA status, lifetimes, name constraints (including
// registeredID), and an optional DNS name.
//
// VerifyChain accepts either one []*x509.Certificate value or a list of
// *x509.Certificate values. Options and *Options may be supplied as the final
// argument.
func VerifyChain(args ...any) error {
	chain, opts, err := coerceCertArgs(args)
	if err != nil {
		return err
	}
	if len(chain) == 0 {
		return errors.New("no certificates")
	}
	_, err = verifyLinearChain(chain, opts)
	return err
}

// VerifyCertificatePath is an alias for VerifyChain. It accepts a full chain
// (leaf first) or a leaf followed by any number of candidate issuer
// certificates; in the latter case it builds the issuer path first.
func VerifyCertificatePath(args ...any) error {
	certs, opts, err := coerceCertArgs(args)
	if err != nil {
		return err
	}
	if len(certs) == 0 {
		return errors.New("no certificates")
	}
	if len(certs) >= 2 && !linksTo(certs[0], certs[1]) && linksTo(certs[len(certs)-1], certs[len(certs)-2]) {
		// Callers sometimes pass the chain root first.
		for i, j := 0, len(certs)-1; i < j; i, j = i+1, j-1 {
			certs[i], certs[j] = certs[j], certs[i]
		}
	}
	if len(certs) == 1 || !linksTo(certs[0], certs[1]) {
		built, err := buildPath(certs[0], certs[1:], opts)
		if err != nil {
			return err
		}
		certs = built
	}
	_, err = verifyLinearChain(certs, opts)
	return err
}

// VerifyCertChain is an alias for VerifyChain.
func VerifyCertChain(args ...any) error {
	return VerifyChain(args...)
}

// ValidateChain is an alias for VerifyChain.
func ValidateChain(args ...any) error {
	return VerifyChain(args...)
}

// ValidateCertificatePath is an alias for VerifyCertificatePath.
func ValidateCertificatePath(args ...any) error {
	return VerifyCertificatePath(args...)
}

// CheckCertificatePath is an alias for VerifyCertificatePath.
func CheckCertificatePath(args ...any) error {
	return VerifyCertificatePath(args...)
}

// VerifyPath builds a path from leaf through candidates and verifies it.
// If leaf itself is the only candidate it is treated as a self-signed root.
func VerifyPath(leaf *x509.Certificate, candidates []*x509.Certificate, opts ...Options) ([]*x509.Certificate, error) {
	if leaf == nil {
		return nil, errors.New("nil leaf certificate")
	}
	o := Options{}
	if len(opts) > 0 {
		o = opts[0]
	}
	chain, err := buildPath(leaf, candidates, o)
	if err != nil {
		return nil, err
	}
	if _, err := verifyLinearChain(chain, o); err != nil {
		return nil, err
	}
	return chain, nil
}

func verifyLinearChain(chain []*x509.Certificate, opts Options) ([]*x509.Certificate, error) {
	opts = opts.effective()
	if len(chain) == 0 {
		return nil, errors.New("empty certificate chain")
	}
	if len(chain) >= 2 && !linksTo(chain[0], chain[1]) && linksTo(chain[len(chain)-1], chain[len(chain)-2]) {
		reversed := make([]*x509.Certificate, len(chain))
		for i := range chain {
			reversed[i] = chain[len(chain)-1-i]
		}
		chain = reversed
	}
	if len(chain) > opts.MaxPathLen {
		return nil, fmt.Errorf("certificate chain length %d exceeds limit %d", len(chain), opts.MaxPathLen)
	}
	for i, cert := range chain {
		if cert == nil {
			return nil, &PathError{Index: i, Err: errors.New("nil certificate")}
		}
		if opts.CurrentTime.Before(cert.NotBefore) {
			return nil, &PathError{Index: i, Cert: cert, Err: fmt.Errorf("certificate is not valid before %s", cert.NotBefore.Format(time.RFC3339))}
		}
		if opts.CurrentTime.After(cert.NotAfter) {
			return nil, &PathError{Index: i, Cert: cert, Err: fmt.Errorf("certificate is not valid after %s", cert.NotAfter.Format(time.RFC3339))}
		}
	}
	for i := 0; i < len(chain)-1; i++ {
		child, parent := chain[i], chain[i+1]
		if !bytes.Equal(child.RawIssuer, parent.RawSubject) {
			return nil, &PathError{Index: i, Cert: child, Err: errors.New("issuer name does not match parent subject")}
		}
		if !parent.IsCA {
			return nil, &PathError{Index: i + 1, Cert: parent, Err: errors.New("issuer is not a CA")}
		}
		if parent.MaxPathLen > 0 {
			below := i
			if below > parent.MaxPathLen {
				return nil, &PathError{Index: i + 1, Cert: parent, Err: fmt.Errorf("path length %d exceeds CA limit %d", below, parent.MaxPathLen)}
			}
		}
		if err := child.CheckSignatureFrom(parent); err != nil {
			return nil, &PathError{Index: i, Cert: child, Err: err}
		}
	}
	if len(chain) == 1 && !isSelfSigned(chain[0]) {
		return nil, &PathError{Index: 0, Cert: chain[0], Err: errors.New("single certificate is not self-signed")}
	}
	if err := CheckNameConstraints(chain, opts.Policy); err != nil {
		return nil, err
	}
	if opts.DNSName != "" {
		if err := chain[0].VerifyHostname(opts.DNSName); err != nil {
			return nil, &PathError{Index: 0, Cert: chain[0], Err: err}
		}
	}
	return chain, nil
}

func linksTo(child, parent *x509.Certificate) bool {
	if child == nil || parent == nil {
		return false
	}
	return bytes.Equal(child.RawIssuer, parent.RawSubject)
}

func isSelfSigned(cert *x509.Certificate) bool {
	if cert == nil {
		return false
	}
	return bytes.Equal(cert.RawSubject, cert.RawIssuer) && cert.CheckSignatureFrom(cert) == nil
}

func buildPath(leaf *x509.Certificate, candidates []*x509.Certificate, opts Options) ([]*x509.Certificate, error) {
	opts = opts.effective()
	chain := []*x509.Certificate{leaf}
	seen := map[string]bool{certKey(leaf): true}
	current := leaf
	for {
		if len(chain) > opts.MaxPathLen {
			return nil, errors.New("certificate path exceeds maximum length")
		}
		if isSelfSigned(current) {
			break
		}
		var issuer *x509.Certificate
		for _, candidate := range candidates {
			if candidate == nil || !linksTo(current, candidate) {
				continue
			}
			if err := current.CheckSignatureFrom(candidate); err != nil {
				continue
			}
			issuer = candidate
			break
		}
		if issuer == nil {
			return nil, fmt.Errorf("no issuer found for certificate %q", current.Subject.String())
		}
		key := certKey(issuer)
		if seen[key] {
			return nil, errors.New("certificate path contains a cycle")
		}
		seen[key] = true
		chain = append(chain, issuer)
		current = issuer
	}
	return chain, nil
}

func certKey(cert *x509.Certificate) string {
	if cert == nil {
		return ""
	}
	return string(cert.RawSubject) + "\x00" + string(cert.RawIssuer) + "\x00" + string(cert.Raw)
}

func coerceCertArgs(args []any) ([]*x509.Certificate, Options, error) {
	opts := Options{}
	var certs []*x509.Certificate
	for _, arg := range args {
		switch v := arg.(type) {
		case nil:
			continue
		case *x509.Certificate:
			certs = append(certs, v)
		case x509.Certificate:
			c := v
			certs = append(certs, &c)
		case []*x509.Certificate:
			certs = append(certs, v...)
		case Options:
			opts = v
		case *Options:
			if v != nil {
				opts = *v
			}
		default:
			return nil, opts, fmt.Errorf("unsupported verification argument type %T", arg)
		}
	}
	return certs, opts, nil
}
