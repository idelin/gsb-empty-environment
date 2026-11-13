package netsec

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
)

// ErrUnsafeValue is returned when a credential field contains a byte that
// would break the line-oriented credential-helper protocol (LF, CR or NUL),
// whether as a raw byte or hidden behind percent-encoding (for example a
// "%0a" embedded in a URL username or path).
var ErrUnsafeValue = errors.New("netsec: credential field contains LF, CR or NUL")

// Request is the request message sent to a git credential helper
// ("git credential fill"/"approve"/"reject" input).
//
// All fields are treated as single protocol values: they never contain
// newlines, carriage returns or NUL bytes. Host is the exact host line that
// is put on the wire and therefore the only value helpers may be trusted to
// answer for.
type Request struct {
	Protocol string
	Host     string
	Path     string
	Username string
}

// Credential is a parsed helper response. Only values that the caller asked
// for in the Request are trusted; see ParseResponse for the exact rules.
type Credential struct {
	Protocol string
	Host     string
	Username string
	Password string
}

// hasProtocolControlByte reports whether s contains a byte that can alter the
// line structure of the credential-helper protocol or terminate a C string:
// line feed, carriage return or NUL.
func hasProtocolControlByte(s string) bool {
	return strings.ContainsAny(s, "\n\r\x00")
}

// Validate rejects a request whose wire fields carry LF, CR or NUL bytes.
// Empty protocol/host are allowed: a caller may construct a partial request,
// and ParseResponse uses emptiness to tell which fields were actually asked
// for. NewRequest still requires both to be present, since a URL-derived
// request is always pinned to a concrete protocol and host.
func (r Request) Validate() error {
	if hasProtocolControlByte(r.Protocol) {
		return fmt.Errorf("%w in protocol %q", ErrUnsafeValue, r.Protocol)
	}
	if hasProtocolControlByte(r.Host) {
		return fmt.Errorf("%w in host %q", ErrUnsafeValue, r.Host)
	}
	if hasProtocolControlByte(r.Path) {
		return fmt.Errorf("%w in path %q", ErrUnsafeValue, r.Path)
	}
	if hasProtocolControlByte(r.Username) {
		return fmt.Errorf("%w in username %q", ErrUnsafeValue, r.Username)
	}
	return nil
}

// Encode serializes the request in the credential-helper wire format: one
// key=value pair per line terminated by a blank line. It fails rather than
// emitting a request whose fields could inject extra lines.
func (r Request) Encode() ([]byte, error) {
	var b strings.Builder
	if _, err := r.WriteTo(&b); err != nil {
		return nil, err
	}
	return []byte(b.String()), nil
}

// WriteTo writes the serialized request to w. It returns the number of bytes
// written alongside any error, satisfying io.WriterTo.
func (r Request) WriteTo(w io.Writer) (int64, error) {
	if err := r.Validate(); err != nil {
		return 0, err
	}
	var b strings.Builder
	b.WriteString("protocol=")
	b.WriteString(r.Protocol)
	b.WriteByte('\n')
	b.WriteString("host=")
	b.WriteString(r.Host)
	b.WriteByte('\n')
	if r.Path != "" {
		b.WriteString("path=")
		b.WriteString(r.Path)
		b.WriteByte('\n')
	}
	if r.Username != "" {
		b.WriteString("username=")
		b.WriteString(r.Username)
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
	n, err := io.WriteString(w, b.String())
	return int64(n), err
}

// NewRequest builds a credential Request from a repository URL.
//
// This is the safe replacement for hand-rolled URL splitting. The URL is
// parsed with net/url, so percent-encoded fields are decoded before they are
// examined; if protocol, host, path or username contains an LF, CR or NUL
// byte after decoding (an injected "%0a", "%0d" or "%00"), the whole URL is
// rejected with ErrUnsafeValue and no request is produced.
//
// The request's Host is pinned to the URL's real authority (including a port
// or IPv6 brackets, if present), so any later check against Host compares
// against the host the caller actually intended to contact.
func NewRequest(rawURL string) (Request, error) {
	// Defense in depth: never let a raw control byte reach the parser or
	// the wire, even if a future url.Parse change becomes more permissive.
	if hasProtocolControlByte(rawURL) {
		return Request{}, fmt.Errorf("%w in URL %q", ErrUnsafeValue, rawURL)
	}
	// Classify encoded separators ("%0a", "%0d", "%00") before parsing:
	// url.Parse rejects some of those as malformed escapes, but the security
	// verdict is the same regardless of where the URL splits them.
	if decoded, derr := url.PathUnescape(rawURL); derr == nil && hasProtocolControlByte(decoded) {
		return Request{}, fmt.Errorf("%w in URL %q", ErrUnsafeValue, rawURL)
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return Request{}, fmt.Errorf("netsec: parse URL: %w", err)
	}

	req := Request{
		Protocol: u.Scheme,
		Host:     u.Host,
	}
	if req.Protocol == "" {
		return Request{}, errors.New("netsec: URL is missing protocol/scheme")
	}
	if req.Host == "" {
		return Request{}, errors.New("netsec: URL is missing host")
	}

	// Percent-escapes in the authority survive url.Parse and are only
	// decoded by Hostname()/Port(); decode up front so an encoded LF, CR or
	// NUL in the host is reported as ErrUnsafeValue instead of reaching the
	// wire or failing opaquely later.
	decodedHost, err := url.PathUnescape(req.Host)
	if err != nil {
		return Request{}, fmt.Errorf("netsec: parse URL host: %w", err)
	}
	// u.Host is the raw authority (brackets/port included). Check it
	// verbatim and also check its percent-decoded pieces: url.Parse keeps
	// percent escapes in Host, and Hostname()/Port() decode them.
	if hasProtocolControlByte(req.Host) ||
		hasProtocolControlByte(decodedHost) ||
		hasProtocolControlByte(u.Hostname()) ||
		hasProtocolControlByte(u.Port()) {
		return Request{}, fmt.Errorf("%w in host %q", ErrUnsafeValue, req.Host)
	}
	if hasProtocolControlByte(req.Protocol) {
		return Request{}, fmt.Errorf("%w in protocol %q", ErrUnsafeValue, req.Protocol)
	}

	// u.Path is already percent-decoded; reject injected line separators
	// rather than carrying them into a "path=" line.
	req.Path = strings.TrimPrefix(u.Path, "/")
	if hasProtocolControlByte(req.Path) {
		return Request{}, fmt.Errorf("%w in path %q", ErrUnsafeValue, req.Path)
	}

	if u.User != nil {
		// Username() returns the percent-decoded username.
		req.Username = u.User.Username()
		if hasProtocolControlByte(req.Username) {
			return Request{}, fmt.Errorf("%w in username %q", ErrUnsafeValue, req.Username)
		}
	}

	return req, nil
}

// ParseResponse parses a credential-helper response in the key=value wire
// format and enforces the CVE-2020-11008 mitigation:
//
//   - A "protocol" or "host" line that the caller did not ask for (the
//     corresponding Request field is empty) is ignored, so a malicious or
//     compromised helper cannot redirect credentials at a different host.
//   - When the caller did ask for protocol/host, the helper's value must
//     match the request exactly; a mismatch is an error and no credential is
//     returned.
//
// Credential.Protocol and Credential.Host are always populated from req, so
// callers can never accidentally trust helper-supplied host data. Unknown
// keys are ignored to stay compatible with future protocol extensions.
func ParseResponse(req Request, r io.Reader) (Credential, error) {
	if err := req.Validate(); err != nil {
		return Credential{}, err
	}

	cred := Credential{
		Protocol: req.Protocol,
		Host:     req.Host,
	}

	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()

		// A bare blank line ends the message, exactly as on the git side.
		if line == "" {
			break
		}
		// CR can smuggle line structure across some consumers; a NUL
		// truncates C-string handling. Reject either anywhere in a line.
		if strings.ContainsAny(line, "\r\x00") {
			return Credential{}, fmt.Errorf("%w in response line %q", ErrUnsafeValue, line)
		}

		eq := strings.IndexByte(line, '=')
		if eq <= 0 {
			return Credential{}, fmt.Errorf("netsec: malformed response line %q", line)
		}
		key, value := line[:eq], line[eq+1:]

		switch key {
		case "protocol":
			// Silently drop an unsolicited protocol override; when the
			// field was requested, it must echo the request exactly.
			if req.Protocol != "" && value != req.Protocol {
				return Credential{}, fmt.Errorf("netsec: response protocol %q does not match requested %q", value, req.Protocol)
			}
		case "host":
			if req.Host != "" && value != req.Host {
				return Credential{}, fmt.Errorf("netsec: response host %q does not match requested %q", value, req.Host)
			}
		case "path":
			// Path is request-side context only; ignore helper-provided values.
			continue
		case "username":
			if hasProtocolControlByte(value) {
				return Credential{}, fmt.Errorf("%w in username %q", ErrUnsafeValue, value)
			}
			cred.Username = value
		case "password":
			if hasProtocolControlByte(value) {
				return Credential{}, fmt.Errorf("%w in password %q", ErrUnsafeValue, value)
			}
			cred.Password = value
		default:
			// Forward compatibility: ignore keys this version does not model.
			continue
		}
	}
	if err := scanner.Err(); err != nil {
		return Credential{}, fmt.Errorf("netsec: read response: %w", err)
	}

	return cred, nil
}
