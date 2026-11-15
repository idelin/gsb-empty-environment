package netsec

import (
	"errors"
	"fmt"
)

const (
	// WindowSize is the maximum LZ back-reference distance in bytes.
	WindowSize = 4096
	// MinMatch is the shortest match that may be emitted.
	MinMatch = 3
	// MaxMatch is the longest match that fits the one-byte length field.
	MaxMatch = 255
)

// Label separates public and confidential dictionaries, including within the
// same tenant.
type Label uint8

const (
	// Public is the public dictionary label.
	Public Label = iota
	// Confidential is the confidential dictionary label.
	Confidential
)

func (l Label) String() string {
	switch l {
	case Public:
		return "public"
	case Confidential:
		return "confidential"
	default:
		return fmt.Sprintf("label(%d)", uint8(l))
	}
}

func (l Label) valid() bool {
	return l == Public || l == Confidential
}

// Version identifies an immutable dictionary prefix. Offset is the number of
// committed plaintext bytes in the tenant/label partition before the stream
// that produced this version.
type Version struct {
	Tenant string
	Label  Label
	Epoch  uint64
	Offset uint64
}

// Compressed is a compressed stream and the dictionary version against which
// it must be decoded.
type Compressed struct {
	Data    []byte
	Version Version
}

// Result is an alias for Compressed.
type Result = Compressed

// Bytes returns the compressed token stream.
func (c Compressed) Bytes() []byte { return c.Data }

// Len reports the compressed length observed by a caller.
func (c Compressed) Len() int { return len(c.Data) }

var (
	// ErrInvalidTenant indicates an empty or otherwise unusable tenant ID.
	ErrInvalidTenant = errors.New("netsec: invalid tenant ID")
	// ErrInvalidLabel indicates a label other than Public or Confidential.
	ErrInvalidLabel = errors.New("netsec: invalid label")
	// ErrUnknownTenant indicates that the tenant is not registered.
	ErrUnknownTenant = errors.New("netsec: unknown tenant")
	// ErrTenantExists indicates an attempt to register an active tenant.
	ErrTenantExists = errors.New("netsec: tenant already registered")
	// ErrStaleDictionary indicates a reset, release, or tenant-ID reuse
	// invalidated the requested dictionary version.
	ErrStaleDictionary = errors.New("netsec: stale dictionary version")
	// ErrVersionMismatch indicates a version used with another tenant or label.
	ErrVersionMismatch = errors.New("netsec: dictionary version does not match tenant or label")
	// ErrStreamClosed indicates a write or finish after the stream left the
	// open state.
	ErrStreamClosed = errors.New("netsec: stream is closed")
	// ErrStreamCanceled indicates a canceled stream.
	ErrStreamCanceled = errors.New("netsec: stream canceled")
	// ErrCorrupt indicates an invalid compressed token stream.
	ErrCorrupt = errors.New("netsec: corrupt compressed stream")
)

func validateTenant(id string) error {
	if id == "" {
		return ErrInvalidTenant
	}
	return nil
}

func validateLabel(label Label) error {
	if !label.valid() {
		return ErrInvalidLabel
	}
	return nil
}

// tailWindow returns the largest suffix of b that can be referenced from a
// position immediately after b. The returned slice aliases b.
func tailWindow(b []byte) []byte {
	if len(b) > WindowSize {
		return b[len(b)-WindowSize:]
	}
	return b
}
