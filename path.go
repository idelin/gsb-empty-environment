package netsec

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

type symlinkEscapeError struct {
	detail string
}

func (e *symlinkEscapeError) Error() string {
	return e.detail
}

func pathErrorIsSymlink(err error) bool {
	var target *symlinkEscapeError
	return errors.As(err, &target)
}

// NormalizePath validates a rsync request path and returns its cleaned,
// slash-separated form relative to the module root. Absolute paths, parent
// traversal, NUL bytes, and backslashes are rejected. Interior components such
// as "a/../b" are allowed because they remain inside the module.
func NormalizePath(requestPath string) (string, error) {
	return normalizeRelativePath(requestPath)
}

func normalizeRelativePath(requestPath string) (string, error) {
	if strings.IndexByte(requestPath, 0) >= 0 {
		return "", errors.New("path contains a NUL byte")
	}
	if strings.ContainsRune(requestPath, '\\') {
		return "", errors.New("path contains a backslash")
	}
	if strings.HasPrefix(requestPath, "/") {
		return "", errors.New("path must be relative to the module root")
	}

	cleaned := path.Clean(requestPath)
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") || strings.HasPrefix(cleaned, "/") {
		return "", errors.New("path escapes the module root")
	}
	return cleaned, nil
}

// CheckModulePath verifies that requestPath is lexically inside root and, when
// useChroot is false, that no existing path component is a symlink whose
// target points outside root. It returns the absolute path inside the module.
func CheckModulePath(root, requestPath string, useChroot bool) (string, error) {
	cleanPath, err := normalizeRelativePath(requestPath)
	if err != nil {
		return "", err
	}
	fullPath, err := securePath(root, cleanPath, useChroot)
	if err != nil {
		return "", err
	}
	if !useChroot {
		if err := checkOriginalSymlinks(root, requestPath); err != nil {
			return "", err
		}
	}
	return fullPath, nil
}

func securePath(root, cleanPath string, useChroot bool) (string, error) {
	if root == "" {
		return "", errors.New("module root is empty")
	}

	absoluteRoot, err := canonicalRoot(root)
	if err != nil {
		return "", err
	}

	fullPath := filepath.Join(absoluteRoot, filepath.FromSlash(cleanPath))
	if !withinRoot(absoluteRoot, fullPath) {
		return "", errors.New("path escapes the module root")
	}

	if !useChroot {
		if err := checkSymlinks(absoluteRoot, fullPath); err != nil {
			return "", err
		}
	}
	return fullPath, nil
}

func canonicalRoot(root string) (string, error) {
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve module root: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(absoluteRoot); err == nil {
		absoluteRoot = resolved
	}
	return absoluteRoot, nil
}

// checkOriginalSymlinks follows the request path in the same order the
// filesystem will. This matters for sequences such as "link/../secret": after
// lexical cleaning the path can look confined, while physical resolution
// follows link and then takes ".." from the symlink's target.
func checkOriginalSymlinks(root, requestPath string) error {
	absoluteRoot, err := canonicalRoot(root)
	if err != nil {
		return &symlinkEscapeError{detail: err.Error()}
	}

	current := absoluteRoot
	for _, component := range strings.Split(requestPath, "/") {
		switch component {
		case "", ".":
			continue
		case "..":
			current = filepath.Dir(current)
			if !withinRoot(absoluteRoot, current) {
				return &symlinkEscapeError{detail: "path traverses outside module root"}
			}
			continue
		}

		current = filepath.Join(current, component)
		if !withinRoot(absoluteRoot, current) {
			return &symlinkEscapeError{detail: fmt.Sprintf("path %q is outside module root", current)}
		}

		info, err := os.Lstat(current)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return &symlinkEscapeError{detail: fmt.Sprintf("inspect %q: %v", current, err)}
		}
		if info.Mode()&os.ModeSymlink == 0 {
			continue
		}

		target, err := os.Readlink(current)
		if err != nil {
			return &symlinkEscapeError{detail: fmt.Sprintf("read symbolic link %q: %v", current, err)}
		}
		resolvedTarget := target
		if !filepath.IsAbs(resolvedTarget) {
			resolvedTarget = filepath.Join(filepath.Dir(current), target)
		}
		resolvedTarget = filepath.Clean(resolvedTarget)
		if !withinRoot(absoluteRoot, resolvedTarget) {
			return &symlinkEscapeError{detail: fmt.Sprintf("symbolic link %q points outside module root", current)}
		}
		if err := inspectPath(absoluteRoot, resolvedTarget, 0, make(map[string]struct{})); err != nil {
			return err
		}

		actual, err := filepath.EvalSymlinks(current)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return &symlinkEscapeError{detail: fmt.Sprintf("resolve symbolic link %q: %v", current, err)}
		}
		if !withinRoot(absoluteRoot, actual) {
			return &symlinkEscapeError{detail: fmt.Sprintf("symbolic link %q resolves outside module root", current)}
		}
		current = actual
	}
	return nil
}

func checkSymlinks(root, fullPath string) error {
	return inspectPath(root, fullPath, 0, make(map[string]struct{}))
}

func inspectPath(root, fullPath string, depth int, seen map[string]struct{}) error {
	if depth > 64 {
		return &symlinkEscapeError{detail: "too many levels of symbolic links"}
	}

	fullPath = filepath.Clean(fullPath)
	if !withinRoot(root, fullPath) {
		return &symlinkEscapeError{detail: fmt.Sprintf("path %q is outside module root", fullPath)}
	}

	relative, err := filepath.Rel(root, fullPath)
	if err != nil {
		return &symlinkEscapeError{detail: fmt.Sprintf("resolve relative path: %v", err)}
	}
	if relative == "." {
		return nil
	}

	parts := strings.Split(relative, string(filepath.Separator))
	current := root
	for index, part := range parts {
		current = filepath.Join(current, part)

		info, err := os.Lstat(current)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return &symlinkEscapeError{detail: fmt.Sprintf("inspect %q: %v", current, err)}
		}

		if info.Mode()&os.ModeSymlink == 0 {
			if index < len(parts)-1 && !info.IsDir() {
				return nil
			}
			continue
		}

		if _, duplicate := seen[current]; duplicate {
			return &symlinkEscapeError{detail: fmt.Sprintf("symbolic link loop at %q", current)}
		}
		seen[current] = struct{}{}

		target, err := os.Readlink(current)
		if err != nil {
			return &symlinkEscapeError{detail: fmt.Sprintf("read symbolic link %q: %v", current, err)}
		}
		resolvedTarget := target
		if !filepath.IsAbs(resolvedTarget) {
			resolvedTarget = filepath.Join(filepath.Dir(current), target)
		}
		resolvedTarget = filepath.Clean(resolvedTarget)
		if !withinRoot(root, resolvedTarget) {
			return &symlinkEscapeError{detail: fmt.Sprintf("symbolic link %q points outside module root", current)}
		}

		if err := inspectPath(root, resolvedTarget, depth+1, seen); err != nil {
			return err
		}

		actual, err := filepath.EvalSymlinks(current)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return &symlinkEscapeError{detail: fmt.Sprintf("resolve symbolic link %q: %v", current, err)}
		}
		if !withinRoot(root, actual) {
			return &symlinkEscapeError{detail: fmt.Sprintf("symbolic link %q resolves outside module root", current)}
		}

		if index == len(parts)-1 {
			return nil
		}
		nextPath := actual
		for _, remaining := range parts[index+1:] {
			nextPath = filepath.Join(nextPath, remaining)
		}
		if !withinRoot(root, nextPath) {
			return &symlinkEscapeError{detail: fmt.Sprintf("symbolic link %q leads outside module root", current)}
		}
		return inspectPath(root, nextPath, depth+1, seen)
	}
	return nil
}

func withinRoot(root, candidate string) bool {
	root = filepath.Clean(root)
	candidate = filepath.Clean(candidate)

	relative, err := filepath.Rel(root, candidate)
	if err != nil || filepath.IsAbs(relative) {
		return false
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)))
}
