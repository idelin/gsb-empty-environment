package netsec

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/asn1"
	"encoding/binary"
	"errors"
	"fmt"
)

const (
	// ExtensionCertificateAuthorities is the TLS extension type assigned to
	// certificate_authorities (RFC 8446, Section 4.2.4).
	ExtensionCertificateAuthorities uint16 = 47

	tlsRecordTypeHandshake         = 22
	tlsHandshakeClientHello        = 1
	tlsHandshakeCertificateRequest = 13
)

var (
	// ErrMalformedCertificateAuthorities is returned when the
	// certificate_authorities extension cannot be decoded.
	ErrMalformedCertificateAuthorities = errors.New("netsec: malformed certificate_authorities extension")

	// ErrIssuerNotAuthorized is returned when the leaf certificate's issuer is
	// not one of the distinguished names in certificate_authorities.
	ErrIssuerNotAuthorized = errors.New("netsec: leaf certificate issuer is not in certificate_authorities")

	// ErrMissingLeafCertificate is returned when no leaf certificate is given.
	ErrMissingLeafCertificate = errors.New("netsec: missing leaf certificate")

	// ErrNoUsableCertificate is returned when none of the candidate
	// certificates may be used for the current handshake.
	ErrNoUsableCertificate = errors.New("netsec: no certificate usable for certificate_authorities")

	// ErrAuthoritiesUnavailable is returned when the peer sent a
	// certificate_authorities extension but its contents were not captured.
	// The handshake fails closed rather than ignoring the extension.
	ErrAuthoritiesUnavailable = errors.New("netsec: certificate_authorities extension was sent but its contents are unavailable")
)

// Authorities is the ordered list of DER-encoded X.501 Distinguished Names
// carried by a certificate_authorities extension.
//
// A nil Authorities means the extension was not sent, so no issuer filtering
// applies. A non-nil, empty Authorities means the extension was sent with an
// empty list; no leaf certificate can satisfy it.
type Authorities [][]byte

// CertificateAuthorities is an alias for Authorities.
type CertificateAuthorities = Authorities

// Extension is the decoded certificate_authorities extension, including
// whether the extension was actually present.
type Extension struct {
	Present     bool
	Authorities Authorities
}

// CertificateAuthoritiesExtension is an alias for Extension.
type CertificateAuthoritiesExtension = Extension

// Contains reports whether issuer exactly matches one of the DER-encoded
// distinguished names in a.
//
// Matching is byte-for-byte on the DER encoding, as required for X.509 name
// chaining. An empty issuer never matches.
func (a Authorities) Contains(issuer []byte) bool {
	if len(issuer) == 0 {
		return false
	}
	for _, authority := range a {
		if len(authority) != 0 && bytes.Equal(authority, issuer) {
			return true
		}
	}
	return false
}

// Clone returns a deep copy of a.
func (a Authorities) Clone() Authorities {
	if a == nil {
		return nil
	}
	out := make(Authorities, len(a))
	for i, authority := range a {
		out[i] = append([]byte(nil), authority...)
	}
	return out
}

// AllowsIssuer reports whether issuer may be used under e. When the extension
// was absent, every issuer is allowed.
func (e Extension) AllowsIssuer(issuer []byte) bool {
	if !e.Present {
		return true
	}
	return e.Authorities.Contains(issuer)
}

// Validate checks that leaf's issuer is acceptable under e. If the extension
// was absent, validation succeeds. Path validation is deliberately not done
// here; use VerifyChain for that.
func (e Extension) Validate(leaf *x509.Certificate) error {
	if !e.Present {
		return nil
	}
	authorities := e.Authorities
	if authorities == nil {
		authorities = Authorities{}
	}
	return authorities.ValidateLeaf(leaf)
}

// ValidateLeaf checks that leaf's issuer occurs in a. A nil a means the
// certificate_authorities extension was absent and therefore does not
// restrict leaf.
func (a Authorities) ValidateLeaf(leaf *x509.Certificate) error {
	issuer, err := issuerDER(leaf)
	if err != nil {
		return err
	}
	if a == nil {
		return nil
	}
	if a.Contains(issuer) {
		return nil
	}
	return &IssuerNotAuthorizedError{
		Issuer:      append([]byte(nil), issuer...),
		Authorities: a.Clone(),
	}
}

// IssuerNotAuthorizedError reports a leaf certificate whose issuer is absent
// from certificate_authorities.
type IssuerNotAuthorizedError struct {
	Issuer      []byte
	Authorities Authorities
}

func (e *IssuerNotAuthorizedError) Error() string {
	return fmt.Sprintf("%v (issuer %x, %d authorities)", ErrIssuerNotAuthorized, e.Issuer, len(e.Authorities))
}

func (e *IssuerNotAuthorizedError) Unwrap() error {
	return ErrIssuerNotAuthorized
}

// ParseCertificateAuthorities decodes the payload of a TLS
// certificate_authorities extension. The payload is the contents of the
// extension_data field, not the two-byte extension type and two-byte length.
//
// A valid empty vector is returned as a non-nil, zero-length Authorities so
// callers can distinguish it from an absent extension.
func ParseCertificateAuthorities(data []byte) (Authorities, error) {
	if len(data) < 2 {
		return nil, fmt.Errorf("%w: payload is %d bytes", ErrMalformedCertificateAuthorities, len(data))
	}
	vectorLen := int(binary.BigEndian.Uint16(data[:2]))
	if vectorLen != len(data)-2 {
		return nil, fmt.Errorf("%w: authority vector length is %d, payload contains %d", ErrMalformedCertificateAuthorities, vectorLen, len(data)-2)
	}

	authorities := Authorities{}
	rest := data[2:]
	for len(rest) > 0 {
		if len(rest) < 2 {
			return nil, fmt.Errorf("%w: truncated distinguished name length", ErrMalformedCertificateAuthorities)
		}
		nameLen := int(binary.BigEndian.Uint16(rest[:2]))
		rest = rest[2:]
		if nameLen == 0 {
			return nil, fmt.Errorf("%w: empty distinguished name", ErrMalformedCertificateAuthorities)
		}
		if nameLen > len(rest) {
			return nil, fmt.Errorf("%w: distinguished name length %d exceeds remaining %d bytes", ErrMalformedCertificateAuthorities, nameLen, len(rest))
		}
		name := append([]byte(nil), rest[:nameLen]...)
		if err := validateDistinguishedName(name); err != nil {
			return nil, err
		}
		authorities = append(authorities, name)
		rest = rest[nameLen:]
	}
	return authorities, nil
}

// ParseCertificateAuthoritiesExtension is an alias for
// ParseCertificateAuthorities. It also accepts a single complete extension
// (extension_type, extension_data length, and payload) or a TLS extension
// block containing certificate_authorities.
func ParseCertificateAuthoritiesExtension(data []byte) (Authorities, error) {
	if authorities, err := ParseCertificateAuthorities(data); err == nil {
		return authorities, nil
	}
	if len(data) >= 4 && binary.BigEndian.Uint16(data[:2]) == ExtensionCertificateAuthorities {
		extensionLen := int(binary.BigEndian.Uint16(data[2:4]))
		if extensionLen == len(data)-4 {
			return ParseCertificateAuthorities(data[4:])
		}
	}
	if extension, err := parseAuthoritiesExtension(data); err == nil && extension.Present {
		return extension.Authorities, nil
	}
	return ParseCertificateAuthorities(data)
}

// ExtractCertificateAuthorities is an alias for
// ParseCertificateAuthorities.
func ExtractCertificateAuthorities(data []byte) (Authorities, error) {
	return ParseCertificateAuthorities(data)
}

// ParseAuthorities is an alias for ParseCertificateAuthorities.
func ParseAuthorities(data []byte) (Authorities, error) {
	return ParseCertificateAuthorities(data)
}

// CertificateAuthoritiesFromExtension is an alias for
// ParseCertificateAuthoritiesExtension.
func CertificateAuthoritiesFromExtension(data []byte) (Authorities, error) {
	return ParseCertificateAuthoritiesExtension(data)
}

// MarshalCertificateAuthorities encodes authorities as the payload of a TLS
// certificate_authorities extension.
func MarshalCertificateAuthorities(authorities Authorities) ([]byte, error) {
	var body bytes.Buffer
	for _, authority := range authorities {
		if len(authority) == 0 {
			return nil, fmt.Errorf("%w: empty distinguished name", ErrMalformedCertificateAuthorities)
		}
		if len(authority) > 0xffff {
			return nil, fmt.Errorf("%w: distinguished name is too large", ErrMalformedCertificateAuthorities)
		}
		if err := validateDistinguishedName(authority); err != nil {
			return nil, err
		}
		var length [2]byte
		binary.BigEndian.PutUint16(length[:], uint16(len(authority)))
		body.Write(length[:])
		body.Write(authority)
	}
	if body.Len() > 0xffff {
		return nil, fmt.Errorf("%w: authority vector is too large", ErrMalformedCertificateAuthorities)
	}
	out := make([]byte, 2+body.Len())
	binary.BigEndian.PutUint16(out[:2], uint16(body.Len()))
	copy(out[2:], body.Bytes())
	return out, nil
}

// ClientHello is the subset of a TLS ClientHello relevant to certificate
// selection.
type ClientHello struct {
	// Version is the legacy_version field.
	Version uint16

	// ServerName is the SNI host name, if one was sent.
	ServerName string

	// Authorities records the certificate_authorities extension.
	Authorities Extension
}

// ParseClientHello decodes a ClientHello. data may be a bare handshake
// message or one or more complete TLS handshake records containing it.
func ParseClientHello(data []byte) (*ClientHello, error) {
	body, err := handshakeBody(data, tlsHandshakeClientHello)
	if err != nil {
		return nil, err
	}
	hello, err := parseClientHelloBody(body)
	if err != nil {
		return nil, err
	}
	return hello, nil
}

// ParseClientHelloCertificateAuthorities returns the
// certificate_authorities extension from a ClientHello. It also accepts a bare
// extension payload, in which case Present is true.
func ParseClientHelloCertificateAuthorities(data []byte) (Extension, error) {
	if looksLikeHandshake(data) {
		hello, err := ParseClientHello(data)
		if err == nil {
			return hello.Authorities, nil
		}
	}
	authorities, err := ParseCertificateAuthoritiesExtension(data)
	if err != nil {
		return Extension{}, err
	}
	return Extension{Present: true, Authorities: authorities}, nil
}

// ClientHelloCertificateAuthorities is an alias for
// ParseClientHelloCertificateAuthorities.
func ClientHelloCertificateAuthorities(data []byte) (Extension, error) {
	return ParseClientHelloCertificateAuthorities(data)
}

// ParseCertificateRequestCertificateAuthorities decodes the
// certificate_authorities extension from a TLS 1.3 CertificateRequest, or the
// certificate_authorities field from a TLS 1.2 CertificateRequest. data may be
// a bare handshake message or complete TLS handshake records.
func ParseCertificateRequestCertificateAuthorities(data []byte) (Extension, error) {
	body, err := handshakeBody(data, tlsHandshakeCertificateRequest)
	if err != nil {
		return Extension{}, err
	}
	if extension, err := parseTLS13CertificateRequest(body); err == nil {
		return extension, nil
	}
	return parseTLS12CertificateRequest(body)
}

// CertificateRequestCertificateAuthorities is an alias for
// ParseCertificateRequestCertificateAuthorities.
func CertificateRequestCertificateAuthorities(data []byte) (Extension, error) {
	return ParseCertificateRequestCertificateAuthorities(data)
}

// AuthoritiesFromClientHelloBytes is an alias for
// ParseClientHelloCertificateAuthorities.
func AuthoritiesFromClientHelloBytes(data []byte) (Extension, error) {
	return ParseClientHelloCertificateAuthorities(data)
}

func parseClientHelloBody(body []byte) (*ClientHello, error) {
	const fixedPrefix = 2 + 32
	if len(body) < fixedPrefix+1 {
		return nil, fmt.Errorf("%w: truncated ClientHello", ErrMalformedCertificateAuthorities)
	}
	hello := &ClientHello{Version: binary.BigEndian.Uint16(body[:2])}
	offset := fixedPrefix

	sessionIDLen := int(body[offset])
	offset++
	if sessionIDLen > len(body)-offset {
		return nil, fmt.Errorf("%w: truncated ClientHello session ID", ErrMalformedCertificateAuthorities)
	}
	offset += sessionIDLen

	if len(body)-offset < 2 {
		return nil, fmt.Errorf("%w: truncated ClientHello cipher suites", ErrMalformedCertificateAuthorities)
	}
	cipherSuitesLen := int(binary.BigEndian.Uint16(body[offset : offset+2]))
	offset += 2
	if cipherSuitesLen > len(body)-offset {
		return nil, fmt.Errorf("%w: truncated ClientHello cipher suites", ErrMalformedCertificateAuthorities)
	}
	offset += cipherSuitesLen

	if len(body)-offset < 1 {
		return nil, fmt.Errorf("%w: truncated ClientHello compression methods", ErrMalformedCertificateAuthorities)
	}
	compressionLen := int(body[offset])
	offset++
	if compressionLen > len(body)-offset {
		return nil, fmt.Errorf("%w: truncated ClientHello compression methods", ErrMalformedCertificateAuthorities)
	}
	offset += compressionLen

	// Extensions are optional in older ClientHello messages.
	if offset == len(body) {
		return hello, nil
	}
	if len(body)-offset < 2 {
		return nil, fmt.Errorf("%w: truncated ClientHello extensions", ErrMalformedCertificateAuthorities)
	}
	extensionsLen := int(binary.BigEndian.Uint16(body[offset : offset+2]))
	offset += 2
	if extensionsLen != len(body)-offset {
		return nil, fmt.Errorf("%w: ClientHello extension length is %d, body contains %d", ErrMalformedCertificateAuthorities, extensionsLen, len(body)-offset)
	}

	var seenAuthorities bool
	for extensions := body[offset:]; len(extensions) > 0; {
		if len(extensions) < 4 {
			return nil, fmt.Errorf("%w: truncated ClientHello extension header", ErrMalformedCertificateAuthorities)
		}
		extensionType := binary.BigEndian.Uint16(extensions[:2])
		extensionLen := int(binary.BigEndian.Uint16(extensions[2:4]))
		extensions = extensions[4:]
		if extensionLen > len(extensions) {
			return nil, fmt.Errorf("%w: truncated ClientHello extension %d", ErrMalformedCertificateAuthorities, extensionType)
		}
		extensionData := extensions[:extensionLen]
		extensions = extensions[extensionLen:]

		switch extensionType {
		case 0:
			name, err := parseServerName(extensionData)
			if err == nil {
				hello.ServerName = name
			}
		case ExtensionCertificateAuthorities:
			if seenAuthorities {
				return nil, fmt.Errorf("%w: duplicate certificate_authorities extension", ErrMalformedCertificateAuthorities)
			}
			seenAuthorities = true
			authorities, err := ParseCertificateAuthorities(extensionData)
			if err != nil {
				return nil, err
			}
			hello.Authorities = Extension{Present: true, Authorities: authorities}
		}
	}
	return hello, nil
}

func parseServerName(data []byte) (string, error) {
	if len(data) < 2 {
		return "", fmt.Errorf("%w: truncated server_name extension", ErrMalformedCertificateAuthorities)
	}
	listLen := int(binary.BigEndian.Uint16(data[:2]))
	if listLen != len(data)-2 {
		return "", fmt.Errorf("%w: invalid server_name list length", ErrMalformedCertificateAuthorities)
	}
	list := data[2:]
	for len(list) > 0 {
		if len(list) < 3 {
			return "", fmt.Errorf("%w: truncated server_name entry", ErrMalformedCertificateAuthorities)
		}
		nameType := list[0]
		nameLen := int(binary.BigEndian.Uint16(list[1:3]))
		list = list[3:]
		if nameLen > len(list) {
			return "", fmt.Errorf("%w: truncated server_name value", ErrMalformedCertificateAuthorities)
		}
		if nameType == 0 {
			return string(list[:nameLen]), nil
		}
		list = list[nameLen:]
	}
	return "", nil
}

func parseTLS13CertificateRequest(body []byte) (Extension, error) {
	if len(body) < 1 {
		return Extension{}, fmt.Errorf("%w: truncated CertificateRequest", ErrMalformedCertificateAuthorities)
	}
	contextLen := int(body[0])
	offset := 1
	if contextLen > len(body)-offset {
		return Extension{}, fmt.Errorf("%w: truncated CertificateRequest context", ErrMalformedCertificateAuthorities)
	}
	offset += contextLen
	if len(body)-offset < 2 {
		return Extension{}, fmt.Errorf("%w: truncated CertificateRequest extensions", ErrMalformedCertificateAuthorities)
	}
	extensionsLen := int(binary.BigEndian.Uint16(body[offset : offset+2]))
	offset += 2
	if extensionsLen != len(body)-offset {
		return Extension{}, fmt.Errorf("%w: invalid CertificateRequest extension length", ErrMalformedCertificateAuthorities)
	}
	extension, err := parseAuthoritiesExtension(body[offset:])
	if err != nil {
		return Extension{}, err
	}
	return extension, nil
}

func parseTLS12CertificateRequest(body []byte) (Extension, error) {
	if len(body) < 1 {
		return Extension{}, fmt.Errorf("%w: truncated CertificateRequest", ErrMalformedCertificateAuthorities)
	}
	certificateTypesLen := int(body[0])
	offset := 1
	if certificateTypesLen > len(body)-offset {
		return Extension{}, fmt.Errorf("%w: truncated CertificateRequest certificate types", ErrMalformedCertificateAuthorities)
	}
	offset += certificateTypesLen
	if len(body)-offset < 2 {
		return Extension{}, fmt.Errorf("%w: truncated CertificateRequest signature algorithms", ErrMalformedCertificateAuthorities)
	}
	signatureAlgorithmsLen := int(binary.BigEndian.Uint16(body[offset : offset+2]))
	offset += 2
	if signatureAlgorithmsLen > len(body)-offset {
		return Extension{}, fmt.Errorf("%w: truncated CertificateRequest signature algorithms", ErrMalformedCertificateAuthorities)
	}
	offset += signatureAlgorithmsLen
	if offset == len(body) {
		return Extension{}, nil
	}
	if len(body)-offset < 2 {
		return Extension{}, fmt.Errorf("%w: truncated CertificateRequest certificate_authorities", ErrMalformedCertificateAuthorities)
	}
	authoritiesLen := int(binary.BigEndian.Uint16(body[offset : offset+2]))
	offset += 2
	if authoritiesLen != len(body)-offset {
		return Extension{}, fmt.Errorf("%w: invalid CertificateRequest certificate_authorities length", ErrMalformedCertificateAuthorities)
	}
	authorities, err := ParseCertificateAuthorities(body[offset-2:])
	if err != nil {
		return Extension{}, err
	}
	return Extension{Present: true, Authorities: authorities}, nil
}

func parseAuthoritiesExtension(extensions []byte) (Extension, error) {
	var result Extension
	for len(extensions) > 0 {
		if len(extensions) < 4 {
			return Extension{}, fmt.Errorf("%w: truncated extension header", ErrMalformedCertificateAuthorities)
		}
		extensionType := binary.BigEndian.Uint16(extensions[:2])
		extensionLen := int(binary.BigEndian.Uint16(extensions[2:4]))
		extensions = extensions[4:]
		if extensionLen > len(extensions) {
			return Extension{}, fmt.Errorf("%w: truncated extension %d", ErrMalformedCertificateAuthorities, extensionType)
		}
		extensionData := extensions[:extensionLen]
		extensions = extensions[extensionLen:]
		if extensionType != ExtensionCertificateAuthorities {
			continue
		}
		if result.Present {
			return Extension{}, fmt.Errorf("%w: duplicate certificate_authorities extension", ErrMalformedCertificateAuthorities)
		}
		authorities, err := ParseCertificateAuthorities(extensionData)
		if err != nil {
			return Extension{}, err
		}
		result = Extension{Present: true, Authorities: authorities}
	}
	return result, nil
}

func handshakeBody(data []byte, wantType byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("%w: empty handshake data", ErrMalformedCertificateAuthorities)
	}

	var handshake []byte
	if data[0] == tlsRecordTypeHandshake || looksLikeTLSRecord(data) {
		for len(data) > 0 {
			if len(data) < 5 {
				return nil, fmt.Errorf("%w: truncated TLS record", ErrMalformedCertificateAuthorities)
			}
			contentType := data[0]
			recordLen := int(binary.BigEndian.Uint16(data[3:5]))
			if recordLen > len(data)-5 {
				return nil, fmt.Errorf("%w: truncated TLS record payload", ErrMalformedCertificateAuthorities)
			}
			if contentType == tlsRecordTypeHandshake {
				handshake = append(handshake, data[5:5+recordLen]...)
			}
			data = data[5+recordLen:]
		}
	} else {
		handshake = data
	}

	for len(handshake) > 0 {
		if len(handshake) < 4 {
			return nil, fmt.Errorf("%w: truncated handshake message", ErrMalformedCertificateAuthorities)
		}
		messageType := handshake[0]
		messageLen := int(handshake[1])<<16 | int(handshake[2])<<8 | int(handshake[3])
		handshake = handshake[4:]
		if messageLen > len(handshake) {
			return nil, fmt.Errorf("%w: truncated handshake message body", ErrMalformedCertificateAuthorities)
		}
		body := handshake[:messageLen]
		handshake = handshake[messageLen:]
		if messageType == wantType {
			return body, nil
		}
	}
	return nil, fmt.Errorf("%w: handshake message type %d not found", ErrMalformedCertificateAuthorities, wantType)
}

func looksLikeHandshake(data []byte) bool {
	if len(data) == 0 {
		return false
	}
	if data[0] == tlsHandshakeClientHello || data[0] == tlsHandshakeCertificateRequest {
		return true
	}
	return data[0] == tlsRecordTypeHandshake || looksLikeTLSRecord(data)
}

func looksLikeTLSRecord(data []byte) bool {
	return len(data) >= 3 &&
		data[0] >= 20 && data[0] <= 24 &&
		data[1] == 0x03 && data[2] <= 0x04
}

func validateDistinguishedName(name []byte) error {
	var value asn1.RawValue
	rest, err := asn1.Unmarshal(name, &value)
	if err != nil {
		return fmt.Errorf("%w: invalid distinguished name: %v", ErrMalformedCertificateAuthorities, err)
	}
	if len(rest) != 0 {
		return fmt.Errorf("%w: trailing data after distinguished name", ErrMalformedCertificateAuthorities)
	}
	if value.Class != asn1.ClassUniversal || value.Tag != asn1.TagSequence || !value.IsCompound {
		return fmt.Errorf("%w: distinguished name is not a DER SEQUENCE", ErrMalformedCertificateAuthorities)
	}
	return nil
}

func issuerDER(leaf *x509.Certificate) ([]byte, error) {
	if leaf == nil {
		return nil, ErrMissingLeafCertificate
	}
	if len(leaf.RawIssuer) != 0 {
		return leaf.RawIssuer, nil
	}
	issuer, err := asn1.Marshal(leaf.Issuer.ToRDNSequence())
	if err != nil {
		return nil, fmt.Errorf("%w: cannot encode leaf issuer: %v", ErrMalformedCertificateAuthorities, err)
	}
	return issuer, nil
}

// FilterCertificates returns the certificates whose leaf issuer is permitted
// by authorities, preserving order. A nil authorities returns all candidates.
// A non-nil empty authorities returns ErrNoUsableCertificate.
func FilterCertificates(certificates []tls.Certificate, authorities Authorities) ([]tls.Certificate, error) {
	if authorities == nil {
		return append([]tls.Certificate(nil), certificates...), nil
	}
	filtered := make([]tls.Certificate, 0, len(certificates))
	var firstIssuer []byte
	for i := range certificates {
		leaf, err := certificateLeaf(&certificates[i])
		if err != nil {
			return nil, err
		}
		issuer, err := issuerDER(leaf)
		if err != nil {
			return nil, err
		}
		if firstIssuer == nil {
			firstIssuer = append([]byte(nil), issuer...)
		}
		if authorities.Contains(issuer) {
			filtered = append(filtered, certificates[i])
		}
	}
	if len(filtered) == 0 {
		if firstIssuer != nil {
			return nil, errors.Join(ErrNoUsableCertificate, &IssuerNotAuthorizedError{
				Issuer:      firstIssuer,
				Authorities: authorities.Clone(),
			})
		}
		return nil, ErrNoUsableCertificate
	}
	return filtered, nil
}

// FilterCertificatesByAuthorities is an alias for FilterCertificates.
func FilterCertificatesByAuthorities(certificates []tls.Certificate, authorities Authorities) ([]tls.Certificate, error) {
	return FilterCertificates(certificates, authorities)
}

// SelectCertificate chooses the first certificate whose leaf issuer is in
// authorities and which is otherwise supported by hello. When authorities is
// nil, this follows crypto/tls's normal selection rules. When the extension is
// present, there is no fallback to a non-matching certificate.
func SelectCertificate(hello *tls.ClientHelloInfo, certificates []tls.Certificate, authorities Authorities) (*tls.Certificate, error) {
	if authorities == nil {
		for i := range certificates {
			if helloSupports(hello, &certificates[i]) {
				return &certificates[i], nil
			}
		}
		if len(certificates) > 0 {
			return &certificates[0], nil
		}
		return nil, ErrNoUsableCertificate
	}

	var firstIssuer []byte
	for i := range certificates {
		certificate := &certificates[i]
		leaf, err := certificateLeaf(certificate)
		if err != nil {
			return nil, err
		}
		issuer, err := issuerDER(leaf)
		if err != nil {
			return nil, err
		}
		if firstIssuer == nil {
			firstIssuer = append([]byte(nil), issuer...)
		}
		if !authorities.Contains(issuer) {
			continue
		}
		if helloSupports(hello, certificate) {
			return certificate, nil
		}
	}
	if firstIssuer == nil {
		return nil, ErrNoUsableCertificate
	}
	return nil, errors.Join(ErrNoUsableCertificate, &IssuerNotAuthorizedError{
		Issuer:      firstIssuer,
		Authorities: authorities.Clone(),
	})
}

// SelectCertificateForAuthorities is an alias for SelectCertificate.
func SelectCertificateForAuthorities(hello *tls.ClientHelloInfo, certificates []tls.Certificate, authorities Authorities) (*tls.Certificate, error) {
	return SelectCertificate(hello, certificates, authorities)
}

func helloSupports(hello *tls.ClientHelloInfo, certificate *tls.Certificate) bool {
	if hello == nil || len(hello.SupportedVersions) == 0 {
		return true
	}
	return hello.SupportsCertificate(certificate) == nil
}

func certificateLeaf(certificate *tls.Certificate) (*x509.Certificate, error) {
	if certificate == nil {
		return nil, ErrMissingLeafCertificate
	}
	if certificate.Leaf != nil {
		return certificate.Leaf, nil
	}
	if len(certificate.Certificate) == 0 {
		return nil, ErrMissingLeafCertificate
	}
	leaf, err := x509.ParseCertificate(certificate.Certificate[0])
	if err != nil {
		return nil, fmt.Errorf("netsec: parse leaf certificate: %w", err)
	}
	return leaf, nil
}
