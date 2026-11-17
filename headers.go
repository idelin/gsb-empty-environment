package netsec

import (
	"fmt"
	"net/http"
	"strings"
)

// HeaderField is one field from a PUSH_PROMISE header block. Pseudo-headers
// retain their leading colon and the original field order is significant.
type HeaderField struct {
	Name  string
	Value string
}

// PromisedRequest is the validated request target and header set extracted from
// a PUSH_PROMISE. Headers never contains pseudo-headers or credential-bearing
// headers; authentication is always taken from the bound parent context.
type PromisedRequest struct {
	Method  string
	Source  Source
	Path    string
	Headers http.Header
}

func parsePromisedRequest(fields []HeaderField) (PromisedRequest, error) {
	var (
		method     string
		scheme     string
		authority  string
		path       string
		seen       = make(map[string]bool, 4)
		sawRegular bool
		regular    = make(http.Header)
	)

	for _, field := range fields {
		if field.Name == "" {
			return PromisedRequest{}, malformed("empty header name")
		}
		if strings.ToLower(field.Name) != field.Name {
			return PromisedRequest{}, malformed("header name %q is not lower-case", field.Name)
		}
		if !validHeaderValue(field.Value) {
			return PromisedRequest{}, malformed("header %q contains an invalid value", field.Name)
		}

		if strings.HasPrefix(field.Name, ":") {
			if sawRegular {
				return PromisedRequest{}, malformed("pseudo-header %q appears after a regular header", field.Name)
			}
			if seen[field.Name] {
				return PromisedRequest{}, malformed("duplicate pseudo-header %q", field.Name)
			}
			seen[field.Name] = true
			if strings.TrimSpace(field.Value) != field.Value {
				return PromisedRequest{}, malformed("pseudo-header %q contains outer whitespace", field.Name)
			}
			switch field.Name {
			case ":method":
				method = field.Value
			case ":scheme":
				scheme = field.Value
			case ":authority":
				authority = field.Value
			case ":path":
				path = field.Value
			default:
				return PromisedRequest{}, malformed("unsupported pseudo-header %q", field.Name)
			}
			continue
		}

		sawRegular = true
		if !validHeaderName(field.Name) {
			return PromisedRequest{}, malformed("invalid header name %q", field.Name)
		}
		switch field.Name {
		case "connection", "proxy-connection", "keep-alive", "transfer-encoding", "upgrade":
			return PromisedRequest{}, malformed("connection-specific header %q is forbidden", field.Name)
		case "host":
			return PromisedRequest{}, malformed("host header is ambiguous with :authority")
		case "te":
			if !strings.EqualFold(strings.TrimSpace(field.Value), "trailers") {
				return PromisedRequest{}, malformed("te header may only contain trailers")
			}
		case "authorization", "proxy-authorization", "cookie", "cookie2":
			// Never let a promise carry a substitute authentication context.
			// The parent-bound source remains the only authority for the push.
			continue
		}
		regular.Add(field.Name, field.Value)
	}

	if method == "" || scheme == "" || authority == "" || path == "" {
		return PromisedRequest{}, malformed("missing required pseudo-header")
	}
	if method != http.MethodGet && method != http.MethodHead {
		return PromisedRequest{}, malformed("method %q is not safe and cacheable for push", method)
	}
	if scheme != "http" && scheme != "https" {
		return PromisedRequest{}, malformed("unsupported :scheme %q", scheme)
	}
	source, err := NewSource(scheme, authority)
	if err != nil {
		return PromisedRequest{}, malformed("invalid :authority: %v", err)
	}
	if path[0] != '/' {
		return PromisedRequest{}, malformed(":path must be an origin-form path")
	}
	if strings.ContainsAny(path, "#\\") {
		return PromisedRequest{}, malformed(":path contains a fragment or backslash")
	}
	for _, r := range path {
		if r <= 0x20 || r == 0x7f {
			return PromisedRequest{}, malformed(":path contains whitespace or control characters")
		}
	}

	return PromisedRequest{
		Method:  method,
		Source:  source,
		Path:    path,
		Headers: regular,
	}, nil
}

func malformed(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrMalformedPromise, fmt.Sprintf(format, args...))
}

func validHeaderName(name string) bool {
	if name == "" {
		return false
	}
	for i := 0; i < len(name); i++ {
		if !isTokenChar(name[i]) {
			return false
		}
	}
	return true
}

func isTokenChar(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z':
		return true
	case c >= '0' && c <= '9':
		return true
	}
	switch c {
	case '!', '#', '$', '%', '&', '\'', '*', '+', '-', '.', '^', '_', '`', '|', '~':
		return true
	}
	return false
}

func validHeaderValue(value string) bool {
	for i := 0; i < len(value); i++ {
		c := value[i]
		if c == 0 || c == '\r' || c == '\n' || c == 0x7f {
			return false
		}
		if c < 0x20 && c != '\t' {
			return false
		}
	}
	return true
}
