package h2guard

import (
	"strconv"
	"strings"
)

// Pseudo-header names defined by RFC 9113 section 8.3.
const (
	PseudoMethod    = ":method"
	PseudoScheme    = ":scheme"
	PseudoAuthority = ":authority"
	PseudoPath      = ":path"
)

// DecodedHeaders is the validated result of decoding one request header
// block. Pseudo-headers are split out; Regular keeps the original wire order
// of non-pseudo fields.
type DecodedHeaders struct {
	Method    string
	Scheme    string
	Authority string
	Path      string
	Regular   []HeaderField

	// ContentLength is the parsed Content-Length when exactly one valid value
	// was present; HasContentLength reports its presence.
	HasContentLength bool
	ContentLength    int64
}

// hopByHopHeaders are connection-specific headers forbidden on HTTP/2
// requests/end-to-end headers (RFC 9113 8.2.2 plus RFC 7230 6.1). Matching is
// case-insensitive.
var hopByHopHeaders = map[string]struct{}{
	"connection":        {},
	"proxy-connection":  {},
	"keep-alive":        {},
	"transfer-encoding": {},
	"upgrade":           {},
	"te":                {}, // only "trailers" is permitted, handled below
}

// validateHeaders enforces RFC 9113 section 8 (and the smuggling-relevant
// parts of RFC 7230) on one decoded request header list.
func validateHeaders(fields []HeaderField) (*DecodedHeaders, error) {
	out := &DecodedHeaders{}
	seenPseudo := map[string]struct{}{}
	pseudoDone := false

	for _, field := range fields {
		name := field.Name
		value := field.Value

		if name == "" {
			return nil, connError(ErrCodeProtocol, "empty header name")
		}
		if containsForbiddenByte(value) {
			return nil, connError(ErrCodeProtocol, "header %q value contains CR/LF/NUL", name)
		}

		if strings.HasPrefix(name, ":") {
			// Pseudo-header rules.
			if pseudoDone {
				return nil, connError(ErrCodeProtocol, "pseudo-header %q after regular header", name)
			}
			if !isKnownPseudo(name) {
				return nil, connError(ErrCodeProtocol, "unknown pseudo-header %q", name)
			}
			if _, dup := seenPseudo[name]; dup {
				return nil, connError(ErrCodeProtocol, "duplicate pseudo-header %q", name)
			}
			seenPseudo[name] = struct{}{}
			switch name {
			case PseudoMethod:
				out.Method = value
			case PseudoScheme:
				out.Scheme = value
			case PseudoAuthority:
				out.Authority = value
			case PseudoPath:
				out.Path = value
			}
			continue
		}

		pseudoDone = true

		// Lowercase mandatory; any uppercase byte in a field name is a
		// protocol error in HTTP/2 and also blocks header-smuggling confusion.
		for i := 0; i < len(name); i++ {
			c := name[i]
			if c >= 'A' && c <= 'Z' {
				return nil, connError(ErrCodeProtocol, "uppercase byte in header name %q", name)
			}
			if c <= 0x20 || c == 0x7f || c == 0 {
				return nil, connError(ErrCodeProtocol, "invalid byte in header name %q", name)
			}
		}

		lower := strings.ToLower(name)
		if _, banned := hopByHopHeaders[lower]; banned {
			if lower == "te" && strings.TrimSpace(value) == "trailers" {
				out.Regular = append(out.Regular, field)
				continue
			}
			return nil, connError(ErrCodeProtocol, "forbidden hop-by-hop header %q", name)
		}

		if lower == "content-length" {
			parsed, err := parseContentLength(value)
			if err != nil {
				return nil, connError(ErrCodeProtocol, "invalid content-length %q", value)
			}
			if out.HasContentLength && out.ContentLength != parsed {
				return nil, connError(ErrCodeProtocol, "conflicting content-length values")
			}
			out.HasContentLength = true
			out.ContentLength = parsed
		}

		out.Regular = append(out.Regular, field)
	}

	// Mandatory request pseudo-headers (RFC 9113 8.3.1). OPTIONS * is the
	// only request allowed to carry :path "*" and still must carry it.
	if _, ok := seenPseudo[PseudoMethod]; !ok {
		return nil, connError(ErrCodeProtocol, "missing :method pseudo-header")
	}
	if _, ok := seenPseudo[PseudoScheme]; !ok {
		return nil, connError(ErrCodeProtocol, "missing :scheme pseudo-header")
	}
	if _, ok := seenPseudo[PseudoPath]; !ok {
		if !(out.Method == "OPTIONS" && out.Path == "") {
			return nil, connError(ErrCodeProtocol, "missing :path pseudo-header")
		}
	}
	if out.Path != "" && out.Path[0] != '/' && !(out.Method == "OPTIONS" && out.Path == "*") {
		return nil, connError(ErrCodeProtocol, ":path must be origin-form or '*'")
	}

	return out, nil
}

func isKnownPseudo(name string) bool {
	switch name {
	case PseudoMethod, PseudoScheme, PseudoAuthority, PseudoPath:
		return true
	}
	return false
}

func containsForbiddenByte(s string) bool {
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case 0, '\r', '\n':
			return true
		}
	}
	return false
}

func parseContentLength(value string) (int64, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return 0, strconv.ErrSyntax
	}
	n, err := strconv.ParseInt(trimmed, 10, 64)
	if err != nil {
		return 0, err
	}
	if n < 0 {
		return 0, strconv.ErrSyntax
	}
	return n, nil
}
