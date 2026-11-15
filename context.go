package netsec

import (
	"encoding/binary"
	"errors"
)

// Purpose identifies how a derived key may be used. Only the explicitly
// enumerated purposes below are accepted.
type Purpose uint8

const (
	// PurposeEncrypt derives a key for data encryption.
	PurposeEncrypt Purpose = 1
	// PurposeAuthenticate derives a key for message authentication.
	PurposeAuthenticate Purpose = 2
	// PurposeTicket derives a key for ticket protection.
	PurposeTicket Purpose = 3
)

// Direction identifies the traffic direction of a derived key. Only the
// explicitly enumerated directions below are accepted.
type Direction uint8

const (
	// DirectionSend identifies keys used for outbound traffic.
	DirectionSend Direction = 1
	// DirectionRecv identifies keys used for inbound traffic.
	DirectionRecv Direction = 2
)

const (
	// MaxTenantLength bounds the tenant byte string. A tenant must be
	// non-empty and must not exceed this length.
	MaxTenantLength = 1024
	// MaxProtocolVersionLength bounds the protocol version byte string.
	MaxProtocolVersionLength = 64
	// prefixLength is the width, in bytes, of every length prefix.
	prefixLength = 2
)

var (
	errEmptyTenant      = errors.New("netsec: tenant must not be empty")
	errTenantTooLong    = errors.New("netsec: tenant exceeds maximum length")
	errVersionTooLong   = errors.New("netsec: protocol version exceeds maximum length")
	errUnknownPurpose   = errors.New("netsec: unknown key purpose")
	errUnknownDirection = errors.New("netsec: unknown key direction")
)

// Context is the application context bound into every derived key. Every
// field participates in the length-prefixed canonical encoding so that
// distinct contexts can never collide after concatenation.
type Context struct {
	// Tenant is the non-empty tenant identifier as an opaque byte string.
	Tenant []byte
	// Purpose is one of PurposeEncrypt, PurposeAuthenticate or PurposeTicket.
	Purpose Purpose
	// ProtocolVersion is the negotiated protocol version.
	ProtocolVersion []byte
	// Direction is one of DirectionSend or DirectionRecv.
	Direction Direction
	// Epoch is the monotonically increasing protocol epoch.
	Epoch uint64
}

// validate rejects every context that would make the canonical encoding
// ambiguous or fall outside the allowed domain. No cryptographic operation is
// permitted to run while validate returns an error.
func (c Context) validate() error {
	if len(c.Tenant) == 0 {
		return errEmptyTenant
	}
	if len(c.Tenant) > MaxTenantLength {
		return errTenantTooLong
	}
	if len(c.ProtocolVersion) > MaxProtocolVersionLength {
		return errVersionTooLong
	}
	switch c.Purpose {
	case PurposeEncrypt, PurposeAuthenticate, PurposeTicket:
	default:
		return errUnknownPurpose
	}
	switch c.Direction {
	case DirectionSend, DirectionRecv:
	default:
		return errUnknownDirection
	}
	return nil
}

// appendLengthPrefix appends value prefixed with its two-byte big-endian
// length. Every variable-length field uses this framing, which makes the
// encoding injective: the length prefixes delimit fields and remove any
// ambiguity about where one field ends and the next begins.
func appendLengthPrefix(dst, value []byte) []byte {
	var prefix [prefixLength]byte
	binary.BigEndian.PutUint16(prefix[:], uint16(len(value)))
	dst = append(dst, prefix[0], prefix[1])
	return append(dst, value...)
}

// canonicalInfo builds the HKDF info string. The layout is:

// len16(Tenant)        | Tenant
// len16(Purpose)       | uint8 Purpose
// len16(ProtocolVer)   | ProtocolVersion
// len16(Direction)     | uint8 Direction
// len16(Epoch)         | uint64 big-endian Epoch
func (c Context) canonicalInfo() []byte {
	info := make([]byte, 0, len(c.Tenant)+len(c.ProtocolVersion)+5*prefixLength+10)
	info = appendLengthPrefix(info, c.Tenant)

	var purpose [1]byte
	purpose[0] = byte(c.Purpose)
	info = appendLengthPrefix(info, purpose[:])

	info = appendLengthPrefix(info, c.ProtocolVersion)

	var direction [1]byte
	direction[0] = byte(c.Direction)
	info = appendLengthPrefix(info, direction[:])

	var epoch [8]byte
	binary.BigEndian.PutUint64(epoch[:], c.Epoch)
	info = appendLengthPrefix(info, epoch[:])

	return info
}
