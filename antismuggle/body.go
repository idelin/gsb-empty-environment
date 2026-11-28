package antismuggle

import (
	"io"
)

type chunkState int

const (
	chunkHeader chunkState = iota
	chunkData
	chunkCRLF
	chunkTrailer
	chunkDone
)

// identityBody serves exactly ContentLength bytes from the stream.
type identityBody struct {
	conn      *Conn
	remaining int64
	delivered int64
	closed    bool
}

func (b *identityBody) Read(p []byte) (int, error) {
	if b.closed {
		return 0, io.EOF
	}
	if b.remaining == 0 {
		b.finish()
		return 0, io.EOF
	}
	if len(p) == 0 {
		return 0, nil
	}
	limit := int64(len(p))
	if limit > b.remaining {
		limit = b.remaining
	}
	n, err := b.conn.br.Read(p[:limit])
	b.remaining -= int64(n)
	b.delivered += int64(n)
	if b.conn.cfg.MaxBody > 0 && b.delivered > b.conn.cfg.MaxBody {
		return n, b.fail(hardError(ReasonBodyTooLarge, "body exceeds %d byte limit", b.conn.cfg.MaxBody))
	}
	if b.remaining == 0 {
		b.finish()
		if err == io.EOF {
			// Underlying stream ended exactly at the frame boundary.
			return n, io.EOF
		}
		if err != nil {
			return n, err
		}
		return n, io.EOF
	}
	if err == io.EOF {
		return n, b.fail(hardError(ReasonTruncated, "identity body truncated: %d bytes missing", b.remaining))
	}
	return n, err
}

func (b *identityBody) finish() {
	b.closed = true
	b.conn.closeBody()
}

func (b *identityBody) fail(e *Error) error {
	b.closed = true
	return b.conn.poison(e)
}

func (b *identityBody) Close() error {
	if !b.closed {
		// Closing without draining makes any further framing ambiguous.
		b.conn.poison(hardError(ReasonStreamPoisoned, "identity body closed before full consumption"))
	}
	return nil
}

// chunkedBody is a strict RFC 9112 chunked decoder. Every boundary byte is
// verified and any ambiguity poisons the whole connection.
type chunkedBody struct {
	conn      *Conn
	state     chunkState
	remaining int64 // bytes left in the current chunk data
	delivered int64
	closed    bool
	// raw accumulates the exact consumed chunked wire bytes (headers, data,
	// CRLFs, last-chunk and validated trailers) for pass-through normalization.
	raw []byte
}

func (b *chunkedBody) Read(p []byte) (int, error) {
	if b.closed {
		return 0, io.EOF
	}
	if len(p) == 0 {
		return 0, nil
	}
	switch b.state {
	case chunkHeader:
		size, err := b.readChunkHeader()
		if err != nil {
			return 0, b.fail(err)
		}
		if size == 0 {
			b.state = chunkTrailer
			return b.Read(p)
		}
		b.remaining = size
		b.state = chunkData
		return b.Read(p)

	case chunkData:
		limit := int64(len(p))
		if limit > b.remaining {
			limit = b.remaining
		}
		n, err := b.conn.br.Read(p[:limit])
		b.remaining -= int64(n)
		b.delivered += int64(n)
		b.raw = append(b.raw, p[:n]...)
		if b.conn.cfg.MaxBody > 0 && b.delivered > b.conn.cfg.MaxBody {
			return n, b.fail(hardError(ReasonBodyTooLarge, "decoded body exceeds %d byte limit", b.conn.cfg.MaxBody))
		}
		if b.remaining == 0 {
			b.state = chunkCRLF
			if err == io.EOF {
				return n, b.fail(hardError(ReasonTruncated, "chunk data truncated"))
			}
			return n, nil
		}
		if err == io.EOF {
			return n, b.fail(hardError(ReasonTruncated, "chunk data truncated"))
		}
		return n, err

	case chunkCRLF:
		if err := b.expectCRLF(); err != nil {
			return 0, b.fail(err)
		}
		b.state = chunkHeader
		return b.Read(p)

	case chunkTrailer:
		if err := b.readTrailers(); err != nil {
			return 0, b.fail(err)
		}
		b.state = chunkDone
		b.closed = true
		b.conn.closeBody()
		if b.conn.last != nil {
			b.conn.last.rawChunked = b.raw
		}
		return 0, io.EOF

	case chunkDone:
		return 0, io.EOF
	}
	return 0, b.fail(hardError(ReasonTruncated, "unknown chunk decoder state"))
}

func (b *chunkedBody) fail(e *Error) error {
	b.closed = true
	return b.conn.poison(e)
}

func (b *chunkedBody) Close() error {
	if !b.closed {
		b.conn.poison(hardError(ReasonStreamPoisoned, "chunked body closed before full consumption"))
	}
	return nil
}

// readChunkHeader reads one chunk-size line and validates size and extension.
func (b *chunkedBody) readChunkHeader() (int64, *Error) {
	line, err := b.conn.readLine()
	if err != nil {
		if err == io.EOF {
			return 0, hardError(ReasonTruncated, "chunk-size line missing")
		}
		return 0, err.(*Error)
	}
	b.raw = append(b.raw, line...)
	b.raw = append(b.raw, '\r', '\n')

	semi := indexByte(line, ';')
	sizePart, ext := line, []byte(nil)
	if semi >= 0 {
		sizePart, ext = line[:semi], line[semi+1:]
	}

	if len(sizePart) == 0 {
		return 0, hardError(ReasonBadChunkSize, "empty chunk-size")
	}
	for _, ch := range sizePart {
		if !isHexDigit(ch) {
			return 0, hardError(ReasonBadChunkSize, "chunk-size contains non-hex byte 0x%02x", ch)
		}
	}

	var size int64
	for _, ch := range sizePart {
		d := int64(hexValue(ch))
		size = size*16 + d
		if size < 0 || (b.conn.cfg.MaxChunkSize > 0 && size > b.conn.cfg.MaxChunkSize) {
			return 0, hardError(ReasonChunkTooLarge, "chunk-size %#x exceeds limit %d", size, b.conn.cfg.MaxChunkSize)
		}
	}

	if len(ext) > b.conn.cfg.MaxChunkExtension {
		return 0, hardError(ReasonBadChunkExtension, "chunk extension longer than %d bytes", b.conn.cfg.MaxChunkExtension)
	}
	for _, ch := range ext {
		if ch < 0x20 || ch == 0x7f {
			return 0, hardError(ReasonBadChunkExtension, "chunk extension contains control byte 0x%02x", ch)
		}
	}

	return size, nil
}

func (b *chunkedBody) expectCRLF() *Error {
	cr, err := b.conn.br.ReadByte()
	if err != nil {
		return hardError(ReasonTruncated, "expected CRLF after chunk data")
	}
	if cr != '\r' {
		return hardError(ReasonBadChunkTerminator, "chunk data not followed by CR (got 0x%02x)", cr)
	}
	lf, err := b.conn.br.ReadByte()
	if err != nil {
		return hardError(ReasonTruncated, "expected LF after CR")
	}
	if lf != '\n' {
		return hardError(ReasonBadChunkTerminator, "chunk data CR not followed by LF (got 0x%02x)", lf)
	}
	b.raw = append(b.raw, '\r', '\n')
	return nil
}

func (b *chunkedBody) readTrailers() *Error {
	for {
		line, err := b.conn.readLine()
		if err != nil {
			if pe, ok := err.(*Error); ok {
				return pe
			}
			return hardError(ReasonTruncated, "%v", err)
		}
		if len(line) == 0 {
			b.raw = append(b.raw, '\r', '\n')
			return nil
		}
		if line[0] == ' ' || line[0] == '\t' {
			return hardError(ReasonObsFold, "obs-fold in trailer section is forbidden")
		}
		colon := indexByte(line, ':')
		if colon <= 0 {
			return hardError(ReasonBadHeaderName, "trailer field missing ':' or empty name")
		}
		name := line[:colon]
		if !allBytes(name, isTokenByte) {
			return hardError(ReasonBadHeaderName, "trailer field name contains illegal byte")
		}
		value := trimOWS(line[colon+1:])
		if !allBytes(value, isFieldVChar) {
			return hardError(ReasonBadHeaderValue, "trailer value contains illegal control byte")
		}
		if err := b.conn.checkTrailerPolicy(string(name)); err != nil {
			return err
		}
		b.raw = append(b.raw, line...)
		b.raw = append(b.raw, '\r', '\n')
	}
}

func indexByte(b []byte, c byte) int {
	for i, x := range b {
		if x == c {
			return i
		}
	}
	return -1
}

func hexValue(b byte) int {
	switch {
	case isDigit(b):
		return int(b - '0')
	case b >= 'a' && b <= 'f':
		return int(b-'a') + 10
	default:
		return int(b-'A') + 10
	}
}

// ReadBodyAll drains a request body and returns its fully decoded bytes.
func ReadBodyAll(req *Request) ([]byte, error) {
	if req.Body == nil {
		return nil, nil
	}
	return io.ReadAll(req.Body)
}
