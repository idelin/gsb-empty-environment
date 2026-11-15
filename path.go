package netsec

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// NormalizePath canonicalizes a package path without changing its Unicode
// characters or case.
//
// Paths are split on ASCII slashes. Empty segments and "." segments are
// discarded, and ".." removes the preceding segment. A ".." at the package
// root is rejected. Backslashes and NUL bytes are rejected so the same path
// cannot be interpreted differently by another platform.
//
// The returned path is relative, slash-separated, and never empty.
func NormalizePath(path string) (string, error) {
	if !utf8.ValidString(path) {
		return "", fmt.Errorf("%w: not valid UTF-8", ErrInvalidPath)
	}
	if strings.IndexByte(path, 0) >= 0 {
		return "", fmt.Errorf("%w: contains NUL", ErrInvalidPath)
	}
	if strings.IndexByte(path, '\\') >= 0 {
		return "", fmt.Errorf("%w: contains backslash", ErrInvalidPath)
	}

	segments := make([]string, 0, strings.Count(path, "/")+1)
	for _, segment := range strings.Split(path, "/") {
		switch segment {
		case "", ".":
			continue
		case "..":
			if len(segments) == 0 {
				return "", fmt.Errorf("%w: %q", ErrPathEscapesRoot, path)
			}
			segments = segments[:len(segments)-1]
		default:
			segments = append(segments, segment)
		}
	}

	canonical := strings.Join(segments, "/")
	if canonical == "" {
		return "", fmt.Errorf("%w: path resolves to package root", ErrInvalidPath)
	}
	return canonical, nil
}

// validateManifestPaths rejects paths that cannot coexist as files in one
// installation tree. The manifest is already sorted, so checking each slash
// delimited ancestor is sufficient.
func validateManifestPaths(entries []ManifestEntry) error {
	paths := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		paths[entry.Path] = struct{}{}
	}

	for _, entry := range entries {
		for offset := strings.IndexByte(entry.Path, '/'); offset >= 0; {
			ancestor := entry.Path[:offset]
			if _, exists := paths[ancestor]; exists {
				return fmt.Errorf("%w: %q is both a file and a parent of %q", ErrPathConflict, ancestor, entry.Path)
			}

			next := strings.IndexByte(entry.Path[offset+1:], '/')
			if next < 0 {
				break
			}
			offset += next + 1
		}
	}
	return nil
}
