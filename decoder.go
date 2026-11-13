package netsec

import (
	"bytes"
	"errors"
	"fmt"
	"io"
)

// DefaultMaxBody is the number of early body bytes buffered when no explicit
// limit is configured.
const DefaultMaxBody = 64 << 10

var (
	// ErrSmuggledRequest reports that the early body contains a complete HTTP
	// request line. A gateway must not send 100 Continue or forward the body
	// when it receives this error.
	ErrSmuggledRequest = errors.New("netsec: complete HTTP request line in early body")

	// ErrInvalidInput reports an unsupported argument to Decode or Feed.
	ErrInvalidInput = errors.New("netsec: invalid decoder input")

	// ErrInvalidConfig reports a negative buffer limit.
	ErrInvalidConfig = errors.New("netsec: invalid decoder configuration")
)

// Config controls how much of the early body a Decoder retains.
type Config struct {
	// MaxBody is the maximum number of body bytes retained before the
	// 100 Continue decision. A zero value disables buffering.
	MaxBody int
}

// Options is an alias for Config.
type Options = Config

// Decision is the decoder's verdict for the early request body.
type Decision uint8

const (
	// DecisionContinue means no complete request line was found and the
	// gateway may proceed with its normal 100 Continue handling.
	DecisionContinue Decision = iota

	// DecisionReject means the early body contains a complete request line.
	DecisionReject
)

// Decoder incrementally screens bytes that arrive before a gateway sends a
// 100 Continue response.
//
// A Decoder retains at most its configured limit. It reports ErrSmuggledRequest
// as soon as a complete HTTP request line appears at the start of a line. A
// short body without such a line, or a body that reaches the limit without one,
// is accepted. Bytes beyond the limit are deliberately not inspected.
//
// Decoder is not safe for concurrent use.
type Decoder struct {
	limit      int
	configured bool

	buf       []byte
	lineStart int
	scanned   int

	err       error
	offending []byte
}

// NewDecoder creates a Decoder. The optional arguments may be an int, an
// int64, a Config, an *Config, or an Options value. If omitted, DefaultMaxBody
// is used. Negative limits are clamped to zero.
func NewDecoder(opts ...any) *Decoder {
	cfg, err := configFrom(opts)
	if err != nil || cfg.MaxBody < 0 {
		cfg.MaxBody = 0
	}
	return newDecoder(cfg.MaxBody)
}

// NewExpectContinueDecoder is an alias for NewDecoder.
func NewExpectContinueDecoder(opts ...any) *Decoder {
	return NewDecoder(opts...)
}

func newDecoder(limit int) *Decoder {
	capacity := limit
	if capacity > 4096 {
		capacity = 4096
	}
	return &Decoder{
		limit:      limit,
		configured: true,
		buf:        make([]byte, 0, capacity),
	}
}

// Limit returns the maximum number of early body bytes retained by the
// decoder.
func (d *Decoder) Limit() int {
	return d.effectiveLimit()
}

func (d *Decoder) effectiveLimit() int {
	if !d.configured && d.limit == 0 {
		return DefaultMaxBody
	}
	return d.limit
}

// Len returns the number of buffered body bytes.
func (d *Decoder) Len() int {
	return len(d.buf)
}

// Full reports whether the buffered body has reached the configured limit.
func (d *Decoder) Full() bool {
	return len(d.buf) >= d.effectiveLimit()
}

// Buffered returns a copy of the retained body prefix. The bytes are safe to
// forward after the decoder has accepted the request.
func (d *Decoder) Buffered() []byte {
	return append([]byte(nil), d.buf...)
}

// Body is an alias for Buffered.
func (d *Decoder) Body() []byte {
	return d.Buffered()
}

// Err returns the rejection error, or nil if no complete request line has been
// found.
func (d *Decoder) Err() error {
	return d.err
}

// Decision returns the current verdict.
func (d *Decoder) Decision() Decision {
	if d.err != nil {
		return DecisionReject
	}
	return DecisionContinue
}

// Allowed reports whether the early body has been accepted.
func (d *Decoder) Allowed() bool {
	return d.err == nil
}

// OffendingLine returns a copy of the request line that caused the rejection.
// It returns nil when no request line has been found.
func (d *Decoder) OffendingLine() []byte {
	return append([]byte(nil), d.offending...)
}

// Feed adds early body bytes to the decoder and screens them. Bytes after the
// configured limit are ignored.
//
// Feed returns ErrSmuggledRequest once a complete request line is present. Once
// rejected, the decoder remains rejected until Reset is called.
func (d *Decoder) Feed(p []byte) error {
	if d.err != nil {
		return d.err
	}
	limit := d.effectiveLimit()
	if limit == 0 || len(p) == 0 {
		return nil
	}

	remaining := limit - len(d.buf)
	if remaining <= 0 {
		return nil
	}
	if len(p) > remaining {
		p = p[:remaining]
	}

	d.buf = append(d.buf, p...)
	for d.scanned < len(d.buf) {
		i := bytes.IndexByte(d.buf[d.scanned:], '\n')
		if i < 0 {
			d.scanned = len(d.buf)
			break
		}
		lineEnd := d.scanned + i
		d.scanned = lineEnd + 1

		line := d.buf[d.lineStart:lineEnd]
		if n := len(line); n > 0 && line[n-1] == '\r' {
			line = line[:n-1]
		}
		if isRequestLine(line) {
			d.err = ErrSmuggledRequest
			d.offending = append(d.offending[:0], line...)
			return d.err
		}
		d.lineStart = d.scanned
	}
	return nil
}

// Check is an alias for Feed.
func (d *Decoder) Check(p []byte) error {
	return d.Feed(p)
}

// Write implements io.Writer. On rejection it returns (0,
// ErrSmuggledRequest) because none of the screened chunk may be forwarded.
func (d *Decoder) Write(p []byte) (int, error) {
	if err := d.Feed(p); err != nil {
		return 0, err
	}
	return len(p), nil
}

// Decode feeds input into the decoder. Input may be a []byte, a string, or an
// io.Reader. For an io.Reader, decoding stops as soon as the configured limit
// is reached or a request line is found.
func (d *Decoder) Decode(input any) error {
	switch v := input.(type) {
	case []byte:
		return d.Feed(v)
	case string:
		return d.Feed([]byte(v))
	case io.Reader:
		return d.feedReader(v)
	default:
		return fmt.Errorf("%w: %T", ErrInvalidInput, input)
	}
}

func (d *Decoder) feedReader(r io.Reader) error {
	if d.err != nil {
		return d.err
	}
	for !d.Full() {
		buf := make([]byte, 4096)
		if remaining := d.effectiveLimit() - len(d.buf); remaining < len(buf) {
			buf = buf[:remaining]
		}
		n, err := r.Read(buf)
		if n > 0 {
			if feedErr := d.Feed(buf[:n]); feedErr != nil {
				return feedErr
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		if n == 0 {
			return nil
		}
	}
	return nil
}

// Finish marks the early-body segment as complete. It returns any rejection
// found while feeding bytes. A short body without a complete request line is
// accepted.
func (d *Decoder) Finish() error {
	return d.err
}

// Reset clears the buffered body and verdict so the decoder can be reused for
// another request.
func (d *Decoder) Reset() {
	d.buf = d.buf[:0]
	d.lineStart = 0
	d.scanned = 0
	d.err = nil
	d.offending = d.offending[:0]
}

// Decode screens a single early body. input may be a []byte, a string, or an
// io.Reader. The optional arguments configure the buffer limit in the same way
// as NewDecoder.
func Decode(input any, opts ...any) error {
	cfg, err := configFrom(opts)
	if err != nil {
		return err
	}
	if cfg.MaxBody < 0 {
		return ErrInvalidConfig
	}
	return newDecoder(cfg.MaxBody).Decode(input)
}

// ValidateEarlyBody screens an early body using a temporary decoder.
func ValidateEarlyBody(body []byte, opts ...any) error {
	return Decode(body, opts...)
}

// Scan is an alias for ValidateEarlyBody.
func Scan(body []byte, opts ...any) error {
	return ValidateEarlyBody(body, opts...)
}

func configFrom(opts []any) (Config, error) {
	if len(opts) == 0 {
		return Config{MaxBody: DefaultMaxBody}, nil
	}
	cfg := Config{MaxBody: DefaultMaxBody}
	for _, opt := range opts {
		switch v := opt.(type) {
		case nil:
			continue
		case int:
			cfg.MaxBody = v
		case int64:
			cfg.MaxBody = int(v)
		case uint64:
			if v > uint64(^uint(0)>>1) {
				return Config{}, ErrInvalidConfig
			}
			cfg.MaxBody = int(v)
		case Config:
			cfg = v
		case *Config:
			if v == nil {
				return Config{}, ErrInvalidConfig
			}
			cfg = *v
		default:
			return Config{}, fmt.Errorf("%w: %T", ErrInvalidInput, opt)
		}
	}
	return cfg, nil
}

// isRequestLine reports whether line is a complete HTTP request line without
// its terminator:
//
//	request-line = method SP request-target SP HTTP-version
func isRequestLine(line []byte) bool {
	first := bytes.IndexByte(line, ' ')
	if first <= 0 {
		return false
	}
	method := line[:first]
	if !isToken(method) {
		return false
	}

	rest := line[first+1:]
	second := bytes.IndexByte(rest, ' ')
	if second <= 0 {
		return false
	}
	target := rest[:second]
	version := rest[second+1:]

	return isRequestTarget(target) && isHTTPVersion(version)
}

func isToken(b []byte) bool {
	if len(b) == 0 {
		return false
	}
	for _, c := range b {
		switch {
		case c >= 'a' && c <= 'z':
		case c >= 'A' && c <= 'Z':
		case c >= '0' && c <= '9':
		case c == '!', c == '#', c == '$', c == '%', c == '&', c == '\'':
		case c == '*', c == '+', c == '-', c == '.', c == '^', c == '_':
		case c == '`', c == '|', c == '~':
		default:
			return false
		}
	}
	return true
}

func isRequestTarget(b []byte) bool {
	if len(b) == 0 {
		return false
	}
	for _, c := range b {
		if c <= ' ' || c == 0x7f {
			return false
		}
	}
	return true
}

func isHTTPVersion(b []byte) bool {
	const prefix = "HTTP/"
	if len(b) < len(prefix)+3 || !bytes.Equal(b[:len(prefix)], []byte(prefix)) {
		return false
	}
	b = b[len(prefix):]
	dot := bytes.IndexByte(b, '.')
	if dot <= 0 || dot == len(b)-1 {
		return false
	}
	for _, c := range b[:dot] {
		if c < '0' || c > '9' {
			return false
		}
	}
	for _, c := range b[dot+1:] {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
