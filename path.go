package netsec

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// ResolveWithinRoot resolves requested as a path inside root. A leading slash
// is interpreted as a path inside the container root; a host absolute path is
// accepted only when it already resolves below root. The returned path is the
// host path suitable for the subsequent agent operation.
//
// The check rejects ".." components (including URL encoded and Windows-style
// forms), NUL bytes, paths equal to the root itself, and any existing symlink
// chain that leaves root. Missing destination components are allowed as long
// as their deepest existing ancestor is still inside root.
func ResolveWithinRoot(root, requested string) (string, error) {
	if strings.TrimSpace(root) == "" {
		return "", errors.New("container root is required")
	}
	if requested == "" {
		return "", errors.New("path is required")
	}
	if strings.ContainsRune(requested, '\x00') {
		return "", errors.New("path contains a NUL byte")
	}

	decoded, err := url.PathUnescape(requested)
	if err == nil {
		requested = decoded
	}
	if strings.ContainsRune(requested, '\x00') {
		return "", errors.New("path contains a NUL byte")
	}
	requested = strings.ReplaceAll(requested, "\\", "/")
	if hasDotDotComponent(root) || hasDotDotComponent(requested) {
		return "", errors.New("path contains a parent-directory component")
	}

	rootAbs, err := filepath.Abs(filepath.Clean(root))
	if err != nil {
		return "", fmt.Errorf("resolve container root: %w", err)
	}
	cleanRequested := filepath.Clean(filepath.FromSlash(requested))

	var candidate string
	if cleanRequested == rootAbs || subtree(rootAbs, cleanRequested) {
		candidate = cleanRequested
	} else if filepath.IsAbs(cleanRequested) {
		guestPath := strings.TrimLeft(filepath.ToSlash(cleanRequested), "/")
		candidate = filepath.Join(rootAbs, filepath.FromSlash(guestPath))
	} else {
		candidate = filepath.Join(rootAbs, cleanRequested)
	}

	if !subtree(rootAbs, candidate) || filepath.Clean(candidate) == rootAbs {
		return "", fmt.Errorf("path %q is outside container root", requested)
	}

	rootReal := rootAbs
	if resolved, err := filepath.EvalSymlinks(rootAbs); err == nil {
		rootReal = resolved
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("resolve container root symlinks: %w", err)
	}

	resolved, err := resolveUnderRoot(rootAbs, rootReal, candidate)
	if err != nil {
		return "", fmt.Errorf("path %q: %w", requested, err)
	}
	if !subtree(rootReal, resolved) {
		return "", fmt.Errorf("path %q escapes container root", requested)
	}
	return resolved, nil
}

// ValidateCopyPath is the error-only form of ResolveWithinRoot.
func ValidateCopyPath(root, requested string) error {
	_, err := ResolveWithinRoot(root, requested)
	return err
}

type (
	CopyFilePathError = PolicyError
)

func CopyFilePathIsSafe(root, requested string) bool {
	return ValidateCopyPath(root, requested) == nil
}

func PathWithinRoot(root, requested string) bool {
	return CopyFilePathIsSafe(root, requested)
}

func hasDotDotComponent(value string) bool {
	value = strings.ReplaceAll(value, "\\", "/")
	for _, part := range strings.Split(value, "/") {
		if part == ".." {
			return true
		}
	}
	return false
}

func resolveUnderRoot(rootAbs, rootReal, candidate string) (string, error) {
	rel, err := filepath.Rel(rootAbs, candidate)
	if err != nil {
		return "", err
	}
	queue := splitComponents(rel)
	current := rootReal
	if len(queue) == 0 {
		return current, fmt.Errorf("path resolves to the container root")
	}

	for depth := 0; len(queue) > 0; depth++ {
		if depth > 1024 {
			return "", errors.New("too many symbolic links")
		}
		component := queue[0]
		queue = queue[1:]
		next := filepath.Join(current, component)
		info, err := os.Lstat(next)
		if err != nil {
			if os.IsNotExist(err) {
				return filepath.Join(append([]string{current}, queue...)...), nil
			}
			return "", fmt.Errorf("inspect path: %w", err)
		}
		if info.Mode()&os.ModeSymlink == 0 {
			current = next
			continue
		}

		linkTarget, err := os.Readlink(next)
		if err != nil {
			return "", fmt.Errorf("read symlink: %w", err)
		}
		if !filepath.IsAbs(linkTarget) {
			linkTarget = filepath.Join(current, linkTarget)
		}

		resolvedTarget, err := filepath.EvalSymlinks(linkTarget)
		if err == nil {
			if !subtree(rootReal, resolvedTarget) {
				return "", errors.New("symbolic link escapes container root")
			}
			current = resolvedTarget
			continue
		}
		if !os.IsNotExist(err) {
			return "", fmt.Errorf("resolve symbolic link: %w", err)
		}

		linkTarget, err = mapRootBoundary(rootAbs, rootReal, linkTarget)
		if err != nil {
			return "", err
		}
		relTarget, err := filepath.Rel(rootReal, linkTarget)
		if err != nil {
			return "", err
		}
		queue = append(splitComponents(relTarget), queue...)
		current = rootReal
	}

	if !subtree(rootReal, current) {
		return "", errors.New("path escapes container root")
	}
	return current, nil
}

func mapRootBoundary(rootAbs, rootReal, target string) (string, error) {
	if subtree(rootReal, target) {
		return target, nil
	}
	if rootAbs != rootReal && subtree(rootAbs, target) {
		rel, err := filepath.Rel(rootAbs, target)
		if err != nil {
			return "", err
		}
		return filepath.Join(rootReal, rel), nil
	}
	return "", errors.New("symbolic link escapes container root")
}

func splitComponents(rel string) []string {
	if rel == "" || rel == "." {
		return nil
	}
	cleaned := filepath.ToSlash(filepath.Clean(rel))
	if cleaned == "." {
		return nil
	}
	if strings.HasPrefix(cleaned, "../") || cleaned == ".." {
		return []string{".."}
	}
	return strings.Split(cleaned, "/")
}

func subtree(root, candidate string) bool {
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(candidate))
	if err != nil || filepath.IsAbs(rel) {
		return false
	}
	if rel == "." {
		return true
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
