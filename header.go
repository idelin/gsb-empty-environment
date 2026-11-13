package netsec

import (
	"bufio"
	"bytes"
	"io"
	"net/textproto"
)

const maxMIMEHeaderSize = 10 << 20

// readMIMEHeader parses a block of RFC 822-style headers terminated by an
// empty line, reading directly from the shared buffered reader so that body
// bytes remain available to the part reader. Keys are canonicalized with
// textproto.CanonicalMIMEHeaderKey and continuation (folded) lines are
// supported.
func readMIMEHeader(br *bufio.Reader) (textproto.MIMEHeader, error) {
	header := make(textproto.MIMEHeader)
	var key, value string
	total := 0

	for {
		line, err := br.ReadBytes('\n')
		if err == io.EOF {
			return nil, io.ErrUnexpectedEOF
		}
		if err != nil {
			return nil, err
		}
		total += len(line)
		if total > maxMIMEHeaderSize {
			return nil, ErrMalformedHeader
		}

		// Normalize CRLF (or a bare LF) away.
		line = trimLineEnd(line)
		if len(line) == 0 {
			if key != "" {
				appendHeader(header, key, value)
			}
			return header, nil
		}

		if line[0] == ' ' || line[0] == '\t' {
			// Continuation of the previous header field.
			if key == "" {
				return nil, ErrMalformedHeader
			}
			value += " " + string(bytes.TrimLeft(line, " \t"))
			continue
		}

		if key != "" {
			appendHeader(header, key, value)
		}

		colon := bytes.IndexByte(line, ':')
		if colon <= 0 || !isValidHeaderKey(line[:colon]) {
			return nil, ErrMalformedHeader
		}
		key = textproto.CanonicalMIMEHeaderKey(string(line[:colon]))
		value = string(bytes.TrimSpace(line[colon+1:]))
	}
}

func appendHeader(header textproto.MIMEHeader, key, value string) {
	header[key] = append(header[key], value)
}

func trimLineEnd(line []byte) []byte {
	if len(line) > 0 && line[len(line)-1] == '\n' {
		line = line[:len(line)-1]
	}
	if len(line) > 0 && line[len(line)-1] == '\r' {
		line = line[:len(line)-1]
	}
	return line
}

func isValidHeaderKey(key []byte) bool {
	if len(key) == 0 {
		return false
	}
	for _, c := range key {
		if c <= ' ' || c == ':' || c > 126 {
			return false
		}
	}
	return true
}
