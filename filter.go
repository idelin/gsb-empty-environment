package netsec

import (
	"bytes"
	"errors"
	"strconv"
)

// Default limits. They are deliberately finite so a malformed request cannot
// make a proxy buffer an unbounded amount of data.
const (
	DefaultMaxDepth        = 8
	DefaultMaxBulkLength   = 16 << 20
	DefaultMaxArguments    = 1 << 16
	DefaultMaxInlineLength = 64 << 10
	DefaultMaxRequestSize  = 64 << 20
)

// Rejection reasons attached to a Frame. They are stable, fixed strings; no
// attacker-controlled bytes are ever included in an error reply.
const (
	ReasonCommandNotAllowed = "command not allowed"
	ReasonProtocol          = "protocol error"
	ReasonDepthExceeded     = "nesting depth exceeded"
	ReasonTooLarge          = "request too large"
	ReasonTooManyArguments  = "too many arguments"
)

// Frame is one request position in the client pipeline.
//
// Exactly one of Request and Reply is non-nil. For an allowed request,
// Forwarded is true and Request contains a canonical RESP array in which every
// argument is encoded as a bulk string using its actual byte length. For a
// rejected request, Forwarded is false, Reply contains a fixed synthetic error
// reply, and nothing must be sent to the backend for that position.
//
// A forwarding loop should enqueue one response slot for each returned Frame,
// in order: a backend slot for Forwarded frames and a local slot containing
// Reply for rejected frames. Filling the slots in that order guarantees that
// synthetic errors stay in their original pipeline positions.
type Frame struct {
	// Args contains the decoded request arguments, including the command name
	// as Args[0]. It is nil for malformed requests.
	Args [][]byte

	// Command is the lower-cased command name, or empty when the request is
	// malformed.
	Command string

	// Forwarded reports whether Request should be sent to the backend.
	Forwarded bool

	// Request is the canonical, re-encoded request to send to the backend.
	// It is nil for rejected requests.
	Request []byte

	// Reply is the fixed synthetic error reply for a rejected request. It is
	// nil for forwarded requests.
	Reply []byte

	// Reason is a stable rejection reason, empty for forwarded requests.
	Reason string

	// Fatal reports that the RESP framing is no longer trustworthy. After a
	// fatal frame the forwarding loop must stop forwarding and close the
	// connection; otherwise a malformed payload could be resynchronised into a
	// new, unintended request.
	Fatal bool
}

// ProtocolError is returned by Stream.Feed or Stream.Flush when framing can no
// longer be recovered. The frames returned alongside the error are still valid
// and must be delivered by the caller before closing.
type ProtocolError struct {
	Reason string
}

func (e *ProtocolError) Error() string {
	if e == nil {
		return "netsec: protocol error"
	}
	return "netsec: " + e.Reason
}

// Option customises a Filter.
type Option func(*Config)

// Config controls parser and re-encoder limits. Zero fields take their
// corresponding Default* value when passed to NewFilterWithConfig.
type Config struct {
	MaxDepth        int
	MaxBulkLength   int
	MaxArguments    int
	MaxInlineLength int
	MaxRequestSize  int
}

// WithMaxDepth limits RESP value nesting. A top-level request array is depth 1.
func WithMaxDepth(n int) Option {
	return func(c *Config) {
		c.MaxDepth = n
	}
}

// WithMaxBulkLength limits a single bulk string, in bytes.
func WithMaxBulkLength(n int) Option {
	return func(c *Config) {
		c.MaxBulkLength = n
	}
}

// WithMaxArguments limits the number of arguments in one request.
func WithMaxArguments(n int) Option {
	return func(c *Config) {
		c.MaxArguments = n
	}
}

// WithMaxInlineLength limits the length of one inline command line.
func WithMaxInlineLength(n int) Option {
	return func(c *Config) {
		c.MaxInlineLength = n
	}
}

// WithMaxRequestSize limits the number of unconsumed bytes a Stream retains for
// one request.
func WithMaxRequestSize(n int) Option {
	return func(c *Config) {
		c.MaxRequestSize = n
	}
}

// Filter parses client requests, applies a command allow-list and emits a
// canonical, length-safe encoding for allowed requests.
type Filter struct {
	allowed         map[string]struct{}
	maxDepth        int
	maxBulkLength   int
	maxArguments    int
	maxInlineLength int
	maxRequestSize  int
}

// NewFilter returns a Filter that allows only the supplied command names,
// compared case-insensitively. An empty list rejects every command.
func NewFilter(allowed []string, opts ...Option) (*Filter, error) {
	cfg := Config{
		MaxDepth:        DefaultMaxDepth,
		MaxBulkLength:   DefaultMaxBulkLength,
		MaxArguments:    DefaultMaxArguments,
		MaxInlineLength: DefaultMaxInlineLength,
		MaxRequestSize:  DefaultMaxRequestSize,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	return newFilter(allowed, cfg)
}

// NewFilterWithConfig is like NewFilter but accepts a Config directly. Zero
// fields are replaced by their defaults.
func NewFilterWithConfig(allowed []string, cfg Config) (*Filter, error) {
	if cfg.MaxDepth == 0 {
		cfg.MaxDepth = DefaultMaxDepth
	}
	if cfg.MaxBulkLength == 0 {
		cfg.MaxBulkLength = DefaultMaxBulkLength
	}
	if cfg.MaxArguments == 0 {
		cfg.MaxArguments = DefaultMaxArguments
	}
	if cfg.MaxInlineLength == 0 {
		cfg.MaxInlineLength = DefaultMaxInlineLength
	}
	if cfg.MaxRequestSize == 0 {
		cfg.MaxRequestSize = DefaultMaxRequestSize
	}
	return newFilter(allowed, cfg)
}

func newFilter(allowed []string, cfg Config) (*Filter, error) {
	limits := []struct {
		name  string
		value int
	}{
		{"max depth", cfg.MaxDepth},
		{"max bulk length", cfg.MaxBulkLength},
		{"max arguments", cfg.MaxArguments},
		{"max inline length", cfg.MaxInlineLength},
		{"max request size", cfg.MaxRequestSize},
	}
	for _, limit := range limits {
		if limit.value <= 0 {
			return nil, errors.New("netsec: " + limit.name + " must be positive")
		}
	}

	f := &Filter{
		allowed:         make(map[string]struct{}, len(allowed)),
		maxDepth:        cfg.MaxDepth,
		maxBulkLength:   cfg.MaxBulkLength,
		maxArguments:    cfg.MaxArguments,
		maxInlineLength: cfg.MaxInlineLength,
		maxRequestSize:  cfg.MaxRequestSize,
	}
	for _, command := range allowed {
		normalized := normalizeCommand([]byte(command))
		if normalized == "" {
			return nil, errors.New("netsec: allowed command name must not be empty")
		}
		f.allowed[normalized] = struct{}{}
	}
	return f, nil
}

// New is a convenience constructor with default limits. Invalid command names
// are ignored; use NewFilter when configuration errors must be reported.
func New(allowed ...string) *Filter {
	clean := make([]string, 0, len(allowed))
	for _, command := range allowed {
		if command != "" {
			clean = append(clean, command)
		}
	}
	f, _ := NewFilter(clean)
	return f
}

// NewStream creates an incremental parser for one client connection.
func (f *Filter) NewStream() *Stream {
	return &Stream{filter: f}
}

// Stream incrementally turns a client RESP stream into per-request Frames.
// Feed may be called with arbitrarily small or large chunks.
type Stream struct {
	filter *Filter
	buf    []byte
	fatal  bool
}

// Feed appends chunk and returns every complete request frame that became
// available. Incomplete requests stay buffered for a later call.
//
// A non-nil error means the frame boundaries cannot be trusted any more. The
// returned frames, including the final fatal Frame, must still be processed
// before the caller closes the connection. Policy rejections alone are not
// stream errors.
func (s *Stream) Feed(chunk []byte) ([]Frame, error) {
	if s.fatal {
		return nil, &ProtocolError{Reason: ReasonProtocol}
	}
	if len(chunk) != 0 {
		s.buf = append(s.buf, chunk...)
	}

	var frames []Frame
	for len(s.buf) > 0 {
		out := s.filter.parseRequest(s.buf)
		if out.incomplete {
			break
		}
		if out.hasFrame {
			frames = append(frames, out.frame)
		}
		s.buf = s.buf[out.consumed:]

		if out.fatal {
			s.fatal = true
			s.buf = nil
			return frames, &ProtocolError{Reason: out.reason}
		}
	}

	if len(s.buf) > s.filter.maxRequestSize {
		s.fatal = true
		s.buf = nil
		frames = append(frames, s.filter.rejectFrame(nil, ReasonTooLarge, true))
		return frames, &ProtocolError{Reason: ReasonTooLarge}
	}
	return frames, nil
}

// Flush reports a truncated request left at end of input. A clean stream
// returns no frames and a nil error.
func (s *Stream) Flush() ([]Frame, error) {
	if s.fatal {
		return nil, &ProtocolError{Reason: ReasonProtocol}
	}
	if len(s.buf) == 0 {
		return nil, nil
	}
	s.fatal = true
	s.buf = nil
	frame := s.filter.rejectFrame(nil, ReasonProtocol, true)
	return []Frame{frame}, &ProtocolError{Reason: ReasonProtocol}
}

// Process feeds a complete byte slice and flushes any truncated tail. It is a
// convenience wrapper around NewStream, Feed and Flush.
func (f *Filter) Process(p []byte) ([]Frame, error) {
	stream := f.NewStream()
	frames, err := stream.Feed(p)
	if err != nil {
		return frames, err
	}
	tail, err := stream.Flush()
	frames = append(frames, tail...)
	return frames, err
}

type parseOutcome struct {
	frame      Frame
	hasFrame   bool
	consumed   int
	incomplete bool
	fatal      bool
	reason     string
}

func (f *Filter) parseRequest(data []byte) parseOutcome {
	if len(data) == 0 {
		return parseOutcome{incomplete: true}
	}
	if data[0] == '*' {
		return f.parseArrayRequest(data)
	}
	return f.parseInlineRequest(data)
}

func (f *Filter) parseInlineRequest(data []byte) parseOutcome {
	lineEnd := bytes.Index(data, []byte("\r\n"))
	if lineEnd < 0 {
		if len(data) > f.maxInlineLength {
			return f.fatalOutcome(ReasonTooLarge)
		}
		return parseOutcome{incomplete: true}
	}

	line := data[:lineEnd]
	consumed := lineEnd + 2
	if len(line) > f.maxInlineLength {
		return parseOutcome{
			frame: f.rejectFrame(nil, ReasonTooLarge, false), hasFrame: true,
			consumed: consumed,
		}
	}

	args, err := splitInline(line)
	if err != nil {
		return parseOutcome{
			frame: f.rejectFrame(nil, ReasonProtocol, false), hasFrame: true,
			consumed: consumed,
		}
	}
	if len(args) == 0 {
		// Empty inline lines are ignored by Redis and produce no reply.
		return parseOutcome{consumed: consumed}
	}
	if len(args) > f.maxArguments {
		return parseOutcome{
			frame: f.rejectFrame(nil, ReasonTooManyArguments, false), hasFrame: true,
			consumed: consumed,
		}
	}
	return parseOutcome{
		frame:    f.makeFrame(args),
		hasFrame: true,
		consumed: consumed,
	}
}

func (f *Filter) parseArrayRequest(data []byte) parseOutcome {
	count, next, status := readCount(data, 0, '*')
	switch status {
	case tokenIncomplete:
		return parseOutcome{incomplete: true}
	case tokenBad:
		return f.fatalOutcome(ReasonProtocol)
	}

	if count < 0 {
		// A null array contains no command and is safe to consume.
		return parseOutcome{
			frame: f.rejectFrame(nil, ReasonProtocol, false), hasFrame: true,
			consumed: next,
		}
	}
	if count == 0 {
		return parseOutcome{
			frame: f.rejectFrame(nil, ReasonProtocol, false), hasFrame: true,
			consumed: next,
		}
	}
	if count > int64(f.maxArguments) {
		pos := next
		for i := int64(0); i < count; i++ {
			end, _, status := skipValue(data, pos, 2, f.maxDepth, f.maxBulkLength)
			switch status {
			case tokenIncomplete:
				return parseOutcome{incomplete: true}
			case tokenBad:
				return f.fatalOutcomeAt(ReasonProtocol, end)
			case tokenTooLarge:
				return f.fatalOutcomeAt(ReasonTooLarge, end)
			}
			pos = end
		}
		return parseOutcome{
			frame: f.rejectFrame(nil, ReasonTooManyArguments, false), hasFrame: true,
			consumed: pos,
		}
	}

	args := make([][]byte, 0, int(count))
	pos := next
	nested := false
	depthExceeded := false
	invalidArgument := false

	for i := int64(0); i < count; i++ {
		if pos >= len(data) {
			return parseOutcome{incomplete: true}
		}

		switch data[pos] {
		case '$':
			payload, null, end, status := readBulk(data, pos, f.maxBulkLength)
			switch status {
			case tokenIncomplete:
				return parseOutcome{incomplete: true}
			case tokenBad:
				return f.fatalOutcomeAt(ReasonProtocol, end)
			case tokenTooLarge:
				return f.fatalOutcomeAt(ReasonTooLarge, end)
			}
			if null {
				invalidArgument = true
				pos = end
				continue
			}
			args = append(args, payload)
			pos = end
		case '+':
			payload, end, status := readSimpleString(data, pos)
			switch status {
			case tokenIncomplete:
				return parseOutcome{incomplete: true}
			case tokenBad:
				return f.fatalOutcomeAt(ReasonProtocol, end)
			}
			args = append(args, payload)
			pos = end
		case '*':
			end, exceeded, status := skipValue(data, pos, 2, f.maxDepth, f.maxBulkLength)
			switch status {
			case tokenIncomplete:
				return parseOutcome{incomplete: true}
			case tokenBad:
				return f.fatalOutcomeAt(ReasonProtocol, end)
			case tokenTooLarge:
				return f.fatalOutcomeAt(ReasonTooLarge, end)
			}
			nested = true
			depthExceeded = depthExceeded || exceeded
			pos = end
		default:
			// A non-string array element is not a Redis command argument. Its
			// extent is unknown, so resynchronising would be unsafe.
			return f.fatalOutcomeAt(ReasonProtocol, pos)
		}
	}

	if depthExceeded {
		return parseOutcome{
			frame: f.rejectFrame(nil, ReasonDepthExceeded, false), hasFrame: true,
			consumed: pos,
		}
	}
	if nested {
		return parseOutcome{
			frame: f.rejectFrame(nil, ReasonProtocol, false), hasFrame: true,
			consumed: pos,
		}
	}
	if invalidArgument {
		return parseOutcome{
			frame: f.rejectFrame(nil, ReasonProtocol, false), hasFrame: true,
			consumed: pos,
		}
	}
	return parseOutcome{
		frame:    f.makeFrame(args),
		hasFrame: true,
		consumed: pos,
	}
}

func (f *Filter) makeFrame(args [][]byte) Frame {
	normalized := normalizeCommand(args[0])
	frame := Frame{
		Args:    cloneArgs(args),
		Command: normalized,
	}
	if _, ok := f.allowed[normalized]; !ok || normalized == "" {
		frame.Reply = errorReply(ReasonCommandNotAllowed)
		frame.Reason = ReasonCommandNotAllowed
		return frame
	}
	frame.Forwarded = true
	frame.Request = encodeBulkArray(args)
	return frame
}

func (f *Filter) rejectFrame(args [][]byte, reason string, fatal bool) Frame {
	frame := Frame{
		Args:   cloneArgs(args),
		Reply:  errorReply(reason),
		Reason: reason,
		Fatal:  fatal,
	}
	if len(args) > 0 {
		frame.Command = normalizeCommand(args[0])
	}
	return frame
}

func (f *Filter) fatalOutcome(reason string) parseOutcome {
	return f.fatalOutcomeAt(reason, 0)
}

func (f *Filter) fatalOutcomeAt(reason string, consumed int) parseOutcome {
	return parseOutcome{
		frame:    f.rejectFrame(nil, reason, true),
		hasFrame: true,
		consumed: consumed,
		fatal:    true,
		reason:   reason,
	}
}

func errorReply(reason string) []byte {
	return []byte("-ERR " + reason + "\r\n")
}

func encodeBulkArray(args [][]byte) []byte {
	var b bytes.Buffer
	// *<count>\r\n + each $<len>\r\n<arg>\r\n. Prefix lengths are always
	// recomputed from the decoded argument's byte length.
	b.Grow(1 + len(args)*8 + totalArgBytes(args))
	b.WriteByte('*')
	b.WriteString(strconv.Itoa(len(args)))
	b.WriteString("\r\n")
	for _, arg := range args {
		b.WriteByte('$')
		b.WriteString(strconv.Itoa(len(arg)))
		b.WriteString("\r\n")
		b.Write(arg)
		b.WriteString("\r\n")
	}
	return b.Bytes()
}

func totalArgBytes(args [][]byte) int {
	total := 0
	for _, arg := range args {
		total += len(arg)
	}
	return total
}

func cloneArgs(args [][]byte) [][]byte {
	if args == nil {
		return nil
	}
	cloned := make([][]byte, len(args))
	for i, arg := range args {
		cloned[i] = append([]byte(nil), arg...)
	}
	return cloned
}

func normalizeCommand(command []byte) string {
	normalized := make([]byte, len(command))
	for i, c := range command {
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		normalized[i] = c
	}
	return string(normalized)
}

type tokenStatus int

const (
	tokenOK tokenStatus = iota
	tokenIncomplete
	tokenBad
	tokenTooLarge
)

// readCount reads a RESP count header such as *3\r\n. pos points at prefix.
func readCount(data []byte, pos int, prefix byte) (int64, int, tokenStatus) {
	if pos >= len(data) || data[pos] != prefix {
		return 0, pos, tokenBad
	}
	lineEnd := bytes.Index(data[pos+1:], []byte("\r\n"))
	if lineEnd < 0 {
		return 0, pos, tokenIncomplete
	}
	lineEnd += pos + 1
	value, ok := parseDecimal(data[pos+1 : lineEnd])
	if !ok {
		return 0, lineEnd + 2, tokenBad
	}
	return value, lineEnd + 2, tokenOK
}

// readBulk reads one $<len>\r\n<bytes>\r\n token. It returns the payload, the
// position just after the trailing CRLF, and whether the token was incomplete,
// malformed, or exceeded the configured bulk limit. The payload is a slice of
// data; callers that retain it must copy it.
func readBulk(data []byte, pos, maxBulk int) (payload []byte, null bool, end int, status tokenStatus) {
	if pos >= len(data) || data[pos] != '$' {
		return nil, false, pos, tokenBad
	}
	lineEnd := bytes.Index(data[pos+1:], []byte("\r\n"))
	if lineEnd < 0 {
		return nil, false, pos, tokenIncomplete
	}
	lineEnd += pos + 1
	length, ok := parseDecimal(data[pos+1 : lineEnd])
	if !ok {
		return nil, false, lineEnd + 2, tokenBad
	}
	next := lineEnd + 2
	if length < 0 {
		if length == -1 {
			return nil, true, next, tokenOK
		}
		return nil, false, next, tokenBad
	}
	if length > int64(maxBulk) {
		return nil, false, next, tokenTooLarge
	}
	if length > int64(len(data)-next) {
		return nil, false, next, tokenIncomplete
	}
	payloadEnd := next + int(length)
	if payloadEnd+2 > len(data) {
		return nil, false, next, tokenIncomplete
	}
	if data[payloadEnd] != '\r' || data[payloadEnd+1] != '\n' {
		return nil, false, payloadEnd, tokenBad
	}
	return data[next:payloadEnd], false, payloadEnd + 2, tokenOK
}

func readSimpleString(data []byte, pos int) ([]byte, int, tokenStatus) {
	if pos >= len(data) || data[pos] != '+' {
		return nil, pos, tokenBad
	}
	lineEnd := bytes.Index(data[pos+1:], []byte("\r\n"))
	if lineEnd < 0 {
		return nil, pos, tokenIncomplete
	}
	lineEnd += pos + 1
	return data[pos+1 : lineEnd], lineEnd + 2, tokenOK
}

// skipValue consumes one arbitrary RESP value without building a tree. It
// tracks arrays iteratively, so an attacker-controlled nesting depth cannot
// overflow the Go stack. exceeded is set when nesting goes beyond maxDepth,
// but the value is still consumed exactly so the stream can stay
// synchronised and return a synthetic error for that request.
func skipValue(data []byte, pos, depth, maxDepth, maxBulk int) (end int, exceeded bool, status tokenStatus) {
	type frame struct {
		remaining int64
		depth     int
	}
	stack := []frame{{remaining: 1, depth: depth}}

	for len(stack) > 0 {
		top := &stack[len(stack)-1]
		if top.remaining == 0 {
			stack = stack[:len(stack)-1]
			continue
		}
		top.remaining--
		if top.depth > maxDepth {
			exceeded = true
		}
		if pos >= len(data) {
			return pos, exceeded, tokenIncomplete
		}

		switch data[pos] {
		case '$':
			_, null, next, st := readBulk(data, pos, maxBulk)
			if st != tokenOK {
				return next, exceeded, st
			}
			_ = null
			pos = next
		case '+', '-', ':', ',':
			lineEnd := bytes.Index(data[pos+1:], []byte("\r\n"))
			if lineEnd < 0 {
				return pos, exceeded, tokenIncomplete
			}
			pos += lineEnd + 3
		case '_':
			lineEnd := bytes.Index(data[pos+1:], []byte("\r\n"))
			if lineEnd < 0 {
				return pos, exceeded, tokenIncomplete
			}
			pos += lineEnd + 3
		case '*':
			count, next, st := readCount(data, pos, '*')
			if st != tokenOK {
				return next, exceeded, st
			}
			pos = next
			if count > 0 {
				stack = append(stack, frame{remaining: count, depth: top.depth + 1})
			}
		default:
			return pos, exceeded, tokenBad
		}
	}
	return pos, exceeded, tokenOK
}

func parseDecimal(b []byte) (int64, bool) {
	if len(b) == 0 {
		return 0, false
	}
	negative := false
	if b[0] == '-' {
		negative = true
		b = b[1:]
		if len(b) == 0 {
			return 0, false
		}
	}
	if len(b) > 19 {
		return 0, false
	}
	var value int64
	for _, c := range b {
		if c < '0' || c > '9' {
			return 0, false
		}
		d := int64(c - '0')
		if value > (1<<63-1-d)/10 {
			return 0, false
		}
		value = value*10 + d
	}
	if negative {
		value = -value
	}
	return value, true
}

// splitInline implements the inline command syntax used by Redis: arguments
// are separated by ASCII whitespace, single and double quotes group text, and
// the usual backslash escapes (including \xHH) are decoded inside quotes.
func splitInline(line []byte) ([][]byte, error) {
	var args [][]byte
	pos := 0
	for {
		for pos < len(line) && isSpace(line[pos]) {
			pos++
		}
		if pos >= len(line) {
			return args, nil
		}

		var arg []byte
		inDouble := false
		inSingle := false
		quoted := false
		done := false

		for !done {
			if pos >= len(line) {
				if inDouble || inSingle {
					return nil, errors.New("unterminated quote")
				}
				done = true
				break
			}

			c := line[pos]
			switch {
			case inDouble:
				if c == '\\' {
					pos++
					if pos >= len(line) {
						return nil, errors.New("trailing escape")
					}
					if line[pos] == 'x' && pos+2 < len(line) && isHex(line[pos+1]) && isHex(line[pos+2]) {
						arg = append(arg, hexByte(line[pos+1], line[pos+2]))
						pos += 3
						continue
					}
					arg = append(arg, unescape(line[pos]))
					pos++
					continue
				}
				if c == '"' {
					pos++
					quoted = true
					if pos < len(line) && !isSpace(line[pos]) {
						return nil, errors.New("data after closing quote")
					}
					done = true
					continue
				}
				arg = append(arg, c)
				pos++
			case inSingle:
				if c == '\\' {
					pos++
					if pos >= len(line) {
						return nil, errors.New("trailing escape")
					}
					arg = append(arg, unescape(line[pos]))
					pos++
					continue
				}
				if c == '\'' {
					pos++
					quoted = true
					if pos < len(line) && !isSpace(line[pos]) {
						return nil, errors.New("data after closing quote")
					}
					done = true
					continue
				}
				arg = append(arg, c)
				pos++
			default:
				if isSpace(c) {
					done = true
					continue
				}
				if c == '"' {
					inDouble = true
					pos++
					continue
				}
				if c == '\'' {
					inSingle = true
					pos++
					continue
				}
				arg = append(arg, c)
				pos++
			}
		}

		// A quoted empty argument is still an argument.
		if len(arg) > 0 || quoted {
			args = append(args, arg)
		}
	}
}

func isSpace(c byte) bool {
	switch c {
	case ' ', '\t', '\n', '\r', '\v', '\f':
		return true
	default:
		return false
	}
}

func isHex(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

func hexByte(a, b byte) byte {
	return hexNibble(a)<<4 | hexNibble(b)
}

func hexNibble(c byte) byte {
	switch {
	case c >= '0' && c <= '9':
		return c - '0'
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10
	default:
		return c - 'A' + 10
	}
}

func unescape(c byte) byte {
	switch c {
	case 'n':
		return '\n'
	case 'r':
		return '\r'
	case 't':
		return '\t'
	case 'b':
		return '\b'
	case 'a':
		return '\a'
	default:
		return c
	}
}
