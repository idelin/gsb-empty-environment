package mountview

import (
	"errors"
	"strings"
)

// CleanPath normalises a relative, root-anchored mount path. All mount-view
// paths are relative to the view root and never escape it: a leading slash is
// accepted for readability but never interpreted as an OS root, and any ".."
// that would cross the view root is rejected rather than clamped (clamping
// would silently alias outside the shared domain).
func CleanPath(p string) (string, error) {
	if p == "" {
		return "", errors.New("mountview: empty path")
	}
	cleaned := strings.ReplaceAll(p, "\\", "/")
	cleaned = strings.TrimPrefix(cleaned, "/")
	if cleaned == "" {
		return "", nil // view root
	}
	parts := strings.Split(cleaned, "/")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		switch part {
		case "", ".":
			continue
		case "..":
			if len(out) == 0 {
				return "", errors.New("mountview: path escapes view root: " + p)
			}
			out = out[:len(out)-1]
		default:
			out = append(out, part)
		}
	}
	return strings.Join(out, "/"), nil
}

// splitPath returns the normalised components of a root-relative path.
func splitPath(p string) []string {
	if p == "" {
		return nil
	}
	return strings.Split(p, "/")
}

// parentPath returns the normalised parent path ("" at the root).
func parentPath(p string) string {
	idx := strings.LastIndex(p, "/")
	if idx < 0 {
		return ""
	}
	return p[:idx]
}

// isWithin reports whether child equals parent or is nested under parent.
func isWithin(child, parent string) bool {
	if parent == "" {
		return true
	}
	if child == parent {
		return true
	}
	return strings.HasPrefix(child, parent+"/")
}

// joinPath joins normalised components without touching the OS filesystem.
func joinPath(base, leaf string) string {
	if base == "" {
		return leaf
	}
	if leaf == "" {
		return base
	}
	return base + "/" + leaf
}
