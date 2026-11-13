package netsec

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"strings"
)

// ValidateOutboundMessage checks the constraints that must hold before a body
// may be placed inside an MLLP frame:
//
//   - no raw 0x0B or 0x1C anywhere;
//   - every 0x0D ends a complete, non-empty HL7 segment;
//   - each segment starts with a three-byte uppercase alphanumeric segment ID
//     followed by the standard field separator '|'.
//
// A trailing 0x0D is accepted as the final segment terminator. Empty segments,
// leading separators, and repeated separators are rejected.
func ValidateOutboundMessage(body []byte) error {
	if len(body) == 0 {
		return fmt.Errorf("%w: empty message", ErrInvalidSegment)
	}
	for _, b := range body {
		switch b {
		case StartBlock, EndBlock:
			return fmt.Errorf("%w: byte %#x", ErrForbiddenByte, b)
		}
	}

	segments := bytes.Split(body, []byte{CarriageReturn})
	if len(segments) > 0 && len(segments[len(segments)-1]) == 0 {
		segments = segments[:len(segments)-1]
	}
	if len(segments) == 0 {
		return fmt.Errorf("%w: no segments", ErrInvalidSegment)
	}

	for i, segment := range segments {
		if len(segment) == 0 {
			return fmt.Errorf("%w: empty segment at index %d", ErrInvalidSegment, i)
		}
		if err := validateSegment(segment); err != nil {
			return err
		}
	}
	return nil
}

func validateSegment(segment []byte) error {
	if len(segment) < 4 {
		return fmt.Errorf("%w: segment %q has no field separator", ErrInvalidSegment, segment)
	}
	for i := 0; i < 3; i++ {
		if !isSegmentIDByte(segment[i]) {
			return fmt.Errorf("%w: invalid segment ID byte %#x", ErrInvalidSegment, segment[i])
		}
	}
	if segment[3] != '|' {
		return fmt.Errorf("%w: segment %q is not followed by '|'", ErrInvalidSegment, segment[:3])
	}
	return nil
}

// DecodeField decodes the HL7 escapes that can affect storage safety and
// rejects any field that, raw or decoded, contains an MLLP or segment
// delimiter.
//
// Hex escapes such as \X0B\ and \X1C\ are decoded before checking, so a field
// cannot smuggle framing bytes into a database value. Standard escapes \F\,
// \S\, \R\, \T\, and \E\ are decoded to their delimiter values. Unknown
// formatting escapes are preserved literally.
func DecodeField(raw string) (string, error) {
	// Reject raw delimiters before parsing escapes. This also covers control
	// bytes accidentally embedded inside an escape payload.
	for i := 0; i < len(raw); i++ {
		if isForbiddenDelimiter(raw[i]) {
			return "", fmt.Errorf("%w: raw byte %#x at offset %d", ErrForbiddenByte, raw[i], i)
		}
	}

	out := make([]byte, 0, len(raw))
	for i := 0; i < len(raw); {
		b := raw[i]
		if b != '\\' {
			out = append(out, b)
			i++
			continue
		}

		rest := raw[i+1:]
		relEnd := strings.IndexByte(rest, '\\')
		if relEnd < 0 {
			return "", fmt.Errorf("%w: unterminated escape at offset %d", ErrMalformedEscape, i)
		}
		esc := rest[:relEnd]
		end := i + 1 + relEnd
		if len(esc) == 0 {
			return "", fmt.Errorf("%w: empty escape at offset %d", ErrMalformedEscape, i)
		}

		switch esc[0] {
		case 'X', 'x':
			payload := esc[1:]
			if len(payload) == 0 || len(payload)%2 != 0 {
				return "", fmt.Errorf("%w: bad hex escape %q", ErrMalformedEscape, esc)
			}
			decoded, err := hex.DecodeString(payload)
			if err != nil {
				return "", fmt.Errorf("%w: bad hex escape %q", ErrMalformedEscape, esc)
			}
			for _, decodedByte := range decoded {
				if isForbiddenDelimiter(decodedByte) {
					return "", fmt.Errorf("%w: \\%s\\ decodes to %#x",
						ErrForbiddenEscape, esc, decodedByte)
				}
			}
			out = append(out, decoded...)

		case 'F':
			out = append(out, '|')
		case 'S':
			out = append(out, '^')
		case 'R':
			out = append(out, '~')
		case 'T':
			out = append(out, '&')
		case 'E':
			out = append(out, '\\')
		default:
			// Preserve formatting escapes such as \.br\ for the storage layer.
			out = append(out, raw[i:end+1]...)
		}

		i = end + 1
	}
	return string(out), nil
}

// DecodeHL7Field is an alias for DecodeField.
func DecodeHL7Field(raw string) (string, error) {
	return DecodeField(raw)
}

// ValidateStoredField reports whether a field is safe to persist after HL7
// escape decoding.
func ValidateStoredField(raw string) error {
	_, err := DecodeField(raw)
	return err
}

func isForbiddenDelimiter(b byte) bool {
	switch b {
	case StartBlock, EndBlock, CarriageReturn:
		return true
	default:
		return false
	}
}

func isSegmentIDByte(b byte) bool {
	return b >= 'A' && b <= 'Z' || b >= '0' && b <= '9'
}
