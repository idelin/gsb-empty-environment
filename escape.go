package netsec

import (
	"fmt"
)

// Default HL7 v2 separator characters. A FieldRules value allows a message
// with a non-standard MSH separator layout to be handled explicitly.
const (
	DefaultFieldSep        byte = '|'
	DefaultComponentSep    byte = '^'
	DefaultRepetitionSep   byte = '~'
	DefaultEscapeChar      byte = '\\'
	DefaultSubcomponentSep byte = '&'
)

// FieldRules describes the separator characters used by a message, per MSH-1
// and MSH-2.
type FieldRules struct {
	Field        byte
	Component    byte
	Repetition   byte
	Escape       byte
	Subcomponent byte
}

// DefaultFieldRules returns the standard HL7 v2 separator set.
func DefaultFieldRules() FieldRules {
	return FieldRules{
		Field:        DefaultFieldSep,
		Component:    DefaultComponentSep,
		Repetition:   DefaultRepetitionSep,
		Escape:       DefaultEscapeChar,
		Subcomponent: DefaultSubcomponentSep,
	}
}

// ValidateStoredField is the storage boundary check. It accepts a field value
// as received (already unescaped or raw) and rejects it if it contains an
// MLLP framing byte (VT/FS), whether literal or produced by decoding an HL7
// escape such as \X0B\ or \X1C\.
func ValidateStoredField(value []byte) error {
	for i := 0; i < len(value); i++ {
		switch value[i] {
		case VT, FS:
			return fmt.Errorf("%w: decoded framing byte 0x%02X at field offset %d",
				ErrEscape, value[i], i)
		}
	}
	return nil
}

// UnescapeField decodes HL7 v2 escape sequences in a stored field using the
// standard rules:
//
//	\F\ \S\ \T\ \R\ \E\   separators and the escape character
//	\Xdd...\               hex-encoded bytes (one or more pairs)
//	\H\ \N\                 highlighting markers (no output)
//
// It fails closed: an unterminated sequence, an unknown sequence, or a decoded
// VT (0x0B) / FS (0x1C) returns an error wrapping ErrEscape, so framing bytes
// cannot reach stored fields through \X0B\ / \X1C\. A literal VT/FS is
// rejected as well.
func UnescapeField(value []byte) ([]byte, error) {
	return UnescapeFieldWithRules(value, DefaultFieldRules())
}

// UnescapeFieldWithRules is UnescapeField with an explicit separator set.
func UnescapeFieldWithRules(value []byte, rules FieldRules) ([]byte, error) {
	out := make([]byte, 0, len(value))
	for i := 0; i < len(value); {
		b := value[i]
		if b != rules.Escape {
			if b == VT || b == FS {
				return nil, fmt.Errorf("%w: literal framing byte 0x%02X at field offset %d",
					ErrEscape, b, i)
			}
			out = append(out, b)
			i++
			continue
		}

		closeIdx := indexByte(value, rules.Escape, i+1)
		if closeIdx < 0 {
			return nil, fmt.Errorf("%w: unterminated escape sequence at field offset %d",
				ErrEscape, i)
		}
		seq := value[i+1 : closeIdx]
		decoded, err := decodeEscape(seq, rules)
		if err != nil {
			return nil, fmt.Errorf("%w: %q at field offset %d", ErrEscape, string(seq), i)
		}
		for _, db := range decoded {
			if db == VT || db == FS {
				return nil, fmt.Errorf("%w: escape %q decodes to framing byte 0x%02X",
					ErrEscape, string(seq), db)
			}
		}
		out = append(out, decoded...)
		i = closeIdx + 1
	}
	return out, nil
}

func decodeEscape(seq []byte, rules FieldRules) ([]byte, error) {
	if len(seq) == 0 {
		return nil, fmt.Errorf("empty escape sequence")
	}
	// Highlighting markers carry no data.
	if string(seq) == "H" || string(seq) == "N" {
		return nil, nil
	}
	if len(seq) == 1 {
		switch seq[0] {
		case 'F':
			return []byte{rules.Field}, nil
		case 'S':
			return []byte{rules.Component}, nil
		case 'T':
			return []byte{rules.Subcomponent}, nil
		case 'R':
			return []byte{rules.Repetition}, nil
		case 'E':
			return []byte{rules.Escape}, nil
		}
		return nil, fmt.Errorf("unknown single-character escape")
	}
	if seq[0] == 'X' || seq[0] == 'x' {
		hexPart := seq[1:]
		if len(hexPart) == 0 || len(hexPart)%2 != 0 {
			return nil, fmt.Errorf("malformed hex escape")
		}
		out := make([]byte, 0, len(hexPart)/2)
		for j := 0; j < len(hexPart); j += 2 {
			hi, ok1 := hexNibble(hexPart[j])
			lo, ok2 := hexNibble(hexPart[j+1])
			if !ok1 || !ok2 {
				return nil, fmt.Errorf("malformed hex escape")
			}
			out = append(out, hi<<4|lo)
		}
		return out, nil
	}
	return nil, fmt.Errorf("unknown escape sequence")
}

func hexNibble(b byte) (byte, bool) {
	switch {
	case b >= '0' && b <= '9':
		return b - '0', true
	case b >= 'A' && b <= 'F':
		return b - 'A' + 10, true
	case b >= 'a' && b <= 'f':
		return b - 'a' + 10, true
	default:
		return 0, false
	}
}

func indexByte(s []byte, c byte, from int) int {
	for i := from; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}
