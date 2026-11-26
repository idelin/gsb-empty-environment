package h2h1

import (
	"fmt"
	"strconv"
	"strings"
)

// RequestMeta is the semantic result of validating a request header block.
type RequestMeta struct {
	Method           string
	Scheme           string
	Path             string
	Authority        string
	Protocol         string
	Headers          []HeaderField
	ContentLength    int64
	HasContentLength bool
}

func reject(kind, format string, args ...any) error {
	return fmt.Errorf("h2h1: %s: %s", kind, fmt.Sprintf(format, args...))
}

func isHeaderTokenRune(c byte) bool {
	if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' {
		return true
	}
	switch c {
	case '!', '#', '$', '%', '&', '\'', '*', '+', '-', '.', '^', '_', '`', '|', '~':
		return true
	default:
		return false
	}
}

func validHeaderName(name string) bool {
	if name == "" {
		return false
	}
	start := 0
	if name[0] == ':' {
		start = 1
		if start == len(name) {
			return false
		}
	}
	for i := start; i < len(name); i++ {
		if !isHeaderTokenRune(name[i]) {
			return false
		}
	}
	return true
}

func containsForbiddenHeaderByte(s string) bool {
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\r', '\n', 0:
			return true
		}
	}
	return false
}

func validHeaderValue(value string) error {
	if containsForbiddenHeaderByte(value) {
		return reject("header-value", "value contains CR, LF, or NUL")
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		if c == '\t' {
			continue
		}
		if c < 0x20 || c == 0x7f {
			return reject("header-value", "value contains control byte 0x%02x", c)
		}
	}
	return nil
}

func validatePseudoValue(name, value string) error {
	if containsForbiddenHeaderByte(value) {
		return reject("pseudo-header", "%s contains CR, LF, or NUL", name)
	}
	switch name {
	case ":method", ":scheme", ":protocol":
		if value == "" {
			return reject("pseudo-header", "%s is empty", name)
		}
		for i := 0; i < len(value); i++ {
			if !isHeaderTokenRune(value[i]) {
				return reject("pseudo-header", "%s contains a non-token byte", name)
			}
		}
	case ":path":
		if value == "" {
			return reject("pseudo-header", ":path is empty")
		}
		for i := 0; i < len(value); i++ {
			c := value[i]
			if c <= 0x20 || c == 0x7f {
				return reject("pseudo-header", ":path contains whitespace or a control byte")
			}
		}
	case ":authority":
		if value == "" {
			return reject("pseudo-header", ":authority is empty")
		}
		for i := 0; i < len(value); i++ {
			c := value[i]
			if c <= 0x20 || c == 0x7f {
				return reject("pseudo-header", ":authority contains whitespace or a control byte")
			}
		}
	}
	return nil
}

func canonicalContentLength(value string) (int64, error) {
	value = strings.Trim(value, " \t")
	if value == "" {
		return 0, reject("content-length", "empty value")
	}
	for i := 0; i < len(value); i++ {
		if value[i] < '0' || value[i] > '9' {
			return 0, reject("content-length", "non-decimal value %q", value)
		}
	}
	if len(value) > 1 && value[0] == '0' {
		return 0, reject("content-length", "leading zero in %q", value)
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, reject("content-length", "value %q is out of range", value)
	}
	return n, nil
}

func isConnectionSpecificHeader(name string) bool {
	switch name {
	case "connection", "keep-alive", "proxy-connection", "upgrade", "transfer-encoding":
		return true
	default:
		return false
	}
}

// ValidateRequestHeaders applies strict HTTP/2 request-header semantics.
func ValidateRequestHeaders(fields []HeaderField) (*RequestMeta, error) {
	meta := &RequestMeta{Headers: make([]HeaderField, 0, len(fields))}
	seenPseudo := make(map[string]bool)
	sawRegular := false
	var contentLength int64
	hasContentLength := false
	hasHost := false

	for _, field := range fields {
		if containsForbiddenHeaderByte(field.Name) {
			return nil, reject("header-name", "name contains CR, LF, or NUL")
		}
		if !validHeaderName(field.Name) {
			return nil, reject("header-name", "invalid name %q", field.Name)
		}
		if field.Name[0] != ':' {
			for i := 0; i < len(field.Name); i++ {
				if field.Name[i] >= 'A' && field.Name[i] <= 'Z' {
					return nil, reject("header-name", "uppercase name %q", field.Name)
				}
			}
		}
		if err := validHeaderValue(field.Value); err != nil {
			return nil, err
		}
		meta.Headers = append(meta.Headers, field)

		if field.Name[0] == ':' {
			if sawRegular {
				return nil, reject("pseudo-order", "%s appears after a regular header", field.Name)
			}
			switch field.Name {
			case ":method", ":scheme", ":path", ":authority", ":protocol":
			default:
				return nil, reject("pseudo-header", "unknown or response pseudo-header %s", field.Name)
			}
			if seenPseudo[field.Name] {
				return nil, reject("pseudo-header", "duplicate %s", field.Name)
			}
			seenPseudo[field.Name] = true
			if err := validatePseudoValue(field.Name, field.Value); err != nil {
				return nil, err
			}
			switch field.Name {
			case ":method":
				meta.Method = field.Value
			case ":scheme":
				meta.Scheme = field.Value
			case ":path":
				meta.Path = field.Value
			case ":authority":
				meta.Authority = field.Value
			case ":protocol":
				meta.Protocol = field.Value
			}
			continue
		}

		sawRegular = true
		if isConnectionSpecificHeader(field.Name) {
			return nil, reject("connection-header", "forbidden connection-specific header %q", field.Name)
		}
		switch field.Name {
		case "host":
			if hasHost {
				return nil, reject("host", "duplicate Host header")
			}
			if err := safeHost(field.Value); err != nil {
				return nil, err
			}
			if meta.Authority != "" && !strings.EqualFold(meta.Authority, field.Value) {
				return nil, reject("host", "conflicting :authority and Host")
			}
			hasHost = true
		case "content-length":
			n, err := canonicalContentLength(field.Value)
			if err != nil {
				return nil, err
			}
			if hasContentLength && n != contentLength {
				return nil, reject("content-length", "conflicting values %d and %d", contentLength, n)
			}
			contentLength = n
			hasContentLength = true
		case "te":
			value := strings.Trim(field.Value, " \t")
			if !strings.EqualFold(value, "trailers") {
				return nil, reject("te", "only TE: trailers is permitted, got %q", field.Value)
			}
		}
	}
	if !seenPseudo[":method"] {
		return nil, reject("pseudo-header", "missing :method")
	}
	if !seenPseudo[":scheme"] {
		return nil, reject("pseudo-header", "missing :scheme")
	}
	if !seenPseudo[":path"] {
		return nil, reject("pseudo-header", "missing :path")
	}
	meta.ContentLength = contentLength
	meta.HasContentLength = hasContentLength
	return meta, nil
}

// ValidateHeaders is an alias for ValidateRequestHeaders.
func ValidateHeaders(fields []HeaderField) (*RequestMeta, error) {
	return ValidateRequestHeaders(fields)
}

// ValidateTrailers validates trailing request header fields. Pseudo headers,
// connection-specific fields, TE, and Content-Length are prohibited.
func ValidateTrailers(fields []HeaderField) error {
	for _, field := range fields {
		if field.Name == "" {
			return reject("trailer", "empty header name")
		}
		if field.Name[0] == ':' {
			return reject("trailer", "pseudo-header %s is not permitted in trailers", field.Name)
		}
		for i := 0; i < len(field.Name); i++ {
			if field.Name[i] >= 'A' && field.Name[i] <= 'Z' {
				return reject("header-name", "uppercase name %q", field.Name)
			}
		}
		if containsForbiddenHeaderByte(field.Name) || !validHeaderName(field.Name) {
			return reject("trailer", "invalid header name %q", field.Name)
		}
		if err := validHeaderValue(field.Value); err != nil {
			return err
		}
		if isConnectionSpecificHeader(field.Name) {
			return reject("trailer", "connection-specific header %q is not permitted in trailers", field.Name)
		}
		switch field.Name {
		case "content-length", "te":
			return reject("trailer", "%s is not permitted in trailers", field.Name)
		}
	}
	return nil
}

// ValidateContentLength checks the accumulated DATA body against the
// Content-Length value in the pseudo/regular header block.
func ValidateContentLength(contentLength int64, hasContentLength bool, body []byte) error {
	if !hasContentLength {
		return nil
	}
	if int64(len(body)) != contentLength {
		return reject("content-length", "declared %d bytes but received %d DATA bytes", contentLength, len(body))
	}
	return nil
}
