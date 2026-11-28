package antismuggle

import (
	"bufio"
	"bytes"
	"io"
)

// forbiddenConnectionOptions are hop/framing headers that must never be
// listed in a Connection header; doing so is a classic smuggling vector.
var forbiddenConnectionOptions = map[string]bool{
	"transfer-encoding": true,
	"content-length":    true,
	"host":              true,
	"te":                true,
	"trailer":           true,
}

// forbiddenTrailerNames can never be accepted as chunked trailers regardless
// of the configured allow list.
var forbiddenTrailerNames = map[string]bool{
	"host":                true,
	"content-length":      true,
	"authorization":       true,
	"transfer-encoding":   true,
	"te":                  true,
	"trailer":             true,
	"connection":          true,
	"expect":              true,
	"content-encoding":    true,
	"content-range":       true,
	"www-authenticate":    true,
	"proxy-authenticate":  true,
	"proxy-authorization": true,
}

// Conn frames one persistent HTTP/1.1 byte stream into validated Requests.
// After a hard error the Conn is permanently poisoned and every subsequent
// call fails, which forces the gateway to close the TCP connection.
type Conn struct {
	br       *bufio.Reader
	cfg      *Config
	err      *Error
	bodyOpen bool
	last     *Request
}

// NewConn wraps an underlying stream with the given strict policy.
func NewConn(r io.Reader, cfg *Config) *Conn {
	if cfg == nil {
		cfg = DefaultConfig()
	}
	return &Conn{
		br:  bufio.NewReaderSize(r, 64*1024),
		cfg: cfg,
	}
}

func (c *Conn) poison(e *Error) *Error {
	if c.err == nil {
		c.err = e
	}
	return c.err
}

// Err returns the hard error that poisoned the stream, if any.
func (c *Conn) Err() *Error { return c.err }

// readLine returns one line with its terminating CRLF stripped. A bare LF or a
// CR not followed by LF poisons the stream immediately.
func (c *Conn) readLine() ([]byte, error) {
	if c.err != nil {
		return nil, c.err
	}
	var line []byte
	for {
		b, err := c.br.ReadByte()
		if err != nil {
			if err == io.EOF && len(line) == 0 {
				return nil, io.EOF
			}
			if err == io.EOF {
				return nil, c.poison(hardError(ReasonTruncated, "incomplete line: no CRLF terminator"))
			}
			return nil, err
		}
		if b == '\n' {
			if len(line) == 0 || line[len(line)-1] != '\r' {
				return nil, c.poison(hardError(ReasonBareLF, "line terminated by bare LF"))
			}
			return line[:len(line)-1], nil
		}
		if b == '\r' {
			nxt, err := c.br.ReadByte()
			if err != nil {
				return nil, c.poison(hardError(ReasonTruncated, "CR with no following LF"))
			}
			if nxt != '\n' {
				return nil, c.poison(hardError(ReasonBareCR, "bare CR not followed by LF"))
			}
			return line, nil
		}
		if len(line) >= c.cfg.MaxHeaderLine {
			return nil, c.poison(hardError(ReasonLineTooLong, "header line exceeds %d bytes", c.cfg.MaxHeaderLine))
		}
		line = append(line, b)
	}
}

// Next reads and validates one request frame. io.EOF is returned only at a
// clean frame boundary (idle keep-alive connection closed by the peer).
func (c *Conn) Next() (*Request, error) {
	if c.err != nil {
		return nil, c.err
	}
	if c.bodyOpen {
		return nil, c.poison(hardError(ReasonBodyPending, "previous request body was not fully consumed"))
	}

	reqLine, err := c.readLine()
	if err != nil {
		if pe, ok := err.(*Error); ok {
			return nil, c.poison(pe)
		}
		return nil, err // io.EOF at a clean boundary
	}
	req, perr := c.parseRequestLine(reqLine)
	if perr != nil {
		return nil, c.poison(perr)
	}

	if herr := c.readHeaders(req); herr != nil {
		return nil, c.poison(herr)
	}
	if ferr := c.applyFramingPolicy(req); ferr != nil {
		return nil, c.poison(ferr)
	}
	c.attachBody(req)
	return req, nil
}

func (c *Conn) parseRequestLine(line []byte) (*Request, *Error) {
	if len(line) == 0 {
		return nil, hardError(ReasonBadRequestLine, "empty request line")
	}
	parts := bytes.Split(line, []byte{' '})
	if len(parts) != 3 {
		return nil, hardError(ReasonBadRequestLine, "request line must be exactly METHOD SP target SP version")
	}
	method, target, version := parts[0], parts[1], parts[2]

	if len(method) == 0 || !allBytes(method, isTokenByte) {
		return nil, hardError(ReasonBadRequestLine, "invalid method token")
	}
	if len(target) == 0 || !allBytes(target, isTargetByte) {
		return nil, hardError(ReasonBadRequestLine, "invalid request target")
	}
	ver := string(version)
	if ver != "HTTP/1.1" && ver != "HTTP/1.0" {
		return nil, hardError(ReasonBadRequestLine, "unsupported or malformed HTTP version %q", ver)
	}

	return &Request{
		Method:  string(method),
		Target:  string(target),
		Version: ver,
		cfg:     c.cfg,
		conn:    c,
	}, nil
}

func isTargetByte(b byte) bool {
	return b >= 0x21 && b != 0x7f
}

func allBytes(b []byte, ok func(byte) bool) bool {
	for _, x := range b {
		if !ok(x) {
			return false
		}
	}
	return true
}

func (c *Conn) readHeaders(req *Request) *Error {
	for {
		line, err := c.readLine()
		if err != nil {
			if pe, ok := err.(*Error); ok {
				return pe
			}
			return hardError(ReasonTruncated, "%v", err)
		}
		if len(line) == 0 {
			return nil // end of header block
		}
		if len(req.Fields) >= c.cfg.MaxHeaderCount {
			return hardError(ReasonLineTooLong, "header count exceeds %d", c.cfg.MaxHeaderCount)
		}
		if line[0] == ' ' || line[0] == '\t' {
			return hardError(ReasonObsFold, "obs-fold header continuation is forbidden")
		}
		colon := bytes.IndexByte(line, ':')
		if colon <= 0 {
			return hardError(ReasonBadHeaderName, "header field missing ':' or empty name")
		}
		name := line[:colon]
		if !allBytes(name, isTokenByte) {
			return hardError(ReasonBadHeaderName, "field name contains whitespace, control char or illegal byte")
		}
		value := trimOWS(line[colon+1:])
		if !allBytes(value, isFieldVChar) {
			return hardError(ReasonBadHeaderValue, "field value contains illegal control byte")
		}
		req.Fields = append(req.Fields, Field{
			Name:     string(name),
			Value:    string(value),
			RawValue: string(line[colon+1:]),
		})
	}
}

func (c *Conn) applyFramingPolicy(req *Request) *Error {
	var clValues, teValues, trailerValues, connectionValues []string
	for _, f := range req.Fields {
		switch {
		case equalFoldASCII(f.Name, "content-length"):
			clValues = append(clValues, f.RawValue)
		case equalFoldASCII(f.Name, "transfer-encoding"):
			teValues = append(teValues, f.RawValue)
		case equalFoldASCII(f.Name, "trailer"):
			trailerValues = append(trailerValues, f.Value)
		case equalFoldASCII(f.Name, "connection"):
			connectionValues = append(connectionValues, f.Value)
		}
	}

	wantClose, keepAlive, cerr := c.connectionDirectives(connectionValues)
	if cerr != nil {
		return cerr
	}

	// Hard gate 1: CL and TE must never coexist. Deleting one to "repair" the
	// request is explicitly forbidden.
	hasTE := len(teValues) > 0
	hasCL := len(clValues) > 0
	if hasTE && hasCL {
		return hardError(ReasonBothCLAndTE, "request carries both Content-Length and Transfer-Encoding")
	}

	if hasTE {
		if err := c.validateTransferEncoding(teValues); err != nil {
			return err
		}
		req.Transfer = BodyChunked
	}

	if hasCL {
		n, err := parseContentLength(clValues, c.cfg.RejectDuplicateContentLength)
		if err != nil {
			return err
		}
		req.Transfer = BodyIdentity
		req.ContentLength = n
	}

	if !hasTE && !hasCL {
		req.Transfer = BodyNone
	}

	if err := c.validateTrailerAnnouncements(trailerValues); err != nil {
		return err
	}

	req.KeepAlive = c.decideKeepAlive(req.Version, wantClose, keepAlive)
	return nil
}

func (c *Conn) connectionDirectives(values []string) (wantClose, keepAlive bool, e *Error) {
	for _, v := range values {
		for _, raw := range bytes.Split([]byte(v), []byte{','}) {
			tok := string(trimOWS(raw))
			if tok == "" {
				continue
			}
			lower := lowerString(tok)
			if forbiddenConnectionOptions[lower] {
				return false, false, hardError(ReasonBadConnectionOption, "forbidden Connection token %q", tok)
			}
			switch lower {
			case "close":
				wantClose = true
			case "keep-alive":
				keepAlive = true
			}
		}
	}
	return wantClose, keepAlive, nil
}

func (c *Conn) validateTransferEncoding(values []string) *Error {
	if len(values) > 1 {
		return hardError(ReasonBadTE, "multiple Transfer-Encoding fields are forbidden")
	}
	v := values[0]
	if c.cfg.AllowChunkedCaseInsensitive {
		if !equalFoldASCII(v, "chunked") {
			return hardError(ReasonBadTE, "only the single token %q is allowed, got %q", "chunked", v)
		}
	} else if v != "chunked" {
		return hardError(ReasonBadTE, "only exact lowercase %q is allowed, got %q", "chunked", v)
	}
	return nil
}

func parseContentLength(values []string, rejectDuplicate bool) (int64, *Error) {
	if len(values) > 1 {
		if rejectDuplicate {
			return 0, hardError(ReasonMultipleCL, "multiple Content-Length fields are forbidden")
		}
		first := values[0]
		for _, v := range values[1:] {
			if v != first {
				return 0, hardError(ReasonMultipleCL, "conflicting Content-Length values %q vs %q", first, v)
			}
		}
	}
	var parsed int64
	for i, v := range values {
		if len(v) == 0 {
			return 0, hardError(ReasonBadCL, "empty Content-Length value")
		}
		var n int64
		for _, b := range []byte(v) {
			if !isDigit(b) {
				return 0, hardError(ReasonBadCL, "Content-Length must be pure ASCII decimal digits, got %q", v)
			}
			n = n*10 + int64(b-'0')
			if n < 0 || n > (1<<63-1) {
				return 0, hardError(ReasonBadCL, "Content-Length overflow in %q", v)
			}
		}
		if i == 0 {
			parsed = n
		}
	}
	return parsed, nil
}

func (c *Conn) validateTrailerAnnouncements(values []string) *Error {
	for _, v := range values {
		for _, raw := range bytes.Split([]byte(v), []byte{','}) {
			name := string(trimOWS(raw))
			if name == "" {
				continue
			}
			if !allBytes([]byte(name), isTokenByte) {
				return hardError(ReasonTrailerAnnouncement, "invalid trailer name %q", name)
			}
			if err := c.checkTrailerPolicy(name); err != nil {
				return hardError(ReasonTrailerAnnouncement, "%v", err)
			}
		}
	}
	return nil
}

func (c *Conn) checkTrailerPolicy(name string) *Error {
	lower := lowerString(name)
	if forbiddenTrailerNames[lower] {
		return hardError(ReasonTrailerForbidden, "trailer %q is a forbidden hop/security header", name)
	}
	if len(c.cfg.AllowedTrailers) == 0 {
		return hardError(ReasonTrailerForbidden, "trailers are disabled; %q rejected", name)
	}
	for _, allowed := range c.cfg.AllowedTrailers {
		if lowerString(allowed) == lower {
			return nil
		}
	}
	return hardError(ReasonTrailerForbidden, "trailer %q is not on the allow list", name)
}

func (c *Conn) decideKeepAlive(version string, wantClose, keepAlive bool) bool {
	if version == "HTTP/1.1" {
		return !wantClose
	}
	return keepAlive
}

func lowerString(s string) string {
	b := []byte(s)
	for i := range b {
		b[i] = lowerASCII(b[i])
	}
	return string(b)
}

func (c *Conn) attachBody(req *Request) {
	c.last = req
	switch req.Transfer {
	case BodyIdentity:
		req.Body = &identityBody{conn: c, remaining: req.ContentLength}
		c.bodyOpen = true
	case BodyChunked:
		req.Body = &chunkedBody{conn: c, state: chunkHeader}
		c.bodyOpen = true
	default:
		req.Body = nil
	}
}

func (c *Conn) closeBody() {
	c.bodyOpen = false
}
