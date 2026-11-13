package netsec

import (
	"path"
	"path/filepath"
	"strings"
)

// NormalizePath validates name as a root-relative archive path and returns the
// absolute path it denotes below root.
func NormalizePath(root, name string) (string, error) {
	rootAbs, err := absoluteRoot(root)
	if err != nil {
		return "", err
	}
	rel, err := normalizeEntryPath(name, "archive path")
	if err != nil {
		return "", err
	}
	return rootedPath(rootAbs, rel, name, "archive path")
}

// SafePath is an alias for NormalizePath.
func SafePath(root, name string) (string, error) {
	return NormalizePath(root, name)
}

// Normalize is an alias for NormalizePath.
func Normalize(root, name string) (string, error) {
	return NormalizePath(root, name)
}

// ValidatePath reports whether name stays below root after normalization.
func ValidatePath(root, name string) error {
	_, err := NormalizePath(root, name)
	return err
}

// ResolveLinkTarget resolves a symlink target against the directory containing
// entryName and verifies that the result stays below root. Absolute targets are
// always rejected because they are not root-relative.
func ResolveLinkTarget(root, entryName, target string) (string, error) {
	rootAbs, err := absoluteRoot(root)
	if err != nil {
		return "", err
	}

	entryRel, err := normalizeEntryPath(entryName, "entry name")
	if err != nil {
		return "", err
	}
	targetRel, err := normalizeTargetPath(target)
	if err != nil {
		if pathErr, ok := err.(*PathError); ok {
			pathErr.Entry = entryName
			return "", pathErr
		}
		return "", err
	}

	joined := targetRel
	if base := path.Dir(entryRel); base != "." {
		joined = path.Join(base, targetRel)
	}
	if escapes(joined) {
		return "", &PathError{
			Entry:  entryName,
			Field:  "unix link target",
			Path:   target,
			Reason: "target escapes extraction root",
		}
	}
	return rootedPath(rootAbs, joined, target, "unix link target")
}

// ValidateLinkTarget reports whether the extra-field link target is safe.
func ValidateLinkTarget(root, entryName, target string) error {
	_, err := ResolveLinkTarget(root, entryName, target)
	return err
}

func absoluteRoot(root string) (string, error) {
	if root == "" {
		root = "."
	}
	rootAbs, err := filepath.Abs(filepath.Clean(root))
	if err != nil {
		return "", err
	}
	return rootAbs, nil
}

func normalizeEntryPath(name, field string) (string, error) {
	rel, reason := cleanRelative(name, false, false)
	if reason != "" {
		return "", &PathError{Field: field, Path: name, Reason: reason}
	}
	return rel, nil
}

func normalizeTargetPath(target string) (string, error) {
	rel, reason := cleanRelative(target, true, true)
	if reason != "" {
		return "", &PathError{Field: "unix link target", Path: target, Reason: reason}
	}
	return rel, nil
}

func cleanRelative(name string, allowRoot, allowUp bool) (string, string) {
	if strings.IndexByte(name, 0) >= 0 {
		return "", "path contains a NUL byte"
	}

	// Treat backslashes as separators on every platform. This prevents a
	// Windows-style path from bypassing checks when the archive is read on
	// Unix, and vice versa.
	name = strings.ReplaceAll(name, `\`, "/")
	if strings.HasPrefix(name, "/") {
		return "", "path is absolute"
	}
	if isWindowsVolume(name) {
		return "", "path contains a drive or device name"
	}

	clean := path.Clean(name)
	if clean == "." {
		if allowRoot {
			return ".", ""
		}
		return "", "path is empty"
	}
	if !allowUp && escapes(clean) {
		return "", "path escapes extraction root"
	}
	return clean, ""
}

func isWindowsVolume(name string) bool {
	if len(name) >= 2 && name[1] == ':' {
		c := name[0]
		if ('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z') {
			return true
		}
	}
	return false
}

func escapes(rel string) bool {
	return rel == ".." || strings.HasPrefix(rel, "../")
}

func rootedPath(rootAbs, rel, original, field string) (string, error) {
	full := filepath.Join(rootAbs, filepath.FromSlash(rel))
	within, err := pathWithin(rootAbs, full)
	if err != nil {
		return "", err
	}
	if !within {
		return "", &PathError{
			Field:  field,
			Path:   original,
			Reason: "path escapes extraction root",
		}
	}
	return full, nil
}

func pathWithin(root, full string) (bool, error) {
	rel, err := filepath.Rel(root, full)
	if err != nil {
		return false, err
	}
	if filepath.IsAbs(rel) {
		return false, nil
	}
	rel = filepath.ToSlash(rel)
	return !escapes(rel), nil
}
