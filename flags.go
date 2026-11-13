package netsec

import "fmt"

// Flags is the flags byte carried in WebAuthn authenticator data.
type Flags uint8

const (
	// FlagUserPresent indicates that the authenticator obtained user presence.
	FlagUserPresent Flags = 0x01
	// FlagUserVerified indicates that the authenticator performed user verification.
	FlagUserVerified Flags = 0x04
	// FlagBackupEligible indicates that the credential may be synchronized.
	FlagBackupEligible Flags = 0x08
	// FlagBackedUp indicates that the credential is currently backed up.
	FlagBackedUp Flags = 0x10
	// FlagAttestedCredentialData indicates that attested credential data is present.
	FlagAttestedCredentialData Flags = 0x40
	// FlagExtensionData indicates that extension data is present.
	FlagExtensionData Flags = 0x80

	// FlagReservedBit1 is reserved and must be zero.
	FlagReservedBit1 Flags = 0x02
	// FlagReservedBit2 is reserved and must be zero.
	FlagReservedBit2 Flags = 0x20

	// ReservedFlagMask covers all reserved bits in the flags byte.
	ReservedFlagMask Flags = FlagReservedBit1 | FlagReservedBit2
)

// Has reports whether all bits in want are set in f.
func (f Flags) Has(want Flags) bool {
	return f&want == want
}

// ReservedBits returns the reserved bits that are set in f.
func (f Flags) ReservedBits() Flags {
	return f & ReservedFlagMask
}

// String returns the hexadecimal representation of f.
func (f Flags) String() string {
	return fmt.Sprintf("0x%02x", uint8(f))
}
