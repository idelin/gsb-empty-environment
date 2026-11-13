package netsec

import (
	"crypto/x509"
	"encoding/asn1"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

// Fulcio private extension OIDs. The 1.3.6.1.4.1.57264.1.x namespace is
// reserved for Fulcio-issued identity claims.
var (
	OIDIssuerLegacy           = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 57264, 1, 1}
	OIDIssuer                 = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 57264, 1, 8}
	OIDBuildSignerURI         = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 57264, 1, 9}
	OIDSourceRepositoryURI    = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 57264, 1, 12}
	OIDSourceRepositoryDigest = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 57264, 1, 13}
	OIDSourceRepositoryRef    = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 57264, 1, 14}
	OIDBuildTrigger           = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 57264, 1, 20}
)

// Field identifies one of the identity claims carried by a Fulcio certificate.
type Field string

const (
	FieldIssuer                 Field = "issuer"
	FieldBuildSignerURI         Field = "build-signer-uri"
	FieldSourceRepositoryURI    Field = "source-repository-uri"
	FieldSourceRepositoryDigest Field = "source-repository-digest"
	FieldSourceRepositoryRef    Field = "source-repository-ref"
	FieldBuildTrigger           Field = "build-trigger"
)

// Identity is the set of Fulcio claims extracted from a certificate.
//
// Issuer is resolved from OID 1.3.6.1.4.1.57264.1.8 when present. The legacy
// issuer OID 1.3.6.1.4.1.57264.1.1 is used only as a fallback when .1.8 is
// absent, and only after proving that the two claims do not conflict.
type Identity struct {
	Issuer                 string
	BuildSignerURI         string
	SourceRepositoryURI    string
	SourceRepositoryDigest string
	SourceRepositoryRef    string
	BuildTrigger           string
}

// Errors returned by ExtractIdentity and VerifyIdentity. Callers can use
// errors.Is to distinguish the failure classes.
var (
	ErrNilCertificate       = errors.New("netsec: nil certificate")
	ErrDuplicateExtension   = errors.New("netsec: duplicate Fulcio identity extension")
	ErrMalformedExtension   = errors.New("netsec: malformed Fulcio identity extension")
	ErrLegacyIssuerMismatch = errors.New("netsec: legacy and current issuer extensions disagree")
	ErrSANMismatch          = errors.New("netsec: certificate SAN disagrees with build signer URI")
	ErrMissingExtension     = errors.New("netsec: required Fulcio identity extension is missing")
	ErrPolicyMismatch       = errors.New("netsec: certificate identity does not satisfy policy")
	ErrRefNotTag            = errors.New("netsec: source repository ref is not a tag")
	ErrEmptyPolicy          = errors.New("netsec: identity policy has no requirements")
	ErrInvalidPolicy        = errors.New("netsec: invalid identity policy")
)

// ExtractIdentity reads the Fulcio identity extensions from cert and checks
// their internal consistency.
//
// Extraction is intentionally strict: duplicate identity extensions, malformed
// .1.8 DER, empty claims, conflicting legacy and current issuers, and a
// subjectAltName that disagrees with the .1.9 build signer URI are all
// rejected. Unknown extensions are ignored.
func ExtractIdentity(cert *x509.Certificate) (Identity, error) {
	if cert == nil {
		return Identity{}, ErrNilCertificate
	}

	var (
		identity        Identity
		legacyIssuer    string
		hasLegacyIssuer bool
	)
	seen := make(map[string]bool)

	record := func(oid asn1.ObjectIdentifier) error {
		key := oid.String()
		if seen[key] {
			return fmt.Errorf("%w: %s", ErrDuplicateExtension, key)
		}
		seen[key] = true
		return nil
	}

	for _, ext := range cert.Extensions {
		switch {
		case ext.Id.Equal(OIDIssuerLegacy):
			if err := record(ext.Id); err != nil {
				return Identity{}, err
			}
			value, err := rawClaim(FieldIssuer, ext.Id, ext.Value)
			if err != nil {
				return Identity{}, err
			}
			legacyIssuer = value
			hasLegacyIssuer = true

		case ext.Id.Equal(OIDIssuer):
			if err := record(ext.Id); err != nil {
				return Identity{}, err
			}
			value, err := parseDERUTF8String(ext.Id, ext.Value)
			if err != nil {
				return Identity{}, err
			}
			identity.Issuer = value

		case ext.Id.Equal(OIDBuildSignerURI):
			if err := record(ext.Id); err != nil {
				return Identity{}, err
			}
			value, err := rawClaim(FieldBuildSignerURI, ext.Id, ext.Value)
			if err != nil {
				return Identity{}, err
			}
			identity.BuildSignerURI = value

		case ext.Id.Equal(OIDSourceRepositoryURI):
			if err := record(ext.Id); err != nil {
				return Identity{}, err
			}
			value, err := rawClaim(FieldSourceRepositoryURI, ext.Id, ext.Value)
			if err != nil {
				return Identity{}, err
			}
			identity.SourceRepositoryURI = value

		case ext.Id.Equal(OIDSourceRepositoryDigest):
			if err := record(ext.Id); err != nil {
				return Identity{}, err
			}
			value, err := rawClaim(FieldSourceRepositoryDigest, ext.Id, ext.Value)
			if err != nil {
				return Identity{}, err
			}
			identity.SourceRepositoryDigest = value

		case ext.Id.Equal(OIDSourceRepositoryRef):
			if err := record(ext.Id); err != nil {
				return Identity{}, err
			}
			value, err := rawClaim(FieldSourceRepositoryRef, ext.Id, ext.Value)
			if err != nil {
				return Identity{}, err
			}
			identity.SourceRepositoryRef = value

		case ext.Id.Equal(OIDBuildTrigger):
			if err := record(ext.Id); err != nil {
				return Identity{}, err
			}
			value, err := rawClaim(FieldBuildTrigger, ext.Id, ext.Value)
			if err != nil {
				return Identity{}, err
			}
			identity.BuildTrigger = value
		}
	}

	switch {
	case identity.Issuer != "" && hasLegacyIssuer && identity.Issuer != legacyIssuer:
		return Identity{}, fmt.Errorf(
			"%w: .1.1=%q .1.8=%q",
			ErrLegacyIssuerMismatch, legacyIssuer, identity.Issuer,
		)
	case identity.Issuer == "" && hasLegacyIssuer:
		identity.Issuer = legacyIssuer
	}

	if identity.BuildSignerURI != "" {
		if err := checkSAN(cert, identity.BuildSignerURI); err != nil {
			return Identity{}, err
		}
	}

	return identity, nil
}

// rawClaim decodes a Fulcio claim whose extension value is stored as raw
// octets rather than as a DER string. This is the representation used by all
// of the OIDs handled here except .1.8.
func rawClaim(field Field, oid asn1.ObjectIdentifier, value []byte) (string, error) {
	if len(value) == 0 {
		return "", fmt.Errorf("%w: %s (%s) is empty", ErrMalformedExtension, field, oid)
	}
	if !utf8.Valid(value) {
		return "", fmt.Errorf("%w: %s (%s) is not valid UTF-8", ErrMalformedExtension, field, oid)
	}
	return string(value), nil
}

// parseDERUTF8String decodes the DER-wrapped UTF8String used by OID .1.8.
// Plain raw bytes, other ASN.1 string types, constructed encodings, and
// trailing data are rejected so that a legacy or malformed value cannot be
// mistaken for the current issuer claim.
func parseDERUTF8String(oid asn1.ObjectIdentifier, value []byte) (string, error) {
	var raw asn1.RawValue
	rest, err := asn1.Unmarshal(value, &raw)
	if err != nil {
		return "", fmt.Errorf("%w: %s is not valid DER: %v", ErrMalformedExtension, oid, err)
	}
	if len(rest) != 0 {
		return "", fmt.Errorf("%w: %s has trailing DER data", ErrMalformedExtension, oid)
	}
	if raw.Class != asn1.ClassUniversal || raw.Tag != asn1.TagUTF8String || raw.IsCompound {
		return "", fmt.Errorf(
			"%w: %s must be a primitive DER UTF8String, got class=%d tag=%d constructed=%t",
			ErrMalformedExtension, oid, raw.Class, raw.Tag, raw.IsCompound,
		)
	}
	if len(raw.Bytes) == 0 {
		return "", fmt.Errorf("%w: %s is empty", ErrMalformedExtension, oid)
	}
	if !utf8.Valid(raw.Bytes) {
		return "", fmt.Errorf("%w: %s is not valid UTF-8", ErrMalformedExtension, oid)
	}
	return string(raw.Bytes), nil
}

// checkSAN binds the certificate's URI subjectAltName to the .1.9 build signer
// URI. Fulcio issues exactly one URI SAN, mirroring the signer identity. If
// any URI SAN is present, every one of them must agree with .1.9.
func checkSAN(cert *x509.Certificate, signerURI string) error {
	if len(cert.URIs) == 0 {
		return fmt.Errorf("%w: certificate has no URI SAN; want %q", ErrSANMismatch, signerURI)
	}
	for _, uri := range cert.URIs {
		if uri == nil {
			return fmt.Errorf("%w: certificate contains a nil URI SAN", ErrSANMismatch)
		}
		if uri.String() != signerURI {
			return fmt.Errorf(
				"%w: SAN %q != build signer URI %q",
				ErrSANMismatch, uri.String(), signerURI,
			)
		}
	}
	return nil
}

func (field Field) valid() bool {
	switch field {
	case FieldIssuer,
		FieldBuildSignerURI,
		FieldSourceRepositoryURI,
		FieldSourceRepositoryDigest,
		FieldSourceRepositoryRef,
		FieldBuildTrigger:
		return true
	default:
		return false
	}
}

func (identity Identity) value(field Field) string {
	switch field {
	case FieldIssuer:
		return identity.Issuer
	case FieldBuildSignerURI:
		return identity.BuildSignerURI
	case FieldSourceRepositoryURI:
		return identity.SourceRepositoryURI
	case FieldSourceRepositoryDigest:
		return identity.SourceRepositoryDigest
	case FieldSourceRepositoryRef:
		return identity.SourceRepositoryRef
	case FieldBuildTrigger:
		return identity.BuildTrigger
	default:
		return ""
	}
}

// tagRefPrefix identifies a Git ref that names an immutable tag.
const tagRefPrefix = "refs/tags/"

// IsTagRef reports whether ref is a well-formed Git tag ref.
func IsTagRef(ref string) bool {
	return strings.HasPrefix(ref, tagRefPrefix) && strings.TrimPrefix(ref, tagRefPrefix) != ""
}
