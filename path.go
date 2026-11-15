package netsec

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

var (
	ErrNilRequest   = errors.New("authcache: nil request")
	ErrInvalidPath  = errors.New("authcache: invalid request path")
	ErrEncodedSlash = errors.New("authcache: encoded slash in request path")
)

// NormalizePath canonicalizes an escaped URL path for use in a cache key.
// It removes dot segments, including percent-encoded dots, and rejects
// encoded slashes at any percent-decoding depth.
func NormalizePath(rawPath string) (string, error) {
	if rawPath == "" {
		rawPath = "/"
	}

	decoded, err := decodePathRepeatedly(rawPath)
	if err != nil {
		return "", err
	}

	normalized := removeDotSegments(decoded)
	if normalized == "" {
		normalized = "/"
	}
	if !strings.HasPrefix(normalized, "/") {
		normalized = "/" + normalized
	}
	return normalized, nil
}

// CanonicalPath is an alias for NormalizePath.
func CanonicalPath(rawPath string) (string, error) {
	return NormalizePath(rawPath)
}

func requestPath(r *http.Request) (string, error) {
	if r == nil {
		return "", ErrNilRequest
	}
	if r.URL == nil {
		if r.RequestURI == "" {
			return "", fmt.Errorf("%w: request has no URL", ErrNilRequest)
		}
		return normalizeRequestTarget(r.RequestURI)
	}

	for _, candidate := range pathCandidates(r) {
		if containsEncodedSlashRepeatedly(candidate) {
			return "", ErrEncodedSlash
		}
	}

	rawPath := r.URL.RawPath
	if rawPath == "" {
		if uriPath, ok := originFormPath(r.RequestURI); ok {
			rawPath = uriPath
		}
	}
	if rawPath == "" {
		rawPath = r.URL.EscapedPath()
	}
	if rawPath == "" {
		rawPath = r.URL.Path
	}
	return NormalizePath(rawPath)
}

func normalizeRequestTarget(target string) (string, error) {
	rawPath, ok := originFormPath(target)
	if !ok {
		parsed, err := url.ParseRequestURI(target)
		if err != nil {
			return "", fmt.Errorf("%w: %v", ErrInvalidPath, err)
		}
		return requestPath(&http.Request{URL: parsed, RequestURI: target})
	}
	return NormalizePath(rawPath)
}

func pathCandidates(r *http.Request) []string {
	candidates := make([]string, 0, 4)
	if r.URL.RawPath != "" {
		candidates = append(candidates, r.URL.RawPath)
	}
	if uriPath, ok := originFormPath(r.RequestURI); ok {
		candidates = append(candidates, uriPath)
	}
	if escaped := r.URL.EscapedPath(); escaped != "" {
		candidates = append(candidates, escaped)
	}
	if r.URL.Path != "" {
		candidates = append(candidates, r.URL.Path)
	}
	return candidates
}

func originFormPath(requestURI string) (string, bool) {
	if requestURI == "" {
		return "", false
	}
	pathPart := requestURI
	if query := strings.IndexByte(pathPart, '?'); query >= 0 {
		pathPart = pathPart[:query]
	}
	if !strings.HasPrefix(pathPart, "/") {
		return "", false
	}
	return pathPart, true
}

func decodePathRepeatedly(rawPath string) (string, error) {
	current := rawPath
	decodedOnce := false
	for depth := 0; depth < 16; depth++ {
		if containsEncodedSlash(current) {
			return "", ErrEncodedSlash
		}
		decoded, err := url.PathUnescape(current)
		if err != nil {
			if decodedOnce {
				return current, nil
			}
			return "", fmt.Errorf("%w: %v", ErrInvalidPath, err)
		}
		if decoded == current {
			return current, nil
		}
		current = decoded
		decodedOnce = true
	}
	if containsEncodedSlash(current) {
		return "", ErrEncodedSlash
	}
	if decoded, err := url.PathUnescape(current); err != nil || decoded != current {
		return "", fmt.Errorf("%w: excessive percent-decoding", ErrInvalidPath)
	}
	return current, nil
}

func containsEncodedSlashRepeatedly(rawPath string) bool {
	current := rawPath
	for depth := 0; depth < 16; depth++ {
		if containsEncodedSlash(current) {
			return true
		}
		decoded, err := url.PathUnescape(current)
		if err != nil || decoded == current {
			return false
		}
		current = decoded
	}
	return containsEncodedSlash(current)
}

func containsEncodedSlash(value string) bool {
	for i := 0; i+2 < len(value); i++ {
		if value[i] != '%' || value[i+1] != '2' {
			continue
		}
		if value[i+2] == 'f' || value[i+2] == 'F' {
			return true
		}
	}
	return false
}

func removeDotSegments(value string) string {
	input := value
	output := ""

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
			output = removeLastPathSegment(output)
		case input == "/..":
			input = "/"
			output = removeLastPathSegment(output)
		case input == "." || input == "..":
			input = ""
		default:
			end := nextSegmentEnd(input)
			output += input[:end]
			input = input[end:]
		}
	}
	return output
}

func nextSegmentEnd(value string) int {
	start := 0
	if strings.HasPrefix(value, "/") {
		start = 1
	}
	if slash := strings.IndexByte(value[start:], '/'); slash >= 0 {
		return start + slash
	}
	return len(value)
}

func removeLastPathSegment(value string) string {
	if value == "" {
		return ""
	}
	slash := strings.LastIndexByte(value, '/')
	if slash < 0 {
		return ""
	}
	return value[:slash]
}
