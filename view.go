package netsec

import (
	"fmt"
	"strings"
	"syscall"
)

// View describes a traced process's filesystem view.
//
// Root names a directory that represents the process's root, normally
// /proc/<pid>/root. Cwd is the process's current working directory expressed
// relative to Root ("" means Root itself). Kernel implementations must open
// paths relative to Root while confining resolution to Root.
type View struct {
	Root string
	Cwd  string
}

// ResolvePath converts a pathname from an openat notification into a path
// relative to the traced process's root. baseRel must be a directory path
// relative to that root (View.Cwd for AT_FDCWD, or the result of
// Kernel.DirFDBase).
//
// The returned path is deliberately not lexically cleaned. openat2 with
// RESOLVE_IN_ROOT must resolve "." and ".." and symlinks itself, otherwise a
// path such as "link/../file" would have different semantics. RESOLVE_IN_ROOT
// also clamps ".." at the process root.
func ResolvePath(baseRel, pathname string) (string, error) {
	if pathname == "" {
		return "", fmt.Errorf("empty pathname: %w", syscall.ENOENT)
	}
	if strings.IndexByte(pathname, 0) >= 0 {
		return "", fmt.Errorf("pathname contains NUL: %w", syscall.EINVAL)
	}
	if len(pathname) > MaxPathBytes {
		return "", fmt.Errorf("pathname is %d bytes: %w", len(pathname), syscall.ENAMETOOLONG)
	}

	base, err := cleanBase(baseRel)
	if err != nil {
		return "", err
	}

	var rel string
	if strings.HasPrefix(pathname, "/") {
		// An absolute pathname ignores dirfd. Resolve it inside the process
		// root rather than using the supervisor's "/".
		rel = strings.TrimLeft(pathname, "/")
	} else if base == "" {
		rel = pathname
	} else {
		rel = base + "/" + pathname
	}
	if rel == "" {
		rel = "."
	}
	if len(rel) > MaxPathBytes {
		return "", fmt.Errorf("resolved pathname is %d bytes: %w", len(rel), syscall.ENAMETOOLONG)
	}
	return rel, nil
}

// cleanBase validates and normalises a root-relative directory path. A view
// returned by a Kernel must never contain "..", but checking here keeps a
// buggy or malicious backend from smuggling an escape into the prefix.
func cleanBase(base string) (string, error) {
	if base == "" || base == "." {
		return "", nil
	}
	if strings.HasPrefix(base, "/") {
		return "", fmt.Errorf("root-relative base %q is absolute: %w", base, syscall.EINVAL)
	}
	if strings.IndexByte(base, 0) >= 0 {
		return "", fmt.Errorf("root-relative base contains NUL: %w", syscall.EINVAL)
	}

	parts := make([]string, 0, strings.Count(base, "/")+1)
	for _, part := range strings.Split(base, "/") {
		switch part {
		case "", ".":
			continue
		case "..":
			return "", fmt.Errorf("root-relative base %q escapes root: %w", base, syscall.EINVAL)
		default:
			parts = append(parts, part)
		}
	}
	return strings.Join(parts, "/"), nil
}
