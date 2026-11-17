package netsec

import (
	"bytes"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"net/http"
	"net/textproto"
	"strings"
)

// DigestFunc computes a cryptographic authentication tag over data. It must be
// keyed when used to authenticate messages; HMAC-SHA-256 is a typical choice.
// The callback is deliberately small so callers can use their existing signing
// primitive.
type DigestFunc func(data []byte) []byte

const signatureDomain = "netsec/trailer-merge/v1\x00"

// SignatureHeader is the conventional header used by the HTTP helpers. It is
// excluded from the signed header section (the signature cannot cover itself)
// and is forbidden in the trailer section.
const SignatureHeader = "X-Message-Signature"

// SigningInput returns the canonical byte string bound by a signature. Header
// and trailer sections are domain-separated and encoded with explicit field
// counts and length prefixes, preserving every occurrence and its position.
// Field names are compared case-insensitively and are encoded in canonical
// form; values are encoded byte-for-byte.
func SigningInput(header, trailers Section) ([]byte, error) {
	var buffer bytes.Buffer
	buffer.WriteString(signatureDomain)
	if err := writeSection(&buffer, "header", header); err != nil {
		return nil, err
	}
	if err := writeSection(&buffer, "trailer", trailers); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

// Sign authenticates the exact ordered header and trailer sections with digest.
// It does not apply trailer policy; use MergeSections or MergeTrailers for the
// complete authenticated-merge operation.
func Sign(header, trailers Section, digest DigestFunc) ([]byte, error) {
	if digest == nil {
		return nil, ErrNilDigest
	}
	input, err := SigningInput(header, trailers)
	if err != nil {
		return nil, err
	}
	signature := digest(input)
	if len(signature) == 0 {
		return nil, ErrEmptySignature
	}
	return append([]byte(nil), signature...), nil
}

// Verify checks signature against the exact ordered header and trailer
// sections. Comparison is constant-time.
func Verify(header, trailers Section, signature []byte, digest DigestFunc) error {
	if digest == nil {
		return ErrNilDigest
	}
	if len(signature) == 0 {
		return ErrEmptySignature
	}
	input, err := SigningInput(header, trailers)
	if err != nil {
		return err
	}
	expected := digest(input)
	if len(expected) == 0 || len(expected) != len(signature) ||
		subtle.ConstantTimeCompare(expected, signature) != 1 {
		return ErrSignatureMismatch
	}
	return nil
}

// SignHTTP is the map-shaped convenience form of Sign. It normalizes the
// Trailer declaration to sorted names and reconstructs it from trailers when
// net/http has removed it. See MergeTrailers for the ordering caveat when using
// http.Header.
func SignHTTP(header, trailers http.Header, digest DigestFunc) ([]byte, error) {
	canonical, err := canonicalHeaderForSignature(header, trailers)
	if err != nil {
		return nil, err
	}
	return Sign(headerSectionForSignature(canonical), SectionFromHTTPHeader(trailers), digest)
}

// VerifyHTTP is the map-shaped convenience form of Verify. It applies the same
// Trailer normalization as SignHTTP.
func VerifyHTTP(header, trailers http.Header, signature []byte, digest DigestFunc) error {
	canonical, err := canonicalHeaderForSignature(header, trailers)
	if err != nil {
		return err
	}
	return Verify(headerSectionForSignature(canonical), SectionFromHTTPHeader(trailers), signature, digest)
}

// EncodeSignature encodes a raw signature for transport in a field value.
func EncodeSignature(signature []byte) string {
	return base64.StdEncoding.EncodeToString(signature)
}

// DecodeSignature decodes a signature produced by EncodeSignature.
func DecodeSignature(value string) ([]byte, error) {
	signature, err := base64.StdEncoding.DecodeString(strings.TrimSpace(value))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidSignatureEncoding, err)
	}
	if len(signature) == 0 {
		return nil, ErrEmptySignature
	}
	return signature, nil
}

// MergeSignedTrailers is the complete HTTP gateway operation for headers that
// still contain an explicit Trailer declaration. It reads the base64 signature
// from SignatureHeader, validates the trailer policy, verifies the signature,
// and returns the merged header map. It returns nil on any error. For net/http
// maps whose Trailer field was removed during parsing, use
// MergeSignedTrailersWithDeclaration.
func MergeSignedTrailers(header, trailers http.Header, digest DigestFunc) (http.Header, error) {
	signature, err := signatureFromHeader(header)
	if err != nil {
		return nil, err
	}
	return MergeTrailers(header, trailers, signature, digest)
}

// MergeSignedTrailersWithDeclaration is MergeSignedTrailers with an explicit
// declaration captured before the body was read. See
// CaptureDeclaredTrailers and MergeTrailersWithDeclaration.
func MergeSignedTrailersWithDeclaration(header, trailers http.Header, declared []string, digest DigestFunc) (http.Header, error) {
	signature, err := signatureFromHeader(header)
	if err != nil {
		return nil, err
	}
	return MergeTrailersWithDeclaration(header, trailers, declared, signature, digest)
}

func signatureFromHeader(header http.Header) ([]byte, error) {
	values := canonicalHeaderValues(header)[strings.ToLower(SignatureHeader)]
	if len(values) == 0 {
		return nil, ErrEmptySignature
	}
	return DecodeSignature(values[0])
}

func writeSection(buffer *bytes.Buffer, label string, section Section) error {
	writeString(buffer, label)
	writeUint64(buffer, uint64(len(section)))
	for _, field := range section {
		canonical, err := canonicalFieldName(field.Name)
		if err != nil {
			return err
		}
		if err := validateFieldValue(field.Value); err != nil {
			return fmt.Errorf("%s: %w", canonical, err)
		}
		writeString(buffer, strings.ToLower(textproto.CanonicalMIMEHeaderKey(canonical)))
		writeString(buffer, field.Value)
	}
	return nil
}

func headerSectionForSignature(header http.Header) Section {
	section := SectionFromHTTPHeader(header)
	filtered := make(Section, 0, len(section))
	for _, field := range section {
		if !strings.EqualFold(field.Name, SignatureHeader) {
			filtered = append(filtered, field)
		}
	}
	return filtered
}

// canonicalHeaderForSignature normalizes the Trailer declaration so the map
// based API is stable across net/http's removal of the Trailer field. Explicit
// declarations are canonicalized by sorting names; when net/http has already
// stripped the declaration, the names are reconstructed from the delivered
// trailer section.
func canonicalHeaderForSignature(header, trailers http.Header) (http.Header, error) {
	declared, err := DeclaredTrailers(header)
	if err != nil {
		return nil, err
	}
	if len(declared) == 0 {
		trailerNames := canonicalHeaderValues(trailers)
		for _, name := range sortedKeys(trailerNames) {
			declared = append(declared, textproto.CanonicalMIMEHeaderKey(name))
		}
	}

	canonical := canonicalHeader(header)
	for key := range canonical {
		if strings.EqualFold(key, TrailerHeader) {
			delete(canonical, key)
		}
	}

	names := canonicalTrailerNames(declared)
	if len(names) > 0 {
		canonical.Set(TrailerHeader, strings.Join(names, ", "))
	}
	return canonical, nil
}

func canonicalTrailerNames(names []string) []string {
	unique := make(map[string]string, len(names))
	for _, name := range names {
		canonical := textproto.CanonicalMIMEHeaderKey(strings.TrimSpace(name))
		if canonical == "" {
			continue
		}
		key := strings.ToLower(canonical)
		if _, ok := unique[key]; !ok {
			unique[key] = canonical
		}
	}
	keys := sortedKeys(unique)
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		out = append(out, unique[key])
	}
	return out
}

func writeString(buffer *bytes.Buffer, value string) {
	writeUint64(buffer, uint64(len(value)))
	buffer.WriteString(value)
}

func writeUint64(buffer *bytes.Buffer, value uint64) {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], value)
	buffer.Write(encoded[:])
}
