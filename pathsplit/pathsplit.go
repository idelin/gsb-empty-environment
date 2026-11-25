// Package pathsplit separates a PHP script path from FastCGI PATH_INFO using a
// virtual site-root manifest.
//
// All paths in the manifest, script directory whitelist, and upload directory
// blacklist are site-root-relative. They are written with forward slashes and a
// leading slash is optional. Symlink targets are interpreted in the same
// virtual filesystem: a target beginning with "/" is relative to the site root,
// and a relative target is resolved from the directory containing the link.
package pathsplit

import (
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
)

// EntryKind describes one entry in a virtual site-root manifest.
type EntryKind uint8

const (
	// RegularFile is a regular file. It is the zero value so a manifest can use
	// pathsplit.Entry{} as shorthand for a regular file.
	RegularFile EntryKind = iota
	// Directory is a directory.
	Directory
	// Symlink is a symbolic link whose target is stored in Entry.Target.
	Symlink
)

// Entry is one virtual filesystem entry.
type Entry struct {
	Kind   EntryKind
	Target string
}

// Manifest maps site-root-relative paths to virtual filesystem entries.
//
// Directories that are not present in the manifest are treated as implicit
// directories. This makes it sufficient to list only files and symlinks, but
// explicit Directory entries are supported and are required at the final
// component when a path must be known to be a directory.
type Manifest map[string]Entry

// Config configures a Splitter.
type Config struct {
	// Root is the host filesystem directory prepended to the resolved script
	// path. An empty root emits site-root-relative paths.
	Root string
	// Files is the virtual site-root manifest.
	Files Manifest
	// ScriptDirs is the allowlist of directories that may contain executable
	// PHP scripts. An empty list permits no scripts.
	ScriptDirs []string
	// UploadDirs lists directories that must never be executed from. Any path
	// below one of these directories is rejected.
	UploadDirs []string
}

// Result is a successfully split request path.
type Result struct {
	// ScriptFilename is the resolved script path, including Config.Root.
	ScriptFilename string
	// PathInfo is the decoded request-path suffix after the script segment.
	// It is empty or begins with "/".
	PathInfo string
}

// Splitter performs path splitting against an immutable virtual manifest.
// It is safe for concurrent use after New returns.
type Splitter struct {
	root            string
	files           Manifest
	scriptDirs      []string
	resolvedScripts []string
	uploadDirs      []string
	resolvedUploads []string
}

// Split errors. Callers can match them with errors.Is.
var (
	ErrNoScript         = errors.New("path has no executable PHP script")
	ErrUnsafePath       = errors.New("path contains an unsafe component")
	ErrScriptNotAllowed = errors.New("script is outside the script directory allowlist")
	ErrUploadPath       = errors.New("path is under an upload directory")
	ErrOutsideRoot      = errors.New("resolved path escapes the site root")
	ErrSymlinkLoop      = errors.New("symbolic link loop")
	ErrNotRegularFile   = errors.New("script candidate is not a regular file")
	ErrPathTooDeep      = errors.New("path resolution is too deep")
)

const (
	maxSymlinkHops = 64
	maxPathSteps   = 8192
)

var errMissingEntry = errors.New("manifest entry does not exist")

// New validates and canonicalizes cfg.
func New(cfg Config) (*Splitter, error) {
	s := &Splitter{
		root:  filepath.Clean(cfg.Root),
		files: make(Manifest, len(cfg.Files)),
	}
	if cfg.Root == "" {
		s.root = ""
	}

	for rawPath, entry := range cfg.Files {
		path, err := canonicalSitePath(rawPath)
		if err != nil {
			return nil, fmt.Errorf("manifest path %q: %w", rawPath, err)
		}
		if err := validateEntry(entry); err != nil {
			return nil, fmt.Errorf("manifest path %q: %w", rawPath, err)
		}
		s.files[path] = entry
	}

	var err error
	s.scriptDirs, err = canonicalDirs(cfg.ScriptDirs)
	if err != nil {
		return nil, fmt.Errorf("script directories: %w", err)
	}
	s.uploadDirs, err = canonicalDirs(cfg.UploadDirs)
	if err != nil {
		return nil, fmt.Errorf("upload directories: %w", err)
	}

	s.resolvedScripts = make([]string, 0, len(s.scriptDirs))
	for _, dir := range s.scriptDirs {
		resolved, err := s.resolveDirectory(dir)
		if err != nil {
			return nil, fmt.Errorf("script directory %q: %w", dir, err)
		}
		s.resolvedScripts = append(s.resolvedScripts, resolved)
	}
	s.resolvedUploads = make([]string, 0, len(s.uploadDirs))
	for _, dir := range s.uploadDirs {
		resolved, err := s.resolveDirectory(dir)
		if err != nil {
			return nil, fmt.Errorf("upload directory %q: %w", dir, err)
		}
		s.resolvedUploads = append(s.resolvedUploads, resolved)
	}

	return s, nil
}

// Split is a convenience wrapper around New and Splitter.Split.
func Split(cfg Config, requestPath string) (Result, error) {
	s, err := New(cfg)
	if err != nil {
		return Result{}, err
	}
	return s.Split(requestPath)
}

// SplitPath is an alias for Split.
func SplitPath(cfg Config, requestPath string) (Result, error) {
	return Split(cfg, requestPath)
}

// Split finds the first eligible ".php" segment, resolves it, and returns its
// host path and the remaining decoded path as PATH_INFO.
func (s *Splitter) Split(requestPath string) (Result, error) {
	segments, err := parseRequestPath(requestPath)
	if err != nil {
		return Result{}, err
	}

	requestPath = "/" + strings.Join(segments, "/")
	if withinAny(requestPath, s.uploadDirs) {
		return Result{}, fmt.Errorf("%w: %q", ErrUploadPath, requestPath)
	}

	notAllowed := false
	for i, segment := range segments {
		name, ok := phpFilename(segment)
		if !ok {
			continue
		}

		candidateSegments := make([]string, i+1)
		copy(candidateSegments, segments[:i])
		candidateSegments[i] = name
		candidatePath := "/" + strings.Join(candidateSegments, "/")

		if withinAny(candidatePath, s.uploadDirs) {
			return Result{}, fmt.Errorf("%w: %q", ErrUploadPath, candidatePath)
		}

		resolvedPath, kind, err := s.resolve(candidatePath, false)
		switch {
		case errors.Is(err, errMissingEntry):
			continue
		case err != nil:
			return Result{}, err
		}

		if withinAny(resolvedPath, s.uploadDirs) ||
			withinAny(resolvedPath, s.resolvedUploads) ||
			withinAny(candidatePath, s.resolvedUploads) {
			return Result{}, fmt.Errorf("%w: %q resolves to %q", ErrUploadPath, candidatePath, resolvedPath)
		}
		if kind != RegularFile {
			continue
		}
		if !withinAny(resolvedPath, s.resolvedScripts) {
			notAllowed = true
			continue
		}

		pathInfo := ""
		if i+1 < len(segments) {
			pathInfo = "/" + strings.Join(segments[i+1:], "/")
		}
		return Result{
			ScriptFilename: s.hostPath(resolvedPath),
			PathInfo:       pathInfo,
		}, nil
	}

	if notAllowed {
		return Result{}, ErrScriptNotAllowed
	}
	return Result{}, ErrNoScript
}

// hostPath turns a resolved virtual path into the SCRIPT_FILENAME path.
func (s *Splitter) hostPath(virtualPath string) string {
	if s.root == "" {
		return filepath.FromSlash(virtualPath)
	}
	if s.root == string(filepath.Separator) {
		return filepath.FromSlash(virtualPath)
	}
	return filepath.Join(s.root, filepath.FromSlash(strings.TrimPrefix(virtualPath, "/")))
}

// resolveDirectory permits a missing final component as an implicit directory
// for allowlist/blacklist entries.
func (s *Splitter) resolveDirectory(path string) (string, error) {
	resolved, kind, err := s.resolve(path, true)
	if err != nil {
		return "", err
	}
	if kind != Directory {
		return "", fmt.Errorf("%w: %q is not a directory", ErrNotRegularFile, path)
	}
	return resolved, nil
}

// resolve performs component-wise symlink resolution. The ".." component is
// intentionally handled here, after symlink expansion, so targets such as
// "dirlink/../file" follow filesystem semantics instead of being collapsed
// lexically before dirlink is resolved.
func (s *Splitter) resolve(path string, implicitFinalDir bool) (string, EntryKind, error) {
	segments, err := canonicalPathSegments(path)
	if err != nil {
		return "", 0, err
	}

	queue := append([]string(nil), segments...)
	resolvedSegments := make([]string, 0, len(segments))
	seenSymlinks := make(map[string]struct{})
	symlinkHops := 0
	steps := 0

	for len(queue) > 0 {
		steps++
		if steps > maxPathSteps || len(queue) > maxPathSteps {
			return "", 0, ErrPathTooDeep
		}

		component := queue[0]
		queue = queue[1:]
		switch component {
		case "", ".":
			continue
		case "..":
			if len(resolvedSegments) == 0 {
				return "", 0, fmt.Errorf("%w: %q", ErrOutsideRoot, path)
			}
			resolvedSegments = resolvedSegments[:len(resolvedSegments)-1]
			continue
		}

		currentSegments := make([]string, len(resolvedSegments)+1)
		copy(currentSegments, resolvedSegments)
		currentSegments[len(resolvedSegments)] = component
		currentPath := "/" + strings.Join(currentSegments, "/")

		entry, exists := s.files[currentPath]
		if !exists {
			if len(queue) == 0 {
				if implicitFinalDir {
					return currentPath, Directory, nil
				}
				return "", 0, fmt.Errorf("%w: %q", errMissingEntry, currentPath)
			}
			resolvedSegments = append(resolvedSegments, component)
			continue
		}

		switch entry.Kind {
		case Symlink:
			state := currentPath + "\x00" + strings.Join(queue, "/")
			if _, exists := seenSymlinks[state]; exists {
				return "", 0, fmt.Errorf("%w: %q", ErrSymlinkLoop, currentPath)
			}
			seenSymlinks[state] = struct{}{}
			symlinkHops++
			if symlinkHops > maxSymlinkHops {
				return "", 0, fmt.Errorf("%w: %q", ErrSymlinkLoop, currentPath)
			}

			target := entry.Target
			if strings.HasPrefix(target, "/") {
				resolvedSegments = resolvedSegments[:0]
				target = strings.TrimPrefix(target, "/")
			}
			targetSegments := strings.Split(target, "/")
			nextQueue := make([]string, 0, len(targetSegments)+len(queue))
			nextQueue = append(nextQueue, targetSegments...)
			nextQueue = append(nextQueue, queue...)
			queue = nextQueue

		case Directory:
			resolvedSegments = append(resolvedSegments, component)
			if len(queue) == 0 {
				return currentPath, Directory, nil
			}

		case RegularFile:
			if len(queue) != 0 {
				return "", 0, fmt.Errorf("%w: %q is below a regular file", ErrNotRegularFile, path)
			}
			resolvedSegments = append(resolvedSegments, component)
			return currentPath, RegularFile, nil

		default:
			return "", 0, fmt.Errorf("%w: unknown manifest kind %d at %q", ErrUnsafePath, entry.Kind, currentPath)
		}
	}

	if implicitFinalDir {
		return "/", Directory, nil
	}
	return "", 0, fmt.Errorf("%w: %q", errMissingEntry, path)
}

// parseRequestPath percent-decodes and validates a request path. It rejects
// embedded NULs, backslashes, malformed escapes, and ".." segments before any
// splitting takes place.
func parseRequestPath(raw string) ([]string, error) {
	decoded, err := url.PathUnescape(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid percent escape in %q", ErrUnsafePath, raw)
	}
	if strings.IndexByte(decoded, 0) >= 0 {
		return nil, fmt.Errorf("%w: NUL byte in %q", ErrUnsafePath, raw)
	}
	if strings.Contains(decoded, "\\") {
		return nil, fmt.Errorf("%w: backslash in %q", ErrUnsafePath, raw)
	}

	parts := strings.Split(decoded, "/")
	segments := make([]string, 0, len(parts))
	for _, part := range parts {
		if part == "" || part == "." {
			continue
		}
		if isDotDotSegment(part) {
			return nil, fmt.Errorf("%w: %q segment in %q", ErrUnsafePath, part, raw)
		}
		segments = append(segments, part)
	}
	return segments, nil
}

// isDotDotSegment also catches the trailing-dot/space form that some web
// servers normalize to "..".
func isDotDotSegment(segment string) bool {
	return segment == ".." || strings.TrimRight(segment, ". ") == ".."
}

// phpFilename normalizes a segment's trailing dots and spaces, then checks the
// ".php" suffix case-insensitively.
func phpFilename(segment string) (string, bool) {
	name := strings.TrimRight(segment, ". ")
	if len(name) < len(".php") || !strings.EqualFold(name[len(name)-len(".php"):], ".php") {
		return "", false
	}
	return name, true
}

func canonicalDirs(dirs []string) ([]string, error) {
	canonical := make([]string, 0, len(dirs))
	for _, dir := range dirs {
		path, err := canonicalSitePath(dir)
		if err != nil {
			return nil, fmt.Errorf("%q: %w", dir, err)
		}
		canonical = append(canonical, path)
	}
	return canonical, nil
}

// canonicalSitePath validates an untrusted-looking configured path. Unlike a
// symlink target, configuration paths may not contain "..".
func canonicalSitePath(raw string) (string, error) {
	if strings.IndexByte(raw, 0) >= 0 {
		return "", fmt.Errorf("%w: NUL byte", ErrUnsafePath)
	}
	if strings.Contains(raw, "\\") {
		return "", fmt.Errorf("%w: backslash", ErrUnsafePath)
	}

	segments := make([]string, 0, strings.Count(raw, "/")+1)
	for _, part := range strings.Split(raw, "/") {
		switch part {
		case "", ".":
			continue
		case "..":
			return "", fmt.Errorf("%w: parent segment", ErrUnsafePath)
		default:
			segments = append(segments, part)
		}
	}
	if len(segments) == 0 {
		return "/", nil
	}
	return "/" + strings.Join(segments, "/"), nil
}

func canonicalPathSegments(path string) ([]string, error) {
	canonical, err := canonicalSitePath(path)
	if err != nil {
		return nil, err
	}
	if canonical == "/" {
		return nil, nil
	}
	return strings.Split(strings.TrimPrefix(canonical, "/"), "/"), nil
}

func validateEntry(entry Entry) error {
	switch entry.Kind {
	case RegularFile, Directory:
		return nil
	case Symlink:
		if entry.Target == "" {
			return fmt.Errorf("%w: empty symlink target", ErrUnsafePath)
		}
		if strings.IndexByte(entry.Target, 0) >= 0 {
			return fmt.Errorf("%w: NUL byte in symlink target", ErrUnsafePath)
		}
		if strings.Contains(entry.Target, "\\") {
			return fmt.Errorf("%w: backslash in symlink target", ErrUnsafePath)
		}
		return nil
	default:
		return fmt.Errorf("%w: unknown entry kind %d", ErrUnsafePath, entry.Kind)
	}
}

func withinAny(path string, directories []string) bool {
	for _, dir := range directories {
		if within(path, dir) {
			return true
		}
	}
	return false
}

func within(path, dir string) bool {
	if dir == "/" {
		return strings.HasPrefix(path, "/")
	}
	return path == dir || strings.HasPrefix(path, dir+"/")
}
