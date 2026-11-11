package manifestbind

import (
	"errors"
	"strings"
)

var (
	// ErrEmptySegment is returned when a path is empty or contains an
	// empty segment (leading, trailing, or doubled slashes).
	ErrEmptySegment = errors.New("manifestbind: path contains empty segment")
	// ErrDotSegment is returned when a path contains a "." or ".." segment.
	ErrDotSegment = errors.New("manifestbind: path contains dot segment")
	// ErrBackslash is returned when a path contains a backslash, which
	// some extractors treat as a directory separator.
	ErrBackslash = errors.New("manifestbind: path contains backslash")
	// ErrDuplicatePath is returned when the same path appears twice.
	ErrDuplicatePath = errors.New("manifestbind: duplicate path")
	// ErrPathConflict is returned when a file path collides with a
	// directory implied by another path.
	ErrPathConflict = errors.New("manifestbind: file/directory path conflict")
)

// ValidatePath reports whether p is a safe, relative, slash-separated
// archive path. Segments are compared byte for byte; no normalization,
// Unicode folding, or case folding is performed, so two paths are equal
// only if their bytes are identical.
func ValidatePath(p string) error {
	if strings.Contains(p, "\\") {
		return ErrBackslash
	}
	if p == "" {
		return ErrEmptySegment
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == "" {
			return ErrEmptySegment
		}
		if seg == "." || seg == ".." {
			return ErrDotSegment
		}
	}
	return nil
}

// pathSet tracks the file paths seen so far plus every ancestor
// directory they imply, so that duplicate paths and file/directory
// conflicts are detected with byte-exact comparisons.
type pathSet struct {
	files map[string]struct{}
	dirs  map[string]struct{}
}

func newPathSet() *pathSet {
	return &pathSet{
		files: make(map[string]struct{}),
		dirs:  make(map[string]struct{}),
	}
}

// Add validates p and registers it as a file path.
func (s *pathSet) Add(p string) error {
	if err := ValidatePath(p); err != nil {
		return err
	}
	if _, ok := s.files[p]; ok {
		return ErrDuplicatePath
	}
	if _, ok := s.dirs[p]; ok {
		// p is already an ancestor directory of another file.
		return ErrPathConflict
	}
	for i := 0; i < len(p); i++ {
		if p[i] != '/' {
			continue
		}
		ancestor := p[:i]
		if _, ok := s.files[ancestor]; ok {
			// A previously seen file is being used as a directory.
			return ErrPathConflict
		}
		s.dirs[ancestor] = struct{}{}
	}
	s.files[p] = struct{}{}
	return nil
}

// Has reports whether p was registered as a file path.
func (s *pathSet) Has(p string) bool {
	_, ok := s.files[p]
	return ok
}
