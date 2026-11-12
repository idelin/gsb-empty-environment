package netsec

import (
	"bufio"
	"encoding/base64"
	"errors"
	"io"
	"strings"
)

// Stanza is an age header stanza (also called a "line").
//
// A stanza has a type, zero or more space-separated arguments and an optional
// binary body. On the wire the body is encoded as one or more wrapped base64
// lines without padding, each at most b64LineLength characters long.
type Stanza struct {
	Type string
	Args [][]byte
	Body []byte
}

// b64LineLength is the maximum length, in characters, of a single base64 line
// in the age header (age v1, section 5).
const b64LineLength = 64

// maxLineLength bounds the length of any single header line so that a
// malformed or adversarial header cannot force unbounded buffering.
const maxLineLength = 1 << 20

var (
	errEmptyType  = errors.New("netsec: stanza with empty type")
	errSpaces     = errors.New("netsec: header argument contains spaces")
	errBadB64Line = errors.New("netsec: malformed wrapped base64 body")
	errLongLine   = errors.New("netsec: header line exceeds maximum length")
)

// MarshalStanza encodes s in the age wire format, including the trailing
// newline after the (possibly empty) body.
func MarshalStanza(s *Stanza) []byte {
	var b strings.Builder
	b.WriteString(s.Type)
	for _, arg := range s.Args {
		b.WriteByte(' ')
		b.Write(arg)
	}
	b.WriteByte('\n')

	encoded := base64.RawStdEncoding.EncodeToString(s.Body)
	for len(encoded) > b64LineLength {
		b.WriteString(encoded[:b64LineLength])
		b.WriteByte('\n')
		encoded = encoded[b64LineLength:]
	}
	// An empty body is represented by a single empty line, i.e. just the
	// terminating newline written above.
	b.WriteString(encoded)
	b.WriteByte('\n')
	return []byte(b.String())
}

// ReadStanza parses a single stanza from r. The first line (type and
// arguments) must already have been read and is supplied as firstLine; the
// remaining lines up to and including the last wrapped base64 line are read
// from r. It returns the stanza together with its exact on-wire bytes
// (including both the first line and the trailing newline), so callers can
// authenticate the bytes they parsed without re-marshalling.
func ReadStanza(r *bufio.Reader, firstLine string) (*Stanza, []byte, error) {
	var raw strings.Builder
	raw.WriteString(firstLine)
	raw.WriteByte('\n')
	fields := strings.Split(firstLine, " ")
	if fields[0] == "" {
		return nil, nil, errEmptyType
	}
	s := &Stanza{Type: fields[0]}
	for _, field := range fields[1:] {
		if len(field) == 0 {
			return nil, nil, errSpaces
		}
		s.Args = append(s.Args, []byte(field))
	}

	var body strings.Builder
	for {
		line, err := readHeaderLine(r)
		if err != nil {
			return nil, nil, err
		}
		raw.WriteString(line)
		raw.WriteByte('\n')
		if len(line) > b64LineLength {
			return nil, nil, errBadB64Line
		}
		// Every body line, including the last one, must contain valid base64
		// without padding.
		if _, err := base64.RawStdEncoding.DecodeString(line); err != nil {
			return nil, nil, errBadB64Line
		}
		body.WriteString(line)
		if len(line) < b64LineLength {
			break
		}
	}

	decoded, err := base64.RawStdEncoding.DecodeString(body.String())
	if err != nil {
		return nil, nil, errBadB64Line
	}
	s.Body = decoded
	return s, []byte(raw.String()), nil
}

// readHeaderLine reads one newline-terminated header line, without the
// trailing '\n'. It rejects CR characters and overlong lines.
func readHeaderLine(r *bufio.Reader) (string, error) {
	line, err := r.ReadString('\n')
	if err != nil && line == "" {
		if err == io.EOF {
			return "", io.ErrUnexpectedEOF
		}
		return "", err
	}
	if len(line) > maxLineLength {
		return "", errLongLine
	}
	if strings.HasSuffix(line, "\r\n") {
		return "", errors.New("netsec: CR character in header line")
	}
	line = strings.TrimSuffix(line, "\n")
	if strings.ContainsRune(line, '\r') {
		return "", errors.New("netsec: CR character in header line")
	}
	return line, nil
}

// UnmarshalStanza validates a stanza's on-wire encoding and returns it.
func UnmarshalStanza(r *bufio.Reader) (*Stanza, error) {
	line, err := readHeaderLine(r)
	if err != nil {
		return nil, err
	}
	s, _, err := ReadStanza(r, line)
	return s, err
}
