package netsec

import (
	"errors"
	"fmt"
	"net/textproto"
	"strings"
)

// Header contains normalized article header fields. Field names are canonical
// MIME header names and folded continuation lines have been unfolded.
type Header map[string][]string

var (
	// ErrFoldedControl is returned when a folded continuation line attempts to
	// introduce a Control header field.
	ErrFoldedControl = errors.New("Control header found in a folded continuation line")

	// ErrDuplicateControl is returned when an article has more than one Control
	// header field.
	ErrDuplicateControl = errors.New("duplicate Control header")

	// ErrDuplicateSupersedes is returned when an article has more than one
	// Supersedes header field.
	ErrDuplicateSupersedes = errors.New("duplicate Supersedes header")
)

// Values returns all values for name. Header names are case-insensitive.
func (h Header) Values(name string) []string {
	return h[textproto.CanonicalMIMEHeaderKey(name)]
}

// Get returns the first value for name, or an empty string when it is absent.
func (h Header) Get(name string) string {
	values := h.Values(name)
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

// ParseHeader parses an article's raw header block. Parsing stops at the first
// empty line, so body content can never be interpreted as a header.
//
// Physical continuation lines are unfolded into one logical field value. A
// continuation line whose content starts with a Control field is rejected:
// such a line is an attempted header injection, not a normal folded value.
// Duplicate Control fields are also rejected.
func ParseHeader(raw []byte) (Header, error) {
	lines := headerLines(raw)

	headers := make(Header)
	var field, value string
	flush := func() {
		if field == "" {
			return
		}
		headers[field] = append(headers[field], value)
	}

	for lineNumber, line := range lines {
		if line == "" {
			break
		}
		if line[0] == ' ' || line[0] == '\t' {
			if field == "" {
				return nil, fmt.Errorf("header line %d: continuation without a header field", lineNumber+1)
			}

			continuation := trimWSP(line)
			if isInjectedControl(continuation) {
				return nil, fmt.Errorf("%w at header line %d", ErrFoldedControl, lineNumber+1)
			}
			if err := validateHeaderValue(continuation); err != nil {
				return nil, fmt.Errorf("header line %d: %w", lineNumber+1, err)
			}

			// Re-normalize the unfolded value with one separating space rather
			// than retaining arbitrary folding whitespace.
			if continuation != "" {
				if value != "" {
					value += " "
				}
				value += continuation
			}
			continue
		}

		flush()
		field, value = "", ""

		colon := strings.IndexByte(line, ':')
		if colon <= 0 {
			return nil, fmt.Errorf("header line %d: missing or invalid colon", lineNumber+1)
		}

		name := line[:colon]
		if err := validateFieldName(name); err != nil {
			return nil, fmt.Errorf("header line %d: %w", lineNumber+1, err)
		}
		canonical := textproto.CanonicalMIMEHeaderKey(name)
		if canonical == "" {
			return nil, fmt.Errorf("header line %d: invalid field name", lineNumber+1)
		}

		value = trimWSP(line[colon+1:])
		if err := validateHeaderValue(value); err != nil {
			return nil, fmt.Errorf("header line %d: %w", lineNumber+1, err)
		}
		field = canonical
	}
	flush()

	if len(headers["Control"]) > 1 {
		return nil, ErrDuplicateControl
	}
	if len(headers["Supersedes"]) > 1 {
		return nil, ErrDuplicateSupersedes
	}
	return headers, nil
}

// headerLines returns physical header lines from the start of raw until the
// first empty line. CRLF, LF, and bare CR are all treated as line endings so
// malformed line endings cannot hide an injected field from the parser.
func headerLines(raw []byte) []string {
	var lines []string
	for offset := 0; offset < len(raw); {
		end := offset
		for end < len(raw) && raw[end] != '\r' && raw[end] != '\n' {
			end++
		}
		line := string(raw[offset:end])
		if line == "" {
			break
		}
		lines = append(lines, line)
		if end == len(raw) {
			break
		}
		if raw[end] == '\r' && end+1 < len(raw) && raw[end+1] == '\n' {
			offset = end + 2
		} else {
			offset = end + 1
		}
	}
	return lines
}

func validateFieldName(name string) error {
	if name == "" {
		return errors.New("empty field name")
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if c < 33 || c > 126 || c == ':' {
			return fmt.Errorf("invalid byte in field name %q", name)
		}
	}
	return nil
}

func validateHeaderValue(value string) error {
	for i := 0; i < len(value); i++ {
		c := value[i]
		if c == '\t' {
			continue
		}
		if c < 32 || c == 127 {
			return fmt.Errorf("control byte 0x%02x in header value", c)
		}
	}
	return nil
}

func trimWSP(value string) string {
	return strings.Trim(value, " \t")
}

// isInjectedControl recognizes a Control field at the beginning of a
// continuation line, including variants with whitespace before the colon.
func isInjectedControl(continuation string) bool {
	const name = "Control"
	if len(continuation) < len(name) || !strings.EqualFold(continuation[:len(name)], name) {
		return false
	}
	rest := trimWSP(continuation[len(name):])
	return strings.HasPrefix(rest, ":")
}
