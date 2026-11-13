package netsec

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// Errors returned by the package. Callers can use errors.Is to classify a
// failure without depending on the exact error text.
var (
	ErrInvalidOption      = errors.New("netsec: invalid TCP option")
	ErrDuplicateOption    = errors.New("netsec: duplicate TCP option")
	ErrConflictingOption  = errors.New("netsec: conflicting TCP option")
	ErrInvalidWindowScale = errors.New("netsec: window scale must be between 0 and 14")
	ErrNoMSSMatch         = errors.New("netsec: no MSS table entry is less than or equal to the advertised MSS")
	ErrBadCookie          = errors.New("netsec: invalid SYN cookie")
	ErrBadACK             = errors.New("netsec: ACK does not acknowledge the cookie")
)

const (
	tcpOptionEnd         = 0
	tcpOptionNOP         = 1
	tcpOptionMSS         = 2
	tcpOptionWindowScale = 3
	tcpOptionSACK        = 4

	// windowScaleAbsent is the four-bit encoding used when the SYN did not
	// contain a window scale option.
	windowScaleAbsent = 15
)

// SYNOptions describes the SYN options that are encoded into a cookie.
//
// A nil MSS or WindowScale pointer means that the option was not present in
// the SYN. A non-nil pointer to zero is different from a nil pointer: zero is
// a valid window scale value. The MSS field holds the value advertised by the
// client; cookie generation maps it to the configured MSS table.
type SYNOptions struct {
	MSS           *uint16
	WindowScale   *uint8
	SACKPermitted bool
}

// MSSOption returns a pointer suitable for use as SYNOptions.MSS.
func MSSOption(v uint16) *uint16 { return &v }

// WindowScaleOption returns a pointer suitable for use as
// SYNOptions.WindowScale.
func WindowScaleOption(v uint8) *uint8 { return &v }

// ParseSYNOptions decodes the option bytes of a TCP SYN.
//
// It accepts EOL and NOP padding and skips unknown well-formed options. The
// three options represented by SYNOptions must have their RFC lengths. A
// repeated MSS or window scale option is rejected as a duplicate (or, when the
// repeated values differ, as a conflict). A repeated SACK-permitted option is
// always a duplicate.
func ParseSYNOptions(raw []byte) (SYNOptions, error) {
	var opts SYNOptions
	var haveMSS, haveWindowScale, haveSACK bool
	var mssValue uint16

	for len(raw) > 0 {
		kind := raw[0]
		switch kind {
		case tcpOptionEnd:
			return opts, nil
		case tcpOptionNOP:
			raw = raw[1:]
			continue
		}

		if len(raw) < 2 {
			return SYNOptions{}, fmt.Errorf("%w: truncated option %d", ErrInvalidOption, kind)
		}
		length := int(raw[1])
		if length < 2 || length > len(raw) {
			return SYNOptions{}, fmt.Errorf("%w: option %d has invalid length %d", ErrInvalidOption, kind, length)
		}
		data := raw[2:length]

		switch kind {
		case tcpOptionMSS:
			if length != 4 {
				return SYNOptions{}, fmt.Errorf("%w: MSS option has length %d", ErrInvalidOption, length)
			}
			value := binary.BigEndian.Uint16(data)
			if value == 0 {
				return SYNOptions{}, fmt.Errorf("%w: MSS option has zero value", ErrInvalidOption)
			}
			if haveMSS {
				if mssValue == value {
					return SYNOptions{}, fmt.Errorf("%w: MSS option repeated", ErrDuplicateOption)
				}
				return SYNOptions{}, fmt.Errorf("%w: MSS options disagree", ErrConflictingOption)
			}
			advertised := value
			opts.MSS = &advertised
			mssValue = value
			haveMSS = true

		case tcpOptionWindowScale:
			if length != 3 {
				return SYNOptions{}, fmt.Errorf("%w: window scale option has length %d", ErrInvalidOption, length)
			}
			value := data[0]
			if value > 14 {
				return SYNOptions{}, fmt.Errorf("%w: got %d", ErrInvalidWindowScale, value)
			}
			if haveWindowScale {
				return SYNOptions{}, fmt.Errorf("%w: window scale option repeated", ErrDuplicateOption)
			}
			scale := value
			opts.WindowScale = &scale
			haveWindowScale = true

		case tcpOptionSACK:
			if length != 2 {
				return SYNOptions{}, fmt.Errorf("%w: SACK-permitted option has length %d", ErrInvalidOption, length)
			}
			if haveSACK {
				return SYNOptions{}, fmt.Errorf("%w: SACK-permitted option repeated", ErrDuplicateOption)
			}
			opts.SACKPermitted = true
			haveSACK = true
		}

		raw = raw[length:]
	}

	return opts, nil
}

// ParseOptions is an alias for ParseSYNOptions.
func ParseOptions(raw []byte) (SYNOptions, error) {
	return ParseSYNOptions(raw)
}

func (o SYNOptions) validate() error {
	if o.MSS != nil {
		if *o.MSS == 0 {
			return fmt.Errorf("%w: MSS option has zero value", ErrInvalidOption)
		}
	}
	if o.WindowScale != nil && *o.WindowScale > 14 {
		return fmt.Errorf("%w: got %d", ErrInvalidWindowScale, *o.WindowScale)
	}
	return nil
}
