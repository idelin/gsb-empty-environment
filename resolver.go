package netsec

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// ErrNotFound is returned for both nonexistent paths and paths that resolve
// outside the declared root, so callers can answer both cases with one
// identical response and avoid leaking which case occurred.
var ErrNotFound = errors.New("netsec: file not found")

// ErrBadRequest is returned for malformed request paths: invalid percent
// encoding, NUL bytes, or backslashes.
var ErrBadRequest = errors.New("netsec: malformed request path")

// Resolver maps request paths under a mount prefix (e.g. "/static") to files
// inside a declared content root. It is safe for concurrent use.
type Resolver struct {
	root   string // absolute, symlink-evaluated content root
	prefix string // mount prefix without trailing slash, e.g. "/static"
}

// NewResolver creates a Resolver serving content under root at the given
// mount prefix. The root is resolved to its real path once, up front, so
// later symlink checks compare against a stable anchor.
func NewResolver(root, prefix string) (*Resolver, error) {
	if root == "" {
		return nil, fmt.Errorf("netsec: empty root directory")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("netsec: resolve root: %w", err)
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, fmt.Errorf("netsec: resolve root symlinks: %w", err)
	}
	info, err := os.Stat(real)
	if err != nil {
		return nil, fmt.Errorf("netsec: stat root: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("netsec: root %q is not a directory", real)
	}
	if prefix == "" || prefix == "/" {
		prefix = ""
	} else {
		if !strings.HasPrefix(prefix, "/") {
			return nil, fmt.Errorf("netsec: prefix %q must start with /", prefix)
		}
		prefix = strings.TrimSuffix(path.Clean(prefix), "/")
	}
	return &Resolver{root: real, prefix: prefix}, nil
}

// Resolved is the single resolution result for one request. Directory
// listing, conditional-request and range-request handling must all derive
// from it instead of re-interpreting the request path.
type Resolved struct {
	// Rel is the normalized slash-separated path relative to the root.
	Rel string
	// Real is the absolute path after symlink resolution, verified to be
	// inside the root.
	Real string
	// Info describes the resolved file or directory.
	Info os.FileInfo
}

// Open opens the resolved file for reading.
func (res *Resolved) Open() (*os.File, error) {
	return os.Open(res.Real)
}

// Resolve maps an escaped request path (e.g. r.URL.EscapedPath()) to a file
// inside the root. The path is decoded exactly once; paths containing NUL
// bytes, backslashes, or invalid percent encoding are rejected with
// ErrBadRequest. Nonexistent paths and paths escaping the root (lexically or
// through symlinks) are both reported as ErrNotFound.
func (r *Resolver) Resolve(escapedPath string) (*Resolved, error) {
	p, err := decodeOnce(escapedPath)
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(p, "/") {
		return nil, ErrBadRequest
	}

	// Prefix match on whole path segments, never on a string prefix:
	// "/static" and "/static/..." match, "/staticfoo" does not.
	var rest string
	switch {
	case r.prefix == "":
		rest = p
	case p == r.prefix:
		rest = "/"
	case strings.HasPrefix(p, r.prefix+"/"):
		rest = p[len(r.prefix):]
	default:
		return nil, ErrNotFound
	}

	// POSIX lexical normalization. ".." segments that would climb above
	// the root are rejected as out of bounds rather than silently clamped.
	if escapesRoot(rest) {
		return nil, ErrNotFound
	}
	rel := strings.TrimPrefix(path.Clean("/"+rest), "/")

	target := filepath.Join(r.root, filepath.FromSlash(rel))

	// Resolve symlinks and re-check the real path against the real root.
	real, err := filepath.EvalSymlinks(target)
	if err != nil {
		// Nonexistent file or dangling symlink: same response as out of bounds.
		return nil, ErrNotFound
	}
	if !withinRoot(r.root, real) {
		return nil, ErrNotFound
	}
	info, err := os.Stat(real)
	if err != nil {
		return nil, ErrNotFound
	}
	return &Resolved{Rel: rel, Real: real, Info: info}, nil
}

// decodeOnce applies exactly one pass of percent decoding and rejects NUL
// bytes, backslashes, and malformed escape sequences.
func decodeOnce(s string) (string, error) {
	if strings.ContainsAny(s, "\x00\\") {
		return "", ErrBadRequest
	}
	decoded, err := url.PathUnescape(s)
	if err != nil {
		return "", ErrBadRequest
	}
	if strings.ContainsAny(decoded, "\x00\\") {
		return "", ErrBadRequest
	}
	return decoded, nil
}

// escapesRoot reports whether the ".." segments in p climb above the
// directory the path is interpreted relative to.
func escapesRoot(p string) bool {
	depth := 0
	for _, seg := range strings.Split(p, "/") {
		switch seg {
		case "", ".":
		case "..":
			depth--
			if depth < 0 {
				return true
			}
		default:
			depth++
		}
	}
	return false
}

// withinRoot reports whether target is root itself or lives under it,
// comparing on path-segment boundaries.
func withinRoot(root, target string) bool {
	return target == root || strings.HasPrefix(target, root+string(filepath.Separator))
}
