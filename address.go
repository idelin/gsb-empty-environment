package netsec

import (
	"encoding/hex"
	"fmt"
)

const (
	// AddressSize is the size of the private address used by this package.
	AddressSize = 6

	// PRandTypeMask selects the two type bits in the first prand byte.
	PRandTypeMask = 0xc0

	// ResolvablePRandType is the 01 type required by this problem.
	ResolvablePRandType = 0x40
)

// Address is a six-octet private address carried in network byte order:
//
//	hash[0] hash[1] hash[2] prand[0] prand[1] prand[2]
//
// The final three bytes are prand and their top two bits must be 01. The
// package follows the problem's hash||prand convention; it does not reorder
// bytes as some BLE APIs do.
type Address [AddressSize]byte

// NewAddress builds an address from the three-byte hash and three-byte prand.
// The slices are copied.
func NewAddress(hash, prand []byte) (Address, error) {
	if len(hash) != 3 {
		return Address{}, fmt.Errorf("%w: hash length %d, want 3", ErrInvalidAddress, len(hash))
	}
	if len(prand) != 3 {
		return Address{}, fmt.Errorf("%w: prand length %d, want 3", ErrInvalidAddress, len(prand))
	}

	var address Address
	copy(address[:3], hash)
	copy(address[3:], prand)
	if err := address.Validate(); err != nil {
		return Address{}, err
	}
	return address, nil
}

// AddressFromBytes parses exactly six address octets in network byte order.
func AddressFromBytes(raw []byte) (Address, error) {
	if len(raw) != AddressSize {
		return Address{}, fmt.Errorf("%w: length %d, want %d", ErrInvalidAddress, len(raw), AddressSize)
	}

	var address Address
	copy(address[:], raw)
	if err := address.Validate(); err != nil {
		return Address{}, err
	}
	return address, nil
}

// ParseAddress parses twelve hexadecimal digits as hash||prand.
func ParseAddress(text string) (Address, error) {
	if len(text) != AddressSize*2 {
		return Address{}, fmt.Errorf("%w: hexadecimal length %d, want %d", ErrInvalidAddress, len(text), AddressSize*2)
	}
	raw, err := hex.DecodeString(text)
	if err != nil {
		return Address{}, fmt.Errorf("%w: %v", ErrInvalidAddress, err)
	}
	return AddressFromBytes(raw)
}

// Validate checks the resolvable private address type bits in prand.
func (a Address) Validate() error {
	if a[3]&PRandTypeMask != ResolvablePRandType {
		return fmt.Errorf("%w: prand type bits are %02b, want 01", ErrInvalidPRand, (a[3]&PRandTypeMask)>>6)
	}
	return nil
}

// IsResolvable reports whether the prand type bits are 01.
func (a Address) IsResolvable() bool {
	return a.Validate() == nil
}

// Hash returns a copy of the first three address octets.
func (a Address) Hash() [3]byte {
	return [3]byte{a[0], a[1], a[2]}
}

// PRand returns a copy of the last three address octets.
func (a Address) PRand() [3]byte {
	return [3]byte{a[3], a[4], a[5]}
}

// String returns the address as lowercase hexadecimal.
func (a Address) String() string {
	return hex.EncodeToString(a[:])
}

// MarshalText implements encoding.TextMarshaler.
func (a Address) MarshalText() ([]byte, error) {
	return []byte(a.String()), nil
}

// UnmarshalText implements encoding.TextUnmarshaler.
func (a *Address) UnmarshalText(text []byte) error {
	address, err := ParseAddress(string(text))
	if err != nil {
		return err
	}
	*a = address
	return nil
}
