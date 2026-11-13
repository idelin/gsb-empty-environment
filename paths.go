package netsec

import (
	"path"
	"strings"
)

// maxSymlinkHops bounds symlink resolution so a hostile link chain can be
// reported as a cycle/depth violation instead of looping forever.
const maxSymlinkHops = 64

// cleanRepoPath converts a caller-supplied path to a cleaned, slash-based,
// root-relative path. Backslashes are normalized to slashes so Windows-style
// separators cannot be used to smuggle a traversal past the checks.
func cleanRepoPath(value string) string {
	value = strings.ReplaceAll(value, "\\", "/")
	return path.Clean(value)
}

// inRoot reports whether a cleaned path stays at or below the repository
// root. Absolute paths and any path whose cleaned form reaches ".." fail.
func inRoot(cleaned string) bool {
	if cleaned == "" || cleaned == "." {
		return true
	}
	if path.IsAbs(cleaned) {
		return false
	}
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return false
	}
	return true
}

// resolveLocalPath normalizes a local reference relative to baseDir (itself
// a root-relative directory, "" for the repository root) and then walks
// every symlink recorded in the catalog. Both the lexical path and every
// resolved target must remain inside the repository root.
//
// Missing intermediate entries are tolerated: a referenced directory that
// the caller did not list simply contributes no symlink, and containment is
// still enforced lexically. Symlink cycles and chains longer than
// maxSymlinkHops are reported with dedicated reasons.
func resolveLocalPath(catalog *FileCatalog, baseDir, value string) (string, Reason, string) {
	if value == "" {
		return "", ReasonEmptyPath, "path is empty"
	}
	normalized := strings.ReplaceAll(value, "\\", "/")
	if path.IsAbs(normalized) {
		return "", ReasonAbsolutePath, "absolute paths are not allowed: " + value
	}

	var joined string
	if baseDir == "" || baseDir == "." {
		joined = path.Clean(normalized)
	} else {
		joined = path.Clean(path.Join(baseDir, normalized))
	}
	if !inRoot(joined) {
		return "", ReasonPathEscapesRoot, "path escapes the repository root: " + value
	}

	resolved, reason, detail := resolveSymlinks(catalog, joined)
	if reason != "" {
		return "", reason, detail
	}
	if !inRoot(resolved) {
		return "", ReasonSymlinkEscapesRoot, "symlink target escapes the repository root: " + value
	}
	return resolved, "", ""
}

// resolveSymlinks walks symlinks recorded in the catalog for current and
// each of its parent directories, mirroring the semantics of filepath.Eval
// without touching the real filesystem. At each directory boundary the
// longest known symlink prefix is expanded.
func resolveSymlinks(catalog *FileCatalog, current string) (string, Reason, string) {
	visited := make(map[string]struct{})
	for hops := 0; hops <= maxSymlinkHops; hops++ {
		if _, seen := visited[current]; seen {
			return "", ReasonSymlinkCycle, "symlink cycle detected at: " + current
		}
		visited[current] = struct{}{}

		linkPath, target, ok := longestSymlink(catalog, current)
		if !ok {
			return current, "", ""
		}

		next := expandLink(linkPath, target, current)
		if path.IsAbs(next) || !inRoot(next) {
			return "", ReasonSymlinkEscapesRoot, "symlink target escapes the repository root: " + target
		}
		current = next
	}
	return "", ReasonSymlinkTooDeep, "symlink chain exceeds the hop limit"
}

// longestSymlink finds the longest prefix of current that is a known
// symlink and returns the link path with its raw target.
func longestSymlink(catalog *FileCatalog, current string) (string, string, bool) {
	if catalog == nil {
		return "", "", false
	}
	prefix := current
	for {
		if target, ok := catalog.linkAt(prefix); ok {
			return prefix, target, true
		}
		parent := path.Dir(prefix)
		if parent == prefix || parent == "." || parent == "/" {
			return "", "", false
		}
		prefix = parent
	}
}

// expandLink rewrites the portion of current below linkPath using target,
// which is interpreted relative to the link's own directory when relative.
func expandLink(linkPath, target, current string) string {
	remainder := strings.TrimPrefix(current, linkPath)
	remainder = strings.TrimPrefix(remainder, "/")

	target = strings.ReplaceAll(target, "\\", "/")
	if path.IsAbs(target) {
		if remainder == "" {
			return path.Clean(target)
		}
		return path.Clean(path.Join(target, remainder))
	}

	linkDir := path.Dir(linkPath)
	if linkDir == "." {
		if remainder == "" {
			return path.Clean(target)
		}
		return path.Clean(path.Join(target, remainder))
	}
	if remainder == "" {
		return path.Clean(path.Join(linkDir, target))
	}
	return path.Clean(path.Join(linkDir, target, remainder))
}
