package netsec

import (
	"errors"
	"fmt"
	"io"
	"unicode/utf8"
)

// MaxFragmentSize is the largest accepted TOML fragment in bytes.
const MaxFragmentSize = 64 * 1024

var (
	ErrFragmentTooLarge     = errors.New("TOML fragment exceeds 64 KiB")
	ErrForbiddenRoot        = errors.New("TOML fragment key must start with user")
	ErrDuplicateDefinition  = errors.New("TOML table or key is defined more than once")
	ErrInlineTableExtension = errors.New("inline table cannot be extended")
	ErrTableArrayConflict   = errors.New("table and array of tables definitions conflict")
	ErrInvalidFragment      = errors.New("invalid TOML fragment")
)

const MaxFragmentBytes = MaxFragmentSize

var (
	ErrTooLarge            = ErrFragmentTooLarge
	ErrRootNotUser         = ErrForbiddenRoot
	ErrTableRedefined      = ErrDuplicateDefinition
	ErrInlineTableMutation = ErrInlineTableExtension
	ErrTableArrayMismatch  = ErrTableArrayConflict
)

// ValidateFragment checks that src is an acceptable TOML 1.0.0 fragment.
//
// Every top-level key path must begin with the literal key user. Keys below
// that prefix may be named anything. Table headers, dotted keys and quoted keys
// are all resolved to the same decoded path before the check is applied.
func ValidateFragment(src []byte) error {
	if len(src) > MaxFragmentSize {
		return fmt.Errorf("%w: got %d bytes", ErrFragmentTooLarge, len(src))
	}
	if !utf8.Valid(src) {
		return fmt.Errorf("%w: input is not valid UTF-8", ErrInvalidFragment)
	}

	p := newParser(src)
	return p.parse()
}

// Validate is an alias for ValidateFragment.
func Validate(src []byte) error {
	return ValidateFragment(src)
}

// ValidateFragmentString is the string form of ValidateFragment.
func ValidateFragmentString(src string) error {
	return ValidateFragment([]byte(src))
}

// ValidateFragmentReader reads at most MaxFragmentSize+1 bytes from r and
// validates the result.
func ValidateFragmentReader(r io.Reader) error {
	if r == nil {
		return fmt.Errorf("%w: nil reader", ErrInvalidFragment)
	}
	limited := io.LimitReader(r, MaxFragmentSize+1)
	src, err := io.ReadAll(limited)
	if err != nil {
		return fmt.Errorf("read TOML fragment: %w", err)
	}
	return ValidateFragment(src)
}

// IsValidFragment reports whether src would be accepted by ValidateFragment.
func IsValidFragment(src []byte) bool {
	return ValidateFragment(src) == nil
}
