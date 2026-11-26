package pathsplit

import (
	"errors"
	"net/url"
	"path"
	"strings"
)

// Split inspects rawURLPath and returns the safe script/PATH_INFO split.
//
// It returns (nil, nil) when the path does not map to any runnable PHP script;
// the caller should then serve the request statically (or produce its own 404).
// A non-nil error always denotes an explicit security or policy violation.
func Split(cfg Config, rawURLPath string) (*Result, error) {
	if cfg.FS == nil {
		return nil, errors.New("pathsplit: Config.FS is required")
	}
	scriptDirs, err := cleanDirs(cfg.ScriptDirs)
	if err != nil {
		return nil, err
	}
	uploadDirs, err := cleanDirs(cfg.UploadDirs)
	if err != nil {
		return nil, err
	}

	segments, err := safeSegments(rawURLPath)
	if err != nil {
		return nil, err
	}
	if len(segments) == 0 {
		return nil, nil
	}

	for i, segment := range segments {
		// Strip trailing dots/spaces (their percent-encoded forms are already
		// decoded) before judging the ".php" suffix.
		name := strings.TrimRight(segment, ". ")
		if !isPHPSegment(name) {
			continue
		}

		candidateParts := make([]string, 0, i+1)
		candidateParts = append(candidateParts, segments[:i]...)
		candidateParts = append(candidateParts, name)
		candidate := "/" + strings.Join(candidateParts, "/")

		// The upload rule takes precedence over the whitelist so that a
		// misconfigured whitelist can never enable execution of uploads.
		if withinAny(candidate, uploadDirs) {
			return nil, ErrUploadDenied
		}
		if !withinAny(candidate, scriptDirs) {
			// Not an executable location: keep scanning later candidates and
			// otherwise fall back to static handling.
			continue
		}

		resolved, rerr := resolve(cfg.FS, candidate)
		if rerr != nil {
			if errors.Is(rerr, ErrSymlinkLoop) || errors.Is(rerr, ErrOutsideRoot) {
				return nil, rerr
			}
			// Missing or dangling target: not a runnable script.
			continue
		}

		entry, ok := cfg.FS.Lookup(resolved)
		if !ok || entry.Kind != KindFile {
			continue
		}
		if withinAny(resolved, uploadDirs) {
			return nil, ErrUploadDenied
		}

		result := &Result{
			ScriptFilename: candidate,
			ScriptName:     candidate,
			ResolvedPath:   resolved,
		}
		if i+1 < len(segments) {
			result.PathInfo = "/" + strings.Join(segments[i+1:], "/")
		}
		return result, nil
	}

	return nil, nil
}

// safeSegments percent-decodes the path and rejects NUL bytes, backslashes and
// ".." segments anywhere in the leading path or future PATH_INFO.
func safeSegments(rawURLPath string) ([]string, error) {
	decoded, err := url.PathUnescape(rawURLPath)
	if err != nil {
		return nil, ErrMalformedPath
	}
	if strings.ContainsRune(decoded, 0) || strings.Contains(decoded, `\`) {
		return nil, ErrMalformedPath
	}

	rawParts := strings.Split(decoded, "/")
	segments := make([]string, 0, len(rawParts))
	for _, part := range rawParts {
		if part == "" || part == "." {
			continue
		}
		if part == ".." {
			return nil, ErrMalformedPath
		}
		segments = append(segments, part)
	}
	return segments, nil
}

func isPHPSegment(name string) bool {
	if len(name) < len(".php") {
		return false
	}
	return strings.EqualFold(name[len(name)-len(".php"):], ".php")
}

func cleanDirs(dirs []string) ([]string, error) {
	cleaned := make([]string, 0, len(dirs))
	for _, dir := range dirs {
		if strings.ContainsRune(dir, 0) || strings.Contains(dir, `\`) {
			return nil, errors.New("pathsplit: invalid configured directory: " + dir)
		}
		if dir == "" {
			continue
		}
		if !strings.HasPrefix(dir, "/") {
			dir = "/" + dir
		}
		cleaned = append(cleaned, path.Clean(dir))
	}
	return cleaned, nil
}

func withinAny(p string, dirs []string) bool {
	for _, dir := range dirs {
		if p == dir || strings.HasPrefix(p, dir+"/") {
			return true
		}
	}
	return false
}
