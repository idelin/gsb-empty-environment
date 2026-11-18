package netsec

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// AccessPath is one entry in the virtual token table.
//
// Root is the real host directory represented by this entry. Subdirs may
// restrict access to a set of directories relative to Root; an empty list
// allows the whole root. The token id is the entry's index in the list.
type AccessPath struct {
	Root    string
	Subdirs []string
}

// Config is the richer constructor form for per-access-path subdirectories.
type Config struct {
	AccessPaths []AccessPath
}

// Translator maps $p(:N:)/... virtual paths to a fixed set of host roots.
//
// Roots and their allowed subdirectories are opened when the Translator is
// constructed. Open, Source, and Glob operate through those loaded handles,
// so replacing a directory after Translate but before Open cannot redirect
// an open outside the configured tree.
type Translator struct {
	mu     sync.RWMutex
	roots  []accessRoot
	closed bool
}

// PathTranslator is an alias kept for callers that prefer the more explicit
// type name.
type PathTranslator = Translator

type accessRoot struct {
	index   int
	real    string
	root    *os.Root
	whole   bool
	subdirs []allowedDir
}

type allowedDir struct {
	rel  string
	real string
	root *os.Root
}

type accessRoute struct {
	root *os.Root
	base string
	rel  string
}

// New configures access paths from a list of roots and a global set of
// accessible subdirectories. A nil or empty subdir list permits the complete
// root. The same subdir list is applied to every root.
func New(accessPaths []string, accessibleSubdirs []string) (*Translator, error) {
	paths := make([]AccessPath, len(accessPaths))
	for i, root := range accessPaths {
		paths[i] = AccessPath{
			Root:    root,
			Subdirs: append([]string(nil), accessibleSubdirs...),
		}
	}
	return NewWithAccessPaths(paths)
}

// NewWithAccessPaths configures access paths with per-root subdirectories.
func NewWithAccessPaths(accessPaths []AccessPath) (*Translator, error) {
	if len(accessPaths) == 0 {
		return nil, accessError("configure", "", ErrNoAccessPath)
	}

	t := &Translator{roots: make([]accessRoot, 0, len(accessPaths))}
	for i, path := range accessPaths {
		root, err := openAccessRoot(i, path)
		if err != nil {
			_ = t.Close()
			return nil, err
		}
		t.roots = append(t.roots, root)
	}
	return t, nil
}

// NewConfig is the Config-based constructor.
func NewConfig(config Config) (*Translator, error) {
	return NewWithAccessPaths(config.AccessPaths)
}

// NewTranslator is a convenience alias for New.
func NewTranslator(accessPaths []string, accessibleSubdirs []string) (*Translator, error) {
	return New(accessPaths, accessibleSubdirs)
}

// NewPathTranslator is a convenience alias for New.
func NewPathTranslator(accessPaths []string, accessibleSubdirs []string) (*Translator, error) {
	return New(accessPaths, accessibleSubdirs)
}

func openAccessRoot(index int, path AccessPath) (accessRoot, error) {
	token := tokenFor(index, "")
	if strings.TrimSpace(path.Root) == "" {
		return accessRoot{}, accessError("configure", token, ErrAccess)
	}

	absolute, err := filepath.Abs(path.Root)
	if err != nil {
		return accessRoot{}, accessError("configure", token, classifyIOError(err))
	}
	realRoot, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return accessRoot{}, accessError("configure", token, classifyIOError(err))
	}

	info, err := os.Stat(realRoot)
	if err != nil {
		return accessRoot{}, accessError("configure", token, classifyIOError(err))
	}
	if !info.IsDir() {
		return accessRoot{}, accessError("configure", token, ErrAccess)
	}

	handle, err := os.OpenRoot(realRoot)
	if err != nil {
		return accessRoot{}, accessError("configure", token, classifyIOError(err))
	}

	result := accessRoot{
		index: index,
		real:  filepath.Clean(realRoot),
		root:  handle,
	}

	subdirs, whole, err := normalizeSubdirs(path.Subdirs)
	if err != nil {
		_ = handle.Close()
		return accessRoot{}, accessError("configure", token, err)
	}
	result.whole = whole

	if whole {
		return result, nil
	}

	for _, rel := range subdirs {
		subroot, err := handle.OpenRoot(filepath.FromSlash(rel))
		if err != nil {
			_ = handle.Close()
			for _, opened := range result.subdirs {
				_ = opened.root.Close()
			}
			return accessRoot{}, accessError("configure", token, classifyIOError(err))
		}

		realSubdir, err := filepath.EvalSymlinks(filepath.Join(result.real, filepath.FromSlash(rel)))
		if err != nil {
			_ = subroot.Close()
			_ = handle.Close()
			for _, opened := range result.subdirs {
				_ = opened.root.Close()
			}
			return accessRoot{}, accessError("configure", token, classifyIOError(err))
		}
		if !withinPath(result.real, realSubdir) {
			_ = subroot.Close()
			_ = handle.Close()
			for _, opened := range result.subdirs {
				_ = opened.root.Close()
			}
			return accessRoot{}, accessError("configure", token, ErrSymlinkEscape)
		}

		result.subdirs = append(result.subdirs, allowedDir{
			rel:  rel,
			real: filepath.Clean(realSubdir),
			root: subroot,
		})
	}

	return result, nil
}

func normalizeSubdirs(subdirs []string) ([]string, bool, error) {
	if len(subdirs) == 0 {
		return nil, true, nil
	}

	seen := make(map[string]struct{}, len(subdirs))
	normalized := make([]string, 0, len(subdirs))
	for _, subdir := range subdirs {
		rel, err := normalizeVirtualPath(subdir)
		if err != nil {
			return nil, false, err
		}
		if rel == "." {
			return nil, true, nil
		}
		if _, ok := seen[rel]; ok {
			continue
		}
		seen[rel] = struct{}{}
		normalized = append(normalized, rel)
	}
	if len(normalized) == 0 {
		return nil, true, nil
	}
	return normalized, false, nil
}

// Close releases the root directory handles held by the Translator.
// It is safe to call more than once.
func (t *Translator) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.closed {
		return nil
	}
	t.closed = true

	var errs []error
	for i := range t.roots {
		for _, subdir := range t.roots[i].subdirs {
			if err := subdir.root.Close(); err != nil {
				errs = append(errs, err)
			}
		}
		if err := t.roots[i].root.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	t.roots = nil
	return errors.Join(errs...)
}

// Translate returns the canonical host path represented by virtualPath.
//
// Translate performs a current symlink resolution and verifies the result is
// still below the token's access root. The returned string is intended for
// diagnostics and interoperability with APIs that do not accept a file
// descriptor. Security-sensitive opens must use Open or OpenSource; opening a
// string returned by Translate reintroduces a check/open race.
func (t *Translator) Translate(virtualPath string) (string, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	index, rel, err := t.parseToken(virtualPath)
	if err != nil {
		return "", err
	}
	root := &t.roots[index]
	route, err := routeFor(root, rel)
	if err != nil {
		return "", accessError("translate", tokenFor(index, ""), err)
	}

	candidate := route.base
	if route.rel != "." {
		candidate = filepath.Join(route.base, filepath.FromSlash(route.rel))
	}

	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", accessError("translate", tokenFor(index, ""), classifyIOError(err))
	}
	resolved = filepath.Clean(resolved)
	if !withinPath(route.base, resolved) || !withinPath(root.real, resolved) {
		return "", accessError("translate", tokenFor(index, ""), ErrSymlinkEscape)
	}

	return resolved, nil
}

// TranslatePath is a convenience alias for Translate.
func (t *Translator) TranslatePath(virtualPath string) (string, error) {
	return t.Translate(virtualPath)
}

// Open opens virtualPath for reading without a path-based check/open race.
//
// The final object is obtained relative to a directory handle opened when the
// Translator was constructed. The resulting file descriptor is then inspected
// through fstat before it is returned.
func (t *Translator) Open(virtualPath string) (*os.File, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.openLocked("open", virtualPath, false)
}

// Tokenize converts a real path below one of the configured access roots back
// to its canonical virtual token. The most specific nested access path wins.
func (t *Translator) Tokenize(realPath string) (string, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	absolute, err := filepath.Abs(realPath)
	if err != nil {
		return "", accessError("tokenize", "", classifyIOError(err))
	}
	absolute = filepath.Clean(absolute)
	lexicallyKnown := t.lexicallyUnderAnyRootLocked(absolute)
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		if !lexicallyKnown {
			return "", accessError("tokenize", "", ErrPathEscape)
		}
		return "", accessError("tokenize", "", classifyIOError(err))
	}
	resolved = filepath.Clean(resolved)
	token, err := t.tokenizeLexicalLocked(resolved)
	if err != nil && errors.Is(err, ErrPathEscape) && lexicallyKnown {
		return "", accessError("tokenize", "", ErrSymlinkEscape)
	}
	return token, err
}

func (t *Translator) tokenizeLexicalLocked(absolute string) (string, error) {
	bestIndex := -1
	bestLength := -1
	bestRel := ""

	for i := range t.roots {
		root := &t.roots[i]
		rel, ok := relativeWithin(root.real, absolute)
		if !ok {
			continue
		}
		if _, err := routeFor(root, rel); err != nil {
			if errors.Is(err, ErrSubdirDenied) {
				continue
			}
			return "", accessError("tokenize", tokenFor(i, ""), err)
		}
		if len(root.real) > bestLength {
			bestIndex = i
			bestLength = len(root.real)
			bestRel = rel
		}
	}

	if bestIndex < 0 {
		return "", accessError("tokenize", "", ErrPathEscape)
	}

	token := tokenFor(bestIndex, bestRel)
	if t.tokenExposesRealPathLocked(token) {
		return "", accessError("tokenize", tokenFor(bestIndex, ""), ErrGlobLeak)
	}
	return token, nil
}

func (t *Translator) tokenExposesRealPathLocked(token string) bool {
	for i := range t.roots {
		root := &t.roots[i]
		if root.real != "" && root.real != string(filepath.Separator) &&
			strings.Contains(token, root.real) {
			return true
		}
		for _, subdir := range root.subdirs {
			if subdir.real != "" && subdir.real != string(filepath.Separator) &&
				strings.Contains(token, subdir.real) {
				return true
			}
		}
	}
	return false
}

func (t *Translator) lexicallyUnderAnyRootLocked(absolute string) bool {
	for i := range t.roots {
		if _, ok := relativeWithin(t.roots[i].real, absolute); ok {
			return true
		}
	}
	return false
}

func (t *Translator) parseToken(virtualPath string) (int, string, error) {
	const prefix = "$p(:"

	if !strings.HasPrefix(virtualPath, prefix) {
		return 0, "", accessError("parse", "", ErrInvalidToken)
	}
	rest := virtualPath[len(prefix):]
	end := strings.Index(rest, ":)")
	if end <= 0 {
		return 0, "", accessError("parse", "", ErrInvalidToken)
	}

	idText := rest[:end]
	for i := 0; i < len(idText); i++ {
		if idText[i] < '0' || idText[i] > '9' {
			return 0, "", accessError("parse", "", ErrInvalidToken)
		}
	}
	id, err := strconv.ParseUint(idText, 10, 63)
	if err != nil {
		return 0, "", accessError("parse", "", ErrTokenOutOfRange)
	}
	if id >= uint64(len(t.roots)) {
		return 0, "", accessError("parse", "", ErrTokenOutOfRange)
	}

	suffix := rest[end+2:]
	if suffix != "" && suffix[0] != '/' {
		return 0, "", accessError("parse", "", ErrInvalidToken)
	}
	suffix = strings.TrimPrefix(suffix, "/")

	rel, err := normalizeVirtualPath(suffix)
	if err != nil {
		return 0, "", accessError("parse", tokenFor(int(id), ""), err)
	}
	return int(id), rel, nil
}

func normalizeVirtualPath(value string) (string, error) {
	if strings.IndexByte(value, 0) >= 0 {
		return "", ErrInvalidToken
	}

	// Virtual Tcl paths are slash-separated. Treat backslashes as separators
	// as well so the same validation remains safe if a host is running on
	// Windows.
	value = strings.ReplaceAll(value, `\`, "/")
	value = strings.TrimLeft(value, "/")

	parts := strings.Split(value, "/")
	stack := make([]string, 0, len(parts))
	for _, part := range parts {
		switch part {
		case "", ".":
			continue
		case "..":
			if len(stack) == 0 {
				return "", ErrPathEscape
			}
			stack = stack[:len(stack)-1]
		default:
			stack = append(stack, part)
		}
	}

	if len(stack) == 0 {
		return ".", nil
	}
	return strings.Join(stack, "/"), nil
}

func routeFor(root *accessRoot, rel string) (accessRoute, error) {
	if root.whole {
		return accessRoute{root: root.root, base: root.real, rel: rel}, nil
	}

	var selected *allowedDir
	for i := range root.subdirs {
		subdir := &root.subdirs[i]
		if !relativeWithinString(subdir.rel, rel) {
			continue
		}
		if selected == nil || len(subdir.rel) > len(selected.rel) {
			selected = subdir
		}
	}
	if selected == nil {
		return accessRoute{}, ErrSubdirDenied
	}

	remainder := "."
	if selected.rel == "." {
		remainder = rel
	} else if rel != selected.rel {
		remainder = strings.TrimPrefix(rel, selected.rel+"/")
	}
	return accessRoute{root: selected.root, base: selected.real, rel: remainder}, nil
}

func tokenFor(index int, rel string) string {
	token := fmt.Sprintf("$p(:%d:)", index)
	if rel != "" && rel != "." {
		token += "/" + rel
	}
	return token
}

func relativeWithin(base, candidate string) (string, bool) {
	rel, err := filepath.Rel(base, candidate)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	if filepath.IsAbs(rel) {
		return "", false
	}
	if rel == "." {
		return ".", true
	}
	return filepath.ToSlash(rel), true
}

func withinPath(base, candidate string) bool {
	_, ok := relativeWithin(base, candidate)
	return ok
}

func relativeWithinString(base, candidate string) bool {
	if base == "." {
		return true
	}
	return candidate == base || strings.HasPrefix(candidate, base+"/")
}
