package pathsplit

import (
	"path"
	"strings"
)

// resolve fully resolves a root-relative virtual path, following every
// intermediate and trailing symlink. It mirrors POSIX path resolution and
// detects symlink loops and escapes above the site root.
func resolve(fs FS, virtual string) (string, error) {
	stack := splitComponents(virtual)
	seen := make(map[string]bool)

	for i := 0; i < len(stack); i++ {
		current := joinComponents(stack[:i+1])
		entry, ok := fs.Lookup(current)
		if !ok {
			return "", errMissing
		}
		if entry.Kind != KindSymlink {
			continue
		}

		if seen[current] {
			return "", ErrSymlinkLoop
		}
		seen[current] = true

		target := entry.Target
		if path.IsAbs(target) {
			// An absolute target replaces the whole stack from the site root.
			replacement, err := expandTarget(target, nil)
			if err != nil {
				return "", err
			}
			remainder := append([]string(nil), stack[i+1:]...)
			stack = append(append([]string(nil), replacement...), remainder...)
			i = -1
		} else {
			// A relative target is resolved from the link's containing dir;
			// expandTarget already includes that prefix, so do not re-add it.
			replacement, err := expandTarget(target, stack[:i])
			if err != nil {
				return "", err
			}
			remainder := append([]string(nil), stack[i+1:]...)
			stack = append(append([]string(nil), replacement...), remainder...)
			i = len(replacement) - 2
		}
	}

	return joinComponents(stack), nil
}

// expandTarget expands a symlink target onto the given prefix components.
// Callers pass the link's containing directory for relative targets and an
// empty prefix for absolute targets.
func expandTarget(target string, prefix []string) ([]string, error) {
	stack := append([]string(nil), prefix...)
	for _, component := range splitComponents(target) {
		switch component {
		case "", ".":
			continue
		case "..":
			if len(stack) == 0 {
				return nil, ErrOutsideRoot
			}
			stack = stack[:len(stack)-1]
		default:
			stack = append(stack, component)
		}
	}
	return stack, nil
}

// splitComponents splits a virtual path on "/" while retaining "." and ".."
// (needed for symlink-target resolution) and dropping empty components.
func splitComponents(p string) []string {
	parts := strings.Split(p, "/")
	components := make([]string, 0, len(parts))
	for _, part := range parts {
		if part == "" {
			continue
		}
		components = append(components, part)
	}
	return components
}

func joinComponents(components []string) string {
	if len(components) == 0 {
		return "/"
	}
	return "/" + strings.Join(components, "/")
}
