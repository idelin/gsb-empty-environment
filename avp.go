package netsec

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// Diameter AVP flags (RFC 6733, Section 4.1).
const (
	FlagVendor    uint8 = 0x80
	FlagMandatory uint8 = 0x40
	FlagProtected uint8 = 0x20

	// Aliases using the names used by several Diameter implementations.
	AVPFlagVendor    = FlagVendor
	AVPFlagMandatory = FlagMandatory
	AVPFlagProtected = FlagProtected
)

// AVP header sizes. The Vendor-Id field is present only when the V flag is set.
const (
	AVPHeaderLen       = 8
	AVPVendorHeaderLen = 12
	maxAVPLength       = 0x00ffffff
)

var (
	// ErrInvalidAVPLength is returned when the AVP length is shorter than its
	// header, extends past its containing buffer, or has invalid padding.
	ErrInvalidAVPLength = errors.New("diameter: invalid AVP length")

	// ErrInvalidVendorID is returned when the V flag and Vendor-Id disagree.
	ErrInvalidVendorID = errors.New("diameter: V flag and Vendor-Id disagree")

	// ErrGroupedTooDeep is returned when a Grouped AVP is nested more than the
	// configured maximum number of levels.
	ErrGroupedTooDeep = errors.New("diameter: grouped AVP nesting too deep")

	// ErrInvalidAVP is returned for malformed AVP containers.
	ErrInvalidAVP = errors.New("diameter: invalid AVP")
)

// AVP is one Diameter Attribute-Value Pair.
//
// Value contains the payload only; it does not include the AVP header or the
// four-byte padding. Raw is the complete, padded AVP as received and is useful
// when forwarding an AVP without re-encoding it.
//
// For a decoded Grouped AVP, Children holds the nested AVPs. Grouped is true
// only when the AVP code is known to be Grouped.
type AVP struct {
	Code     uint32
	Flags    uint8
	VendorID uint32
	Value    []byte
	Raw      []byte
	Grouped  bool
	Children []*AVP
}

// HasVendor reports whether the Vendor-Id field is present.
func (a *AVP) HasVendor() bool {
	return a != nil && a.Flags&FlagVendor != 0
}

// Mandatory reports whether the M bit is set.
func (a *AVP) Mandatory() bool {
	return a != nil && a.Flags&FlagMandatory != 0
}

// HeaderLen returns the AVP header length implied by the V flag.
func (a *AVP) HeaderLen() int {
	if a != nil && a.HasVendor() {
		return AVPVendorHeaderLen
	}
	return AVPHeaderLen
}

// PaddedLen returns the number of octets occupied by the AVP, including padding.
func (a *AVP) PaddedLen() int {
	if a == nil {
		return 0
	}
	return pad4(a.HeaderLen() + len(a.value()))
}

// Length returns the AVP Length field value: header plus value, without
// trailing padding.
func (a *AVP) Length() int {
	if a == nil {
		return 0
	}
	return a.HeaderLen() + len(a.value())
}

func (a *AVP) value() []byte {
	if a == nil {
		return nil
	}
	// Children are used by callers that construct Grouped AVPs programmatically.
	if a.Grouped && len(a.Children) > 0 {
		var buf []byte
		for _, child := range a.Children {
			if child == nil {
				continue
			}
			encoded, err := child.MarshalBinary()
			if err != nil {
				return a.Value
			}
			buf = append(buf, encoded...)
		}
		return buf
	}
	return a.Value
}

// MarshalBinary encodes the AVP, including the length-to-four-byte padding.
func (a *AVP) MarshalBinary() ([]byte, error) {
	if a == nil {
		return nil, fmt.Errorf("%w: nil AVP", ErrInvalidAVP)
	}

	value := a.value()
	headerLen := a.HeaderLen()
	length := headerLen + len(value)
	if length > maxAVPLength {
		return nil, fmt.Errorf("%w: AVP value too large", ErrInvalidAVPLength)
	}

	out := make([]byte, pad4(length))
	binary.BigEndian.PutUint32(out[0:4], a.Code)
	out[4] = a.Flags
	putUint24(out[5:8], uint32(length))

	offset := headerLen
	if a.HasVendor() {
		binary.BigEndian.PutUint32(out[8:12], a.VendorID)
		offset = AVPVendorHeaderLen
	}
	copy(out[offset:length], value)
	return out, nil
}

// Bytes encodes the AVP and discards the error. Callers that construct AVPs
// dynamically should prefer MarshalBinary.
func (a *AVP) Bytes() []byte {
	b, err := a.MarshalBinary()
	if err != nil {
		return nil
	}
	return b
}

func pad4(n int) int {
	if rem := n % 4; rem != 0 {
		return n + 4 - rem
	}
	return n
}

func putUint24(dst []byte, v uint32) {
	dst[0] = byte(v >> 16)
	dst[1] = byte(v >> 8)
	dst[2] = byte(v)
}

func getUint24(src []byte) uint32 {
	return uint32(src[0])<<16 | uint32(src[1])<<8 | uint32(src[2])
}
