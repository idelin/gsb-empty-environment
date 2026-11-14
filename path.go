package netsec

import (
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// normalizeRequest converts a request path into a slash-separated path
// relative to the module root. It deliberately rejects absolute paths and
// Windows-style paths even when running on a Unix host, because the same
// request may be processed by a differently configured worker.
func normalizeRequest(name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("%w: empty path", ErrInvalidPath)
	}
	if strings.ContainsRune(name, 0) {
		return "", fmt.Errorf("%w: NUL byte", ErrInvalidPath)
	}
	if strings.ContainsRune(name, '\\') {
		return "", fmt.Errorf("%w: backslash separator", ErrInvalidPath)
	}
	if path.IsAbs(name) || filepath.IsAbs(name) || windowsAbsolute(name) {
		return "", fmt.Errorf("%w: absolute path", ErrInvalidPath)
	}
	if err := rejectEncodedEscape(name); err != nil {
		return "", err
	}

	clean := path.Clean(name)
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("%w: traversal above module root", ErrPathEscape)
	}
	// path.Clean can only produce an absolute path if the input was absolute,
	// which was rejected above. Keep this assertion as defense in depth.
	if path.IsAbs(clean) {
		return "", fmt.Errorf("%w: non-relative cleaned path", ErrPathEscape)
	}
	return clean, nil
}

// rejectEncodedEscape guards services that pass a still-escaped path to this
// package or that decode more than once. It only rejects decoded forms that
// are absolute, contain NUL/backslash, or clean to a traversal above the root;
// ordinary percent-containing file names remain valid.
func rejectEncodedEscape(name string) error {
	decoded := name
	for i := 0; i < 32; i++ {
		next, err := url.PathUnescape(decoded)
		if err != nil || next == decoded {
			return nil
		}
		decoded = next
		if strings.ContainsRune(decoded, 0) || strings.ContainsRune(decoded, '\\') {
			return fmt.Errorf("%w: encoded path control character", ErrInvalidPath)
		}
		if path.IsAbs(decoded) || windowsAbsolute(decoded) {
			return fmt.Errorf("%w: encoded absolute path", ErrInvalidPath)
		}
		clean := path.Clean(decoded)
		if clean == ".." || strings.HasPrefix(clean, "../") {
			return fmt.Errorf("%w: encoded traversal above module root", ErrPathEscape)
		}
	}
	return nil
}

func windowsAbsolute(name string) bool {
	if len(name) >= 2 && isASCIILetter(name[0]) && name[1] == ':' {
		return true
	}
	return strings.HasPrefix(name, `\\`)
}

func isASCIILetter(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

// withinRoot reports whether candidate is the root itself or is below it.
// Both arguments must be native, cleaned paths.
func withinRoot(root, candidate string) bool {
	if candidate == "" {
		return false
	}
	rel, err := filepath.Rel(root, candidate)
	if err != nil || filepath.IsAbs(rel) {
		return false
	}
	if rel == "." {
		return true
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	return true
}

func (r *Root) lexicalPath(rel string) string {
	return filepath.Join(r.path, filepath.FromSlash(rel))
}

// evalRequestPath performs the required second check after symbolic-link
// resolution. If the final component does not exist, the longest existing
// ancestor is resolved and the remaining lexical components are re-attached.
// A dangling symbolic link is handled by resolving its target text without
// requiring the target to exist. The returned path is always inside r.path.
func (r *Root) evalRequestPath(rel string) (string, error) {
	resolved, err := r.evalAbsolute(r.lexicalPath(rel), 0, make(map[string]struct{}))
	if err != nil {
		return "", err
	}
	if !withinRoot(r.path, resolved) {
		return "", fmt.Errorf("%w: resolved to %s", ErrPathEscape, resolved)
	}
	return resolved, nil
}

func (r *Root) evalAbsolute(absolute string, depth int, seen map[string]struct{}) (string, error) {
	if depth > 64 {
		return "", fmt.Errorf("%w: symbolic-link chain is too deep", ErrInvalidPath)
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err == nil {
		return resolved, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("%w: cannot resolve %q: %v", ErrInvalidPath, absolute, err)
	}

	existing := filepath.Clean(absolute)
	var tail []string
	for {
		info, statErr := os.Lstat(existing)
		if statErr == nil {
			if info.Mode()&fs.ModeSymlink != 0 {
				target, linkErr := r.resolveDanglingSymlink(existing, depth, seen)
				if linkErr != nil {
					return "", linkErr
				}
				return filepath.Join(append([]string{target}, tail...)...), nil
			}
			resolved, evalErr := filepath.EvalSymlinks(existing)
			if evalErr != nil {
				return "", fmt.Errorf("%w: cannot resolve ancestor %q: %v", ErrInvalidPath, existing, evalErr)
			}
			return filepath.Join(append([]string{resolved}, tail...)...), nil
		} else if !errors.Is(statErr, fs.ErrNotExist) {
			return "", fmt.Errorf("%w: cannot inspect %q: %v", ErrInvalidPath, existing, statErr)
		}

		parent := filepath.Dir(existing)
		if parent == existing {
			return "", fmt.Errorf("%w: unresolved path", ErrInvalidPath)
		}
		tail = append([]string{filepath.Base(existing)}, tail...)
		existing = parent
	}
}

func (r *Root) resolveDanglingSymlink(link string, depth int, seen map[string]struct{}) (string, error) {
	link = filepath.Clean(link)
	if _, ok := seen[link]; ok {
		return "", fmt.Errorf("%w: symbolic-link cycle at %q", ErrInvalidPath, link)
	}
	seen[link] = struct{}{}
	defer delete(seen, link)

	target, err := os.Readlink(link)
	if err != nil {
		return "", fmt.Errorf("%w: cannot read symbolic link %q: %v", ErrInvalidPath, link, err)
	}
	if filepath.IsAbs(target) {
		target = filepath.Clean(target)
	} else {
		target = filepath.Join(filepath.Dir(link), target)
	}
	return r.evalAbsolute(target, depth+1, seen)
}

func requestOrRoot(rel string) bool {
	return rel == "." || rel == ""
}
