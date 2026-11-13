package chunked

import (
	"bufio"
	"io"
	"net/http"
	"strings"
)

// MaxLineLength bounds chunk-size and trailer lines. A chunk-data line is not
// bounded here because chunk data is read according to its declared size.
const MaxLineLength = 64 << 10

func readCRLFLine(src *bufio.Reader) ([]byte, error) {
	line := make([]byte, 0, 128)
	for {
		c, err := src.ReadByte()
		if err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				return nil, unexpectedEOF("line is missing CRLF terminator")
			}
			return nil, err
		}

		switch c {
		case '\n':
			if len(line) == 0 || line[len(line)-1] != '\r' {
				return nil, malformed("bare LF in line")
			}
			return line[:len(line)-1], nil
		case '\r':
			next, err := src.ReadByte()
			if err != nil {
				if err == io.EOF || err == io.ErrUnexpectedEOF {
					return nil, unexpectedEOF("CR is not followed by LF")
				}
				return nil, err
			}
			if next != '\n' {
				return nil, malformed("CR is not followed by LF")
			}
			return line, nil
		default:
			line = append(line, c)
			if len(line) > MaxLineLength {
				return nil, malformed("line exceeds %d bytes", MaxLineLength)
			}
		}
	}
}

func expectCRLF(src *bufio.Reader, where string) error {
	c, err := src.ReadByte()
	if err != nil {
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			return unexpectedEOF("%s is missing CRLF", where)
		}
		return err
	}
	if c == '\n' {
		return malformed("bare LF after %s", where)
	}
	if c != '\r' {
		return malformed("%s must be CRLF", where)
	}

	c, err = src.ReadByte()
	if err != nil {
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			return unexpectedEOF("%s is missing LF", where)
		}
		return err
	}
	if c != '\n' {
		return malformed("%s must be CRLF", where)
	}
	return nil
}

func splitTrailerLine(line []byte) (string, string, bool) {
	colon := -1
	for i, c := range line {
		if c == ':' {
			colon = i
			break
		}
	}
	if colon <= 0 {
		return "", "", false
	}

	name := string(line[:colon])
	for i := 0; i < len(name); i++ {
		if !isTokenChar(name[i]) {
			return "", "", false
		}
	}

	value := strings.Trim(string(line[colon+1:]), " \t")
	for i := 0; i < len(value); i++ {
		c := value[i]
		if c == '\r' || c == '\n' || c == 0x7f || c < 0x20 && c != '\t' {
			return "", "", false
		}
	}
	return http.CanonicalHeaderKey(name), value, true
}

func isForbiddenTrailerName(name string) bool {
	switch strings.ToLower(name) {
	case "content-length", "transfer-encoding", "host":
		return true
	default:
		return false
	}
}
