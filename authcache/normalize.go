package authcache

import (
	"errors"
	"net/url"
	"strings"
)

// ErrEncodedSlash is returned when a request path contains an encoded slash
// (%2f or %2F). Encoded slashes are rejected before decoding so they cannot be
// used to make the gateway and an upstream route the same request differently.
var ErrEncodedSlash = errors.New("authcache: encoded slash (%2f) in request path")

// NormalizePath decodes percent-escapes in rawPath and removes dot segments
// ("." and "..", including encoded variants such as "%2e" or "%2E%2E") using
// the algorithm defined by RFC 3986 section 5.2.4.
//
// An encoded slash anywhere in the raw path is rejected with ErrEncodedSlash.
// NormalizePath operates on the path component only; the query string is bound
// to the cache key separately and left untouched.
func NormalizePath(rawPath string) (string, error) {
	for i := 0; i+2 < len(rawPath); i++ {
		if rawPath[i] == '%' && rawPath[i+1] == '2' &&
			(rawPath[i+2] == 'f' || rawPath[i+2] == 'F') {
			return "", ErrEncodedSlash
		}
	}

	decoded, err := url.PathUnescape(rawPath)
	if err != nil {
		return "", err
	}
	return removeDotSegments(decoded), nil
}

// removeDotSegments implements RFC 3986 section 5.2.4.
func removeDotSegments(path string) string {
	input := path
	var out strings.Builder

	for len(input) > 0 {
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
			trimLastSegment(&out)
		case input == "/..":
			input = "/"
			trimLastSegment(&out)
		case input == "." || input == "..":
			input = ""
		default:
			segment := takeSegment(input)
			out.WriteString(segment)
			input = input[len(segment):]
		}
	}

	return out.String()
}

func takeSegment(input string) string {
	if i := strings.IndexByte(input[1:], '/'); i >= 0 {
		return input[:i+1]
	}
	return input
}

func trimLastSegment(out *strings.Builder) {
	current := out.String()
	i := strings.LastIndexByte(current, '/')
	out.Reset()
	if i > 0 {
		out.WriteString(current[:i])
	}
}
