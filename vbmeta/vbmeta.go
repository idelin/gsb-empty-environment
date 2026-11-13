// Package vbmeta implements admission checks for vbmeta images in a
// simulated device boot model. It verifies the vbmeta header (magic,
// flags, rollback index, signing key digest) and its chained partition
// descriptors against the caller-supplied root of trust and the
// device's persisted rollback state and lock status.
package vbmeta

import (
	"errors"
	"fmt"
)

// Magic is the four-byte magic every vbmeta header must carry.
const Magic = "AVB0"

// Header flags.
const (
	// FlagHashtreeDisabled (0x1) disables hashtree verification.
	FlagHashtreeDisabled uint32 = 1 << 0
	// FlagVerificationDisabled (0x2) disables verification entirely.
	FlagVerificationDisabled uint32 = 1 << 1
)

// RollbackIndexLocationMain is the reserved rollback index location used
// by the top-level vbmeta image itself. Chained partition descriptors
// must not reference it.
const RollbackIndexLocationMain uint32 = 0

var (
	ErrBadMagic           = errors.New("vbmeta: bad magic")
	ErrUntrustedKey       = errors.New("vbmeta: public key digest does not match root of trust on locked device")
	ErrDisableFlagLocked  = errors.New("vbmeta: verification-disabling flag set on locked device")
	ErrRollback           = errors.New("vbmeta: rollback index older than stored value")
	ErrLocationZero       = errors.New("vbmeta: chained partition uses reserved rollback index location 0")
	ErrLocationDuplicated = errors.New("vbmeta: duplicate rollback index location in chain")
)

// Digest is a public key digest (e.g. SHA-256 of the key).
type Digest [32]byte

// Header describes the relevant fields of a vbmeta image header.
type Header struct {
	Magic           string
	Flags           uint32
	RollbackIndex   uint64
	PublicKeyDigest Digest
}

// ChainDescriptor describes a chained partition entry carried in the
// vbmeta image.
type ChainDescriptor struct {
	PartitionName         string
	PublicKeyDigest       Digest
	RollbackIndexLocation uint32
}

// Device models the persisted boot state of a device.
type Device struct {
	// Locked reports whether the device is in the locked state.
	Locked bool
	// Rollback stores the persisted rollback index per location.
	// A nil map is treated as all zeros.
	Rollback map[uint32]uint64
}

func (d *Device) stored(location uint32) uint64 {
	if d.Rollback == nil {
		return 0
	}
	return d.Rollback[location]
}

// Admit verifies a vbmeta header and its chained partition descriptors
// against the device state and the caller-supplied root of trust.
//
// On success, stored rollback indices that were exceeded are advanced.
// On any failure, the device state is left untouched.
func (d *Device) Admit(h Header, chain []ChainDescriptor, rootOfTrust Digest) error {
	if h.Magic != Magic {
		return ErrBadMagic
	}
	if h.PublicKeyDigest != rootOfTrust && d.Locked {
		return ErrUntrustedKey
	}
	if d.Locked && h.Flags&(FlagHashtreeDisabled|FlagVerificationDisabled) != 0 {
		return fmt.Errorf("%w: flags=0x%x", ErrDisableFlagLocked, h.Flags)
	}
	seen := make(map[uint32]string, len(chain))
	for _, desc := range chain {
		if desc.RollbackIndexLocation == RollbackIndexLocationMain {
			return fmt.Errorf("%w: partition %q", ErrLocationZero, desc.PartitionName)
		}
		if prev, ok := seen[desc.RollbackIndexLocation]; ok {
			return fmt.Errorf("%w: location %d used by %q and %q",
				ErrLocationDuplicated, desc.RollbackIndexLocation, prev, desc.PartitionName)
		}
		seen[desc.RollbackIndexLocation] = desc.PartitionName
	}
	storedMain := d.stored(RollbackIndexLocationMain)
	if h.RollbackIndex < storedMain {
		return fmt.Errorf("%w: got %d, stored %d", ErrRollback, h.RollbackIndex, storedMain)
	}
	// The whole chain passed; only now advance the stored rollback value.
	if h.RollbackIndex > storedMain {
		if d.Rollback == nil {
			d.Rollback = make(map[uint32]uint64)
		}
		d.Rollback[RollbackIndexLocationMain] = h.RollbackIndex
	}
	return nil
}
