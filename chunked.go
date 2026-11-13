package netsec

import (
	"bytes"
	"errors"
	"io"
)

// Status describes how far decoding a chunked request got.
type Status int

const (
	// Incomplete means more input bytes are required to finish the request.
	Incomplete Status = iota
	// Invalid means the bytes cannot form a well-formed chunked request.
	Invalid
	// Complete means the request ended exactly at its chunked terminator.
	Complete
	// CompleteWithNextRequest means the request ended and remaining bytes
	// start the next request on the same connection.
	CompleteWithNextRequest
)

// Verdict is the result of decoding one chunked request.
type Verdict struct {
	Status    Status
	Body      []byte
	Remaining []byte
	Err       error
}

// ReadChunked decodes a complete chunked request body.
func ReadChunked(input []byte) (body []byte, remaining []byte, err error) {
	verdict := decodeChunkedBody(input)
	switch verdict.Status {
	case Complete:
		return verdict.Body, nil, nil
	case CompleteWithNextRequest:
		return verdict.Body, verdict.Remaining, nil
	case Incomplete:
		return nil, nil, io.ErrUnexpectedEOF
	default:
		return nil, nil, verdict.Err
	}
}

// Decode parses one HTTP/1.1 request whose body uses chunked transfer coding.
// Bytes belonging to a pipelined following request are returned in Remaining.
func Decode(input []byte) Verdict {
	headerEnd := bytes.Index(input, []byte("\r\n\r\n"))
	if headerEnd < 0 {
		if bytes.Contains(input, []byte("\n\n")) {
			return invalid("malformed header terminator")
		}
		return Verdict{Status: Incomplete}
	}

	headerBlock := input[:headerEnd]
	body := input[headerEnd+4:]
	lines := splitLines(headerBlock)
	if len(lines) == 0 {
		return invalid("missing request line")
	}
	if !isRequestLine(lines[0]) {
		return invalid("malformed request line")
	}
	if !hasChunkedTransferEncoding(lines[1:]) {
		return invalid("missing chunked transfer encoding")
	}

	return decodeChunkedBody(body)
}

// DecodeChunked parses a chunked body without an HTTP request header block.
// It is useful when the gateway has already parsed request headers.
func DecodeChunked(body []byte) Verdict {
	return decodeChunkedBody(body)
}

// Decoder is an importable stateful wrapper around DecodeChunked.
type Decoder struct {
	buffer       []byte
	readOffset   int
	decodedBody  []byte
	decodeCalled bool
}

// NewDecoder creates a decoder for a chunked request body.
func NewDecoder() *Decoder {
	return &Decoder{}
}

// Write appends bytes received from the connection.
func (d *Decoder) Write(p []byte) (int, error) {
	d.buffer = append(d.buffer, p...)
	return len(p), nil
}

// Decode inspects all bytes currently buffered by d.
func (d *Decoder) Decode() Verdict {
	return decodeChunkedBody(d.buffer)
}

// Read implements io.Reader for the decoded request body.
func (d *Decoder) Read(p []byte) (int, error) {
	if !d.decodeCalled {
		verdict := d.Decode()
		switch verdict.Status {
		case Incomplete:
			return 0, io.ErrUnexpectedEOF
		case Invalid:
			return 0, verdict.Err
		}
		d.decodedBody = verdict.Body
		d.decodeCalled = true
	}

	if d.readOffset >= len(d.decodedBody) {
		return 0, io.EOF
	}
	n := copy(p, d.decodedBody[d.readOffset:])
	if n == 0 {
		return 0, io.EOF
	}
	d.readOffset += n
	return n, nil
}

func decodeChunkedBody(input []byte) Verdict {
	body := make([]byte, 0)
	offset := 0

	for {
		line, next, ok := nextLine(input, offset)
		if !ok {
			return Verdict{Status: Incomplete}
		}
		size, err := parseChunkSize(line)
		if err != nil {
			return invalid(err.Error())
		}
		offset = next

		if size == 0 {
			break
		}

		dataEnd := offset + int(size)
		if dataEnd+2 > len(input) {
			return Verdict{Status: Incomplete}
		}
		body = append(body, input[offset:dataEnd]...)
		if input[dataEnd] != '\r' || input[dataEnd+1] != '\n' {
			return invalid("missing chunk data CRLF")
		}
		offset = dataEnd + 2
	}

	for {
		line, next, ok := nextLine(input, offset)
		if !ok {
			return Verdict{Status: Incomplete}
		}
		offset = next
		if len(line) == 0 {
			break
		}
		if !isTrailerField(line) {
			return invalid("malformed trailer field")
		}
	}

	if offset == len(input) {
		return Verdict{Status: Complete, Body: body}
	}
	if looksLikeRequestLine(input[offset:]) {
		return Verdict{
			Status:    CompleteWithNextRequest,
			Body:      body,
			Remaining: append([]byte(nil), input[offset:]...),
		}
	}
	return invalid("extra chunk after terminating chunk")
}

func nextLine(input []byte, offset int) ([]byte, int, bool) {
	end := bytes.Index(input[offset:], []byte("\r\n"))
	if end < 0 {
		return nil, offset, false
	}
	return input[offset : offset+end], offset + end + 2, true
}

func splitLines(block []byte) [][]byte {
	if len(block) == 0 {
		return nil
	}
	return bytes.Split(block, []byte("\r\n"))
}

func parseChunkSize(line []byte) (uint64, error) {
	sizePart := line
	if semicolon := bytes.IndexByte(line, ';'); semicolon >= 0 {
		sizePart = line[:semicolon]
	}
	sizePart = bytes.TrimSpace(sizePart)
	if len(sizePart) == 0 {
		return 0, errors.New("missing chunk size")
	}

	var size uint64
	for _, c := range sizePart {
		var value byte
		switch {
		case c >= '0' && c <= '9':
			value = c - '0'
		case c >= 'a' && c <= 'f':
			value = c - 'a' + 10
		case c >= 'A' && c <= 'F':
			value = c - 'A' + 10
		default:
			return 0, errors.New("malformed chunk size")
		}
		const maxUint64 = ^uint64(0)
		if size > (maxUint64-uint64(value))/16 {
			return 0, errors.New("chunk size too large")
		}
		size = size*16 + uint64(value)
	}
	return size, nil
}

func hasChunkedTransferEncoding(headerLines [][]byte) bool {
	for _, line := range headerLines {
		colon := bytes.IndexByte(line, ':')
		if colon <= 0 {
			continue
		}
		name := bytes.TrimSpace(line[:colon])
		if !equalFold(name, []byte("Transfer-Encoding")) {
			continue
		}
		value := bytes.TrimSpace(line[colon+1:])
		if equalFold(value, []byte("chunked")) {
			return true
		}
	}
	return false
}

func equalFold(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		al := a[i]
		bl := b[i]
		if al >= 'A' && al <= 'Z' {
			al += 'a' - 'A'
		}
		if bl >= 'A' && bl <= 'Z' {
			bl += 'a' - 'A'
		}
		if al != bl {
			return false
		}
	}
	return true
}

func isRequestLine(line []byte) bool {
	parts := bytes.SplitN(line, []byte(" "), 3)
	if len(parts) != 3 || len(parts[0]) == 0 || len(parts[1]) == 0 || len(parts[2]) == 0 {
		return false
	}
	return isToken(parts[0]) && isHTTPVersion(parts[2])
}

func looksLikeRequestLine(rest []byte) bool {
	line, _, found := bytes.Cut(rest, []byte("\r\n"))
	if found {
		return isRequestLine(line)
	}

	parts := bytes.SplitN(line, []byte(" "), 3)
	if len(parts) < 2 || len(parts[0]) == 0 || len(parts[1]) == 0 || !isToken(parts[0]) {
		return false
	}
	if len(parts) == 2 {
		return true
	}
	return bytes.HasPrefix(parts[2], []byte("HTTP/"))
}

func isHTTPVersion(version []byte) bool {
	return bytes.HasPrefix(version, []byte("HTTP/")) && len(version) > len("HTTP/")
}

func isTrailerField(line []byte) bool {
	colon := bytes.IndexByte(line, ':')
	if colon <= 0 {
		return false
	}
	return isToken(line[:colon])
}

func isToken(value []byte) bool {
	if len(value) == 0 {
		return false
	}
	for _, c := range value {
		if bytes.IndexByte([]byte("!#$%&'*+-.^_`|~0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"), c) < 0 {
			return false
		}
	}
	return true
}

func invalid(message string) Verdict {
	return Verdict{Status: Invalid, Err: errors.New(message)}
}
