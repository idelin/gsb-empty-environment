package netsec

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const tokenPrefix = "$p(:"

type accessRoot struct {
	real          string
	allowedSubdir []string
	restricted    bool
}

// Translator maps safe-interpreter path tokens to real paths and back.  A
// token has the form $p(:N:)/relative/path, where N is a zero-based index
// into the access paths supplied to New.
type Translator struct {
	roots []accessRoot
}

// Resolver is an alias for Translator.
type Resolver = Translator

// PathResolver is an alias for Translator.
type PathResolver = Translator

// PathTranslator is an alias for Translator.
type PathTranslator = Translator

// PathMap is an alias for Translator.
type PathMap = Translator

// AccessPathMap is an alias for Translator.
type AccessPathMap = Translator

// Config describes access paths and, optionally, the only subdirectories
// that may be reached through them.
type Config struct {
	AccessPaths    []string
	Paths          []string
	AllowedSubdirs []string
	Subdirs        []string
}

// New creates a translator.  If allowedSubdirs is omitted, every descendant
// of every access path is reachable.  If it is supplied, requests are
// restricted to the named subdirectories.  Relative subdirectories are
// interpreted with respect to an access path; absolute ones must lie under
// an access path.  A "." or empty entry permits the whole access path.
func New(accessPaths []string, allowedSubdirs ...any) (*Translator, error) {
	subdirs, err := flattenSubdirs(allowedSubdirs)
	if err != nil {
		return nil, err
	}
	return newTranslator(accessPaths, subdirs)
}

// NewResolver creates a translator.  See New for the meaning of its
// arguments.
func NewResolver(accessPaths []string, allowedSubdirs ...any) (*Translator, error) {
	return New(accessPaths, allowedSubdirs...)
}

// NewTranslator creates a translator.  See New for the meaning of its
// arguments.
func NewTranslator(accessPaths []string, allowedSubdirs ...any) (*Translator, error) {
	return New(accessPaths, allowedSubdirs...)
}

// NewPathTranslator creates a translator.  See New for the meaning of its
// arguments.
func NewPathTranslator(accessPaths []string, allowedSubdirs ...any) (*Translator, error) {
	return New(accessPaths, allowedSubdirs...)
}

// NewPathMap creates a translator.  See New for the meaning of its
// arguments.
func NewPathMap(accessPaths []string, allowedSubdirs ...any) (*Translator, error) {
	return New(accessPaths, allowedSubdirs...)
}

// NewAccessPath creates a translator.  See New for the meaning of its
// arguments.
func NewAccessPath(accessPaths []string, allowedSubdirs ...any) (*Translator, error) {
	return New(accessPaths, allowedSubdirs...)
}

// NewAccessPaths creates a translator.  See New for the meaning of its
// arguments.
func NewAccessPaths(accessPaths []string, allowedSubdirs ...any) (*Translator, error) {
	return New(accessPaths, allowedSubdirs...)
}

// NewPathResolver creates a translator.  See New for the meaning of its
// arguments.
func NewPathResolver(accessPaths []string, allowedSubdirs ...any) (*Translator, error) {
	return New(accessPaths, allowedSubdirs...)
}

// NewWithConfig creates a translator from a Config.
func NewWithConfig(cfg Config) (*Translator, error) {
	paths := cfg.AccessPaths
	if len(paths) == 0 {
		paths = cfg.Paths
	}
	subdirs := append([]string(nil), cfg.AllowedSubdirs...)
	subdirs = append(subdirs, cfg.Subdirs...)
	return New(paths, subdirs)
}

// NewFromConfig is an alias for NewWithConfig.
func NewFromConfig(cfg Config) (*Translator, error) {
	return NewWithConfig(cfg)
}

// MustNew is the panicking form of New.  It is intended for static host
// configuration, not for data controlled by a safe interpreter.
func MustNew(accessPaths []string, allowedSubdirs ...any) *Translator {
	t, err := New(accessPaths, allowedSubdirs...)
	if err != nil {
		panic(err)
	}
	return t
}

func newTranslator(accessPaths, subdirs []string) (*Translator, error) {
	t := &Translator{roots: make([]accessRoot, 0, len(accessPaths))}
	for _, path := range accessPaths {
		if path == "" || strings.IndexByte(path, 0) >= 0 {
			return nil, ErrInvalidConfig
		}
		real, err := canonicalPath(path)
		if err != nil {
			return nil, ErrInvalidConfig
		}
		info, err := os.Stat(real)
		if err != nil || info.IsDir() == false {
			return nil, ErrInvalidConfig
		}
		t.roots = append(t.roots, accessRoot{real: real})
	}

	if len(subdirs) == 0 {
		return t, nil
	}

	relative, absolute, unrestricted, err := splitSubdirs(subdirs)
	if err != nil {
		return nil, err
	}
	if unrestricted {
		return t, nil
	}
	for _, candidate := range absolute {
		member := false
		for i := range t.roots {
			if within(t.roots[i].real, candidate) {
				member = true
				break
			}
		}
		if !member {
			return nil, ErrInvalidConfig
		}
	}

	parallel := len(t.roots) > 1 && len(relative) == len(t.roots)
	for i := range t.roots {
		root := &t.roots[i]
		root.restricted = true
		if err := configureSubdirs(root, relative, absolute, i, parallel); err != nil {
			return nil, err
		}
	}
	return t, nil
}

func splitSubdirs(subdirs []string) (relative, absolute []string, unrestricted bool, err error) {
	for _, subdir := range subdirs {
		if subdir == "" || subdir == "." {
			return nil, nil, true, nil
		}
		if strings.IndexByte(subdir, 0) >= 0 || hasDotDot(subdir) {
			return nil, nil, false, ErrInvalidConfig
		}
		if filepath.IsAbs(subdir) {
			real, err := canonicalPath(subdir)
			if err != nil {
				return nil, nil, false, ErrInvalidConfig
			}
			info, err := os.Stat(real)
			if err != nil || !info.IsDir() {
				return nil, nil, false, ErrInvalidConfig
			}
			absolute = append(absolute, real)
			continue
		}
		relative = append(relative, subdir)
	}
	return relative, absolute, false, nil
}

func configureSubdirs(root *accessRoot, relative, absolute []string, index int, parallel bool) error {
	selected := relative
	if parallel {
		selected = []string{relative[index]}
	}
	for _, subdir := range selected {
		candidate := filepath.Join(root.real, filepath.FromSlash(subdir))
		real, err := canonicalPath(candidate)
		if err != nil || !within(root.real, real) {
			return ErrInvalidConfig
		}
		info, err := os.Stat(real)
		if err != nil || !info.IsDir() {
			return ErrInvalidConfig
		}
		root.allowedSubdir = append(root.allowedSubdir, real)
	}
	for _, allowed := range absolute {
		if within(root.real, allowed) {
			root.allowedSubdir = append(root.allowedSubdir, allowed)
		}
	}
	return nil
}

func flattenSubdirs(args []any) ([]string, error) {
	var out []string
	for _, arg := range args {
		switch v := arg.(type) {
		case nil:
			continue
		case string:
			out = append(out, v)
		case []string:
			out = append(out, v...)
		case []any:
			for _, item := range v {
				s, ok := item.(string)
				if !ok {
					return nil, ErrInvalidConfig
				}
				out = append(out, s)
			}
		default:
			return nil, ErrInvalidConfig
		}
	}
	return out, nil
}

func canonicalPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	return filepath.Clean(real), nil
}

func within(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	if err != nil || filepath.IsAbs(rel) {
		return false
	}
	if rel == "." {
		return true
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func hasDotDot(path string) bool {
	for _, component := range strings.FieldsFunc(path, func(r rune) bool {
		return r == '/' || r == '\\'
	}) {
		if component == ".." {
			return true
		}
	}
	return false
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// parseToken validates the untrusted token prefix before any filesystem
// operation is attempted.
func (t *Translator) parseToken(path string) (int, string, error) {
	if !strings.HasPrefix(path, tokenPrefix) {
		return 0, "", ErrInvalidToken
	}
	rest := path[len(tokenPrefix):]
	close := strings.Index(rest, ":)")
	if close < 0 {
		return 0, "", ErrInvalidToken
	}
	indexText := rest[:close]
	if !allDigits(indexText) {
		return 0, "", ErrInvalidToken
	}
	if len(indexText) > 1 && indexText[0] == '0' {
		return 0, "", ErrInvalidToken
	}
	index, err := strconv.Atoi(indexText)
	if err != nil {
		return 0, "", ErrTokenOutOfRange
	}
	if index < 0 || index >= len(t.roots) {
		return 0, "", ErrTokenOutOfRange
	}

	tail := rest[close+2:]
	var subpath string
	switch {
	case tail == "":
		subpath = ""
	case strings.HasPrefix(tail, "/"):
		subpath = tail[1:]
	default:
		return 0, "", ErrInvalidToken
	}

	if strings.IndexByte(subpath, 0) >= 0 {
		return 0, "", ErrInvalidToken
	}
	if strings.HasPrefix(subpath, "/") || strings.HasPrefix(subpath, "\\") {
		return 0, "", ErrInvalidToken
	}
	if hasDotDot(subpath) {
		return 0, "", ErrPathTraversal
	}
	return index, subpath, nil
}

// Translate maps a virtual token path to its canonical real path.  It
// rejects malformed or out-of-range tokens, ".." components, and targets
// whose fully resolved path leaves the selected access path.
func (t *Translator) Translate(tokenPath string) (string, error) {
	index, subpath, err := t.parseToken(tokenPath)
	if err != nil {
		return "", err
	}
	return t.resolve(index, subpath)
}

// TranslatePath is an alias for Translate.
func (t *Translator) TranslatePath(tokenPath string) (string, error) {
	return t.Translate(tokenPath)
}

// Resolve is an alias for Translate.
func (t *Translator) Resolve(tokenPath string) (string, error) {
	return t.Translate(tokenPath)
}

// RealPath is an alias for Translate.
func (t *Translator) RealPath(tokenPath string) (string, error) {
	return t.Translate(tokenPath)
}

func (t *Translator) resolve(index int, subpath string) (string, error) {
	root := &t.roots[index]
	candidate := root.real
	if subpath != "" {
		candidate = filepath.Join(root.real, filepath.FromSlash(subpath))
	}
	return t.validate(index, candidate)
}

func (t *Translator) validate(index int, candidate string) (string, error) {
	real, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", ErrOutsideAccessPath
	}
	real = filepath.Clean(real)
	root := &t.roots[index]
	if !within(root.real, real) {
		return "", ErrOutsideAccessPath
	}
	if root.restricted && !withinAny(root.allowedSubdir, real) {
		return "", ErrOutsideAccessPath
	}
	return real, nil
}

func withinAny(roots []string, target string) bool {
	for _, root := range roots {
		if within(root, target) {
			return true
		}
	}
	return false
}

// PathToken maps a real path back to the first matching virtual token.  The
// real path is canonicalized and must pass the same containment checks as
// forward translation.
func (t *Translator) PathToken(realPath string) (string, error) {
	if realPath == "" || strings.IndexByte(realPath, 0) >= 0 {
		return "", ErrInvalidToken
	}
	real, err := canonicalPath(realPath)
	if err != nil {
		return "", ErrOutsideAccessPath
	}
	for i := range t.roots {
		root := &t.roots[i]
		if !within(root.real, real) {
			continue
		}
		if root.restricted && !withinAny(root.allowedSubdir, real) {
			continue
		}
		rel, err := filepath.Rel(root.real, real)
		if err != nil {
			continue
		}
		return formatToken(i, rel), nil
	}
	return "", ErrOutsideAccessPath
}

// TokenFor is an alias for PathToken.
func (t *Translator) TokenFor(realPath string) (string, error) {
	return t.PathToken(realPath)
}

// TranslateBack is an alias for PathToken.
func (t *Translator) TranslateBack(realPath string) (string, error) {
	return t.PathToken(realPath)
}

// ToToken is an alias for PathToken.
func (t *Translator) ToToken(realPath string) (string, error) {
	return t.PathToken(realPath)
}

func formatToken(index int, rel string) string {
	rel = filepath.ToSlash(filepath.Clean(rel))
	if rel == "." || rel == "" {
		return fmt.Sprintf("%s%d:)", tokenPrefix, index)
	}
	return fmt.Sprintf("%s%d:)/%s", tokenPrefix, index, rel)
}

// Glob expands a virtual glob pattern and returns only virtual token paths.
// Targets that resolve outside the selected access path are omitted; if the
// pattern explicitly names such a target, the request fails.
func (t *Translator) Glob(pattern string) ([]string, error) {
	index, subpath, err := t.parseToken(pattern)
	if err != nil {
		return nil, err
	}
	root := &t.roots[index]
	realPattern := root.real
	if subpath != "" {
		realPattern = filepath.Join(root.real, filepath.FromSlash(subpath))
	}

	// If the pattern has a literal directory prefix, reject a symlink there
	// even when the glob itself contains wildcards.  For example,
	// $p(:0:)/escape/* must not quietly bypass a directory that was replaced
	// by an external symlink.
	baseSubpath := staticGlobBase(subpath)
	if baseSubpath != "" {
		basePath := filepath.Join(root.real, filepath.FromSlash(baseSubpath))
		if _, statErr := os.Lstat(basePath); statErr == nil {
			if _, err := t.validate(index, basePath); err != nil {
				return nil, err
			}
		}
	}

	matches, err := filepath.Glob(realPattern)
	if err != nil {
		return nil, ErrGlobPattern
	}
	tokens := make([]string, 0, len(matches))
	hasMeta := strings.ContainsAny(subpath, "*?[")
	for _, match := range matches {
		if !within(root.real, match) {
			if !hasMeta {
				return nil, ErrOutsideAccessPath
			}
			continue
		}
		real, err := t.validate(index, match)
		if err != nil {
			if !hasMeta {
				return nil, err
			}
			continue
		}
		rel, err := filepath.Rel(root.real, match)
		if err != nil || !within(root.real, real) {
			if !hasMeta {
				return nil, ErrOutsideAccessPath
			}
			continue
		}
		tokens = append(tokens, formatToken(index, rel))
	}
	return tokens, nil
}

func staticGlobBase(pattern string) string {
	firstMeta := strings.IndexAny(pattern, "*?[")
	if firstMeta < 0 {
		return pattern
	}
	prefix := pattern[:firstMeta]
	lastSlash := strings.LastIndex(prefix, "/")
	if lastSlash < 0 {
		return ""
	}
	return prefix[:lastSlash]
}

// GlobTokens is an alias for Glob.
func (t *Translator) GlobTokens(pattern string) ([]string, error) {
	return t.Glob(pattern)
}

// Source opens a token-designated .tcl file, rechecks the target after the
// open, and returns its contents.  The recheck makes the file descriptor
// usable only when it still refers to the same file that the canonical
// containment check approved.
func (t *Translator) Source(tokenPath string) (string, error) {
	index, subpath, err := t.parseToken(tokenPath)
	if err != nil {
		return "", err
	}
	if !isTclPath(subpath) {
		return "", ErrNotTcl
	}
	realPath, err := t.resolveSource(index, subpath)
	if err != nil {
		return "", err
	}

	file, err := os.Open(realPath)
	if err != nil {
		return "", ErrOutsideAccessPath
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return "", ErrNotRegular
	}
	if !isTclPath(realPath) {
		return "", ErrNotTcl
	}

	current, err := t.resolveSource(index, subpath)
	if err != nil {
		return "", err
	}
	currentInfo, err := os.Stat(current)
	if err != nil || !os.SameFile(info, currentInfo) {
		return "", ErrOutsideAccessPath
	}

	data, err := io.ReadAll(file)
	if err != nil {
		return "", ErrReadFailed
	}
	return string(data), nil
}

// ReadSource is an alias for Source that returns bytes.
func (t *Translator) ReadSource(tokenPath string) ([]byte, error) {
	s, err := t.Source(tokenPath)
	if err != nil {
		return nil, err
	}
	return []byte(s), nil
}

// SourceBytes is an alias for ReadSource.
func (t *Translator) SourceBytes(tokenPath string) ([]byte, error) {
	return t.ReadSource(tokenPath)
}

// CheckSource performs the same validation as Source without exposing the
// file contents.
func (t *Translator) CheckSource(tokenPath string) error {
	_, err := t.Source(tokenPath)
	return err
}

func (t *Translator) resolveSource(index int, subpath string) (string, error) {
	real, err := t.resolve(index, subpath)
	if err != nil {
		return "", err
	}
	if !isTclPath(real) {
		return "", ErrNotTcl
	}
	info, err := os.Stat(real)
	if err != nil {
		return "", ErrOutsideAccessPath
	}
	if !info.Mode().IsRegular() {
		return "", ErrNotRegular
	}
	return real, nil
}

func isTclPath(path string) bool {
	return filepath.Ext(path) == ".tcl"
}

// Package-level convenience functions mirror the method API for hosts that
// keep a static access-path configuration in one call.

func TranslatePath(accessPaths []string, tokenPath string) (string, error) {
	t, err := New(accessPaths)
	if err != nil {
		return "", err
	}
	return t.Translate(tokenPath)
}

func Translate(accessPaths []string, tokenPath string) (string, error) {
	return TranslatePath(accessPaths, tokenPath)
}

func Resolve(accessPaths []string, tokenPath string) (string, error) {
	return TranslatePath(accessPaths, tokenPath)
}

func TokenPath(accessPaths []string, realPath string) (string, error) {
	t, err := New(accessPaths)
	if err != nil {
		return "", err
	}
	return t.PathToken(realPath)
}

func PathToken(accessPaths []string, realPath string) (string, error) {
	return TokenPath(accessPaths, realPath)
}

func ToToken(accessPaths []string, realPath string) (string, error) {
	return TokenPath(accessPaths, realPath)
}

func GlobTokens(accessPaths []string, pattern string) ([]string, error) {
	t, err := New(accessPaths)
	if err != nil {
		return nil, err
	}
	return t.Glob(pattern)
}

func Glob(accessPaths []string, pattern string) ([]string, error) {
	return GlobTokens(accessPaths, pattern)
}

func SourceFile(accessPaths []string, tokenPath string) (string, error) {
	t, err := New(accessPaths)
	if err != nil {
		return "", err
	}
	return t.Source(tokenPath)
}

func Source(accessPaths []string, tokenPath string) (string, error) {
	return SourceFile(accessPaths, tokenPath)
}
