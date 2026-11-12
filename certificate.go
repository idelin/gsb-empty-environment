package netsec

import (
	"bytes"
	"crypto/sha256"
	"crypto/x509"
	"encoding/asn1"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

func decodeCertificates(value string) ([]*x509.Certificate, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, errors.New("empty certificate value")
	}

	var certificates []*x509.Certificate
	remaining := []byte(value)
	for {
		block, rest := pem.Decode(remaining)
		if block == nil {
			if len(bytes.TrimSpace(remaining)) != 0 && len(certificates) == 0 {
				break
			}
			if len(bytes.TrimSpace(remaining)) != 0 {
				return nil, errors.New("certificate value contains trailing non-PEM data")
			}
			break
		}
		if block.Type != "CERTIFICATE" {
			return nil, fmt.Errorf("unexpected PEM block %q", block.Type)
		}
		parsed, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("parse PEM certificate: %w", err)
		}
		certificates = append(certificates, parsed)
		remaining = rest
	}
	if len(certificates) != 0 {
		return certificates, nil
	}

	der, err := base64.StdEncoding.Strict().DecodeString(value)
	if err != nil {
		der, err = base64.RawStdEncoding.Strict().DecodeString(value)
	}
	if err != nil {
		der, err = base64.URLEncoding.Strict().DecodeString(value)
	}
	if err != nil {
		der, err = base64.RawURLEncoding.Strict().DecodeString(value)
	}
	if err != nil {
		return nil, fmt.Errorf("certificate is neither PEM nor base64 DER: %w", err)
	}
	parsed, err := x509.ParseCertificates(der)
	if err != nil {
		return nil, fmt.Errorf("parse DER certificate: %w", err)
	}
	return parsed, nil
}

func publicKeyFingerprint(certificate *x509.Certificate) (string, error) {
	encoded, err := x509.MarshalPKIXPublicKey(certificate.PublicKey)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func verifyCertificateChain(leaf *x509.Certificate, chain []*x509.Certificate, policy Policy) error {
	if leaf == nil {
		return errors.New("netsec: signing certificate is missing")
	}
	roots := policy.trustedRoots()
	if roots == nil {
		return errors.New("netsec: policy has no trusted certificate roots")
	}

	intermediates := x509.NewCertPool()
	for _, certificate := range chain {
		if certificate == nil || bytes.Equal(certificate.Raw, leaf.Raw) {
			continue
		}
		intermediates.AddCert(certificate)
	}

	_, err := leaf.Verify(x509.VerifyOptions{
		Roots:         roots,
		Intermediates: intermediates,
		CurrentTime:   policy.verificationTime(),
		KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageAny},
	})
	if err != nil {
		return fmt.Errorf("netsec: signing certificate chain is not valid at policy time: %w", err)
	}

	if leaf.KeyUsage != 0 && leaf.KeyUsage&x509.KeyUsageDigitalSignature == 0 {
		return errors.New("netsec: signing certificate is not valid for digital signatures")
	}
	return nil
}

func certificateIdentity(certificate *x509.Certificate, oid asn1.ObjectIdentifier) (string, error) {
	if certificate == nil {
		return "", errors.New("netsec: signing certificate is missing")
	}
	if len(oid) == 0 {
		return "", errors.New("netsec: identity extension OID is empty")
	}

	var extensionValue []byte
	for _, extension := range certificate.Extensions {
		if extension.Id.Equal(oid) {
			if extensionValue != nil {
				return "", fmt.Errorf("netsec: certificate contains duplicate identity extension %s", oid)
			}
			extensionValue = extension.Value
		}
	}
	if extensionValue == nil {
		return "", fmt.Errorf("netsec: certificate has no identity extension %s", oid)
	}

	identity, err := decodeIdentityExtension(extensionValue)
	if err != nil {
		return "", fmt.Errorf("netsec: decode certificate identity extension %s: %w", oid, err)
	}
	if strings.TrimSpace(identity) == "" {
		return "", fmt.Errorf("netsec: certificate identity extension %s is empty", oid)
	}
	return identity, nil
}

func decodeIdentityExtension(value []byte) (string, error) {
	var identity string
	if rest, err := asn1.Unmarshal(value, &identity); err == nil && len(rest) == 0 {
		return identity, nil
	}

	var octets []byte
	if rest, err := asn1.Unmarshal(value, &octets); err == nil && len(rest) == 0 {
		var nested string
		if nestedRest, nestedErr := asn1.Unmarshal(octets, &nested); nestedErr == nil && len(nestedRest) == 0 {
			return nested, nil
		}
	}

	// Some private PKIs put the UTF-8 identity directly in the extension
	// value instead of wrapping it in an ASN.1 string.  It is still read only
	// from the caller-selected extension, never from Subject/SAN/CN.
	if utf8.Valid(value) && bytes.IndexFunc(value, func(r rune) bool { return r < 0x20 }) < 0 {
		return string(value), nil
	}
	return "", errors.New("extension value is not a valid identity string")
}
