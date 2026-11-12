// Package gemini reads a single Gemini protocol request from a connection.
//
// A Gemini request is one absolute URL terminated by CRLF. The readers in
// this package enforce strict framing so that a second request smuggled on
// the same TLS connection cannot be processed: ReadRequest returns the first
// (and only) request and never treats bytes after the terminating CRLF as
// another request.
package gemini

import (
	"bytes"
	"errors"
	"io"
	"net/url"
	"strconv"
	"strings"
)

const (
	// MaxURL is the maximum permitted length of the URL line in bytes.
	MaxURL = 1024
	// MaxRequest is MaxURL plus the two-byte CRLF terminator.
	MaxRequest = MaxURL + 2

	// DefaultPort is used when the request URL does not specify a port.
	DefaultPort = 1965
)

// Config identifies the service host and port that a request must target.
type Config struct {
	Host string
	Port int
}

// Request is one validated Gemini request.
type Request struct {
	// URL is the parsed request URL.
	URL *url.URL
	// Host is the lowercased host, without a port or IPv6 brackets.
	Host string
	// Port is the effective port (the URL port or DefaultPort).
	Port int
	// Path is the dot-segment-normalized, percent-decoded path.
	Path string
}

// Error is a Gemini protocol error carrying the response status code.
type Error struct {
	// Status is the two-digit Gemini status code (53 or 59).
	Status int
	// Meta is the human-readable response meta text.
	Meta string
}

func (e *Error) Error() string {
	return "gemini: status " + strconv.Itoa(e.Status) + ": " + e.Meta
}

func badRequest(meta string) *Error {
	return &Error{Status: 59, Meta: meta}
}

func proxyRefused(meta string) *Error {
	return &Error{Status: 53, Meta: meta}
}

// ReadRequest reads and validates one request from r.
//
// It reads at most MaxRequest bytes and requires the URL line to be at most
// MaxURL bytes and terminated by CRLF. A bare LF, a missing CR, a line that
// exceeds the limits, or an EOF before the terminator yields status 59.
//
// Only the first request on the connection is handled: reading stops as soon
// as the CRLF is found, so any further bytes already buffered or still on the
// connection are discarded by the caller closing the connection and are never
// parsed as a second request.
func ReadRequest(r io.Reader, cfg Config) (*Request, error) {
	line, err := readLine(r)
	if err != nil {
		return nil, err
	}
	return parseRequest(line, cfg)
}

// readLine returns the URL line preceding the first CRLF.
//
// Bytes are consumed one at a time from arbitrarily sized reads so framing is
// independent of how the transport splits the stream. Once CRLF is found the
// current read's remaining bytes are intentionally ignored, implementing the
// single-request-per-connection rule.
func readLine(r io.Reader) ([]byte, error) {
	var (
		buf   [512]byte
		line  = make([]byte, 0, MaxURL)
		total int
		cr    bool
		done  bool
	)
	for !done {
		n, readErr := r.Read(buf[:])
		for i := 0; i < n; i++ {
			b := buf[i]
			total++
			if total > MaxRequest {
				return nil, badRequest("request exceeds 1026 bytes")
			}
			if cr {
				if b == '\n' {
					done = true
					break
				}
				// A CR must be immediately followed by LF.
				return nil, badRequest("bare CR in request line")
			}
			if b == '\n' {
				return nil, badRequest("bare LF: request must end with CRLF")
			}
			if b == '\r' {
				cr = true
				continue
			}
			line = append(line, b)
		}
		if done {
			break
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return nil, badRequest("request must end with CRLF")
			}
			return nil, readErr
		}
	}
	if len(line) == 0 {
		return nil, badRequest("empty request line")
	}
	if len(line) > MaxURL {
		return nil, badRequest("URL exceeds 1024 bytes")
	}
	return line, nil
}

func parseRequest(line []byte, cfg Config) (*Request, error) {
	// Reject whitespace and control characters outright; valid URLs never
	// contain them and this stops parser-confusion tricks at the front door.
	for _, b := range line {
		if b <= 0x20 || b == 0x7f {
			return nil, badRequest("control character or space in URL")
		}
	}

	u, err := url.Parse(string(line))
	if err != nil {
		return nil, badRequest("malformed URL")
	}
	if !u.IsAbs() || !strings.EqualFold(u.Scheme, "gemini") {
		return nil, badRequest("scheme must be gemini")
	}
	if u.User != nil {
		return nil, badRequest("userinfo is not allowed")
	}
	if u.Fragment != "" || bytes.Contains(line, []byte("#")) {
		return nil, badRequest("fragment is not allowed")
	}

	host := strings.ToLower(u.Hostname())
	if host == "" {
		return nil, proxyRefused("missing host")
	}
	port := DefaultPort
	if portText := u.Port(); portText != "" {
		parsed, err := strconv.Atoi(portText)
		if err != nil || parsed < 0 || parsed > 65535 {
			return nil, badRequest("invalid port")
		}
		port = parsed
	}
	if !strings.EqualFold(host, strings.ToLower(cfg.Host)) || port != cfg.Port {
		return nil, proxyRefused("host and port do not match this server")
	}

	path, err := normalizePath(u)
	if err != nil {
		return nil, err
	}

	return &Request{URL: u, Host: host, Port: port, Path: path}, nil
}

// normalizePath percent-decodes the path and resolves dot segments.
//
// Decoding happens before normalization so encoded forms such as %2e%2e and
// %2f cannot hide traversal from the dot-segment check. Any resolved segment
// that would escape the root is rejected as path traversal.
func normalizePath(u *url.URL) (string, error) {
	decoded, err := url.PathUnescape(u.EscapedPath())
	if err != nil {
		return "", badRequest("malformed percent-encoding in path")
	}
	if decoded == "" || decoded[0] != '/' {
		decoded = "/" + decoded
	}

	absolute := decoded[0] == '/'
	trailing := len(decoded) > 1 && decoded[len(decoded)-1] == '/'
	segments := strings.Split(decoded, "/")
	stack := make([]string, 0, len(segments))
	for _, seg := range segments {
		switch seg {
		case "", ".":
			continue
		case "..":
			if absolute {
				if len(stack) == 0 {
					return "", badRequest("path traversal escapes document root")
				}
				stack = stack[:len(stack)-1]
			} else {
				stack = append(stack, "..")
			}
		default:
			stack = append(stack, seg)
		}
	}

	path := "/" + strings.Join(stack, "/")
	if trailing && path != "/" {
		path += "/"
	}
	return path, nil
}
