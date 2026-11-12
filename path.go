package netsec

import (
	"errors"
	"path"
	"path/filepath"
	"strings"
)

var (
	// ErrFilenameNUL is returned when a filename contains a NUL byte.
	ErrFilenameNUL = errors.New("netsec: filename contains a NUL byte")
	// ErrUnsafeFilename is returned for empty, absolute, or escaping filenames.
	ErrUnsafeFilename = errors.New("netsec: filename escapes the root directory")
)

// NormalizeFilename validates a client-supplied TFTP filename and returns a
// clean, slash-separated path relative to the service root.
//
// NUL bytes, absolute paths, Windows drive paths, and paths that normalize
// outside the root are rejected. ".." components that are cancelled by an
// earlier component are permitted; for example "sub/../file" becomes "file".
func NormalizeFilename(name string) (string, error) {
	if strings.IndexByte(name, 0) >= 0 {
		return "", ErrFilenameNUL
	}
	if name == "" {
		return "", ErrUnsafeFilename
	}

	// Treat backslashes as separators too. This keeps "..\\file" from being
	// interpreted differently on hosts where TFTP clients target Windows.
	name = strings.ReplaceAll(name, `\`, "/")
	if strings.HasPrefix(name, "/") {
		return "", ErrUnsafeFilename
	}
	if len(name) >= 2 && isDriveLetter(name[0]) && name[1] == ':' {
		return "", ErrUnsafeFilename
	}

	clean := path.Clean(name)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", ErrUnsafeFilename
	}
	for _, segment := range strings.Split(clean, "/") {
		if len(segment) >= 2 && isDriveLetter(segment[0]) && segment[1] == ':' {
			return "", ErrUnsafeFilename
		}
	}
	if strings.IndexByte(clean, 0) >= 0 {
		return "", ErrFilenameNUL
	}
	return clean, nil
}

// ResolvePath normalizes filename and joins it to root. The returned path is
// lexically confined to root. Filesystem operations in this package additionally
// use os.Root, which prevents symbolic-link escapes.
func ResolvePath(root, name string) (string, error) {
	if root == "" {
		return "", ErrUnsafeFilename
	}
	rel, err := NormalizeFilename(name)
	if err != nil {
		return "", err
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	absRoot = filepath.Clean(absRoot)
	if absRoot == "." {
		return "", ErrUnsafeFilename
	}
	full := filepath.Join(absRoot, filepath.FromSlash(rel))
	within, err := filepath.Rel(absRoot, full)
	if err != nil {
		return "", err
	}
	if within == ".." || strings.HasPrefix(within, ".."+string(filepath.Separator)) {
		return "", ErrUnsafeFilename
	}
	return full, nil
}

func isDriveLetter(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}
