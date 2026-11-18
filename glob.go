package netsec

import (
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// Glob expands a virtual glob pattern and translates every result back to a
// virtual token.
//
// Expansion happens inside the configured root handle. Each match is then
// resolved through a rooted stat to reject symbolic-link escapes. If any
// match fails that validation, or any translated token would contain a real
// access-root prefix, Glob returns no results and an error; it never returns
// a partially translated batch.
func (t *Translator) Glob(virtualPattern string) ([]string, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	index, pattern, err := t.parseToken(virtualPattern)
	if err != nil {
		return nil, err
	}
	root := &t.roots[index]
	operationToken := tokenFor(index, "")

	if _, err := path.Match(pattern, ""); err != nil {
		return nil, accessError("glob", operationToken, ErrBadPattern)
	}
	if err := validateGlobPrefix(root, pattern); err != nil {
		return nil, accessError("glob", operationToken, err)
	}
	if err := validateGlobTraversal(root, pattern); err != nil {
		return nil, accessError("glob", operationToken, err)
	}

	rawMatches, err := fs.Glob(root.root.FS(), pattern)
	if err != nil {
		if errors.Is(err, path.ErrBadPattern) {
			return nil, accessError("glob", operationToken, ErrBadPattern)
		}
		return nil, accessError("glob", operationToken, classifyIOError(err))
	}

	tokens := make([]string, 0, len(rawMatches))
	for _, match := range rawMatches {
		rel := path.Clean(match)
		route, err := routeFor(root, rel)
		if err != nil {
			if errors.Is(err, ErrSubdirDenied) {
				continue
			}
			return nil, accessError("glob", operationToken, err)
		}

		// Stat follows a final symlink but resolves every component relative
		// to the already-open access/subdirectory handle. This validates the
		// match without opening a FIFO or device node.
		if _, err := route.root.Stat(filepath.FromSlash(route.rel)); err != nil {
			return nil, accessError("glob", operationToken, classifyIOError(err))
		}

		lexical := filepath.Join(root.real, filepath.FromSlash(rel))
		token, err := t.tokenizeLexicalLocked(lexical)
		if err != nil {
			return nil, accessError("glob", operationToken, err)
		}
		if t.tokenExposesRealPathLocked(token) {
			return nil, accessError("glob", operationToken, ErrGlobLeak)
		}
		tokens = append(tokens, token)
	}

	return tokens, nil
}

// GlobPaths is a convenience alias for Glob.
func (t *Translator) GlobPaths(virtualPattern string) ([]string, error) {
	return t.Glob(virtualPattern)
}

func validateGlobPrefix(root *accessRoot, pattern string) error {
	if pattern == "." {
		return nil
	}

	current := ""
	for _, part := range strings.Split(pattern, "/") {
		if hasGlobMeta(part) {
			break
		}
		if current == "" {
			current = part
		} else {
			current = path.Join(current, part)
		}

		info, err := root.root.Lstat(filepath.FromSlash(current))
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return classifyIOError(err)
		}
		if info.Mode()&os.ModeSymlink == 0 {
			continue
		}

		target, err := root.root.Stat(filepath.FromSlash(current))
		if err != nil {
			return classifyIOError(err)
		}
		if !target.IsDir() {
			break
		}
	}
	return nil
}

// validateGlobTraversal walks every directory branch that a wildcard can
// select before the final component. fs.Glob ignores traversal errors, so an
// external symlink in the middle of a pattern would otherwise look like an
// ordinary empty match instead of a rejected escape.
func validateGlobTraversal(root *accessRoot, pattern string) error {
	if pattern == "." {
		return nil
	}

	parts := strings.Split(pattern, "/")
	prefixes := []string{"."}
	for i, part := range parts {
		if i == len(parts)-1 || len(prefixes) == 0 {
			break
		}

		next := make([]string, 0, len(prefixes))
		for _, prefix := range prefixes {
			entries, err := fs.ReadDir(root.root.FS(), prefix)
			if err != nil {
				// A missing branch simply cannot contribute matches.
				continue
			}
			for _, entry := range entries {
				matched, err := path.Match(part, entry.Name())
				if err != nil {
					return ErrBadPattern
				}
				if !matched {
					continue
				}

				child := path.Join(prefix, entry.Name())
				if _, err := routeFor(root, child); err != nil {
					if errors.Is(err, ErrSubdirDenied) {
						continue
					}
					return err
				}

				if entry.Type()&fs.ModeSymlink != 0 {
					target, err := root.root.Stat(filepath.FromSlash(child))
					if err != nil {
						return classifyIOError(err)
					}
					if !target.IsDir() {
						continue
					}
				} else if !entry.IsDir() {
					continue
				}
				next = append(next, child)
			}
		}
		prefixes = next
	}
	return nil
}

func hasGlobMeta(value string) bool {
	return strings.ContainsAny(value, `*?[\`)
}
