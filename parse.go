package main

import (
	"errors"
	"io"
	"strconv"
)

// maxHeadBlock caps the total size of a request line plus header section so a
// hostile peer cannot exhaust memory with an endless header.
const maxHeadBlock = 1024 * 1024

var (
	errSyntax    = errors.New("syntax error")
	errTruncated = errors.New("truncated request")
)

type framing int

const (
	framingEmpty framing = iota
	framingContentLength
	framingChunked
)

type headerField struct {
	name  string
	value string
}

type messageHead struct {
	fields      []headerField
	hasCL       bool
	clValue     uint64
	hasTE       bool
	headEnd     int64
	headerLimit int
}

// readMessageHead parses one request line and its headers starting at the
// cursor. The head is parsed once because both peers share the same wire
// syntax; strategy-specific decisions happen afterwards.
func readMessageHead(c *Cursor, headerLimit int) (*messageHead, error) {
	requestLine, err := c.readLine(headerLimit)
	if err != nil {
		return nil, mapReadErr(err)
	}
	if !validRequestLine(requestLine) {
		return nil, errSyntax
	}

	head := &messageHead{headerLimit: headerLimit}
	// readLine strips the terminator; account for at least one LF byte per
	// line so the aggregate cap cannot be bypassed with bare-LF framing.
	var headBytes int64 = int64(len(requestLine)) + 1

	for {
		lineStart := c.Offset()
		line, err := c.readLine(headerLimit)
		if err != nil {
			return nil, mapReadErr(err)
		}
		headBytes += c.Offset() - lineStart
		if headBytes > maxHeadBlock {
			return nil, errSyntax
		}
		if len(line) == 0 {
			break
		}
		name, value, ok := splitField(line)
		if !ok {
			return nil, errSyntax
		}
		head.fields = append(head.fields, headerField{name: name, value: value})
	}

	head.headEnd = c.Offset()
	if err := head.validate(); err != nil {
		return nil, err
	}
	return head, nil
}

// mapReadErr turns any read-side failure into the two externally visible
// failure classes: syntax error and truncation.
func mapReadErr(err error) error {
	if errors.Is(err, errLineTooLong) {
		return errSyntax
	}
	if errors.Is(err, errCRLF) {
		return errSyntax
	}
	return errTruncated
}

func validRequestLine(line []byte) bool {
	parts := splitSp(line)
	if len(parts) != 3 {
		return false
	}
	if !isToken(parts[0]) {
		return false
	}
	target := parts[1]
	if len(target) == 0 {
		return false
	}
	for _, b := range target {
		if b == '\r' || b == '\n' || b == ' ' || b == '\t' {
			return false
		}
	}
	version := parts[2]
	if len(version) != 8 || string(version[:5]) != "HTTP/" {
		return false
	}
	if string(version[5:]) != "1.1" && string(version[5:]) != "1.0" {
		return false
	}
	return true
}

func splitSp(line []byte) [][]byte {
	var parts [][]byte
	for len(line) > 0 {
		start := 0
		for start < len(line) && line[start] == ' ' {
			start++
		}
		if start == len(line) {
			break
		}
		end := start
		for end < len(line) && line[end] != ' ' {
			end++
		}
		parts = append(parts, line[start:end])
		line = line[end:]
	}
	return parts
}

func isToken(s []byte) bool {
	if len(s) == 0 {
		return false
	}
	for _, b := range s {
		if !isTokenChar(b) {
			return false
		}
	}
	return true
}

// isTokenChar implements the RFC 7230 tchar set.
func isTokenChar(b byte) bool {
	switch {
	case b >= 'a' && b <= 'z', b >= 'A' && b <= 'Z', b >= '0' && b <= '9':
		return true
	}
	switch b {
	case '!', '#', '$', '%', '&', '\'', '*', '+', '-', '.', '^', '_', '`', '|', '~':
		return true
	}
	return false
}

// splitField splits "Name: value", rejecting obsolete line folding and empty or
// non-token field names.
func splitField(line []byte) (string, string, bool) {
	colon := -1
	for i, b := range line {
		if b == ':' {
			colon = i
			break
		}
	}
	if colon <= 0 {
		return "", "", false
	}
	name := line[:colon]
	if !isToken(name) {
		return "", "", false
	}
	value := trimOWS(line[colon+1:])
	for _, b := range value {
		if b == '\r' || b == '\n' {
			return "", "", false
		}
	}
	return string(name), string(value), true
}

func trimOWS(b []byte) []byte {
	for len(b) > 0 && (b[0] == ' ' || b[0] == '\t') {
		b = b[1:]
	}
	for len(b) > 0 && (b[len(b)-1] == ' ' || b[len(b)-1] == '\t') {
		b = b[:len(b)-1]
	}
	return b
}

func equalFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ac, bc := a[i], b[i]
		if ac >= 'A' && ac <= 'Z' {
			ac += 'a' - 'A'
		}
		if bc >= 'A' && bc <= 'Z' {
			bc += 'a' - 'A'
		}
		if ac != bc {
			return false
		}
	}
	return true
}

// validate inspects every header before any framing strategy is selected, as
// required. Header syntax problems fail both peers identically.
func (h *messageHead) validate() error {
	for _, f := range h.fields {
		switch {
		case equalFold(f.name, "Content-Length"):
			value, ok := parseContentLength(f.value)
			if !ok {
				return errSyntax
			}
			if h.hasCL && h.clValue != value {
				return errSyntax
			}
			h.hasCL = true
			h.clValue = value
		case equalFold(f.name, "Transfer-Encoding"):
			if !validChunkedTE(f.value) {
				return errSyntax
			}
			if h.hasTE {
				// Only a single Transfer-Encoding header carrying the single
				// "chunked" coding is accepted.
				return errSyntax
			}
			h.hasTE = true
		}
	}
	return nil
}

// parseContentLength accepts comma-separated decimal lengths. Repeated headers
// are validated at the caller; within one field every decimal value must be
// identical or the header is invalid.
func parseContentLength(value string) (uint64, bool) {
	parts := splitComma(value)
	if len(parts) == 0 {
		return 0, false
	}
	var first uint64
	for i, part := range parts {
		if len(part) == 0 {
			return 0, false
		}
		for _, b := range part {
			if b < '0' || b > '9' {
				return 0, false
			}
		}
		n, err := strconv.ParseUint(part, 10, 64)
		if err != nil {
			return 0, false
		}
		if i == 0 {
			first = n
		} else if n != first {
			return 0, false
		}
	}
	return first, true
}

func splitComma(value string) []string {
	var out []string
	start := 0
	for i := 0; i <= len(value); i++ {
		if i == len(value) || value[i] == ',' {
			part := trimOWS([]byte(value[start:i]))
			out = append(out, string(part))
			start = i + 1
		}
	}
	return out
}

// validChunkedTE accepts exactly one transfer coding: the single token
// "chunked" (case-insensitive). Any list, other coding, or empty value is a
// header syntax error.
func validChunkedTE(value string) bool {
	value = string(trimOWS([]byte(value)))
	if len(value) == 0 {
		return false
	}
	for i := 0; i < len(value); i++ {
		if value[i] == ',' {
			return false
		}
	}
	return equalFold(value, "chunked")
}

// chooseFraming implements one peer's preference. The preferred header wins
// when present and valid; otherwise the other header is used; with neither
// header present the body is empty.
func chooseFraming(h *messageHead, preferCL bool) framing {
	if preferCL {
		switch {
		case h.hasCL:
			return framingContentLength
		case h.hasTE:
			return framingChunked
		default:
			return framingEmpty
		}
	}
	switch {
	case h.hasTE:
		return framingChunked
	case h.hasCL:
		return framingContentLength
	default:
		return framingEmpty
	}
}

// scanBody advances a cursor across exactly one message body and returns the
// absolute, exclusive end offset. Body bytes themselves are never retained.
func scanBody(c *Cursor, h *messageHead, f framing) (int64, error) {
	switch f {
	case framingEmpty:
		return c.Offset(), nil
	case framingContentLength:
		return readFixedBody(c, int64(h.clValue))
	default:
		return readChunkedBody(c, h.headerLimit)
	}
}

func readFixedBody(c *Cursor, length int64) (int64, error) {
	if err := c.discard(length); err != nil {
		return 0, errTruncated
	}
	return c.Offset(), nil
}

// readChunkedBody parses chunks with extensions, an optional trailer section,
// and the terminating empty line after the zero-size chunk.
func readChunkedBody(c *Cursor, trailerLimit int) (int64, error) {
	for {
		line, err := c.readLine(maxLineLength)
		if err != nil {
			return 0, mapReadErr(err)
		}
		size, ok := parseChunkSize(line)
		if !ok {
			return 0, errSyntax
		}
		if err := c.discard(int64(size)); err != nil {
			return 0, errTruncated
		}
		if size == 0 {
			// The zero chunk's own CRLF was consumed with its size line; the
			// next lines are trailers terminated by an empty line.
			break
		}
		if err := c.expectCRLF(); err != nil {
			if errors.Is(err, io.EOF) {
				return 0, errTruncated
			}
			return 0, errSyntax
		}
	}

	for {
		lineStart := c.Offset()
		line, err := c.readLine(trailerLimit)
		if err != nil {
			return 0, mapReadErr(err)
		}
		if len(line) == 0 {
			return c.Offset(), nil
		}
		if _, _, ok := splitField(line); !ok {
			return 0, errSyntax
		}
		if c.Offset()-lineStart > maxHeadBlock {
			return 0, errSyntax
		}
	}
}

// parseChunkSize parses a chunk-size line, ignoring any chunk extension after
// the first semicolon.
func parseChunkSize(line []byte) (uint64, bool) {
	if semi := indexByte(line, ';'); semi >= 0 {
		line = line[:semi]
	}
	line = trimOWS(line)
	if len(line) == 0 {
		return 0, false
	}
	for _, b := range line {
		if !isHexDigit(b) {
			return 0, false
		}
	}
	n, err := strconv.ParseUint(string(line), 16, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

func indexByte(b []byte, want byte) int {
	for i, v := range b {
		if v == want {
			return i
		}
	}
	return -1
}

func isHexDigit(b byte) bool {
	switch {
	case b >= '0' && b <= '9', b >= 'a' && b <= 'f', b >= 'A' && b <= 'F':
		return true
	}
	return false
}
