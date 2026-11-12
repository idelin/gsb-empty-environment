package netsec

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
)

const maxSymlinkExpansions = 40

var errNotDirectory = errors.New("path component is not a directory")

// normalizePath converts the path supplied by an SFTP client into a clean,
// root-relative slash path. Absolute client paths are virtual paths rooted at
// the configured root (the usual chroot-style SFTP convention). As a
// convenience, an absolute path that already starts with the configured root is
// treated as a physical path and must remain inside that root.
func (a *Authorizer) normalizePath(name string) (string, error) {
	if strings.IndexByte(name, 0) >= 0 {
		return "", fmt.Errorf("%w: NUL byte in path", ErrPermissionDenied)
	}

	if a.looksPhysical(name) {
		cleaned := filepath.Clean(name)
		if !withinRoot(a.root, cleaned) {
			return "", ErrRootEscape
		}
		rel, err := filepath.Rel(a.root, cleaned)
		if err != nil {
			return "", ErrRootEscape
		}
		if rel == "." {
			return "", nil
		}
		return filepath.ToSlash(rel), nil
	}

	parts := strings.Split(filepath.ToSlash(name), "/")
	stack := make([]string, 0, len(parts))
	for _, part := range parts {
		switch part {
		case "", ".":
			continue
		case "..":
			if len(stack) == 0 {
				return "", ErrRootEscape
			}
			stack = stack[:len(stack)-1]
		default:
			stack = append(stack, part)
		}
	}
	return strings.Join(stack, "/"), nil
}

func (a *Authorizer) looksPhysical(name string) bool {
	if !filepath.IsAbs(name) {
		return false
	}
	if a.root == string(filepath.Separator) {
		return true
	}
	return name == a.root || strings.HasPrefix(name, a.root+string(filepath.Separator))
}

// evalRel resolves every symlink in rel and verifies that each intermediate
// and final real path stays inside the configured root. A missing final
// component is allowed and returned as-is; missing intermediate components are
// errors.
func (a *Authorizer) evalRel(rel string) (string, error) {
	if rel == "" {
		return a.root, nil
	}

	components := strings.Split(filepath.ToSlash(rel), "/")
	resolved := a.root
	expansions := 0

	for i := 0; i < len(components); {
		component := components[i]
		if component == "" {
			i++
			continue
		}

		candidate := filepath.Join(resolved, filepath.FromSlash(component))
		if !withinRoot(a.root, candidate) {
			return "", ErrRootEscape
		}

		info, err := a.fsys.Lstat(candidate)
		if err != nil {
			if isNotExist(err) && i == len(components)-1 {
				return candidate, nil
			}
			return "", err
		}

		if info.Mode()&fs.ModeSymlink != 0 {
			expansions++
			if expansions > maxSymlinkExpansions {
				return "", fmt.Errorf("too many symlink expansions")
			}

			target, err := a.fsys.Readlink(candidate)
			if err != nil {
				return "", err
			}
			if target == "" || strings.IndexByte(target, 0) >= 0 {
				return "", fmt.Errorf("invalid symlink target")
			}

			var next string
			if filepath.IsAbs(target) {
				next = filepath.Clean(target)
			} else {
				next = filepath.Join(filepath.Dir(candidate), target)
			}
			if !withinRoot(a.root, next) {
				return "", ErrRootEscape
			}

			nextRel, err := filepath.Rel(a.root, next)
			if err != nil || !withinRoot(a.root, next) {
				return "", ErrRootEscape
			}

			var nextComponents []string
			if nextRel != "." {
				nextComponents = strings.Split(filepath.ToSlash(nextRel), "/")
			}

			rest := append([]string(nil), components[i+1:]...)
			components = append(nextComponents, rest...)
			resolved = a.root
			i = 0
			continue
		}

		if i < len(components)-1 && !info.IsDir() {
			return "", errNotDirectory
		}
		resolved = candidate
		i++
	}

	return resolved, nil
}

// resolveRel returns the symlink-resolved absolute path and its Stat info.
// When mustExist is false, a missing final component is accepted with nil
// info.
func (a *Authorizer) resolveRel(rel string, mustExist bool) (string, fs.FileInfo, error) {
	abs, err := a.evalRel(rel)
	if err != nil {
		return "", nil, err
	}
	if !withinRoot(a.root, abs) {
		return "", nil, ErrRootEscape
	}

	info, err := a.fsys.Stat(abs)
	if err != nil {
		if !mustExist && isNotExist(err) {
			return abs, nil, nil
		}
		return "", nil, err
	}
	return abs, info, nil
}

func splitEntry(rel string) (parent, base string, ok bool) {
	if rel == "" {
		return "", "", false
	}
	index := strings.LastIndex(rel, "/")
	if index < 0 {
		return "", rel, true
	}
	return rel[:index], rel[index+1:], true
}

func withinRoot(root, name string) bool {
	if name == root {
		return true
	}
	rel, err := filepath.Rel(root, name)
	if err != nil || filepath.IsAbs(rel) {
		return false
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	// filepath.Rel can return a slash-normalized value on some platforms.
	return !strings.HasPrefix(rel, "../")
}
