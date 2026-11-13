package netsec

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"math/big"
	"strings"
)

const (
	// SafetyNumberDigits is the length of the decimal check value.
	SafetyNumberDigits = 60

	// SafetyNumberGroups is the number of displayed groups.
	SafetyNumberGroups = 12

	// SafetyNumberGroupDigits is the number of digits per displayed group.
	SafetyNumberGroupDigits = 5
)

// SafetyNumber is the decimal display and raw checksum for one two-party
// session.
type SafetyNumber struct {
	// Digits is exactly 60 ASCII decimal digits, including leading zeroes.
	Digits string

	// Display is Digits split into 12 groups of 5 digits separated by
	// single spaces.
	Display string

	// Checksum is the raw 32-byte SHA256 value before reduction modulo
	// 10^60. This is the value carried by a QR payload.
	Checksum [32]byte

	// LowerDigest and HigherDigest are the two account identity digests in
	// the byte order used for the session hash.
	LowerDigest  [32]byte
	HigherDigest [32]byte
}

// MultiDeviceSafetyNumber and PairwiseSafetyNumber are descriptive aliases.
type MultiDeviceSafetyNumber = SafetyNumber
type PairwiseSafetyNumber = SafetyNumber

// String returns the grouped display form.
func (s *SafetyNumber) String() string {
	if s == nil {
		return ""
	}
	return s.Display
}

// RawChecksum returns the raw 32-byte checksum.
func (s *SafetyNumber) RawChecksum() [32]byte {
	if s == nil {
		return [32]byte{}
	}
	return s.Checksum
}

// Groups returns the 12 five-digit display groups.
func (s *SafetyNumber) Groups() []string {
	if s == nil || len(s.Digits) != SafetyNumberDigits {
		return nil
	}
	groups := make([]string, 0, SafetyNumberGroups)
	for i := 0; i < SafetyNumberDigits; i += SafetyNumberGroupDigits {
		groups = append(groups, s.Digits[i:i+SafetyNumberGroupDigits])
	}
	return groups
}

// ComputeSafetyNumber computes the two-party safety number from two complete
// account snapshots. Account order does not matter.
func ComputeSafetyNumber(a, b *DeviceListSnapshot) (*SafetyNumber, error) {
	left, err := EnsureValid(a)
	if err != nil {
		return nil, err
	}
	right, err := EnsureValid(b)
	if err != nil {
		return nil, err
	}
	if left.AccountID == right.AccountID {
		return nil, ErrDuplicateAccount
	}
	return SafetyNumberFromAccountDigests(left.AccountDigest, right.AccountDigest), nil
}

// SafetyNumberFromAccountDigests computes a safety number directly from two
// account identity digests. The two digests are sorted bytewise before
// hashing, which makes QR direction swaps deterministic.
func SafetyNumberFromAccountDigests(first, second [32]byte) *SafetyNumber {
	checksum, lower, higher := sessionChecksum(first, second)
	digits := DecimalCheckValue(checksum)

	return &SafetyNumber{
		Digits:       digits,
		Display:      GroupSafetyNumberDigits(digits),
		Checksum:     checksum,
		LowerDigest:  lower,
		HigherDigest: higher,
	}
}

func sessionChecksum(first, second [32]byte) ([32]byte, [32]byte, [32]byte) {
	lower, higher := first, second
	if bytes.Compare(lower[:], higher[:]) > 0 {
		lower, higher = higher, lower
	}

	var message [64]byte
	copy(message[:32], lower[:])
	copy(message[32:], higher[:])
	return sha256.Sum256(message[:]), lower, higher
}

// ComputeSessionSafetyNumber is an alias for ComputeSafetyNumber.
func ComputeSessionSafetyNumber(a, b *DeviceListSnapshot) (*SafetyNumber, error) {
	return ComputeSafetyNumber(a, b)
}

// PairwiseChecksum returns SHA256 of the bytewise-sorted account identity
// digests. It is the raw checksum embedded in a QR payload.
func PairwiseChecksum(first, second [32]byte) [32]byte {
	checksum, _, _ := sessionChecksum(first, second)
	return checksum
}

// DecimalCheckValue interprets checksum as a big-endian integer, reduces it
// modulo 10^60, and left-pads with zeroes to exactly 60 digits.
func DecimalCheckValue(checksum [32]byte) string {
	value := new(big.Int).SetBytes(checksum[:])
	modulus := new(big.Int).Exp(big.NewInt(10), big.NewInt(SafetyNumberDigits), nil)
	value.Mod(value, modulus)
	digits := value.String()
	if len(digits) >= SafetyNumberDigits {
		return digits[len(digits)-SafetyNumberDigits:]
	}
	return strings.Repeat("0", SafetyNumberDigits-len(digits)) + digits
}

// GroupSafetyNumberDigits validates a 60-digit value and inserts a space
// after every group of five digits.
func GroupSafetyNumberDigits(digits string) string {
	if len(digits) != SafetyNumberDigits {
		return digits
	}
	for _, digit := range digits {
		if digit < '0' || digit > '9' {
			return digits
		}
	}
	var builder strings.Builder
	builder.Grow(SafetyNumberDigits + SafetyNumberGroups - 1)
	for i := 0; i < len(digits); i += SafetyNumberGroupDigits {
		if i > 0 {
			builder.WriteByte(' ')
		}
		builder.WriteString(digits[i : i+SafetyNumberGroupDigits])
	}
	return builder.String()
}

// FormatCheckValue exposes the decimal formatting operation for tests and
// display code that already has a raw checksum.
func FormatCheckValue(checksum [32]byte) string {
	return DecimalCheckValue(checksum)
}

// ValidateDecimalSafetyNumber checks the exact 60-digit invariant.
func ValidateDecimalSafetyNumber(digits string) error {
	if len(digits) != SafetyNumberDigits {
		return fmt.Errorf("%w: got %d digits, want %d", ErrInvalidQRPayload, len(digits), SafetyNumberDigits)
	}
	for _, digit := range digits {
		if digit < '0' || digit > '9' {
			return fmt.Errorf("%w: non-decimal safety-number digit", ErrInvalidQRPayload)
		}
	}
	return nil
}
