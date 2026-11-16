package netsec

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// Field limits for an application derivation context. These bounds keep the
// encoding canonical and prevent attacker-controlled contexts from growing
// without limit.
const (
	// MaxTenantBytes is the maximum accepted tenant identifier length.
	MaxTenantBytes = 1024

	// MaxContextBytes bounds the complete encoded context.
	MaxContextBytes = 4096
)

var (
	ErrEmptyTenant      = errors.New("netsec: tenant must not be empty")
	ErrTenantTooLong    = errors.New("netsec: tenant exceeds maximum length")
	ErrUnknownPurpose   = errors.New("netsec: unknown purpose")
	ErrUnknownDirection = errors.New("netsec: unknown direction")
	ErrContextTooLong   = errors.New("netsec: encoded context exceeds maximum length")
)

// Purpose is the intended use of a derived key. Only the three values below
// are accepted; zero and every other value are rejected as unknown.
type Purpose uint8

const (
	PurposeEncryption     Purpose = 1
	PurposeAuthentication Purpose = 2
	PurposeTicket         Purpose = 3
)

// Valid reports whether p is one of the defined purposes.
func (p Purpose) Valid() bool {
	return p == PurposeEncryption || p == PurposeAuthentication || p == PurposeTicket
}

func (p Purpose) String() string {
	switch p {
	case PurposeEncryption:
		return "encryption"
	case PurposeAuthentication:
		return "authentication"
	case PurposeTicket:
		return "ticket"
	default:
		return fmt.Sprintf("purpose(%d)", uint8(p))
	}
}

// Direction binds a derived key to one direction of a protocol channel.
type Direction uint8

const (
	DirectionSend    Direction = 1
	DirectionReceive Direction = 2
)

// Valid reports whether d is one of the defined directions.
func (d Direction) Valid() bool {
	return d == DirectionSend || d == DirectionReceive
}

func (d Direction) String() string {
	switch d {
	case DirectionSend:
		return "send"
	case DirectionReceive:
		return "receive"
	default:
		return fmt.Sprintf("direction(%d)", uint8(d))
	}
}

// Context identifies one key in a tenant's protocol key space.
//
// Tenant is an opaque byte string, not necessarily UTF-8. Version is the
// protocol version and Epoch is the protocol key epoch. All fields participate
// in both validation and the canonical encoding used as HKDF info.
type Context struct {
	Tenant    []byte
	Purpose   Purpose
	Version   uint16
	Direction Direction
	Epoch     uint64
}

// Validate rejects contexts that are not representable in the host key space.
func (c Context) Validate() error {
	switch {
	case len(c.Tenant) == 0:
		return ErrEmptyTenant
	case len(c.Tenant) > MaxTenantBytes:
		return fmt.Errorf("%w: %d > %d", ErrTenantTooLong, len(c.Tenant), MaxTenantBytes)
	case !c.Purpose.Valid():
		return fmt.Errorf("%w: %d", ErrUnknownPurpose, uint8(c.Purpose))
	case !c.Direction.Valid():
		return fmt.Errorf("%w: %d", ErrUnknownDirection, uint8(c.Direction))
	}
	return nil
}

// Encode returns the canonical wire encoding of the context:
//
//	u32be(len(tenant))    || tenant
//	u32be(1)              || purpose
//	u32be(2)              || version
//	u32be(1)              || direction
//	u32be(8)              || epoch
//
// Every field, including the fixed-width scalar fields, carries its own
// four-byte big-endian length prefix. This makes the field boundaries
// unambiguous and prevents concatenation-collision attacks in which one
// field's bytes are reinterpreted as another field.
func (c Context) Encode() ([]byte, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}

	var purpose [1]byte
	purpose[0] = byte(c.Purpose)
	var version [2]byte
	binary.BigEndian.PutUint16(version[:], c.Version)
	var direction [1]byte
	direction[0] = byte(c.Direction)
	var epoch [8]byte
	binary.BigEndian.PutUint64(epoch[:], c.Epoch)

	size := 4 + len(c.Tenant) + 4 + len(purpose) + 4 + len(version) + 4 + len(direction) + 4 + len(epoch)
	if size > MaxContextBytes {
		return nil, ErrContextTooLong
	}

	out := make([]byte, 0, size)
	out = appendField(out, c.Tenant)
	out = appendField(out, purpose[:])
	out = appendField(out, version[:])
	out = appendField(out, direction[:])
	out = appendField(out, epoch[:])
	return out, nil
}

// MarshalBinary implements encoding.BinaryMarshaler.
func (c Context) MarshalBinary() ([]byte, error) {
	return c.Encode()
}

func appendField(dst, field []byte) []byte {
	var length [4]byte
	binary.BigEndian.PutUint32(length[:], uint32(len(field)))
	dst = append(dst, length[:]...)
	return append(dst, field...)
}
