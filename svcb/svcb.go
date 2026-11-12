// Package svcb parses and validates SVCB/HTTPS RDATA carried in DNS wire
// format (RFC 9460). It is intended as a safety gate for a parsing host:
// tampered SvcParams are reported as typed errors so the host can discard the
// record instead of silently dropping parameters such as ech.
package svcb

import (
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
)

// SvcParam keys defined by RFC 9460, section 14.3.2.
const (
	KeyMandatory     uint16 = 0
	KeyALPN          uint16 = 1
	KeyNoDefaultALPN uint16 = 2
	KeyPort          uint16 = 3
	KeyIPv4Hint      uint16 = 4
	KeyECH           uint16 = 5
	KeyIPv6Hint      uint16 = 6
	KeyDOHPath       uint16 = 7
)

// Sentinel errors identify the kind of violation. Wrap them with errors.Is,
// or inspect an *Error for the offending SvcParam key.
var (
	// ErrTruncated means the RDATA ends before the record is complete.
	ErrTruncated = errors.New("truncated SVCB RDATA")
	// ErrCompressedName means TargetName uses a DNS compression pointer,
	// which is forbidden inside RDATA.
	ErrCompressedName = errors.New("compressed TargetName is not allowed in RDATA")
	// ErrInvalidName means TargetName cannot be represented on the wire.
	ErrInvalidName = errors.New("invalid TargetName")

	// ErrKeysNotAscending means SvcParam keys are not in strict ascending order.
	ErrKeysNotAscending = errors.New("SvcParam keys are not strictly ascending")
	// ErrDuplicateKey means the same SvcParam key appears more than once.
	ErrDuplicateKey = errors.New("duplicate SvcParam key")
	// ErrAliasModeParams means SvcPriority is 0 but SvcParams are present.
	ErrAliasModeParams = errors.New("alias mode (SvcPriority=0) must not carry SvcParams")
	// ErrMalformedValue means a parameter value cannot be decoded per its key.
	ErrMalformedValue = errors.New("malformed SvcParam value")

	// ErrMandatorySelf means the mandatory list includes key 0 (itself).
	ErrMandatorySelf = errors.New("mandatory list must not contain key 0")
	// ErrMandatoryNotAscending means the mandatory key list is not strictly
	// ascending (duplicates included).
	ErrMandatoryNotAscending = errors.New("mandatory key list is not strictly ascending")
	// ErrMandatoryMissingKey means a mandatory key is absent from the record.
	ErrMandatoryMissingKey = errors.New("mandatory key is not present in the record")
	// ErrMandatoryUnsupported means a mandatory key is not supported by the
	// client; RFC 9460 requires the whole record to be ignored.
	ErrMandatoryUnsupported = errors.New("mandatory key is not supported by the client")

	// ErrNoDefaultALPNValue means no-default-alpn carries a non-empty value.
	ErrNoDefaultALPNValue = errors.New("no-default-alpn must have an empty value")
	// ErrNoDefaultALPNNoALPN means no-default-alpn is present without alpn.
	ErrNoDefaultALPNNoALPN = errors.New("no-default-alpn requires an alpn parameter")

	// ErrIPv4HintLength means ipv4hint is not a multiple of 4 bytes.
	ErrIPv4HintLength = errors.New("ipv4hint value length must be a multiple of 4")
	// ErrIPv6HintLength means ipv6hint is not a multiple of 16 bytes.
	ErrIPv6HintLength = errors.New("ipv6hint value length must be a multiple of 16")
)

// Error attaches the offending SvcParam key to a validation failure. Its Kind
// field is one of the sentinel errors above.
type Error struct {
	Kind error
	Key  uint16
}

func (e *Error) Error() string {
	return fmt.Sprintf("svcb: %s (svcparam key %d)", e.Kind, e.Key)
}

// Unwrap exposes the sentinel kind for errors.Is.
func (e *Error) Unwrap() error { return e.Kind }

// Param is one SvcParam: a 16-bit key and its raw wire-format value.
type Param struct {
	Key   uint16
	Value []byte
}

// Record is a decoded SVCB (or HTTPS) RDATA payload.
type Record struct {
	SvcPriority uint16
	TargetName  string
	Params      []Param
}

// Parse decodes one SVCB/HTTPS RDATA blob. It performs structural decoding
// only; call Validate (or ParseAndValidate) to enforce the SvcParams rules.
// Parsed value bytes are copied and do not alias the input slice.
func Parse(rdata []byte) (*Record, error) {
	p := &parser{data: rdata}
	priority := p.u16()
	if p.err != nil {
		return nil, p.err
	}
	target := p.name()
	if p.err != nil {
		return nil, p.err
	}

	var params []Param
	for p.off < len(p.data) {
		key := p.u16()
		length := int(p.u16())
		value := p.bytes(length)
		if p.err != nil {
			return nil, p.err
		}
		params = append(params, Param{Key: key, Value: value})
	}

	return &Record{
		SvcPriority: priority,
		TargetName:  target,
		Params:      params,
	}, nil
}

// Validate applies the package rules with the default set of supported keys
// (keys 0 through 7). A non-nil error means the host must ignore the record.
func Validate(r *Record) error {
	return ValidateWith(r, nil)
}

// ParseAndValidate parses RDATA and validates it in one step using the
// default supported-key set.
func ParseAndValidate(rdata []byte) (*Record, error) {
	return ParseAndValidateWith(rdata, nil)
}

// ParseAndValidateWith parses RDATA and validates it against a caller-supplied
// supported-key set (see SupportedKeys). A nil set means the default set.
func ParseAndValidateWith(rdata []byte, supported map[uint16]bool) (*Record, error) {
	r, err := Parse(rdata)
	if err != nil {
		return nil, err
	}
	if err := ValidateWith(r, supported); err != nil {
		return nil, err
	}
	return r, nil
}

// ValidateWith enforces:
//
//   - SvcPriority 0 (alias mode) must not carry any SvcParams;
//   - SvcParam keys are strictly ascending and unique;
//   - the mandatory list is ascending, excludes key 0, references only keys
//     present in the record, and references only keys the client supports;
//   - no-default-alpn has an empty value and an accompanying alpn;
//   - ipv4hint length is a multiple of 4 and ipv6hint a multiple of 16.
//
// A nil supported set uses the default set (keys 0-7). The map is never
// mutated. Unknown keys outside mandatory are tolerated per RFC 9460.
func ValidateWith(r *Record, supported map[uint16]bool) error {
	if supported == nil {
		supported = defaultSupported
	}
	if r.SvcPriority == 0 && len(r.Params) > 0 {
		return &Error{Kind: ErrAliasModeParams, Key: r.Params[0].Key}
	}

	present := make(map[uint16]bool, len(r.Params))
	var previous uint16
	for i, param := range r.Params {
		if i > 0 {
			switch {
			case param.Key == previous:
				return &Error{Kind: ErrDuplicateKey, Key: param.Key}
			case param.Key < previous:
				return &Error{Kind: ErrKeysNotAscending, Key: param.Key}
			}
		}
		previous = param.Key
		present[param.Key] = true
	}

	for _, param := range r.Params {
		switch param.Key {
		case KeyMandatory:
			if err := validateMandatory(param.Value, present, supported); err != nil {
				return err
			}
		case KeyNoDefaultALPN:
			if len(param.Value) != 0 {
				return &Error{Kind: ErrNoDefaultALPNValue, Key: KeyNoDefaultALPN}
			}
			if !present[KeyALPN] {
				return &Error{Kind: ErrNoDefaultALPNNoALPN, Key: KeyNoDefaultALPN}
			}
		case KeyIPv4Hint:
			if len(param.Value)%4 != 0 {
				return &Error{Kind: ErrIPv4HintLength, Key: KeyIPv4Hint}
			}
		case KeyIPv6Hint:
			if len(param.Value)%16 != 0 {
				return &Error{Kind: ErrIPv6HintLength, Key: KeyIPv6Hint}
			}
		}
	}
	return nil
}

func validateMandatory(value []byte, present, supported map[uint16]bool) error {
	if len(value)%2 != 0 {
		return &Error{Kind: ErrMalformedValue, Key: KeyMandatory}
	}
	var previous uint16
	for offset := 0; offset < len(value); offset += 2 {
		key := binary.BigEndian.Uint16(value[offset : offset+2])
		if key == KeyMandatory {
			return &Error{Kind: ErrMandatorySelf, Key: key}
		}
		if offset > 0 && key <= previous {
			return &Error{Kind: ErrMandatoryNotAscending, Key: key}
		}
		previous = key
		if !present[key] {
			return &Error{Kind: ErrMandatoryMissingKey, Key: key}
		}
		if !supported[key] {
			return &Error{Kind: ErrMandatoryUnsupported, Key: key}
		}
	}
	return nil
}

// SupportedKeys builds a supported-key set for ValidateWith from the given
// keys.
func SupportedKeys(keys ...uint16) map[uint16]bool {
	set := make(map[uint16]bool, len(keys))
	for _, key := range keys {
		set[key] = true
	}
	return set
}

// DefaultSupportedKeys returns a copy of the default supported-key set
// (RFC 9460 keys 0 through 7, including ech).
func DefaultSupportedKeys() map[uint16]bool {
	set := make(map[uint16]bool, len(defaultSupported))
	for key := range defaultSupported {
		set[key] = true
	}
	return set
}

var defaultSupported = map[uint16]bool{
	KeyMandatory:     true,
	KeyALPN:          true,
	KeyNoDefaultALPN: true,
	KeyPort:          true,
	KeyIPv4Hint:      true,
	KeyECH:           true,
	KeyIPv6Hint:      true,
	KeyDOHPath:       true,
}

type parser struct {
	data []byte
	off  int
	err  error
}

func (p *parser) setErr(err error) {
	if p.err == nil {
		p.err = err
	}
}

func (p *parser) u16() uint16 {
	if p.err != nil || p.off+2 > len(p.data) {
		p.setErr(ErrTruncated)
		return 0
	}
	v := binary.BigEndian.Uint16(p.data[p.off : p.off+2])
	p.off += 2
	return v
}

func (p *parser) bytes(n int) []byte {
	if p.err != nil || p.off+n > len(p.data) {
		p.setErr(ErrTruncated)
		return nil
	}
	out := make([]byte, n)
	copy(out, p.data[p.off:p.off+n])
	p.off += n
	return out
}

// name reads an uncompressed, length-prefixed domain name. Compression
// pointers are rejected because they must not appear in RDATA.
func (p *parser) name() string {
	var labels []string
	consumed := 0
	for {
		if p.err != nil || p.off >= len(p.data) {
			p.setErr(ErrTruncated)
			return ""
		}
		length := int(p.data[p.off])
		p.off++
		if length&0xC0 != 0 {
			p.setErr(ErrCompressedName)
			return ""
		}
		if length == 0 {
			break
		}
		if p.off+length > len(p.data) {
			p.setErr(ErrTruncated)
			return ""
		}
		labels = append(labels, string(p.data[p.off:p.off+length]))
		p.off += length
		consumed += length + 1
		if consumed > 255 {
			p.setErr(ErrInvalidName)
			return ""
		}
	}
	if len(labels) == 0 {
		return "."
	}
	return strings.Join(labels, ".")
}
