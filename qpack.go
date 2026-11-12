// Package netsec provides a conservative QPACK field-section encoder for
// reverse proxies. Sensitive fields are always emitted as literal field lines
// with the QPACK N bit set, and this encoder never uses the dynamic table.
package netsec

import (
	"errors"
	"fmt"
	"strings"
)

// HeaderCookie and HeaderAuthorization are always treated as sensitive.
const (
	HeaderCookie        = "cookie"
	HeaderAuthorization = "authorization"
)

var (
	// ErrInvalidHeaderName is returned when a header name is not a valid
	// HTTP field name.
	ErrInvalidHeaderName = errors.New("netsec: invalid header name")
	// ErrInvalidDynamicTableCapacity is returned for a negative dynamic table
	// capacity.
	ErrInvalidDynamicTableCapacity = errors.New("netsec: invalid dynamic table capacity")
)

// HeaderField is a decoded HTTP field. Sensitive preserves the QPACK N bit
// reported by a downstream decoder. The encoder sets Sensitive for Cookie,
// Authorization, and configured names regardless of its incoming value.
type HeaderField struct {
	Name      string
	Value     string
	Sensitive bool
}

// Field is an alias for HeaderField.
type Field = HeaderField

// Config controls the encoder policy.
type Config struct {
	// SensitiveHeaders adds case-insensitive header names to the built-in
	// Cookie and Authorization policy.
	SensitiveHeaders []string

	// MaxDynamicTableCapacity is the capacity offered by the decoder. This
	// encoder never inserts into, or references, the dynamic table, so the
	// value may be zero or any other non-negative capacity.
	MaxDynamicTableCapacity int
}

// Policy decides which field names must be encoded as sensitive literals.
// It is safe for concurrent reads after construction.
type Policy struct {
	sensitive map[string]struct{}
}

// NewPolicy creates a policy containing Cookie, Authorization, and the
// supplied case-insensitive names.
func NewPolicy(extraNames ...string) (*Policy, error) {
	p := &Policy{sensitive: make(map[string]struct{}, len(extraNames)+2)}
	for _, name := range []string{HeaderCookie, HeaderAuthorization} {
		p.sensitive[name] = struct{}{}
	}
	for _, name := range extraNames {
		normalized, err := normalizeHeaderName(name)
		if err != nil {
			return nil, err
		}
		p.sensitive[normalized] = struct{}{}
	}
	return p, nil
}

// IsSensitive reports whether name is covered by the policy. Lookup is
// case-insensitive.
func (p *Policy) IsSensitive(name string) bool {
	_, ok := p.sensitive[strings.ToLower(name)]
	return ok
}

// Mark returns a copy of fields with Sensitive set for policy matches. Fields
// whose Sensitive bit was already set remain sensitive.
func (p *Policy) Mark(fields []HeaderField) []HeaderField {
	marked := make([]HeaderField, len(fields))
	copy(marked, fields)
	for i := range marked {
		if p.IsSensitive(marked[i].Name) {
			marked[i].Sensitive = true
		}
	}
	return marked
}

// Encoder encodes QPACK field sections without ever using the dynamic table.
// It may be reused across requests.
type Encoder struct {
	policy                  *Policy
	maxDynamicTableCapacity int
}

// NewEncoder creates an encoder with the built-in sensitive-field policy and
// the caller-provided additions.
func NewEncoder(config Config) (*Encoder, error) {
	if config.MaxDynamicTableCapacity < 0 {
		return nil, fmt.Errorf("%w: %d", ErrInvalidDynamicTableCapacity, config.MaxDynamicTableCapacity)
	}
	policy, err := NewPolicy(config.SensitiveHeaders...)
	if err != nil {
		return nil, err
	}
	return &Encoder{
		policy:                  policy,
		maxDynamicTableCapacity: config.MaxDynamicTableCapacity,
	}, nil
}

// NewDefaultEncoder creates an encoder with only the built-in Cookie and
// Authorization sensitivity policy.
func NewDefaultEncoder() *Encoder {
	encoder, err := NewEncoder(Config{})
	if err != nil {
		panic(err)
	}
	return encoder
}

// Policy returns the encoder's field-name policy.
func (e *Encoder) Policy() *Policy {
	return e.policy
}

// Encode encodes a complete QPACK field section. The returned block has a
// Required Insert Count of zero and never contains dynamic table references.
func (e *Encoder) Encode(fields []HeaderField) ([]byte, error) {
	return e.EncodeFieldSection(fields)
}

// EncodeFieldSection encodes a complete QPACK field section, including the
// Required Insert Count and Delta Base prefix. The output is:
//
//	Required Insert Count = 0
//	Delta Base             = 0
//	one or more literal field lines
func (e *Encoder) EncodeFieldSection(fields []HeaderField) ([]byte, error) {
	lines, err := e.EncodeFieldLines(fields)
	if err != nil {
		return nil, err
	}

	block := make([]byte, 0, len(lines)+2)
	block = append(block, 0x00, 0x00)
	block = append(block, lines...)
	return block, nil
}

// EncodeFieldLines encodes only the field-line portion of a field section.
// Callers that use their own QPACK field-section prefix can use this method,
// but must keep the encoder's no-dynamic-table invariant.
func (e *Encoder) EncodeFieldLines(fields []HeaderField) ([]byte, error) {
	for _, field := range fields {
		if err := validateHeaderName(field.Name); err != nil {
			return nil, err
		}
	}

	// Literal name and value lengths plus two length prefixes are a close
	// enough lower bound for the common case.
	estimate := 0
	for _, field := range fields {
		estimate += len(field.Name) + len(field.Value) + 2
	}
	lines := make([]byte, 0, estimate)

	for _, field := range fields {
		name := strings.ToLower(field.Name)
		sensitive := field.Sensitive || e.policy.IsSensitive(name)
		lines = appendLiteralFieldLine(lines, name, field.Value, sensitive)
	}
	return lines, nil
}

// DynamicTableEntries returns the number of entries in the encoder's dynamic
// table. It is always zero for this policy encoder.
func (e *Encoder) DynamicTableEntries() int {
	return 0
}

// DynamicTableCapacity returns the dynamic table capacity in effect for this
// encoder. It is always zero because no dynamic table size update is emitted.
func (e *Encoder) DynamicTableCapacity() int {
	return 0
}

// MaxDynamicTableCapacity returns the decoder-offered capacity recorded by the
// encoder.
func (e *Encoder) MaxDynamicTableCapacity() int {
	return e.maxDynamicTableCapacity
}

// SetMaxDynamicTableCapacity updates the decoder-offered capacity. The encoder
// still never inserts entries, so changing this value does not affect output
// sizes or field representations.
func (e *Encoder) SetMaxDynamicTableCapacity(capacity int) error {
	if capacity < 0 {
		return fmt.Errorf("%w: %d", ErrInvalidDynamicTableCapacity, capacity)
	}
	e.maxDynamicTableCapacity = capacity
	return nil
}

func appendLiteralFieldLine(dst []byte, name, value string, sensitive bool) []byte {
	// Literal Field Line with a Literal Name:
	//   0 0 1 N H Nlen(3+)
	//   Name
	//   Vlen(7+)
	//   H Value
	first := byte(0x20) // 001
	if sensitive {
		first |= 0x10 // N
	}
	// The H bit for the name is deliberately left at zero. Length-preserving
	// output is important for the anti-oracle property.
	dst = appendPrefixedInt(dst, first, 3, len(name))
	dst = append(dst, name...)
	dst = appendPrefixedInt(dst, 0, 7, len(value))
	dst = append(dst, value...)
	return dst
}

func appendPrefixedInt(dst []byte, first byte, prefix uint, value int) []byte {
	max := 1<<prefix - 1
	if value < max {
		return append(dst, first|byte(value))
	}

	dst = append(dst, first|byte(max))
	value -= max
	for value >= 0x80 {
		dst = append(dst, byte(value&0x7f)|0x80)
		value >>= 7
	}
	return append(dst, byte(value))
}

func normalizeHeaderName(name string) (string, error) {
	if err := validateHeaderName(name); err != nil {
		return "", err
	}
	return strings.ToLower(name), nil
}

func validateHeaderName(name string) error {
	if name == "" {
		return fmt.Errorf("%w: empty name", ErrInvalidHeaderName)
	}

	start := 0
	if name[0] == ':' {
		// HTTP/3 pseudo-header fields use a colon prefix.
		start = 1
		if len(name) == 1 {
			return fmt.Errorf("%w: %q", ErrInvalidHeaderName, name)
		}
	}
	for i := start; i < len(name); i++ {
		if !isTokenByte(name[i]) {
			return fmt.Errorf("%w: %q", ErrInvalidHeaderName, name)
		}
	}
	return nil
}

func isTokenByte(c byte) bool {
	switch {
	case 'a' <= c && c <= 'z':
		return true
	case 'A' <= c && c <= 'Z':
		return true
	case '0' <= c && c <= '9':
		return true
	}
	switch c {
	case '!', '#', '$', '%', '&', '\'', '*', '+', '-', '.', '^', '_', '`', '|', '~':
		return true
	default:
		return false
	}
}
