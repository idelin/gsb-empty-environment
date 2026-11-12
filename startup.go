// Package netsec validates PostgreSQL startup messages for a
// connection proxy. It enforces a strict whitelist on startup
// parameters so that clients cannot smuggle dangerous settings
// such as search_path, replication or session_authorization past
// the proxy.
package netsec

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	// ProtocolVersion30 is the PostgreSQL frontend/backend protocol 3.0.
	ProtocolVersion30 uint32 = 196608

	// SSLRequestCode is the protocol code of an SSLRequest packet.
	SSLRequestCode uint32 = 80877103
	// GSSENCRequestCode is the protocol code of a GSSENCRequest packet.
	GSSENCRequestCode uint32 = 80877104

	// MaxStartupMessageLength is the largest accepted startup message,
	// including its 4-byte length prefix.
	MaxStartupMessageLength = 10000
)

var (
	ErrMessageTooLarge     = errors.New("netsec: startup message exceeds 10000 bytes")
	ErrMalformedMessage    = errors.New("netsec: malformed startup message")
	ErrUnsupportedProtocol = errors.New("netsec: unsupported protocol version")
	ErrDisallowedParameter = errors.New("netsec: disallowed startup parameter")
	ErrDuplicateParameter  = errors.New("netsec: duplicate startup parameter")
	ErrMissingUser         = errors.New("netsec: startup message missing user parameter")
	ErrInvalidOptions      = errors.New("netsec: invalid options parameter")
	ErrTimeoutExceedsLimit = errors.New("netsec: timeout exceeds configured limit")
	ErrPipelinedBytes      = errors.New("netsec: unread buffered bytes after SSL/GSSENC request")
)

// Limits carries caller-imposed upper bounds for the session
// parameters a client may request through the options parameter.
// A zero value forbids the corresponding -c setting entirely
// (except the literal value 0, which disables the timeout).
type Limits struct {
	MaxStatementTimeout time.Duration
	MaxLockTimeout      time.Duration
}

// StartupParams holds the whitelisted parameters of a valid
// startup message.
type StartupParams struct {
	User            string
	Database        string
	ApplicationName string
	ClientEncoding  string
	Options         string

	// StatementTimeout and LockTimeout are the parsed values of the
	// permitted -c settings inside Options, zero when unset.
	StatementTimeout time.Duration
	LockTimeout      time.Duration
}

// ReadStartupPacket reads one length-prefixed packet from r.
//
// For SSLRequest and GSSENCRequest packets it returns the request
// code and a nil message. If r already buffers additional unread
// bytes after such a request, it returns ErrPipelinedBytes: the
// peer is pipelining data behind the negotiation request, which is
// a desynchronization attempt, and the caller must terminate the
// connection instead of upgrading it.
//
// For any other packet it returns the protocol code and the full
// raw message including the 4-byte length prefix, ready to be
// passed to ValidateStartupMessage.
func ReadStartupPacket(r *bufio.Reader) (code uint32, msg []byte, err error) {
	header := make([]byte, 8)
	if _, err := io.ReadFull(r, header); err != nil {
		return 0, nil, fmt.Errorf("netsec: reading startup header: %w", err)
	}
	length := binary.BigEndian.Uint32(header[0:4])
	if length < 8 {
		return 0, nil, fmt.Errorf("%w: declared length %d below header size", ErrMalformedMessage, length)
	}
	if length > MaxStartupMessageLength {
		return 0, nil, fmt.Errorf("%w: declared length %d", ErrMessageTooLarge, length)
	}
	code = binary.BigEndian.Uint32(header[4:8])

	if code == SSLRequestCode || code == GSSENCRequestCode {
		if length != 8 {
			return 0, nil, fmt.Errorf("%w: SSL/GSSENC request with body", ErrMalformedMessage)
		}
		if r.Buffered() > 0 {
			return 0, nil, ErrPipelinedBytes
		}
		return code, nil, nil
	}

	msg = make([]byte, length)
	copy(msg, header)
	if _, err := io.ReadFull(r, msg[8:]); err != nil {
		return 0, nil, fmt.Errorf("netsec: reading startup body: %w", err)
	}
	return code, msg, nil
}

// ValidateStartupMessage validates a raw protocol 3.0 startup
// message, including its 4-byte length prefix, and returns the
// whitelisted parameters it carries.
func ValidateStartupMessage(msg []byte, lim Limits) (*StartupParams, error) {
	if len(msg) < 8 {
		return nil, fmt.Errorf("%w: shorter than header", ErrMalformedMessage)
	}
	length := binary.BigEndian.Uint32(msg[0:4])
	if length > MaxStartupMessageLength {
		return nil, fmt.Errorf("%w: declared length %d", ErrMessageTooLarge, length)
	}
	if int(length) != len(msg) {
		return nil, fmt.Errorf("%w: declared length %d does not match %d message bytes",
			ErrMalformedMessage, length, len(msg))
	}
	if code := binary.BigEndian.Uint32(msg[4:8]); code != ProtocolVersion30 {
		return nil, fmt.Errorf("%w: %d", ErrUnsupportedProtocol, code)
	}

	rest := msg[8:]
	params := &StartupParams{}
	seen := make(map[string]bool)
	var options string
	haveOptions := false

	for {
		key, n, ok := cutCString(rest)
		if !ok {
			return nil, fmt.Errorf("%w: parameter key not NUL-terminated", ErrMalformedMessage)
		}
		rest = rest[n:]
		if key == "" {
			if len(rest) != 0 {
				return nil, fmt.Errorf("%w: %d trailing bytes after terminator", ErrMalformedMessage, len(rest))
			}
			break
		}
		value, n, ok := cutCString(rest)
		if !ok {
			return nil, fmt.Errorf("%w: value for %q not NUL-terminated", ErrMalformedMessage, key)
		}
		rest = rest[n:]

		if !utf8.ValidString(key) || !utf8.ValidString(value) {
			return nil, fmt.Errorf("%w: non-UTF-8 parameter %q", ErrMalformedMessage, key)
		}
		if seen[key] {
			return nil, fmt.Errorf("%w: %q", ErrDuplicateParameter, key)
		}
		seen[key] = true

		switch key {
		case "user":
			params.User = value
		case "database":
			params.Database = value
		case "application_name":
			params.ApplicationName = value
		case "client_encoding":
			params.ClientEncoding = value
		case "options":
			options = value
			haveOptions = true
		default:
			return nil, fmt.Errorf("%w: %q", ErrDisallowedParameter, key)
		}
	}

	if params.User == "" {
		return nil, ErrMissingUser
	}
	if haveOptions {
		params.Options = options
		if err := parseOptions(options, lim, params); err != nil {
			return nil, err
		}
	}
	return params, nil
}

// cutCString returns the NUL-terminated string at the start of b
// and the number of bytes consumed including the NUL.
func cutCString(b []byte) (s string, n int, ok bool) {
	for i, c := range b {
		if c == 0 {
			return string(b[:i]), i + 1, true
		}
	}
	return "", 0, false
}

// parseOptions validates the options parameter. Only
// "-c statement_timeout=..." and "-c lock_timeout=..." settings
// within the caller's limits are accepted; everything else,
// notably --search_path and -c search_path, is rejected.
func parseOptions(opts string, lim Limits, params *StartupParams) error {
	tokens, err := tokenizeOptions(opts)
	if err != nil {
		return err
	}
	for i := 0; i < len(tokens); i++ {
		tok := tokens[i]
		var assignment string
		switch {
		case tok == "-c":
			i++
			if i >= len(tokens) {
				return fmt.Errorf("%w: -c without setting", ErrInvalidOptions)
			}
			assignment = tokens[i]
		case strings.HasPrefix(tok, "-c"):
			assignment = tok[2:]
		default:
			return fmt.Errorf("%w: forbidden option %q", ErrInvalidOptions, tok)
		}

		name, value, found := strings.Cut(assignment, "=")
		if !found || name == "" || value == "" {
			return fmt.Errorf("%w: malformed -c setting %q", ErrInvalidOptions, assignment)
		}
		// GUC names are case-insensitive server side; normalize so
		// that e.g. "-c Search_Path=..." cannot slip past the check.
		switch strings.ToLower(name) {
		case "statement_timeout":
			d, err := parseTimeoutValue(value)
			if err != nil {
				return fmt.Errorf("%w: statement_timeout: %v", ErrInvalidOptions, err)
			}
			if d > lim.MaxStatementTimeout {
				return fmt.Errorf("%w: statement_timeout %s > %s", ErrTimeoutExceedsLimit, d, lim.MaxStatementTimeout)
			}
			params.StatementTimeout = d
		case "lock_timeout":
			d, err := parseTimeoutValue(value)
			if err != nil {
				return fmt.Errorf("%w: lock_timeout: %v", ErrInvalidOptions, err)
			}
			if d > lim.MaxLockTimeout {
				return fmt.Errorf("%w: lock_timeout %s > %s", ErrTimeoutExceedsLimit, d, lim.MaxLockTimeout)
			}
			params.LockTimeout = d
		default:
			return fmt.Errorf("%w: -c %s is not permitted", ErrInvalidOptions, name)
		}
	}
	return nil
}

// tokenizeOptions splits an options string on ASCII whitespace.
// A backslash escapes the following byte, so "\ " stays inside a
// token; the escape is resolved here so that obfuscated tokens
// such as "-\c" or "state\ment_timeout" are checked in their
// effective form.
func tokenizeOptions(s string) ([]string, error) {
	var tokens []string
	var cur strings.Builder
	inToken := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\\':
			i++
			if i >= len(s) {
				return nil, fmt.Errorf("%w: trailing backslash in options", ErrInvalidOptions)
			}
			cur.WriteByte(s[i])
			inToken = true
		case c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\v' || c == '\f':
			if inToken {
				tokens = append(tokens, cur.String())
				cur.Reset()
				inToken = false
			}
		default:
			cur.WriteByte(c)
			inToken = true
		}
	}
	if inToken {
		tokens = append(tokens, cur.String())
	}
	return tokens, nil
}

// parseTimeoutValue parses a PostgreSQL timeout value: a
// non-negative integer with an optional unit suffix (us, ms, s,
// min, h, d). The unit defaults to milliseconds, matching the
// server. Anything else is rejected.
func parseTimeoutValue(s string) (time.Duration, error) {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i == 0 {
		return 0, fmt.Errorf("missing numeric value")
	}
	num := s[:i]
	unit := s[i:]

	var scale time.Duration
	switch unit {
	case "", "ms":
		scale = time.Millisecond
	case "us":
		scale = time.Microsecond
	case "s":
		scale = time.Second
	case "min":
		scale = time.Minute
	case "h":
		scale = time.Hour
	case "d":
		scale = 24 * time.Hour
	default:
		return 0, fmt.Errorf("unsupported unit %q", unit)
	}

	var n uint64
	for _, c := range num {
		d := uint64(c - '0')
		if n > (1<<63-1-d)/10 {
			return 0, fmt.Errorf("value out of range")
		}
		n = n*10 + d
	}
	if n > uint64((1<<63-1)/int64(scale)) {
		return 0, fmt.Errorf("value out of range")
	}
	return time.Duration(n) * scale, nil
}
