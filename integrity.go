package netsec

import (
	"fmt"
	"strings"
)

// IntegrityLevel models the creator integrity carried by a namespace object.
type IntegrityLevel uint8

const (
	IntegrityUntrusted IntegrityLevel = iota
	IntegrityLow
	IntegrityMedium
	IntegrityHigh
	IntegritySystem
)

// GlobalCreatorMinimumIntegrity is the minimum integrity accepted for a
// creator of a cross-session Global object. Medium-integrity user processes
// are treated as low privilege for this purpose; policy allowlists cannot
// make them acceptable.
const GlobalCreatorMinimumIntegrity = IntegrityHigh

func (l IntegrityLevel) String() string {
	switch l {
	case IntegrityUntrusted:
		return "untrusted"
	case IntegrityLow:
		return "low"
	case IntegrityMedium:
		return "medium"
	case IntegrityHigh:
		return "high"
	case IntegritySystem:
		return "system"
	default:
		return fmt.Sprintf("integrity(%d)", uint8(l))
	}
}

// Valid reports whether l is a defined integrity level.
func (l IntegrityLevel) Valid() bool {
	return l <= IntegritySystem
}

// IsLowPrivilege reports whether a creator at this integrity must never be
// admitted for a Global, cross-session object.
func (l IntegrityLevel) IsLowPrivilege() bool {
	return !l.Valid() || l < GlobalCreatorMinimumIntegrity
}

// Creator identifies the security context recorded on a created object.
type Creator struct {
	SID       string
	Integrity IntegrityLevel
}

// Validate checks the creator record.
func (c Creator) Validate() error {
	if c.SID == "" {
		return fmt.Errorf("%w: empty creator SID", ErrInvalidCreator)
	}
	if strings.ContainsRune(c.SID, 0) {
		return fmt.Errorf("%w: creator SID contains NUL", ErrInvalidCreator)
	}
	if !c.Integrity.Valid() {
		return fmt.Errorf("%w: %d", ErrInvalidIntegrity, c.Integrity)
	}
	return nil
}
