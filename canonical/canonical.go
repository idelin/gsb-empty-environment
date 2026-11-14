// Package canonical exposes the netsec canonicalization component under a
// package name that is convenient for signature services.
package canonical

import (
	"net/url"

	netsec "example.org/netsec/q0488"
)

// Field is one logical key/value pair.
type Field = netsec.Field

// Params is a map of non-null string values.
type Params = netsec.Params

// Values is an alias for url.Values.
type Values = netsec.Values

// Fields is a slice of fields.
type Fields = netsec.Fields

// HMACSigner signs canonical data with a fixed secret.
type HMACSigner = netsec.HMACSigner

// Canonicalizer is the stateless canonicalization component.
type Canonicalizer = netsec.Canonicalizer

// HMACSize is the size in bytes of the SHA-256 HMAC.
const HMACSize = netsec.HMACSize

var (
	ErrDuplicateKey       = netsec.ErrDuplicateKey
	ErrEmptyKey           = netsec.ErrEmptyKey
	ErrInvalidUTF8        = netsec.ErrInvalidUTF8
	ErrIllegalCharacter   = netsec.ErrIllegalCharacter
	ErrNullWithValue      = netsec.ErrNullWithValue
	ErrMalformedEscape    = netsec.ErrMalformedEscape
	ErrAmbiguousPlus      = netsec.ErrAmbiguousPlus
	ErrAmbiguousSeparator = netsec.ErrAmbiguousSeparator
	ErrEmptyComponent     = netsec.ErrEmptyComponent
	ErrNonCanonical       = netsec.ErrNonCanonical
	ErrUnsupportedInput   = netsec.ErrUnsupportedInput
)

// Canonicalize returns the canonical byte representation of input.
func Canonicalize(input any) ([]byte, error) {
	return netsec.Canonicalize(input)
}

// CanonicalBytes is an alias for Canonicalize.
func CanonicalBytes(input any) ([]byte, error) {
	return netsec.Canonicalize(input)
}

// Canonical is an alias for Canonicalize.
func Canonical(input any) ([]byte, error) {
	return netsec.Canonicalize(input)
}

// Normalize is an alias for Canonicalize.
func Normalize(input any) ([]byte, error) {
	return netsec.Canonicalize(input)
}

// NormalizeQuery is an alias for CanonicalizeQuery.
func NormalizeQuery(raw string) ([]byte, error) {
	return netsec.CanonicalizeQuery(raw)
}

// CanonicalizeStrict rejects non-canonical string or []byte input.
func CanonicalizeStrict(input any) ([]byte, error) {
	return netsec.CanonicalizeStrict(input)
}

// CanonicalString is Canonicalize with a string result.
func CanonicalString(input any) (string, error) {
	return netsec.CanonicalString(input)
}

// CanonicalizeFields canonicalizes explicit logical fields.
func CanonicalizeFields(fields []Field) ([]byte, error) {
	return netsec.CanonicalizeFields(fields)
}

// CanonicalizeMap canonicalizes a map of non-null values.
func CanonicalizeMap(params map[string]string) ([]byte, error) {
	return netsec.CanonicalizeMap(params)
}

// CanonicalizeParams is an alias for CanonicalizeMap.
func CanonicalizeParams(params map[string]string) ([]byte, error) {
	return netsec.CanonicalizeMap(params)
}

// CanonicalizeValues canonicalizes url.Values.
func CanonicalizeValues(values url.Values) ([]byte, error) {
	return netsec.CanonicalizeValues(values)
}

// CanonicalizeQuery canonicalizes a raw query string.
func CanonicalizeQuery(raw string) ([]byte, error) {
	return netsec.CanonicalizeQuery(raw)
}

// CanonicalizeQueryBytes canonicalizes a raw query byte slice.
func CanonicalizeQueryBytes(raw []byte) ([]byte, error) {
	return netsec.CanonicalizeQueryBytes(raw)
}

// ParseQuery decodes a raw query string once.
func ParseQuery(raw string) ([]Field, error) {
	return netsec.ParseQuery(raw)
}

// EscapeComponent escapes one logical key or value.
func EscapeComponent(component string) (string, error) {
	return netsec.EscapeComponent(component)
}

// ValidateCanonical checks that input is already canonical.
func ValidateCanonical(input any) error {
	return netsec.ValidateCanonical(input)
}

// ValidateCanonicalQuery checks that a raw query is already canonical.
func ValidateCanonicalQuery(raw string) error {
	return netsec.ValidateCanonicalQuery(raw)
}

// HMACSign signs canonical bytes.
func HMACSign(secret, canonical []byte) []byte {
	return netsec.HMACSign(secret, canonical)
}

// HMACVerify verifies a signature over canonical bytes.
func HMACVerify(secret, canonical, signature []byte) bool {
	return netsec.HMACVerify(secret, canonical, signature)
}

// Sign canonicalizes input and signs it.
func Sign(secret []byte, input any) ([]byte, error) {
	return netsec.Sign(secret, input)
}

// Verify canonicalizes input and verifies a signature.
func Verify(secret []byte, input any, signature []byte) (bool, error) {
	return netsec.Verify(secret, input, signature)
}

// VerifySignature reports any verification or canonicalization error as false.
func VerifySignature(secret []byte, input any, signature []byte) bool {
	return netsec.VerifySignature(secret, input, signature)
}

// SignHex is Sign returning a hexadecimal signature.
func SignHex(secret []byte, input any) (string, error) {
	return netsec.SignHex(secret, input)
}

// VerifyHex is Verify accepting a hexadecimal signature.
func VerifyHex(secret []byte, input any, signature string) (bool, error) {
	return netsec.VerifyHex(secret, input, signature)
}

// NewHMACSigner returns a signer with a private copy of secret.
func NewHMACSigner(secret []byte) *HMACSigner {
	return netsec.NewHMACSigner(secret)
}

// NewCanonicalizer returns the fixed canonicalizer.
func NewCanonicalizer() *Canonicalizer {
	return netsec.NewCanonicalizer()
}
