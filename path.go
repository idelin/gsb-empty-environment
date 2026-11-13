package netsec

import (
	"errors"
	"fmt"
	"strings"
)

var (
	// ErrInvalidPercentEncoding reports a malformed percent escape in the
	// request path. Rejecting it avoids treating an ambiguous spelling as a
	// different path from the one a provider will actually open.
	ErrInvalidPercentEncoding = errors.New("netsec: invalid percent encoding")

	// ErrEscapesRoot reports a path whose dot segments would leave the
	// provider's root. The path is rejected rather than clamped to the root.
	ErrEscapesRoot = errors.New("netsec: path escapes provider root")
)

// DecodePercentToFixedPoint repeatedly applies percent decoding until the
// result no longer changes. Encoded slashes become real separators and encoded
// dots become real dot segments before normalization.
//
// A literal '%' that remains after decoding is treated as data. Because every
// '%' in the original input must introduce a valid escape, malformed input is
// rejected before decoding starts.
func DecodePercentToFixedPoint(raw string) (string, error) {
	if err := validatePercentEscapes(raw); err != nil {
		return "", err
	}

	decoded := raw
	// Every replacement of "%XY" by one byte shortens the string by at least
	// two bytes, so no input can require more passes than it has bytes.
	for pass := 0; pass <= len(raw); pass++ {
		next, changed := decodePercentOnce(decoded)
		if !changed {
			return next, nil
		}
		decoded = next
	}
	return "", fmt.Errorf("%w: decoding did not reach a fixed point", ErrInvalidPercentEncoding)
}

// Canonicalize decodes percent escapes to a fixed point and then normalizes
// slash and dot segments. It returns ErrEscapesRoot if ".." would climb above
// the URI root.
func Canonicalize(raw string) (string, error) {
	decoded, err := DecodePercentToFixedPoint(raw)
	if err != nil {
		return "", err
	}

	normalized, escaped := normalizeDotSegments(decoded)
	if escaped {
		return "", fmt.Errorf("%w: %q", ErrEscapesRoot, raw)
	}
	return normalized, nil
}

// CanonicalizeWithin is Canonicalize with an additional provider-root
// containment check. A path is inside root only when it equals root or
// continues at a path-segment boundary, so "/public" does not contain
// "/publicity".
func CanonicalizeWithin(root, raw string) (string, error) {
	canonicalRoot, err := Canonicalize(root)
	if err != nil {
		return "", fmt.Errorf("netsec: invalid provider root: %w", err)
	}

	canonicalPath, err := Canonicalize(raw)
	if err != nil {
		return "", err
	}
	if !pathWithinRoot(canonicalRoot, canonicalPath) {
		return "", fmt.Errorf("%w: %q is outside %q", ErrEscapesRoot, canonicalPath, canonicalRoot)
	}
	return canonicalPath, nil
}

// NormalizeDotSegments normalizes slash and dot segments in an already
// decoded path. The returned boolean reports whether ".." attempted to climb
// above the URI root.
func NormalizeDotSegments(path string) (string, bool) {
	return normalizeDotSegments(path)
}

// PathWithinRoot reports whether path is root itself or is separated from root
// by a slash boundary. Both arguments must already be canonical.
func PathWithinRoot(root, path string) bool {
	return pathWithinRoot(root, path)
}

func validatePercentEscapes(s string) error {
	for i := 0; i < len(s); i++ {
		if s[i] != '%' {
			continue
		}
		if i+2 >= len(s) || !isHex(s[i+1]) || !isHex(s[i+2]) {
			return fmt.Errorf("%w: at byte %d", ErrInvalidPercentEncoding, i)
		}
		i += 2
	}
	return nil
}

func decodePercentOnce(s string) (string, bool) {
	var b strings.Builder
	b.Grow(len(s))

	changed := false
	for i := 0; i < len(s); i++ {
		if s[i] != '%' || i+2 >= len(s) || !isHex(s[i+1]) || !isHex(s[i+2]) {
			b.WriteByte(s[i])
			continue
		}
		b.WriteByte(hexValue(s[i+1])<<4 | hexValue(s[i+2]))
		i += 2
		changed = true
	}
	return b.String(), changed
}

func normalizeDotSegments(path string) (string, bool) {
	if path == "" {
		return "/", false
	}

	escaped := false
	segments := make([]string, 0, strings.Count(path, "/")+1)
	for _, segment := range strings.Split(path, "/") {
		switch segment {
		case "", ".":
			continue
		case "..":
			if len(segments) == 0 {
				escaped = true
				continue
			}
			segments = segments[:len(segments)-1]
		default:
			segments = append(segments, segment)
		}
	}

	if len(segments) == 0 {
		return "/", escaped
	}
	return "/" + strings.Join(segments, "/"), escaped
}

func pathWithinRoot(root, path string) bool {
	if root == "/" {
		return true
	}
	return path == root || strings.HasPrefix(path, root+"/")
}

func isHex(c byte) bool {
	return '0' <= c && c <= '9' || 'a' <= c && c <= 'f' || 'A' <= c && c <= 'F'
}

func hexValue(c byte) byte {
	switch {
	case '0' <= c && c <= '9':
		return c - '0'
	case 'a' <= c && c <= 'f':
		return c - 'a' + 10
	default:
		return c - 'A' + 10
	}
}
