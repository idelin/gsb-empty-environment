package oci

import (
	"errors"
	"fmt"
	"path"
	"strings"
)

const (
	WhiteoutPrefix = ".wh."
	OpaqueWhiteout = ".wh..wh..opq"
	WhiteoutOpaque = OpaqueWhiteout
)

var (
	ErrInvalidPath     = errors.New("invalid OCI path")
	ErrPathEscape      = errors.New("path escapes image root")
	ErrInvalidWhiteout = errors.New("invalid whiteout name")
	ErrNotWhiteout     = errors.New("not a whiteout name")
)

// CleanPath normalizes a layer path to a slash-separated root-relative path.
// Leading slashes and ./ components are treated as image-root relative.
// Entry names containing .. traversal are rejected.
func CleanPath(raw string) (string, error) {
	if strings.ContainsRune(raw, 0) {
		return "", fmt.Errorf("%w: NUL byte", ErrInvalidPath)
	}
	if raw == "" {
		return ".", nil
	}

	p := path.Clean(raw)
	if path.IsAbs(p) {
		p = path.Clean(strings.TrimLeft(p, "/"))
		if p == "" || p == "/" {
			p = "."
		}
	}
	if p == ".." || strings.HasPrefix(p, "../") {
		return "", fmt.Errorf("%w: %q", ErrPathEscape, raw)
	}
	if p == "" {
		p = "."
	}
	return p, nil
}

// Whiteout describes a parsed .wh marker.
type Whiteout struct {
	// Path is the marker's normalized full path.
	Path string `json:"path"`
	// Dir is the directory containing the marker.
	Dir string `json:"dir"`
	// Name is the marker basename.
	Name string `json:"name"`
	// Target is the deleted entry name for ordinary whiteouts.
	Target string `json:"target,omitempty"`
	// Opaque reports the .wh..wh..opq marker.
	Opaque bool `json:"opaque"`
	// Layer and Order identify processing position.
	Layer int `json:"layer"`
	Order int `json:"order"`
}

// ParseWhiteout validates and parses a whiteout path. The input may be either
// a basename (".wh.foo") or a full layer path ("etc/.wh.passwd").
func ParseWhiteout(name string) (Whiteout, error) {
	if strings.ContainsRune(name, 0) {
		return Whiteout{}, fmt.Errorf("%w: NUL byte", ErrInvalidWhiteout)
	}

	rawSegments := strings.Split(name, "/")
	for i, segment := range rawSegments {
		if segment == "" || segment == "." || segment == ".." {
			continue
		}
		if !strings.HasPrefix(segment, WhiteoutPrefix) {
			continue
		}

		suffix := strings.TrimPrefix(segment, WhiteoutPrefix)
		if suffix == "" || suffix == "." || suffix == ".." {
			return Whiteout{}, fmt.Errorf("%w: illegal marker %q", ErrInvalidWhiteout, segment)
		}
		if strings.ContainsAny(suffix, `/\`) {
			return Whiteout{}, fmt.Errorf("%w: cross-directory target in %q", ErrInvalidWhiteout, segment)
		}
		if i != len(rawSegments)-1 {
			return Whiteout{}, fmt.Errorf("%w: whiteout marker %q used as non-final path component", ErrInvalidWhiteout, segment)
		}
	}

	clean, err := CleanPath(name)
	if err != nil {
		return Whiteout{}, errors.Join(ErrInvalidWhiteout, err)
	}
	base := path.Base(clean)
	if base == "." || base == "/" {
		return Whiteout{}, fmt.Errorf("%w: empty marker", ErrInvalidWhiteout)
	}
	if base == OpaqueWhiteout {
		return Whiteout{Path: clean, Dir: path.Dir(clean), Name: base, Opaque: true}, nil
	}
	if !strings.HasPrefix(base, WhiteoutPrefix) {
		return Whiteout{}, fmt.Errorf("%w: %q", ErrNotWhiteout, name)
	}

	target := strings.TrimPrefix(base, WhiteoutPrefix)
	if target == "" || target == "." || target == ".." {
		return Whiteout{}, fmt.Errorf("%w: illegal target %q", ErrInvalidWhiteout, name)
	}
	if target != path.Base(target) || strings.ContainsAny(target, `/\`) {
		return Whiteout{}, fmt.Errorf("%w: cross-directory target %q", ErrInvalidWhiteout, target)
	}

	return Whiteout{
		Path:   clean,
		Dir:    path.Dir(clean),
		Name:   base,
		Target: target,
	}, nil
}

// IsWhiteoutName reports whether base is an ordinary or opaque whiteout name.
func IsWhiteoutName(name string) bool {
	return name == OpaqueWhiteout || strings.HasPrefix(path.Base(name), WhiteoutPrefix)
}

// IsValidWhiteoutName reports whether name is a legal ordinary or opaque
// whiteout basename/path.
func IsValidWhiteoutName(name string) bool {
	_, err := ParseWhiteout(name)
	return err == nil
}

// ValidateWhiteoutName is the error-returning alias for ParseWhiteout.
func ValidateWhiteoutName(name string) error {
	_, err := ParseWhiteout(name)
	return err
}

// LinkLocation is the lexical result of resolving a link target.
type LinkLocation struct {
	Raw          string `json:"raw"`
	RootRelative string `json:"root_relative,omitempty"`
	Resolved     string `json:"resolved,omitempty"`
	Absolute     bool   `json:"absolute"`
	EscapesRoot  bool   `json:"escapes_root"`
}

// AnalyzeLinkTarget resolves a symlink or hardlink target relative to the
// link's directory. Absolute targets are interpreted relative to the image
// root. A target that lexically rises above the root is marked escaping.
func AnalyzeLinkTarget(linkPath, rawTarget string) LinkLocation {
	loc := LinkLocation{Raw: rawTarget}
	if rawTarget == "" {
		return loc
	}

	normalizedLink, err := CleanPath(linkPath)
	if err != nil {
		normalizedLink = strings.TrimLeft(path.Clean("/"+linkPath), "/")
	}
	linkDir := path.Dir(normalizedLink)
	if linkDir == "/" || linkDir == "" {
		linkDir = "."
	}

	if path.IsAbs(rawTarget) || strings.HasPrefix(rawTarget, "\\") {
		loc.Absolute = true
		rootRel := path.Clean(strings.TrimLeft(rawTarget, "/\\"))
		if rootRel == "." || rootRel == "/" {
			rootRel = "."
		}
		loc.RootRelative = rootRel
		loc.Resolved = rootRel
		return loc
	}

	joined := path.Clean(path.Join(linkDir, filepathToSlash(rawTarget)))
	if joined == ".." || strings.HasPrefix(joined, "../") {
		loc.EscapesRoot = true
		loc.Resolved = joined
		return loc
	}

	joined = strings.TrimPrefix(joined, "./")
	loc.Resolved = joined
	loc.RootRelative = joined
	return loc
}

func filepathToSlash(p string) string { return strings.ReplaceAll(p, "\\", "/") }
