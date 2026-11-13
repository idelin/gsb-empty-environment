package netsec

import (
	"fmt"
)

// DeviceID is the stable identity assigned to a paired device.
type DeviceID string

func (id DeviceID) String() string {
	return string(id)
}

// IRK is the 128-bit identity resolving key handle stored with a device
// record. It is supplied to the host's AES callback and is never an
// authentication credential by itself.
type IRK [16]byte

// RecordGeneration identifies one version of a device record. Rotations and
// revocations advance it so tickets from an older record cannot complete.
type RecordGeneration uint64

// RecordState is the revocation state of a device record.
type RecordState uint8

const (
	// RecordActive records can participate in address resolution.
	RecordActive RecordState = iota + 1

	// RecordRevoked records are retained for identity bookkeeping but never
	// participate in resolution or connection completion.
	RecordRevoked
)

func (s RecordState) String() string {
	switch s {
	case RecordActive:
		return "active"
	case RecordRevoked:
		return "revoked"
	default:
		return fmt.Sprintf("unknown(%d)", s)
	}
}

// DeviceRecord is the table entry needed to resolve a private address and to
// verify the device's link-layer identity proof.
//
// IdentityPublicKey is the device's long-term public key in a format understood
// by the host's SignatureVerifier callback. It is not used for address
// resolution.
type DeviceRecord struct {
	DeviceID          DeviceID
	IRK               IRK
	Generation        RecordGeneration
	State             RecordState
	IdentityPublicKey []byte
}

func (r DeviceRecord) validate() error {
	if r.DeviceID == "" {
		return fmt.Errorf("%w: empty device ID", ErrInvalidRecord)
	}
	if r.State != RecordActive && r.State != RecordRevoked {
		return fmt.Errorf("%w: invalid state %d", ErrInvalidRecord, r.State)
	}
	return nil
}

func (r DeviceRecord) clone() DeviceRecord {
	r.IdentityPublicKey = append([]byte(nil), r.IdentityPublicKey...)
	return r
}
