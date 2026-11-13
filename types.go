package netsec

import "errors"

// Access is a bit mask of object access rights. Concrete rights live in the
// low bits; generic rights live in the high bits and must be expanded through
// the object type's fixed mapping before any comparison.
type Access uint32

const (
	AccessRead    Access = 1 << 0
	AccessWrite   Access = 1 << 1
	AccessExecute Access = 1 << 2

	GenericRead  Access = 1 << 30
	GenericWrite Access = 1 << 29

	genericBits = GenericRead | GenericWrite
)

// ObjectType defines which access bits may be duplicated (CopyableMask) and
// the fixed expansion of generic rights into concrete bits.
type ObjectType struct {
	Name         string
	CopyableMask Access
	GenericRead  Access
	GenericWrite Access
}

// Expand maps generic bits to concrete bits and drops the generic bits.
// Callers must expand before comparing masks.
func (t ObjectType) Expand(a Access) Access {
	out := a &^ genericBits
	if a&GenericRead != 0 {
		out |= t.GenericRead
	}
	if a&GenericWrite != 0 {
		out |= t.GenericWrite
	}
	return out
}

// FileType is the object type used by the model scenarios.
var FileType = ObjectType{
	Name:         "File",
	CopyableMask: AccessRead | AccessWrite | AccessExecute,
	GenericRead:  AccessRead,
	GenericWrite: AccessWrite,
}

// ProcessRef binds a process ID to a generation. A request carrying a stale
// generation must not resolve to the restarted process.
type ProcessRef struct {
	ID         uint32
	Generation uint32
}

// HandleRef binds a table slot to a handle generation. After a slot is closed
// and reused, references to the old generation are invalid.
type HandleRef struct {
	Slot       uint32
	Generation uint32
}

// ObjectID identifies a kernel object in the host.
type ObjectID uint32

// IntegrityLevel of a process. Duplicating a handle across integrity levels
// requires a host-issued broker capability.
type IntegrityLevel int

const (
	IntegrityLow IntegrityLevel = iota
	IntegrityMedium
	IntegrityHigh
)

// Capability is a host-issued broker capability. It binds the source and
// target process generations and the object type. The MAC is computed by the
// host over a secret; callers can copy a capability but cannot mint one.
type Capability struct {
	Source     ProcessRef
	Target     ProcessRef
	ObjectType string
	mac        uint64
}

// DuplicateRequest describes one handle duplication. The request is only
// meaningful while every bound generation still matches the live state at the
// linearization point.
type DuplicateRequest struct {
	Source        ProcessRef
	SourceHandle  HandleRef
	Target        ProcessRef
	DesiredAccess Access
	SameAccess    bool
	Inherit       bool
	Broker        *Capability
}

var (
	ErrNoSuchProcess     = errors.New("unknown or stale process reference")
	ErrNoSuchObject      = errors.New("unknown object")
	ErrNoSuchObjectType  = errors.New("unknown object type")
	ErrNoSuchHandle      = errors.New("unknown handle slot")
	ErrStaleHandle       = errors.New("handle closed or slot reused")
	ErrStaleObject       = errors.New("object generation changed")
	ErrBrokerRequired    = errors.New("cross-integrity duplication needs a valid broker capability")
	ErrAccessConflict    = errors.New("sameAccess combined with non-zero desiredAccess")
	ErrEmptyAccess       = errors.New("resulting access mask is empty")
	ErrAccessNotGranted  = errors.New("desired access exceeds source granted access")
	ErrAccessNotCopyable = errors.New("desired access exceeds object copyable mask")
)
