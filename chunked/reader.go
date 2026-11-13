package chunked

import (
	"bufio"
	"io"
	"net/http"
	"strconv"
)

type readerState uint8

const (
	stateChunkSize readerState = iota
	stateChunkData
	stateChunkDataCRLF
	stateTrailers
	stateDone
	stateFailed
)

// Reader validates and decodes an HTTP chunked body. It implements io.Reader.
//
// A Reader consumes exactly the bytes belonging to the chunked body. Bytes
// after the terminating empty trailer line are left for the caller.
type Reader struct {
	src       *bufio.Reader
	state     readerState
	remaining uint64
	trailers  http.Header
	err       error
}

// NewReader returns a strict chunked reader over src.
func NewReader(src io.Reader) *Reader {
	return &Reader{
		src:      bufio.NewReader(src),
		state:    stateChunkSize,
		trailers: make(http.Header),
	}
}

// NewDecoder is an alias for NewReader.
func NewDecoder(src io.Reader) *Reader {
	return NewReader(src)
}

// Decoder is an alias for Reader.
type Decoder = Reader

// Read implements io.Reader.
func (r *Reader) Read(p []byte) (int, error) {
	if r.err != nil {
		return 0, r.err
	}
	if len(p) == 0 {
		return 0, nil
	}

	for {
		switch r.state {
		case stateChunkSize:
			line, err := readCRLFLine(r.src)
			if err != nil {
				return r.fail(err)
			}
			size, err := parseChunkSizeLine(line)
			if err != nil {
				return r.fail(err)
			}
			if size == 0 {
				r.state = stateTrailers
				continue
			}
			r.remaining = size
			r.state = stateChunkData

		case stateChunkData:
			if r.remaining == 0 {
				r.state = stateChunkDataCRLF
				continue
			}
			want := uint64(len(p))
			if want > r.remaining {
				want = r.remaining
			}
			n, err := r.src.Read(p[:want])
			if n > 0 {
				r.remaining -= uint64(n)
				return n, nil
			}
			if err == nil {
				continue
			}
			return r.fail(chunkDataError(err))

		case stateChunkDataCRLF:
			if err := expectCRLF(r.src, "chunk data terminator"); err != nil {
				return r.fail(err)
			}
			r.state = stateChunkSize

		case stateTrailers:
			if err := r.readTrailers(); err != nil {
				return r.fail(err)
			}
			r.state = stateDone

		case stateDone:
			return 0, io.EOF

		default:
			return 0, io.ErrClosedPipe
		}
	}
}

func (r *Reader) fail(err error) (int, error) {
	r.state = stateFailed
	r.err = err
	return 0, err
}

// Trailers returns a copy of the trailer fields parsed after the zero-size
// chunk. It returns an empty header if decoding has not reached that point.
func (r *Reader) Trailers() http.Header {
	header := make(http.Header, len(r.trailers))
	for name, values := range r.trailers {
		header[name] = append([]string(nil), values...)
	}
	return header
}

func (r *Reader) readTrailers() error {
	for {
		line, err := readCRLFLine(r.src)
		if err != nil {
			return err
		}
		if len(line) == 0 {
			return nil
		}
		name, value, ok := splitTrailerLine(line)
		if !ok {
			return malformed("invalid trailer line %q", line)
		}
		if isForbiddenTrailerName(name) {
			return malformed("forbidden trailer field %q", name)
		}
		r.trailers.Add(name, value)
	}
}

func chunkDataError(err error) error {
	if err == io.EOF || err == io.ErrUnexpectedEOF {
		return unexpectedEOF("chunk data truncated")
	}
	return err
}

// parseChunkSizeLine parses chunk-size chunk-ext. The chunk-size itself must
// be one or more HEXDIG characters with no sign, prefix, or whitespace.
func parseChunkSizeLine(line []byte) (uint64, error) {
	sizeEnd := 0
	for sizeEnd < len(line) && isHexDigit(line[sizeEnd]) {
		sizeEnd++
	}
	if sizeEnd == 0 {
		return 0, malformed("chunk size must start with a hexadecimal digit")
	}

	size, err := strconv.ParseUint(string(line[:sizeEnd]), 16, 64)
	if err != nil {
		return 0, malformed("invalid chunk size %q", line[:sizeEnd])
	}
	if sizeEnd == len(line) {
		return size, nil
	}
	if line[sizeEnd] != ';' {
		return 0, malformed("chunk size must be followed by ';' or CRLF")
	}

	pos := sizeEnd + 1
	for {
		pos = skipOWS(line, pos)
		if pos == len(line) {
			return 0, malformed("empty chunk extension")
		}

		nameStart := pos
		for pos < len(line) && isTokenChar(line[pos]) {
			pos++
		}
		if nameStart == pos {
			return 0, malformed("invalid chunk extension name")
		}

		pos = skipOWS(line, pos)
		if pos < len(line) && line[pos] == '=' {
			pos++
			pos = skipOWS(line, pos)
			if pos == len(line) {
				return 0, malformed("missing chunk extension value")
			}
			pos, err = parseChunkExtensionValue(line, pos)
			if err != nil {
				return 0, err
			}
		}

		pos = skipOWS(line, pos)
		if pos == len(line) {
			return size, nil
		}
		if line[pos] != ';' {
			return 0, malformed("invalid character after chunk extension")
		}
		pos++
	}
}

func parseChunkExtensionValue(line []byte, pos int) (int, error) {
	if line[pos] == '"' {
		return parseQuotedString(line, pos)
	}

	start := pos
	for pos < len(line) && isTokenChar(line[pos]) {
		pos++
	}
	if start == pos {
		return 0, malformed("invalid chunk extension value")
	}
	return pos, nil
}

// parseQuotedString applies the RFC 9110 quoted-string grammar and explicitly
// rejects CR and LF, including escaped line breaks.
func parseQuotedString(line []byte, pos int) (int, error) {
	pos++ // opening DQUOTE
	for pos < len(line) {
		switch c := line[pos]; {
		case c == '"':
			return pos + 1, nil
		case c == '\r' || c == '\n':
			return 0, malformed("newline in quoted chunk extension")
		case c == '\\':
			pos++
			if pos == len(line) {
				return 0, malformed("unterminated quoted-pair in chunk extension")
			}
			escaped := line[pos]
			if escaped == '\r' || escaped == '\n' {
				return 0, malformed("newline in quoted chunk extension")
			}
			if !isQuotedPairChar(escaped) {
				return 0, malformed("invalid quoted-pair in chunk extension")
			}
			pos++
		case !isQDTEXT(c):
			return 0, malformed("invalid character in quoted chunk extension")
		default:
			pos++
		}
	}
	return 0, malformed("unterminated quoted chunk extension")
}

func skipOWS(line []byte, pos int) int {
	for pos < len(line) && (line[pos] == ' ' || line[pos] == '\t') {
		pos++
	}
	return pos
}

func isHexDigit(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

func isTokenChar(c byte) bool {
	if c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' {
		return true
	}
	switch c {
	case '!', '#', '$', '%', '&', '\'', '*', '+', '-', '.', '^', '_', '`', '|', '~':
		return true
	default:
		return false
	}
}

func isQDTEXT(c byte) bool {
	return c == '\t' || c == ' ' || c == '!' || c >= '#' && c <= '[' || c >= ']' && c <= '~' || c >= 0x80
}

func isQuotedPairChar(c byte) bool {
	return c == '\t' || c == ' ' || c >= '!' && c <= '~' || c >= 0x80
}
