package netsec

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// PathErrorCode identifies the lexical or handle-level reason a path was
// rejected.
type PathErrorCode string

const (
	PathEmpty         PathErrorCode = "empty-path"
	PathAbsolute      PathErrorCode = "absolute-path"
	PathDotSegment    PathErrorCode = "dot-segment"
	PathEmptySegment  PathErrorCode = "empty-segment"
	PathBackslash     PathErrorCode = "backslash-path"
	PathNUL           PathErrorCode = "nul-path"
	PathControl       PathErrorCode = "control-path"
	PathMissingParent PathErrorCode = "missing-parent"
	PathNotDirectory  PathErrorCode = "not-directory"
	PathNotFile       PathErrorCode = "not-file"
	PathSymlinkParent PathErrorCode = "symlink-parent"
	PathSymlinkTarget PathErrorCode = "symlink-target"
	PathDestination   PathErrorCode = "invalid-destination"
)

// PathError reports a refused package-relative path. Component is the
// offending path component when one is available.
type PathError struct {
	Code      PathErrorCode
	Path      string
	Component string
	Message   string
}

func (e *PathError) Error() string {
	if e.Component != "" {
		return fmt.Sprintf("netsec: path %q rejected (%s at %q)", e.Path, e.Code, e.Component)
	}
	return fmt.Sprintf("netsec: path %q rejected (%s)", e.Path, e.Code)
}

// ValidatePackagePath enforces the lexical rules for paths interpreted from a
// package source or output root. Paths are relative, use '/' separators, and
// never contain "." or ".." components.
func ValidatePackagePath(p string) error {
	return validatePath(p)
}

func validatePath(p string) error {
	if p == "" {
		return &PathError{Code: PathEmpty, Path: p, Message: "path must not be empty"}
	}
	if strings.ContainsRune(p, '\x00') {
		return &PathError{Code: PathNUL, Path: p, Message: "path contains NUL"}
	}
	if strings.ContainsRune(p, '\\') {
		return &PathError{Code: PathBackslash, Path: p, Message: "path must use '/' separators"}
	}
	for _, r := range p {
		if unicode.IsControl(r) {
			return &PathError{Code: PathControl, Path: p, Message: "path contains a control character"}
		}
	}
	if strings.HasPrefix(p, "/") {
		return &PathError{Code: PathAbsolute, Path: p, Message: "absolute paths are forbidden"}
	}
	// Reject drive-letter paths even on Unix so a declaration has the same
	// meaning on every host.
	if len(p) >= 2 && p[1] == ':' {
		r, _ := utf8.DecodeRuneInString(p[:1])
		if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') {
			return &PathError{Code: PathAbsolute, Path: p, Component: p[:2], Message: "absolute paths are forbidden"}
		}
	}
	for _, component := range strings.Split(p, "/") {
		switch {
		case component == "":
			return &PathError{Code: PathEmptySegment, Path: p, Component: component, Message: "empty path segment"}
		case component == ".":
			return &PathError{Code: PathDotSegment, Path: p, Component: component, Message: "dot segments are forbidden"}
		case component == "..":
			return &PathError{Code: PathDotSegment, Path: p, Component: component, Message: "parent segments are forbidden"}
		}
	}
	return nil
}

func pathErrorCode(code PathErrorCode) Code {
	switch code {
	case PathEmpty, PathEmptySegment:
		return CodeInvalidPath
	case PathAbsolute:
		return CodeAbsolutePath
	case PathDotSegment:
		return CodeDotSegment
	case PathBackslash, PathNUL, PathControl:
		return CodeInvalidPath
	case PathSymlinkParent:
		return CodeSymlinkParent
	case PathSymlinkTarget:
		return CodeSymlinkTarget
	case PathMissingParent:
		return CodeMissingParent
	case PathNotDirectory:
		return CodeNotDirectory
	case PathNotFile:
		return CodeNotFile
	case PathDestination:
		return CodeInvalidDestination
	default:
		return CodeInvalidPath
	}
}
