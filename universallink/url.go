package universallink

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

var (
	// ErrEncodedSlash is returned when a URL path contains an encoded slash
	// (%2F or %2f), which must never be decoded into a path separator.
	ErrEncodedSlash = errors.New("universallink: encoded slash (%2F) in URL path is rejected")
	// ErrDuplicateSlash is returned when a URL path contains two adjacent
	// slashes, including a leading double slash after the host.
	ErrDuplicateSlash = errors.New("universallink: duplicate slashes in URL path are rejected")
)

// preparedURL holds the decoded URL parts used by component matching.
type preparedURL struct {
	path     string
	rawQuery string
	fragment string
}

// prepareURL parses rawURL and applies the security normalization required
// before component matching: encoded slashes and duplicate slashes are
// rejected, then dot segments are removed from the path per RFC 3986.
func prepareURL(rawURL string) (*preparedURL, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("universallink: malformed URL %q: %w", rawURL, err)
	}
	if parsed.Opaque != "" {
		return nil, fmt.Errorf("universallink: URL %q has no hierarchical path", rawURL)
	}

	escapedPath := parsed.EscapedPath()
	if strings.Contains(strings.ToLower(escapedPath), "%2f") {
		return nil, ErrEncodedSlash
	}

	path := parsed.Path
	if strings.Contains(path, "//") {
		return nil, ErrDuplicateSlash
	}

	path = removeDotSegments(path)
	if path == "" {
		path = "/"
	}

	return &preparedURL{
		path:     path,
		rawQuery: parsed.RawQuery,
		fragment: parsed.Fragment,
	}, nil
}

// removeDotSegments implements the remove_dotSegments algorithm from
// RFC 3986 section 5.2.4.
func removeDotSegments(path string) string {
	input := path
	var out string

	for input != "" {
		switch {
		case strings.HasPrefix(input, "../"):
			input = input[3:]
		case strings.HasPrefix(input, "./"):
			input = input[2:]
		case strings.HasPrefix(input, "/./"):
			input = "/" + input[3:]
		case input == "/.":
			input = "/"
		case strings.HasPrefix(input, "/../"):
			input = "/" + input[4:]
			out = dropLastSegment(out)
		case input == "/..":
			input = "/"
			out = dropLastSegment(out)
		case input == "." || input == "..":
			input = ""
		default:
			next := strings.IndexByte(input[1:], '/')
			if next < 0 {
				out += input
				input = ""
			} else {
				out += input[:next+1]
				input = input[next+1:]
			}
		}
	}

	return out
}

// dropLastSegment removes the final path segment (and its content, keeping the
// preceding slash boundary) from an output buffer built by removeDotSegments.
func dropLastSegment(out string) string {
	if i := strings.LastIndexByte(out, '/'); i >= 0 {
		return out[:i]
	}
	return ""
}

// parseQuery splits a query string on '&' and decodes each key/value pair.
// Unlike url.ParseQuery it never treats ';' as a separator. Repeated keys are
// preserved.
func parseQuery(raw string) (url.Values, error) {
	values := url.Values{}
	if raw == "" {
		return values, nil
	}

	for _, pair := range strings.Split(raw, "&") {
		key, rawValue, hasValue := strings.Cut(pair, "=")
		decodedKey, err := url.QueryUnescape(key)
		if err != nil {
			return nil, fmt.Errorf("universallink: invalid query key %q: %w", key, err)
		}
		if !hasValue {
			values.Add(decodedKey, "")
			continue
		}
		decodedValue, err := url.QueryUnescape(rawValue)
		if err != nil {
			return nil, fmt.Errorf("universallink: invalid query value %q: %w", rawValue, err)
		}
		values.Add(decodedKey, decodedValue)
	}

	return values, nil
}
