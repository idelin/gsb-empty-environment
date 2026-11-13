package netsec

import "errors"

// Header is one parsed header field line.
//
// Name is the field name exactly as it appeared, without the trailing colon.
// Value is the field value with only leading and trailing optional whitespace
// (SP/HTAB) removed; every space and tab inside the value is preserved.
type Header struct {
	Name  string
	Value string
}

// FieldError locates a rejected header field line inside the decoded block.
// Line numbers are 1-based and count physical lines, not reconstructed fields.
type FieldError struct {
	Line int
	Err  error
}

func (e *FieldError) Error() string {
	return "netsec: header line " + itoa(e.Line) + ": " + e.Err.Error()
}

func (e *FieldError) Unwrap() error { return e.Err }

// Sentinel errors returned, possibly wrapped in *FieldError, by the decoders.
var (
	// ErrLineFolding is returned when an obs-fold (RFC 7230 section 3.2.4)
	// is observed: a physical line beginning with SP or HTAB. The whole
	// block is rejected; the continuation is never spliced into the previous
	// field and is never treated as a new field.
	ErrLineFolding = errors.New("obs-fold continuation line is not allowed")

	// ErrMalformedHeader is returned when a physical line cannot be parsed
	// as a single header field (missing colon, empty or invalid field name,
	// or forbidden control bytes in the field value).
	ErrMalformedHeader = errors.New("malformed header field line")
)

// DecodeHeaders decodes a raw HTTP header block without line folding.
//
// Lines are split on CRLF (preferred) or a bare LF; a trailing empty line ends
// the block and any following bytes are ignored. A line beginning with SP or
// HTAB is an obs-fold continuation and makes the entire call fail with
// ErrLineFolding: continuation lines are neither appended to the preceding
// field nor accepted as standalone fields.
//
// For a regular "Name: value" line, leading and trailing SP/HTAB are trimmed
// from the value only. Whitespace inside the value, including interior runs of
// spaces, is preserved byte for byte.
func DecodeHeaders(data []byte) ([]Header, error) {
	headers := make([]Header, 0)
	lineNo := 0
	for len(data) > 0 {
		lineNo++
		line := data
		if idx := indexLF(line); idx >= 0 {
			line = line[:idx]
			data = data[idx+1:]
		} else {
			data = nil
		}
		if len(line) > 0 && line[len(line)-1] == '\r' {
			line = line[:len(line)-1]
		}

		if len(line) == 0 {
			return headers, nil
		}
		if line[0] == ' ' || line[0] == '\t' {
			return nil, &FieldError{Line: lineNo, Err: ErrLineFolding}
		}

		colon := -1
		for i, b := range line {
			if b == ':' {
				colon = i
				break
			}
		}
		if colon <= 0 {
			return nil, &FieldError{Line: lineNo, Err: ErrMalformedHeader}
		}

		name := string(line[:colon])
		if !isToken(name) {
			return nil, &FieldError{Line: lineNo, Err: ErrMalformedHeader}
		}
		value := trimOWS(line[colon+1:])
		if !isFieldValue(value) {
			return nil, &FieldError{Line: lineNo, Err: ErrMalformedHeader}
		}

		headers = append(headers, Header{Name: name, Value: string(value)})
	}
	return headers, nil
}

// DecodeHeaderString is the string form of DecodeHeaders.
func DecodeHeaderString(block string) ([]Header, error) {
	return DecodeHeaders([]byte(block))
}

func indexLF(b []byte) int {
	for i, c := range b {
		if c == '\n' {
			return i
		}
	}
	return -1
}

// trimOWS removes only leading and trailing SP/HTAB (RFC 7230 OWS).
// Interior bytes are never touched.
func trimOWS(b []byte) []byte {
	start := 0
	for start < len(b) && (b[start] == ' ' || b[start] == '\t') {
		start++
	}
	for end := len(b); end > start; end-- {
		if b[end-1] != ' ' && b[end-1] != '\t' {
			return b[start:end]
		}
	}
	return b[start:start]
}

// isToken reports whether name is an RFC 7230 token (field-name).
func isToken(name string) bool {
	if name == "" {
		return false
	}
	for i := 0; i < len(name); i++ {
		if !isTokenChar(name[i]) {
			return false
		}
	}
	return true
}

func isTokenChar(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	}
	switch c {
	case '!', '#', '$', '%', '&', '\'', '*', '+', '-', '.', '^', '_', '`', '|', '~':
		return true
	}
	return false
}

// isFieldValue rejects NUL, CR and LF; everything else, including HTAB and all
// interior spaces, is retained. Other CTLs cannot occur here because lines are
// split at LF and a CR only terminates a line as part of CRLF.
func isFieldValue(b []byte) bool {
	for _, c := range b {
		if c == 0 || c == '\r' || c == '\n' {
			return false
		}
	}
	return true
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
