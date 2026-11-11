package netsec

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// MethodResult is a single method=result clause of an Authentication-Results
// header, with its optional reason and ptype.property=pvalue properties.
type MethodResult struct {
	Method string
	Result string
	Reason string
	Props  map[string]string
}

// AuthResult is a parsed Authentication-Results header (RFC 8601).
type AuthResult struct {
	AuthservID string
	Version    int
	None       bool
	Methods    []MethodResult
	Raw        string
}

// ParseAuthResults parses one Authentication-Results header value.
func ParseAuthResults(raw string) (*AuthResult, error) {
	segs, err := splitSegments(raw)
	if err != nil {
		return nil, err
	}
	ar := &AuthResult{Raw: raw, Version: 1}
	head := strings.Fields(segs[0])
	if len(head) == 0 {
		return nil, errors.New("netsec: missing authserv-id")
	}
	if len(head) > 2 {
		return nil, fmt.Errorf("netsec: unexpected tokens after authserv-id: %q", segs[0])
	}
	ar.AuthservID = head[0]
	if len(head) == 2 {
		v, err := strconv.Atoi(head[1])
		if err != nil || v < 1 {
			return nil, fmt.Errorf("netsec: bad authserv-id version %q", head[1])
		}
		ar.Version = v
	}
	rest := segs[1:]
	if len(rest) == 1 && strings.EqualFold(strings.TrimSpace(rest[0]), "none") {
		ar.None = true
		return ar, nil
	}
	for _, seg := range rest {
		if strings.TrimSpace(seg) == "" {
			continue
		}
		mr, err := parseMethodResult(seg)
		if err != nil {
			return nil, err
		}
		ar.Methods = append(ar.Methods, mr)
	}
	if len(ar.Methods) == 0 {
		ar.None = true
	}
	return ar, nil
}

func parseMethodResult(seg string) (MethodResult, error) {
	fields, err := splitFields(seg)
	if err != nil {
		return MethodResult{}, err
	}
	if len(fields) == 0 {
		return MethodResult{}, errors.New("netsec: empty method clause")
	}
	key, val, ok := strings.Cut(fields[0], "=")
	if !ok || key == "" || val == "" {
		return MethodResult{}, fmt.Errorf("netsec: bad method clause %q", fields[0])
	}
	if strings.Contains(key, ".") || strings.EqualFold(key, "reason") {
		return MethodResult{}, fmt.Errorf("netsec: clause must start with method=result, got %q", fields[0])
	}
	mr := MethodResult{Result: strings.ToLower(unquote(val)), Props: map[string]string{}}
	mr.Method = strings.ToLower(key)
	if slash := strings.IndexByte(key, '/'); slash >= 0 {
		mr.Method = strings.ToLower(key[:slash])
	}
	for _, f := range fields[1:] {
		k, v, ok := strings.Cut(f, "=")
		if !ok || k == "" {
			return MethodResult{}, fmt.Errorf("netsec: bad property %q", f)
		}
		v = unquote(v)
		if strings.EqualFold(k, "reason") {
			mr.Reason = v
			continue
		}
		if !strings.Contains(k, ".") {
			return MethodResult{}, fmt.Errorf("netsec: bad property key %q", k)
		}
		mr.Props[strings.ToLower(k)] = v
	}
	return mr, nil
}

// splitSegments splits a header value on ";" that appear outside of quoted
// strings and comments.
func splitSegments(s string) ([]string, error) {
	var segs []string
	depth := 0
	inQuote := false
	esc := false
	start := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case esc:
			esc = false
		case inQuote && c == '\\':
			esc = true
		case c == '"':
			inQuote = !inQuote
		case !inQuote && c == '(':
			depth++
		case !inQuote && c == ')':
			depth--
			if depth < 0 {
				return nil, errors.New("netsec: unbalanced comment")
			}
		case c == ';' && !inQuote && depth == 0:
			segs = append(segs, s[start:i])
			start = i + 1
		}
	}
	if inQuote || depth != 0 {
		return nil, errors.New("netsec: unbalanced quote or comment")
	}
	return append(segs, s[start:]), nil
}

// splitFields splits a clause on whitespace, keeping quoted strings intact
// and stripping comments.
func splitFields(s string) ([]string, error) {
	var out []string
	var b strings.Builder
	depth := 0
	inQuote := false
	esc := false
	flush := func() {
		if b.Len() > 0 {
			out = append(out, b.String())
			b.Reset()
		}
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case esc:
			b.WriteByte(c)
			esc = false
		case inQuote && c == '\\':
			b.WriteByte(c)
			esc = true
		case c == '"':
			inQuote = !inQuote
			b.WriteByte(c)
		case !inQuote && c == '(':
			depth++
		case !inQuote && c == ')':
			depth--
			if depth < 0 {
				return nil, errors.New("netsec: unbalanced comment")
			}
		case depth > 0:
		case !inQuote && (c == ' ' || c == '\t' || c == '\r' || c == '\n'):
			flush()
		default:
			b.WriteByte(c)
		}
	}
	if inQuote || depth != 0 {
		return nil, errors.New("netsec: unbalanced quote or comment")
	}
	flush()
	return out, nil
}

func unquote(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		s = s[1 : len(s)-1]
		s = strings.ReplaceAll(s, `\"`, `"`)
		s = strings.ReplaceAll(s, `\\`, `\`)
	}
	return s
}
