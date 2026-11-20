package netsec

import (
	"runtime"
	"strings"
)

// sepChar is the separator used in normalized paths.
const sepChar = '/'

// caseInsensitive reports whether the platform's default filesystem
// semantics treat paths case-insensitively (macOS APFS, Windows).
var caseInsensitive = runtime.GOOS == "darwin" || runtime.GOOS == "windows"

// normalizePath applies platform path rules: separators are unified,
// redundant and trailing slashes are removed, and case is folded on
// case-insensitive platforms. It deliberately does NOT resolve "." or
// ".." — those are resolved segment-by-segment against the filesystem
// so that symlinks in earlier components are honoured.
func normalizePath(p string) string {
	p = strings.ReplaceAll(p, "\\", "/")
	for strings.Contains(p, "//") {
		p = strings.ReplaceAll(p, "//", "/")
	}
	for len(p) > 1 && strings.HasSuffix(p, "/") {
		p = p[:len(p)-1]
	}
	if caseInsensitive {
		p = strings.ToLower(p)
	}
	return p
}

// splitComps splits a normalized path into its components.
func splitComps(p string) []string {
	return strings.FieldsFunc(p, func(r rune) bool { return r == '/' })
}

// joinPath joins an absolute resolved prefix with one more component.
func joinPath(dir, name string) string {
	if dir == "" || dir == "/" {
		return "/" + name
	}
	return dir + "/" + name
}

// parentOf returns the parent of a normalized absolute path.
func parentOf(p string) string {
	if p == "" || p == "/" {
		return "/"
	}
	i := strings.LastIndexByte(p, '/')
	if i <= 0 {
		return "/"
	}
	return p[:i]
}

// baseOf returns the last component of a normalized path.
func baseOf(p string) string {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[i+1:]
	}
	return p
}

func isAbs(p string) bool {
	return strings.HasPrefix(p, "/")
}
