// Command framingcheck reads a raw HTTP/1.1 byte stream from stdin and parses
// the pipelined requests twice under two boundary policies:
//
//   - gateway: prefer Content-Length, fall back to Transfer-Encoding: chunked
//   - origin:  prefer Transfer-Encoding: chunked, fall back to Content-Length
//
// Both sides validate every header before a policy is chosen. On the first
// boundary divergence (or a shared syntax error / truncation) it prints one
// JSON line {"start":N,"gatewayEnd":N,"originEnd":N} with 0-based offsets,
// -1 marking the side that failed, and stops. If both sides agree on every
// request boundary it prints OK.
//
// Bodies are streamed to io.Discard; neither the whole stream nor any body
// is held in memory.
package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
	"strings"
)

const maxLine = 1 << 20 // cap on request/header/chunk-size line length

var errCleanEOF = errors.New("clean end of stream at request boundary")

// parser is a byte-counting streaming reader.
type parser struct {
	r   *bufio.Reader
	pos int64
}

// readLine returns one CRLF-terminated line without the CRLF. A bare EOF
// before any byte yields errCleanEOF; EOF mid-line is a truncation error.
func (p *parser) readLine() ([]byte, error) {
	var buf []byte
	for {
		frag, err := p.r.ReadSlice('\n')
		p.pos += int64(len(frag))
		buf = append(buf, frag...)
		if err == bufio.ErrBufferFull {
			if len(buf) > maxLine {
				return nil, errors.New("line too long")
			}
			continue
		}
		if err != nil {
			if err == io.EOF && len(buf) == 0 {
				return nil, errCleanEOF
			}
			return nil, errors.New("truncated line")
		}
		if len(buf) > maxLine {
			return nil, errors.New("line too long")
		}
		if len(buf) < 2 || buf[len(buf)-2] != '\r' {
			return nil, errors.New("missing CRLF")
		}
		return buf[:len(buf)-2], nil
	}
}

// discard drops exactly n bytes, returning how many were actually dropped.
func (p *parser) discard(n int64) (int64, error) {
	m, err := io.CopyN(io.Discard, p.r, n)
	p.pos += m
	return m, err
}

// readN reads exactly n bytes.
func (p *parser) readN(n int) ([]byte, error) {
	buf := make([]byte, n)
	m, err := io.ReadFull(p.r, buf)
	p.pos += int64(m)
	if err != nil {
		return nil, err
	}
	return buf, nil
}

func isToken(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
			strings.IndexByte("!#$%&'*+-.^_`|~", c) >= 0 {
			continue
		}
		return false
	}
	return true
}

func trimOWS(s string) string {
	return strings.Trim(s, " \t")
}

// validateHeaderLine checks "Name: value" syntax and returns name/value.
func validateHeaderLine(line []byte) (name, value string, err error) {
	i := strings.IndexByte(string(line), ':')
	if i <= 0 {
		return "", "", errors.New("header missing colon")
	}
	name = string(line[:i])
	if !isToken(name) {
		return "", "", errors.New("invalid header name")
	}
	return name, trimOWS(string(line[i+1:])), nil
}

// readHead parses the request line and all headers, validating everything
// before any policy is chosen. It reports whether Content-Length and
// Transfer-Encoding: chunked are (validly) present.
func (p *parser) readHead() (hasCL bool, cl uint64, hasTE bool, err error) {
	line, err := p.readLine()
	if err != nil {
		return false, 0, false, err
	}
	parts := strings.Split(string(line), " ")
	if len(parts) != 3 || !isToken(parts[0]) || parts[1] == "" ||
		!strings.HasPrefix(parts[2], "HTTP/") || len(parts[2]) <= len("HTTP/") {
		return false, 0, false, errors.New("bad request line")
	}

	var clParts, teParts []string
	clSeen, teSeen := false, false
	for {
		line, err = p.readLine()
		if err != nil {
			return false, 0, false, err
		}
		if len(line) == 0 {
			break
		}
		name, value, verr := validateHeaderLine(line)
		if verr != nil {
			return false, 0, false, verr
		}
		switch strings.ToLower(name) {
		case "content-length":
			clSeen = true
			clParts = append(clParts, strings.Split(value, ",")...)
		case "transfer-encoding":
			teSeen = true
			teParts = append(teParts, strings.Split(value, ",")...)
		}
	}

	if clSeen {
		if len(clParts) == 0 {
			return false, 0, false, errors.New("empty Content-Length")
		}
		first := true
		for _, part := range clParts {
			part = trimOWS(part)
			if part == "" {
				return false, 0, false, errors.New("empty Content-Length value")
			}
			for i := 0; i < len(part); i++ {
				if part[i] < '0' || part[i] > '9' {
					return false, 0, false, errors.New("non-decimal Content-Length")
				}
			}
			n, perr := strconv.ParseUint(part, 10, 64)
			if perr != nil {
				return false, 0, false, errors.New("Content-Length overflow")
			}
			if first {
				cl, first = n, false
			} else if n != cl {
				return false, 0, false, errors.New("conflicting Content-Length values")
			}
		}
		hasCL = true
	}

	if teSeen {
		if len(teParts) != 1 || !strings.EqualFold(trimOWS(teParts[0]), "chunked") {
			return false, 0, false, errors.New("Transfer-Encoding must be a single chunked")
		}
		hasTE = true
	}
	return hasCL, cl, hasTE, nil
}

// readCLBody consumes exactly n body bytes.
func (p *parser) readCLBody(n uint64) error {
	if n > math.MaxInt64 {
		return errors.New("Content-Length too large")
	}
	_, err := p.discard(int64(n))
	return err
}

// readChunkedBody consumes a chunked body including extensions and trailers.
func (p *parser) readChunkedBody() error {
	for {
		line, err := p.readLine()
		if err != nil {
			return err
		}
		sizeField := string(line)
		if i := strings.IndexByte(sizeField, ';'); i >= 0 { // chunk extensions
			sizeField = sizeField[:i]
		}
		if sizeField == "" {
			return errors.New("empty chunk size")
		}
		size, err := strconv.ParseUint(sizeField, 16, 64)
		if err != nil {
			return errors.New("bad chunk size")
		}
		if size > math.MaxInt64 {
			return errors.New("chunk size too large")
		}
		if size == 0 {
			for { // trailers, terminated by an empty line
				line, err = p.readLine()
				if err != nil {
					return err
				}
				if len(line) == 0 {
					return nil
				}
				if _, _, terr := validateHeaderLine(line); terr != nil {
					return terr
				}
			}
		}
		if _, err = p.discard(int64(size)); err != nil {
			return err
		}
		crlf, err := p.readN(2)
		if err != nil {
			return err
		}
		if crlf[0] != '\r' || crlf[1] != '\n' {
			return errors.New("chunk data not followed by CRLF")
		}
	}
}

type result struct {
	Start      int64 `json:"start"`
	GatewayEnd int64 `json:"gatewayEnd"`
	OriginEnd  int64 `json:"originEnd"`
}

// run processes the stream and returns the process exit code:
// 0 when every boundary agrees (OK), 1 when a JSON divergence/error line
// was emitted.
func run(in io.Reader, out io.Writer) int {
	p := &parser{r: bufio.NewReaderSize(in, 64*1024)}
	enc := json.NewEncoder(out)
	for {
		start := p.pos
		hasCL, cl, hasTE, err := p.readHead()
		if err == errCleanEOF {
			fmt.Fprintln(out, "OK")
			return 0
		}
		if err != nil {
			enc.Encode(result{start, -1, -1}) // both sides validate all headers first
			return 1
		}

		switch {
		case hasCL && hasTE:
			// Policies diverge: gateway uses Content-Length, origin uses
			// chunked. Parse the chunked form from the shared stream and
			// derive the Content-Length end arithmetically.
			bodyStart := p.pos
			chunkErr := p.readChunkedBody()
			chunkConsumed := p.pos - bodyStart

			var gatewayEnd, originEnd int64
			if chunkErr != nil {
				originEnd = -1
			} else {
				originEnd = bodyStart + chunkConsumed
			}
			switch {
			case cl > math.MaxInt64:
				gatewayEnd = -1
			case chunkConsumed >= int64(cl):
				gatewayEnd = bodyStart + int64(cl)
			default:
				if _, derr := p.discard(int64(cl) - chunkConsumed); derr != nil {
					gatewayEnd = -1
				} else {
					gatewayEnd = bodyStart + int64(cl)
				}
			}
			if chunkErr == nil && gatewayEnd == originEnd {
				continue // rare: both policies describe the same bytes
			}
			enc.Encode(result{start, gatewayEnd, originEnd})
			return 1

		case hasCL: // both sides use Content-Length
			if err := p.readCLBody(cl); err != nil {
				enc.Encode(result{start, -1, -1})
				return 1
			}
		case hasTE: // both sides use chunked
			if err := p.readChunkedBody(); err != nil {
				enc.Encode(result{start, -1, -1})
				return 1
			}
		default: // neither header: empty body on both sides
		}
	}
}

func main() {
	os.Exit(run(os.Stdin, os.Stdout))
}
