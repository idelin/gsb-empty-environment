package netsec

import (
	"bytes"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
)

func verifyDetachedSignature(sigData, content []byte, cfg *verifyConfig, member string) (*SignatureInfo, error) {
	if len(sigData) == 0 {
		return nil, fmt.Errorf("%w: empty signature for %s", ErrInvalidSignature, member)
	}

	if info, handled, err := verifyCMSDetached(sigData, content, cfg, member); handled {
		return info, err
	}
	if info, handled, err := verifyPEMDetached(sigData, content, cfg, member); handled {
		return info, err
	}
	if certs, signature, ok := splitCertificateAndSignature(sigData); ok {
		info, err := verifyRawSignature(signature, content, certs, cfg, member)
		return info, err
	}
	return verifyRawSignature(sigData, content, nil, cfg, member)
}

func verifyPEMDetached(sigData, content []byte, cfg *verifyConfig, member string) (*SignatureInfo, bool, error) {
	if !bytes.Contains(sigData, []byte("-----BEGIN ")) {
		return nil, false, nil
	}

	var certs []*x509.Certificate
	var signatures [][]byte
	var cmsBlocks [][]byte
	rest := sigData
	for {
		block, next := pem.Decode(rest)
		if block == nil {
			break
		}
		rest = next
		switch {
		case block.Type == "CERTIFICATE", block.Type == "TRUSTED CERTIFICATE":
			cert, err := x509.ParseCertificate(block.Bytes)
			if err != nil {
				return nil, true, fmt.Errorf("%w: parse PEM certificate: %v", ErrInvalidSignature, err)
			}
			certs = append(certs, cert)
		case strings.Contains(strings.ToUpper(block.Type), "PKCS7"),
			strings.Contains(strings.ToUpper(block.Type), "CMS"):
			cmsBlocks = append(cmsBlocks, block.Bytes)
		case strings.Contains(strings.ToUpper(block.Type), "SIGNATURE"),
			strings.Contains(strings.ToUpper(block.Type), "RSA"):
			signatures = append(signatures, block.Bytes)
		default:
			// Be permissive with PEM labels used by test fixtures.
			signatures = append(signatures, block.Bytes)
		}
	}

	for _, cmsData := range cmsBlocks {
		if info, handled, err := verifyCMSDetached(cmsData, content, cfg, member); handled {
			return info, true, err
		}
		return nil, true, fmt.Errorf("%w: PEM block is not CMS SignedData", ErrInvalidSignature)
	}
	if len(signatures) == 0 {
		return nil, true, fmt.Errorf("%w: PEM signature has no signature block", ErrInvalidSignature)
	}
	for _, signature := range signatures {
		if info, handled, err := verifyCMSDetached(signature, content, cfg, member); handled && err == nil {
			return info, true, nil
		}
		info, err := verifyRawSignature(signature, content, certs, cfg, member)
		if err == nil {
			return info, true, nil
		}
	}
	// Give the caller a precise chain/signature failure.
	info, err := verifyRawSignature(signatures[0], content, certs, cfg, member)
	return info, true, err
}

func splitCertificateAndSignature(data []byte) ([]*x509.Certificate, []byte, bool) {
	var certs []*x509.Certificate
	rest := data
	var signature []byte

	for {
		elem, next, err := readDER(rest)
		if err != nil {
			break
		}
		cert, certErr := x509.ParseCertificate(elem.full)
		if certErr != nil {
			if elem.tag == 0x04 {
				signature = elem.content
			} else {
				signature = append([]byte(nil), rest...)
			}
			rest = nil
			break
		}
		certs = append(certs, cert)
		rest = next
		if len(rest) == 0 {
			break
		}
	}
	if len(certs) == 0 {
		return nil, nil, false
	}
	if len(signature) == 0 {
		signature = append([]byte(nil), rest...)
	}
	if len(signature) == 0 {
		return certs, nil, true
	}
	return certs, signature, true
}

func verifyRawSignature(signature, content []byte, certs []*x509.Certificate, cfg *verifyConfig, member string) (*SignatureInfo, error) {
	candidates := make([]*x509.Certificate, 0, len(certs)+len(cfg.certs)+1)
	if cfg.signer != nil {
		candidates = append(candidates, cfg.signer)
	}
	candidates = append(candidates, certs...)
	candidates = append(candidates, cfg.certs...)
	candidates = deduplicateCertificates(candidates)
	if len(candidates) == 0 {
		return nil, fmt.Errorf("%w: no signer certificate available for %s", ErrInvalidSignature, member)
	}

	var chainErr error
	var signatureErr error
	for _, cert := range candidates {
		publicKey, ok := cert.PublicKey.(*rsa.PublicKey)
		if !ok {
			continue
		}
		chain, err := verifyCertificateChain(cert, certs, cfg)
		if err != nil {
			if errors.Is(err, ErrNoTrustRoots) {
				return nil, err
			}
			chainErr = err
			continue
		}
		digest := sha256.Sum256(content)
		if err := rsa.VerifyPKCS1v15(publicKey, crypto.SHA256, digest[:], signature); err != nil {
			signatureErr = err
			continue
		}
		return &SignatureInfo{
			Member:        member,
			Algorithm:     "RSA-SHA256",
			SignerSubject: cert.Subject.String(),
			Chain:         chain,
		}, nil
	}
	if signatureErr != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrInvalidSignature, member, signatureErr)
	}
	if chainErr != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrUntrustedCertificate, member, chainErr)
	}
	return nil, fmt.Errorf("%w: no RSA signer certificate for %s", ErrInvalidSignature, member)
}

func verifyCertificateChain(leaf *x509.Certificate, embedded []*x509.Certificate, cfg *verifyConfig) ([]*x509.Certificate, error) {
	roots := cfg.trustPool()
	if roots == nil {
		return nil, fmt.Errorf("%w: cannot verify %q", ErrNoTrustRoots, leaf.Subject.String())
	}

	intermediates := cfg.intermediates
	if intermediates == nil {
		intermediates = x509.NewCertPool()
	}
	all := append([]*x509.Certificate(nil), embedded...)
	all = append(all, cfg.certs...)
	for _, cert := range all {
		if cert == nil {
			continue
		}
		if cert.Equal(leaf) {
			continue
		}
		intermediates.AddCert(cert)
	}

	chains, err := leaf.Verify(x509.VerifyOptions{
		Roots:         roots,
		Intermediates: intermediates,
		CurrentTime:   cfg.now(),
		KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageAny},
	})
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUntrustedCertificate, err)
	}
	if len(chains) == 0 || len(chains[0]) == 0 {
		return nil, fmt.Errorf("%w: empty verified chain", ErrUntrustedCertificate)
	}
	return chains[0], nil
}

func deduplicateCertificates(certs []*x509.Certificate) []*x509.Certificate {
	seen := make(map[string]struct{}, len(certs))
	out := make([]*x509.Certificate, 0, len(certs))
	for _, cert := range certs {
		if cert == nil {
			continue
		}
		key := string(cert.Raw)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, cert)
	}
	return out
}
