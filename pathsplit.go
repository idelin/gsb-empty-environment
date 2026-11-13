package netsec

import (
	"errors"
	"fmt"
	"net/url"
	"path"
	"strings"
)

// ErrNotScript means that the request path has no eligible PHP script prefix.
var ErrNotScript = errors.New("netsec: no eligible PHP script")

// ErrBlocked means that the path violates the script or upload directory
// policy. No SCRIPT_FILENAME should be produced for such a path.
var ErrBlocked = errors.New("netsec: path blocked by directory policy")

// ErrInvalidPath means that the request contains an unsafe path component or
// a symlink that cannot be resolved safely within the site root.
var ErrInvalidPath = errors.New("netsec: invalid request path")

// ErrInvalidManifest means that the virtual site file listing is malformed.
var ErrInvalidManifest = errors.New("netsec: invalid virtual file manifest")

// VirtualFile describes one entry in a virtual site file listing. A non-empty
// Target makes the entry a symbolic link. Targets are either site-root-relative
// (starting with "/") or relative to the directory containing the link.
//
// The zero value describes a regular file.
type VirtualFile struct {
	Target string
}

// Symlink constructs a symbolic-link manifest entry.
func Symlink(target string) VirtualFile {
	return VirtualFile{Target: target}
}

// Manifest is a virtual listing of the files reachable below the site root.
// Keys are site-root-relative paths.
type Manifest map[string]VirtualFile

// SplitResult is the FastCGI path split for a request.
type SplitResult struct {
	// ScriptFilename is the normalized, site-root-relative script path. A
	// gateway normally prepends its physical site root to this value before
	// placing it in SCRIPT_FILENAME.
	ScriptFilename string

	// PathInfo is the remainder of the request path following the script.
	PathInfo string

	// ResolvedScript is the final regular-file path after following any
	// symbolic links. It is useful for auditing policy decisions.
	ResolvedScript string
}

// Splitter applies a virtual site file listing and directory policy to paths.
// A Splitter is safe for concurrent use after construction.
type Splitter struct {
	files      map[string]VirtualFile
	scriptDirs []string
	uploadDirs []string
}

// NewSplitter validates and normalizes the virtual file manifest and policy
// directories. Directory paths are interpreted relative to the virtual site
// root; a leading slash is optional.
func NewSplitter(files Manifest, scriptDirs, uploadDirs []string) (*Splitter, error) {
	normalizedFiles := make(map[string]VirtualFile, len(files))
	for rawPath, file := range files {
		filePath, err := cleanVirtualPath(rawPath)
		if err != nil {
			return nil, fmt.Errorf("%w: path %q", ErrInvalidManifest, rawPath)
		}
		if strings.ContainsRune(file.Target, 0) {
			return nil, fmt.Errorf("%w: target for %q contains NUL", ErrInvalidManifest, rawPath)
		}
		if previous, exists := normalizedFiles[filePath]; exists && previous != file {
			return nil, fmt.Errorf("%w: duplicate path %q", ErrInvalidManifest, filePath)
		}
		normalizedFiles[filePath] = file
	}

	normalizedScriptDirs, err := normalizeDirectories(scriptDirs)
	if err != nil {
		return nil, fmt.Errorf("%w: script directory: %v", ErrInvalidManifest, err)
	}
	normalizedUploadDirs, err := normalizeDirectories(uploadDirs)
	if err != nil {
		return nil, fmt.Errorf("%w: upload directory: %v", ErrInvalidManifest, err)
	}

	return &Splitter{
		files:      normalizedFiles,
		scriptDirs: normalizedScriptDirs,
		uploadDirs: normalizedUploadDirs,
	}, nil
}

// SplitPath is a convenience wrapper that constructs a Splitter and splits one
// request path. requestPath is the percent-encoded URL path without the query
// string. Plain paths without percent escapes are accepted as-is.
func SplitPath(requestPath string, files Manifest, scriptDirs, uploadDirs []string) (SplitResult, error) {
	splitter, err := NewSplitter(files, scriptDirs, uploadDirs)
	if err != nil {
		return SplitResult{}, err
	}
	return splitter.SplitPath(requestPath)
}

// SplitPath splits requestPath into a script file and PATH_INFO. It scans
// left-to-right and returns the first eligible ".php" segment. The extension
// check is case-insensitive and ignores trailing dots and spaces on the
// segment. Paths under an upload directory are never eligible, including when
// reached through a symbolic link.
func (s *Splitter) SplitPath(requestPath string) (SplitResult, error) {
	segments, trailingSlash, err := parseRequestPath(requestPath)
	if err != nil {
		return SplitResult{}, err
	}

	for i, segment := range segments {
		scriptName, isPHP := phpScriptName(segment)
		if !isPHP {
			continue
		}

		scriptFilename := virtualPathFor(segments[:i], scriptName)
		if pathInDirectories(s.uploadDirs, scriptFilename) {
			return SplitResult{}, fmt.Errorf("%w: %s is in an upload directory", ErrBlocked, scriptFilename)
		}
		if !pathInDirectories(s.scriptDirs, scriptFilename) {
			continue
		}

		resolved, found, err := s.resolveFile(scriptFilename, make(map[string]struct{}), 0)
		if err != nil {
			return SplitResult{}, err
		}
		if !found {
			continue
		}
		if !pathInDirectories(s.scriptDirs, resolved) {
			return SplitResult{}, fmt.Errorf("%w: symlink target %s is outside script directories", ErrBlocked, resolved)
		}

		pathInfo := ""
		if i+1 < len(segments) {
			pathInfo = "/" + strings.Join(segments[i+1:], "/")
		}
		if trailingSlash {
			pathInfo += "/"
		}

		return SplitResult{
			ScriptFilename: scriptFilename,
			PathInfo:       pathInfo,
			ResolvedScript: resolved,
		}, nil
	}

	return SplitResult{}, ErrNotScript
}

func parseRequestPath(requestPath string) ([]string, bool, error) {
	decoded, err := url.PathUnescape(requestPath)
	if err != nil {
		return nil, false, fmt.Errorf("%w: malformed URL escape", ErrInvalidPath)
	}
	if strings.ContainsRune(decoded, 0) {
		return nil, false, fmt.Errorf("%w: NUL byte", ErrInvalidPath)
	}

	parts := strings.Split(decoded, "/")
	segments := make([]string, 0, len(parts))
	for _, part := range parts {
		switch part {
		case "", ".":
			continue
		case "..":
			return nil, false, fmt.Errorf("%w: parent-directory segment", ErrInvalidPath)
		default:
			segments = append(segments, part)
		}
	}

	return segments, strings.HasSuffix(decoded, "/"), nil
}

func phpScriptName(segment string) (string, bool) {
	name := strings.TrimRight(segment, ". ")
	if len(name) < len(".php") || !strings.EqualFold(name[len(name)-len(".php"):], ".php") {
		return "", false
	}
	return name, true
}

func virtualPathFor(prefix []string, name string) string {
	if len(prefix) == 0 {
		return "/" + name
	}
	return "/" + strings.Join(prefix, "/") + "/" + name
}

func normalizeDirectories(dirs []string) ([]string, error) {
	normalized := make([]string, 0, len(dirs))
	seen := make(map[string]struct{}, len(dirs))
	for _, dir := range dirs {
		if dir == "" {
			return nil, errors.New("empty directory path")
		}
		cleaned, err := cleanVirtualPath(dir)
		if err != nil {
			return nil, fmt.Errorf("directory %q: %w", dir, err)
		}
		if _, exists := seen[cleaned]; exists {
			continue
		}
		seen[cleaned] = struct{}{}
		normalized = append(normalized, cleaned)
	}
	return normalized, nil
}

// cleanVirtualPath resolves "." and ".." components against the virtual site
// root. Unlike path.Clean, it reports an error instead of silently discarding
// components that climb above the root.
func cleanVirtualPath(raw string) (string, error) {
	if strings.ContainsRune(raw, 0) {
		return "", errors.New("path contains NUL")
	}

	parts := strings.Split(raw, "/")
	stack := make([]string, 0, len(parts))
	for _, part := range parts {
		switch part {
		case "", ".":
			continue
		case "..":
			if len(stack) == 0 {
				return "", errors.New("path escapes site root")
			}
			stack = stack[:len(stack)-1]
		default:
			stack = append(stack, part)
		}
	}
	if len(stack) == 0 {
		return "/", nil
	}
	return "/" + strings.Join(stack, "/"), nil
}

func pathInDirectories(directories []string, filePath string) bool {
	for _, dir := range directories {
		if dir == "/" {
			return true
		}
		if filePath == dir || strings.HasPrefix(filePath, dir+"/") {
			return true
		}
	}
	return false
}

func (s *Splitter) resolveFile(filePath string, followed map[string]struct{}, depth int) (string, bool, error) {
	if depth > 64 {
		return "", false, fmt.Errorf("%w: symbolic-link chain is too deep", ErrInvalidPath)
	}

	filePath, err := cleanVirtualPath(filePath)
	if err != nil {
		return "", false, fmt.Errorf("%w: %v", ErrInvalidPath, err)
	}
	if pathInDirectories(s.uploadDirs, filePath) {
		return "", false, fmt.Errorf("%w: %s is in an upload directory", ErrBlocked, filePath)
	}
	if _, exists := followed[filePath]; exists {
		return "", false, fmt.Errorf("%w: symbolic-link cycle at %s", ErrInvalidPath, filePath)
	}

	parts := strings.Split(strings.TrimPrefix(filePath, "/"), "/")
	for i := 1; i <= len(parts); i++ {
		prefix := "/" + strings.Join(parts[:i], "/")
		file, exists := s.files[prefix]
		if !exists {
			if i == len(parts) {
				return "", false, nil
			}
			continue
		}

		if file.Target == "" {
			if i != len(parts) {
				return "", false, nil
			}
			return prefix, true, nil
		}

		if _, exists := followed[prefix]; exists {
			return "", false, fmt.Errorf("%w: symbolic-link cycle at %s", ErrInvalidPath, prefix)
		}
		followed[prefix] = struct{}{}

		target, err := resolveSymlinkTarget(prefix, file.Target)
		if err != nil {
			return "", false, fmt.Errorf("%w: symlink %s: %v", ErrInvalidPath, prefix, err)
		}
		if pathInDirectories(s.uploadDirs, target) {
			return "", false, fmt.Errorf("%w: symlink %s points into an upload directory", ErrBlocked, prefix)
		}
		if i < len(parts) {
			target += "/" + strings.Join(parts[i:], "/")
		}
		return s.resolveFile(target, followed, depth+1)
	}

	return "", false, nil
}

func resolveSymlinkTarget(linkPath, target string) (string, error) {
	if target == "" {
		return "", errors.New("empty symlink target")
	}
	if strings.ContainsRune(target, 0) {
		return "", errors.New("symlink target contains NUL")
	}

	if strings.HasPrefix(target, "/") {
		return cleanVirtualPath(target)
	}
	return cleanVirtualPath(path.Dir(linkPath) + "/" + target)
}
