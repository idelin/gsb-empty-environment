package netsec

import (
	"crypto/x509"
	"errors"
	"fmt"
	"os"
	"time"
)

// VerifySignedData parses der and verifies every signer in the SignedData.
//
// Accepted optional arguments are []byte detached content, *x509.CertPool
// roots, []*x509.Certificate or *x509.Certificate trusted certificates,
// time.Time, VerifyConfig, and VerifyOption values. Supplying content that
// differs from encapsulated eContent is rejected.
func VerifySignedData(der []byte, args ...any) error {
	_, err := VerifySignedDataSigners(der, args...)
	return err
}

// Verify is an alias for VerifySignedData.
func Verify(der []byte, args ...any) error {
	return VerifySignedData(der, args...)
}

// VerifyDetached verifies a detached CMS signature over content.
func VerifyDetached(der, content []byte, args ...any) error {
	args = append([]any{WithContent(content)}, args...)
	return VerifySignedData(der, args...)
}

// VerifySignedDataContent verifies der against content. If der contains
// encapsulated content, content must be identical to it.
func VerifySignedDataContent(der, content []byte, args ...any) error {
	return VerifyDetached(der, content, args...)
}

// VerifySignedDataSigners verifies der and returns the verified SignerInfo
// values. Their Certificate fields point into the suitable signer's parsed
// certificate.
func VerifySignedDataSigners(der []byte, args ...any) ([]*SignerInfo, error) {
	sd, err := ParseSignedData(der)
	if err != nil {
		return nil, err
	}
	return VerifyParsedSignedData(sd, args...)
}

// VerifyParsedSignedData verifies an already parsed SignedData value.
func VerifyParsedSignedData(sd *SignedData, args ...any) ([]*SignerInfo, error) {
	cfg, err := parseVerifyArgs(args)
	if err != nil {
		return nil, err
	}
	return verifyParsedSignedData(sd, cfg)
}

// VerifyParsed is an alias for VerifyParsedSignedData.
func VerifyParsed(sd *SignedData, args ...any) ([]*SignerInfo, error) {
	return VerifyParsedSignedData(sd, args...)
}

// Verify verifies an already parsed SignedData value.
func (sd *SignedData) Verify(args ...any) error {
	_, err := VerifyParsedSignedData(sd, args...)
	return err
}

// VerifyContent verifies an already parsed SignedData value against content.
func (sd *SignedData) VerifyContent(content []byte, args ...any) error {
	args = append([]any{WithContent(content)}, args...)
	_, err := VerifyParsedSignedData(sd, args...)
	return err
}

// VerifyFile reads a PEM or DER signed-data file and verifies it. The
// signature argument may be a path string or the encoded SignedData bytes.
// String arguments after it are interpreted as paths to detached content
// files; []byte arguments supply content directly.
func VerifyFile(signature any, args ...any) error {
	der, err := loadBytes(signature, "signature")
	if err != nil {
		return err
	}

	converted, err := contentFilesToBytes(args)
	if err != nil {
		return err
	}
	return VerifySignedData(der, converted...)
}

// VerifySignedFile is an alias for VerifyFile.
func VerifySignedFile(signature any, args ...any) error {
	return VerifyFile(signature, args...)
}

// VerifyFiles verifies signature over content. Either argument may be a path
// string or the corresponding bytes.
func VerifyFiles(signature, content any, args ...any) error {
	contentBytes, err := loadBytes(content, "content")
	if err != nil {
		return err
	}
	args = append([]any{WithContent(contentBytes)}, args...)
	return VerifyFile(signature, args...)
}

// VerifyDetachedFile is an alias for VerifyFiles.
func VerifyDetachedFile(signature, content any, args ...any) error {
	return VerifyFiles(signature, content, args...)
}

func loadBytes(value any, kind string) ([]byte, error) {
	switch value := value.(type) {
	case string:
		data, err := os.ReadFile(value)
		if err != nil {
			return nil, fmt.Errorf("netsec: read %s file: %w", kind, err)
		}
		return data, nil
	case []byte:
		return value, nil
	default:
		return nil, fmt.Errorf("netsec: %s must be a path or []byte, got %T", kind, value)
	}
}

func contentFilesToBytes(args []any) ([]any, error) {
	out := make([]any, 0, len(args))
	for _, arg := range args {
		path, ok := arg.(string)
		if !ok {
			out = append(out, arg)
			continue
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("netsec: read content file %q: %w", path, err)
		}
		out = append(out, WithContent(content))
	}
	return out, nil
}

func verifyParsedSignedData(sd *SignedData, cfg *VerifyConfig) ([]*SignerInfo, error) {
	if sd == nil {
		return nil, fmt.Errorf("netsec: nil SignedData")
	}
	if len(sd.SignerInfos) == 0 {
		return nil, errNoSigners
	}

	content, err := resolveContent(sd, cfg)
	if err != nil {
		return nil, err
	}

	var verified []*SignerInfo
	var firstErr error
	for _, signer := range sd.SignerInfos {
		if signer == nil {
			if cfg.AllowAnySigner {
				continue
			}
			return nil, errors.New("netsec: nil SignerInfo")
		}
		cert, err := findSignerCertificate(sd, signer, cfg.TrustedCertificates)
		if err != nil {
			if cfg.AllowAnySigner {
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			return nil, err
		}
		signer.Certificate = cert

		if err := verifyCertificate(sd, cert, cfg); err != nil {
			if cfg.AllowAnySigner {
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			return nil, err
		}
		if err := validateSigner(sd, signer, content, cfg); err != nil {
			if cfg.AllowAnySigner {
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			return nil, err
		}
		verified = append(verified, signer)
	}

	if len(verified) == 0 {
		if firstErr != nil {
			return nil, firstErr
		}
		return nil, fmt.Errorf("netsec: no signer verified")
	}
	return verified, nil
}

func verifyCertificate(sd *SignedData, cert *x509.Certificate, cfg *VerifyConfig) error {
	if cfg.SkipCertificateVerification {
		return nil
	}

	if cfg.Roots == nil && len(cfg.TrustedCertificates) == 0 {
		// Signature-only mode. Callers that require archive trust must supply
		// WithRoots or trusted certificates.
		return nil
	}

	now := cfg.CurrentTime
	if now.IsZero() {
		now = time.Now()
	}
	if now.Before(cert.NotBefore) || now.After(cert.NotAfter) {
		return fmt.Errorf("netsec: signer certificate is not valid at %s", now)
	}

	for _, trusted := range cfg.TrustedCertificates {
		if trusted != nil && bytesEqual(trusted.Raw, cert.Raw) {
			return nil
		}
	}

	roots := cfg.Roots
	if roots == nil && len(cfg.TrustedCertificates) != 0 {
		roots = x509.NewCertPool()
		for _, trusted := range cfg.TrustedCertificates {
			if trusted != nil {
				roots.AddCert(trusted)
			}
		}
	}
	intermediates := x509.NewCertPool()
	for _, intermediate := range sd.Certificates {
		if intermediate != nil && !bytesEqual(intermediate.Raw, cert.Raw) {
			intermediates.AddCert(intermediate)
		}
	}
	for _, intermediate := range cfg.Intermediates {
		if intermediate != nil {
			intermediates.AddCert(intermediate)
		}
	}

	_, err := cert.Verify(x509.VerifyOptions{
		Roots:         roots,
		Intermediates: intermediates,
		CurrentTime:   now,
		KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageAny},
	})
	if err != nil {
		return fmt.Errorf("netsec: signer certificate chain: %w", err)
	}
	return nil
}
