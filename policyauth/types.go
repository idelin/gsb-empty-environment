package policyauth

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
)

// Digest is a SHA-256 policy digest (the TPM2B_DIGEST approvedPolicy).
type Digest [sha256.Size]byte

// Name is a TPM object name: the nameAlg digest over the object's public area.
type Name [sha256.Size]byte

// PolicyRef binds an authorization to exactly one device and one sealed
// object, so a token issued for another device or object cannot be replayed.
type PolicyRef struct {
	DeviceID string
	ObjectID string
}

var (
	// ErrEmptyDeviceID is returned when a policyRef carries no device id.
	ErrEmptyDeviceID = errors.New("policyauth: policyRef device id must not be empty")
	// ErrEmptyObjectID is returned when a policyRef carries no object id.
	ErrEmptyObjectID = errors.New("policyauth: policyRef object id must not be empty")
)

// MarshalBinary returns the canonical encoding of the ref used inside the
// signed message: a u16-length-prefixed UTF-8 device id followed by the
// u16-length-prefixed object id. The length prefixes make the encoding
// unambiguous and deterministic.
func (r PolicyRef) MarshalBinary() ([]byte, error) {
	if r.DeviceID == "" {
		return nil, ErrEmptyDeviceID
	}
	if r.ObjectID == "" {
		return nil, ErrEmptyObjectID
	}
	var buf bytes.Buffer
	if err := writeRefField(&buf, r.DeviceID); err != nil {
		return nil, err
	}
	if err := writeRefField(&buf, r.ObjectID); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func writeRefField(buf *bytes.Buffer, value string) error {
	if len(value) > 0xFFFF {
		return errors.New("policyauth: policyRef field exceeds 65535 bytes")
	}
	var lengthBytes [2]byte
	binary.BigEndian.PutUint16(lengthBytes[:], uint16(len(value)))
	buf.Write(lengthBytes[:])
	buf.WriteString(value)
	return nil
}

// Equal reports whether two refs bind the same device and object.
func (r PolicyRef) Equal(other PolicyRef) bool {
	return r.DeviceID == other.DeviceID && r.ObjectID == other.ObjectID
}
