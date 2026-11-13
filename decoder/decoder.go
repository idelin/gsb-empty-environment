// Package decoder provides an importable hostname decoder that applies the
// RFC 5892 ContextJ rules for ZWNJ and ZWJ.
package decoder

import (
	netsec "example.org/netsec/q0324"
)

// Decoder decodes and validates hostnames.
type Decoder struct {
	inner *netsec.Decoder
}

// New creates a Decoder. With no options it validates labels, including the
// ContextJ rules.
func New(opts ...netsec.Option) *Decoder {
	return &Decoder{inner: netsec.NewDecoder(opts...)}
}

// NewDecoder creates a Decoder.
func NewDecoder(opts ...netsec.Option) *Decoder {
	return New(opts...)
}

// Decode decodes and validates a hostname.
func (d *Decoder) Decode(host string) (string, error) {
	return d.inner.Decode(host)
}

// DecodeHostname decodes and validates a hostname.
func (d *Decoder) DecodeHostname(host string) (string, error) {
	return d.inner.DecodeHostname(host)
}

// DecodeLabel decodes and validates a single label.
func (d *Decoder) DecodeLabel(label string) (string, error) {
	return d.inner.DecodeLabel(label)
}

// Decode decodes and validates a hostname with the registration profile.
func Decode(host string) (string, error) {
	return netsec.Decode(host)
}

// DecodeHostname decodes and validates a hostname with the registration
// profile.
func DecodeHostname(host string) (string, error) {
	return netsec.DecodeHostname(host)
}

// DecodeLabel decodes and validates a single label with the registration
// profile.
func DecodeLabel(label string) (string, error) {
	return netsec.DecodeLabel(label)
}

// Validate checks whether a hostname is valid for registration.
func Validate(host string) error {
	return netsec.Validate(host)
}

// ToASCII converts a hostname to its ASCII form.
func ToASCII(host string) (string, error) {
	return netsec.ToASCII(host)
}

// ToUnicode converts a hostname to its Unicode form.
func ToUnicode(host string) (string, error) {
	return netsec.ToUnicode(host)
}

// ValidateLabel checks a single label for registration.
func ValidateLabel(label string) error {
	return netsec.ValidateLabel(label)
}

// EncodeLabel validates a single label and returns its A-label form.
func EncodeLabel(label string) (string, error) {
	return netsec.EncodeLabel(label)
}

// CheckJoinerContext verifies the ContextJ rules for ZWNJ and ZWJ.
func CheckJoinerContext(label string) error {
	return netsec.CheckJoinerContext(label)
}

// ValidateJoiners verifies the ContextJ rules for ZWNJ and ZWJ.
func ValidateJoiners(label string) error {
	return netsec.ValidateJoiners(label)
}
