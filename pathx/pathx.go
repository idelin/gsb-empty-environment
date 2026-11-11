// Package pathx canonicalizes request paths exactly once so that route
// matching and upstream forwarding in a reverse proxy always agree on the
// same segments, even when the raw path contains encoded separators.
package pathx

import (
	"errors"
	"net/url"
	"strings"
)

var (
	// ErrInvalidEscape is returned when a '%' is not followed by two hex digits.
	ErrInvalidEscape = errors.New("pathx: invalid percent escape")
	// ErrDoubleEncodedControl is returned when a control character or path
	// delimiter would only appear after a second round of percent decoding.
	ErrDoubleEncodedControl = errors.New("pathx: control character hidden behind double encoding")
)

// isPathControl reports whether b alters path structure or is a C0/DEL
// control character. These bytes must never materialize from a second
// decoding pass.
func isPathControl(b byte) bool {
	switch b {
	case '/', '\\', '?', '#':
		return true
	}
	return b < 0x20 || b == 0x7f
}

func isHex(c byte) bool {
	return '0' <= c && c <= '9' || 'a' <= c && c <= 'f' || 'A' <= c && c <= 'F'
}

func unhex(c byte) byte {
	switch {
	case '0' <= c && c <= '9':
		return c - '0'
	case 'a' <= c && c <= 'f':
		return c - 'a' + 10
	default:
		return c - 'A' + 10
	}
}

// DecodeOnce percent-decodes s exactly once. It rejects malformed escapes and
// any residual escape that would reveal a path control character under a
// second decode, which is how smuggled separators (e.g. %252F) are caught.
func DecodeOnce(s string) (string, error) {
	if !strings.Contains(s, "%") {
		return s, nil
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '%' {
			b.WriteByte(c)
			continue
		}
		if i+2 >= len(s) || !isHex(s[i+1]) || !isHex(s[i+2]) {
			return "", ErrInvalidEscape
		}
		b.WriteByte(unhex(s[i+1])<<4 | unhex(s[i+2]))
		i += 2
	}
	out := b.String()
	for i := 0; i+2 < len(out); i++ {
		if out[i] == '%' && isHex(out[i+1]) && isHex(out[i+2]) &&
			isPathControl(unhex(out[i+1])<<4|unhex(out[i+2])) {
			return "", ErrDoubleEncodedControl
		}
	}
	return out, nil
}

// escapeSegment re-encodes one decoded segment for the upstream request.
// url.PathEscape uses path-segment mode, so structural characters such as
// '/', '?', ';' and ',' can never leak onto the wire unescaped.
func escapeSegment(seg string) string {
	return url.PathEscape(seg)
}

// Normalize canonicalizes a raw, still percent-encoded request path. It
// splits on literal '/', decodes each segment exactly once, resolves '.'
// and '..' (clamped at the root), and returns both the decoded canonical
// path for route matching and its escaped form for forwarding. A trailing
// slash is preserved.
func Normalize(rawPath string) (decoded, escaped string, err error) {
	if rawPath == "" || rawPath[0] != '/' {
		return "", "", errors.New("pathx: path must be absolute")
	}
	trailing := strings.HasSuffix(rawPath, "/")
	rawSegs := strings.Split(rawPath, "/")
	segs := make([]string, 0, len(rawSegs))
	for _, raw := range rawSegs {
		seg, derr := DecodeOnce(raw)
		if derr != nil {
			return "", "", derr
		}
		switch seg {
		case "", ".":
			continue
		case "..":
			if len(segs) > 0 {
				segs = segs[:len(segs)-1]
			}
			continue
		}
		segs = append(segs, seg)
	}
	decoded = "/" + strings.Join(segs, "/")
	escSegs := make([]string, len(segs))
	for i, seg := range segs {
		escSegs[i] = escapeSegment(seg)
	}
	escaped = "/" + strings.Join(escSegs, "/")
	if trailing && len(segs) > 0 {
		decoded += "/"
		escaped += "/"
	}
	return decoded, escaped, nil
}
